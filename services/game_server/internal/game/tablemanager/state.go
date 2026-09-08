package tablemanager

import (
	"context"
	"encoding/json"
	"time"

	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/room"
	"texas/services/game_server/internal/tablestate"
)

// 进行中牌局的持久化与恢复。
//
// 在此之前，进程异常退出会让进行中的那一手作废：底池退回上一手结算后的
// 状态，玩家的决策全部白费。优雅停机会等牌局打完，崩溃、OOM 与宿主机重启
// 不会等，而部署之外的这几种情况恰恰无法预告。
//
// 只在牌局进行中保存，手结束立即删除。手间的权威状态（筹码、准备、座位）
// 本来就在房间成员表里，重复保存只会多一处可能不一致的副本。
//
// 恢复是尽力而为：任何一步失败都退回「本手作废」的旧行为，绝不能拿一个
// 不确定的状态继续发牌。

// runtimeStateVersion 与引擎状态的版本各自独立：牌桌管理层的字段可能单独
// 增减。版本对不上就放弃恢复。
const runtimeStateVersion = 1

// runtimeState 是恢复一手牌所需的牌桌管理层状态。
//
// 不包含手间才有意义的字段（自动准备倒计时、换座申请），也不包含连接相关的
// 瞬时状态（online）——玩家重连时自然会重建。
type runtimeState struct {
	Version int               `json:"version"`
	Engine  holdem.TableState `json:"engine"`
	Manager managerHandState  `json:"manager"`
}

// managerHandState 是牌桌管理层在一手牌之内累积的状态。漏掉任何一项，恢复
// 后的这手牌都会与崩溃前不同：少了加时卡、丢了动作记录、观战者白付了看牌费。
type managerHandState struct {
	HandStartedAt            time.Time                                 `json:"handStartedAt"`
	PersistedHandID          string                                    `json:"persistedHandId"`
	TimeExtensions           map[string]int                            `json:"timeExtensions,omitempty"`
	Actions                  []history.Action                          `json:"actions,omitempty"`
	LastAction               *ConfirmedActionSnapshot                  `json:"lastAction,omitempty"`
	VoluntarilyRevealedHands map[string]holdem.RevealedHand            `json:"voluntarilyRevealedHands,omitempty"`
	PrivateHoleCardViews     map[string]map[string]holdem.RevealedHand `json:"privateHoleCardViews,omitempty"`
	PendingCashOuts          map[string]string                         `json:"pendingCashOuts,omitempty"`
	KnownDisplayNames        map[string]string                         `json:"knownDisplayNames,omitempty"`
	PendingSpectate          map[string]bool                           `json:"pendingSpectate,omitempty"`
	PendingSeat              map[string]bool                           `json:"pendingSeat,omitempty"`
	SpectatorAccess          map[string]bool                           `json:"spectatorAccess,omitempty"`
	SpectatorFees            *SpectatorFeeSnapshot                     `json:"spectatorFees,omitempty"`
}

// persistStateLocked 保存或清除牌桌状态。调用方必须持有 runtime.mu。
//
// 手间删除记录而不是留着：留着的话，重启后会用一份没有牌局的状态覆盖
// 成员表带来的正确筹码，反而添乱。
//
// 只做序列化与投递，真正的写库在后台：这段代码运行在持有牌桌锁的路径上，
// 同步写库会让数据库一慢就把整桌人卡住。写失败也不回滚动作——动作已经在
// 内存里生效并即将广播给玩家，此时回滚会让玩家的操作凭空消失，比「万一
// 崩溃时这手作废」严重得多。
func (manager *Manager) persistStateLocked(runtime *runtime) {
	if manager.stateWriter == nil {
		return
	}
	if isBetweenHands(runtime.engine) {
		manager.stateWriter.remove(runtime.roomID)
		return
	}
	state := runtimeState{
		Version: runtimeStateVersion,
		Engine:  runtime.engine.State(),
		Manager: managerHandState{
			HandStartedAt:            runtime.handStartedAt,
			PersistedHandID:          runtime.persistedHandID,
			TimeExtensions:           runtime.timeExtensions,
			Actions:                  runtime.actions,
			LastAction:               runtime.lastAction,
			VoluntarilyRevealedHands: runtime.voluntarilyRevealedHands,
			PrivateHoleCardViews:     runtime.privateHoleCardViews,
			PendingCashOuts:          runtime.pendingCashOuts,
			KnownDisplayNames:        runtime.knownDisplayNames,
			PendingSpectate:          runtime.pendingSpectate,
			PendingSeat:              runtime.pendingSeat,
			SpectatorAccess:          runtime.spectatorAccess,
			SpectatorFees:            runtime.spectatorFees,
		},
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		manager.logStateError("could not encode the table state", runtime.roomID, err)
		return
	}
	manager.stateWriter.save(tablestate.Record{
		RoomID:    runtime.roomID,
		HandID:    runtime.engine.HandID(),
		Revision:  runtime.engine.Revision(),
		State:     encoded,
		UpdatedAt: manager.now().UTC(),
	})
}

// restoreLocked 尝试用持久化的状态重建一个刚创建的 runtime。
//
// 返回是否恢复成功。任何异常都当作「没有可恢复的状态」处理并清掉那条记录：
// 留着一份装不回去的状态，只会在每次有人进房间时重复失败。
func (manager *Manager) restoreLocked(created *runtime, roomValue room.Room) bool {
	if manager.tableStates == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), tableStatePersistTimeout)
	defer cancel()

	record, found, err := manager.tableStates.Load(ctx, roomValue.RoomID)
	if err != nil {
		manager.logStateError("could not load the table state", roomValue.RoomID, err)
		return false
	}
	if !found {
		return false
	}
	discard := func(reason string, cause error) bool {
		manager.logStateError(reason, roomValue.RoomID, cause)
		if err := manager.tableStates.Delete(ctx, roomValue.RoomID); err != nil {
			manager.logStateError("could not drop the unusable table state", roomValue.RoomID, err)
		}
		return false
	}

	var state runtimeState
	if err := json.Unmarshal(record.State, &state); err != nil {
		return discard("stored table state could not be decoded", cause(err))
	}
	if state.Version != runtimeStateVersion {
		return discard("stored table state has an unsupported version", nil)
	}
	// 房间规则可能在停机期间被改过（盲注、座位数）。用不同的规则继续打
	// 到一半的牌局，会让底池与盲注对不上，宁可作废。
	if state.Engine.Config.SmallBlind != roomValue.Rules.SmallBlind ||
		state.Engine.Config.BigBlind != roomValue.Rules.BigBlind ||
		state.Engine.Config.MaxSeats != roomValue.MaxPlayers {
		return discard("room rules changed while the hand was stored", nil)
	}
	engine, err := holdem.RestoreTable(state.Engine)
	if err != nil {
		return discard("stored table state is not consistent", cause(err))
	}
	if isBetweenHands(engine) {
		// 手间的状态没有恢复价值，成员表已经是权威。
		return discard("stored table state holds no hand in progress", nil)
	}

	created.engine = engine
	created.handStartedAt = state.Manager.HandStartedAt
	created.persistedHandID = state.Manager.PersistedHandID
	created.actions = state.Manager.Actions
	created.lastAction = state.Manager.LastAction
	adoptMap(&created.timeExtensions, state.Manager.TimeExtensions)
	adoptMap(&created.voluntarilyRevealedHands, state.Manager.VoluntarilyRevealedHands)
	adoptMap(&created.privateHoleCardViews, state.Manager.PrivateHoleCardViews)
	adoptMap(&created.pendingCashOuts, state.Manager.PendingCashOuts)
	adoptMap(&created.knownDisplayNames, state.Manager.KnownDisplayNames)
	adoptMap(&created.pendingSpectate, state.Manager.PendingSpectate)
	adoptMap(&created.pendingSeat, state.Manager.PendingSeat)
	adoptMap(&created.spectatorAccess, state.Manager.SpectatorAccess)
	created.spectatorFees = state.Manager.SpectatorFees

	// 行动倒计时必须重新安排：崩溃时那个定时器随进程一起没了，不重建的话
	// 这手牌会永远等一个不会到来的动作，整桌人都动不了。
	//
	// 倒计时从现在重新起算，不沿用崩溃前的截止时间：玩家在服务端停摆期间
	// 无法行动，一上来就判他超时弃牌是替服务端的故障惩罚玩家。
	manager.refreshDeadlineLocked(created)
	manager.logStateInfo("restored a hand in progress", roomValue.RoomID, created.engine.HandID())
	return true
}

// adoptMap 用存下来的内容替换 runtime 上的 map，并保证结果非 nil：
// runtime 的其他代码直接往这些 map 里写，nil 会 panic。
func adoptMap[Key comparable, Value any](target *map[Key]Value, stored map[Key]Value) {
	if stored == nil {
		if *target == nil {
			*target = make(map[Key]Value)
		}
		return
	}
	*target = stored
}

// cause 让 discard 的调用点读起来一致：有的分支有底层错误，有的没有。
func cause(err error) error { return err }

func (manager *Manager) logStateError(message, roomID string, err error) {
	if manager.logger == nil {
		return
	}
	if err != nil {
		manager.logger.Warn(message, "roomId", roomID, "error", err)
		return
	}
	manager.logger.Warn(message, "roomId", roomID)
}

func (manager *Manager) logStateInfo(message, roomID, handID string) {
	if manager.logger == nil {
		return
	}
	manager.logger.Info(message, "roomId", roomID, "handId", handID)
}
