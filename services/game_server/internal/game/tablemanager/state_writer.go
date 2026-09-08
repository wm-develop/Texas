package tablemanager

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"texas/services/game_server/internal/tablestate"
)

// stateWriter 把牌桌状态异步写入存储。
//
// 保存发生在持有牌桌锁的路径上：同步写库意味着数据库一慢，整桌人跟着卡住，
// 而这是崩溃恢复引入的新故障模式——在此之前牌局根本不碰数据库。序列化很快
// （几十微秒），真正慢的是那次网络往返，所以把往返挪到后台。
//
// 每个房间只保留最新的一次待写：一手牌里每个动作都会保存，中间状态丢了没有
// 影响，恢复只用得上最新的那份。这同时消除了乱序——同一房间永远只有一个
// 待写项，后来的直接覆盖先来的。
type stateWriter struct {
	store  tablestate.Store
	logger *slog.Logger

	mu      sync.Mutex
	idle    *sync.Cond
	pending map[string]pendingWrite
	writing bool
	closed  bool

	wake chan struct{}
	done chan struct{}
}

// pendingWrite 要么保存一份状态，要么删除该房间的记录（手结束）。
type pendingWrite struct {
	record tablestate.Record
	remove bool
}

func newStateWriter(store tablestate.Store, logger *slog.Logger) *stateWriter {
	writer := &stateWriter{
		store:   store,
		logger:  logger,
		pending: make(map[string]pendingWrite),
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	writer.idle = sync.NewCond(&writer.mu)
	go writer.run()
	return writer
}

// save 与 remove 都不阻塞：它们只更新待写项并叫醒后台。
func (writer *stateWriter) save(record tablestate.Record) {
	writer.enqueue(record.RoomID, pendingWrite{record: record})
}

func (writer *stateWriter) remove(roomID string) {
	writer.enqueue(roomID, pendingWrite{record: tablestate.Record{RoomID: roomID}, remove: true})
}

func (writer *stateWriter) enqueue(roomID string, write pendingWrite) {
	writer.mu.Lock()
	if writer.closed {
		writer.mu.Unlock()
		return
	}
	writer.pending[roomID] = write
	writer.mu.Unlock()
	select {
	case writer.wake <- struct{}{}:
	default:
		// 已经有唤醒信号在路上，后台会看到最新的待写项
	}
}

func (writer *stateWriter) run() {
	defer close(writer.done)
	for range writer.wake {
		writer.drain()
		writer.mu.Lock()
		closed := writer.closed
		writer.mu.Unlock()
		if closed {
			// 关闭后再清一次：叫醒信号与最后一批写入可能有竞争
			writer.drain()
			return
		}
	}
}

// drain 把当前所有待写项写出去。取走后再写，避免长时间持有自己的锁。
func (writer *stateWriter) drain() {
	for {
		writer.mu.Lock()
		if len(writer.pending) == 0 {
			writer.mu.Unlock()
			return
		}
		batch := writer.pending
		writer.pending = make(map[string]pendingWrite)
		writer.writing = true
		writer.mu.Unlock()

		for roomID, write := range batch {
			ctx, cancel := context.WithTimeout(context.Background(), tableStatePersistTimeout)
			var err error
			if write.remove {
				err = writer.store.Delete(ctx, roomID)
			} else {
				err = writer.store.Save(ctx, write.record)
			}
			cancel()
			if err != nil && writer.logger != nil {
				// 只记录不重试：下一个动作会带来更新的状态并再写一次，
				// 而重试一份已经过期的状态没有意义。
				writer.logger.Warn("could not persist the table state",
					"roomId", roomID, "remove", write.remove, "error", err)
			}
		}
		writer.mu.Lock()
		writer.writing = false
		writer.idle.Broadcast()
		writer.mu.Unlock()
	}
}

// waitIdle 等到所有待写项都落盘。只有测试需要这个确定性；生产路径从不等待，
// 那正是把写库挪到后台的意义。
func (writer *stateWriter) waitIdle() {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	for len(writer.pending) > 0 || writer.writing {
		writer.idle.Wait()
	}
}

// close 停止后台并把剩余的待写项写完。
//
// 优雅停机时必须等它写完：那正是最需要状态落盘的时刻——排空超时后仍在进行的
// 那一手，靠这份状态才能在新进程里继续。
func (writer *stateWriter) close() {
	writer.mu.Lock()
	if writer.closed {
		writer.mu.Unlock()
		return
	}
	writer.closed = true
	writer.mu.Unlock()

	select {
	case writer.wake <- struct{}{}:
	default:
	}
	close(writer.wake)
	select {
	case <-writer.done:
	case <-time.After(2 * tableStatePersistTimeout):
		if writer.logger != nil {
			writer.logger.Warn("timed out flushing table states on shutdown")
		}
	}
}
