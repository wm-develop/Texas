// Package tablestate 保存进行中牌局的状态快照，供进程重启后恢复那一手。
//
// 只在牌局进行中存在记录，手结束即删除：手间的权威状态（筹码、准备、座位）
// 本来就在房间成员表里。因此这张表平时几乎是空的，最多与进行中的牌桌同数量。
package tablestate

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Record 是一个牌桌的状态快照。State 是引擎状态的 JSON 编码，本包不解释它。
type Record struct {
	RoomID    string
	HandID    string
	Revision  uint64
	State     []byte
	UpdatedAt time.Time
}

// Store 读写牌桌状态。
//
// Save 必须覆盖同一房间的旧记录：一手牌里每个动作都会保存一次，只保留最新的。
type Store interface {
	Save(ctx context.Context, record Record) error
	Load(ctx context.Context, roomID string) (Record, bool, error)
	Delete(ctx context.Context, roomID string) error
	// DeleteOlderThan 清理没人回来的牌桌留下的孤儿记录。房间被删除时外键会
	// 级联删除，但房间还在、所有人都不再回来的情况需要这一条兜底。
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error)
}

// MemoryStore 是进程内实现，用于测试与未接数据库的部署。
//
// 它当然无法跨进程恢复——恢复本身就是为跨进程准备的。用它只是让不接数据库
// 的部署与测试走同一条代码路径，避免出现「只有生产才跑到」的分支。
type MemoryStore struct {
	mu      sync.RWMutex
	records map[string]Record
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: make(map[string]Record)}
}

func (store *MemoryStore) Save(_ context.Context, record Record) error {
	if err := validate(record); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	copied := record
	copied.State = append([]byte(nil), record.State...)
	store.records[record.RoomID] = copied
	return nil
}

func (store *MemoryStore) Load(_ context.Context, roomID string) (Record, bool, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	record, found := store.records[roomID]
	if !found {
		return Record{}, false, nil
	}
	record.State = append([]byte(nil), record.State...)
	return record, true, nil
}

func (store *MemoryStore) Delete(_ context.Context, roomID string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.records, roomID)
	return nil
}

func (store *MemoryStore) DeleteOlderThan(_ context.Context, cutoff time.Time) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	removed := 0
	for roomID, record := range store.records {
		if record.UpdatedAt.Before(cutoff) {
			delete(store.records, roomID)
			removed++
		}
	}
	return removed, nil
}

func validate(record Record) error {
	if record.RoomID == "" {
		return errors.New("room id is required")
	}
	if record.HandID == "" {
		return errors.New("hand id is required")
	}
	if len(record.State) == 0 {
		return errors.New("state is required")
	}
	return nil
}
