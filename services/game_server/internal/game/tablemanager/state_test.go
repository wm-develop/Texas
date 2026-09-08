package tablemanager

import (
	"context"
	"encoding/json"
	"testing"

	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/room"
	"texas/services/game_server/internal/tablestate"
)

// restartFixture 建一个带状态存储的牌桌，并返回「重启」用的构造器。
//
// 重启 = 丢掉整个 Manager（所有内存状态随之消失），用同一个房间服务与同一个
// 状态存储新建一个。这正是进程崩溃后发生的事：数据库还在，内存没了。
func restartFixture(t *testing.T, userIDs ...string) (
	*Manager, *room.Service, room.Room, tablestate.Store, func(*testing.T) *Manager,
) {
	t.Helper()
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: userIDs[0], DisplayName: "玩家" + userIDs[0]}, room.PresetStandard, 6, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range userIDs[1:] {
		if _, err := rooms.Join(ctx, room.Participant{UserID: userID, DisplayName: "玩家" + userID}, created.Code, ""); err != nil {
			t.Fatal(err)
		}
	}
	states := tablestate.NewMemoryStore()
	build := func(t *testing.T) *Manager {
		t.Helper()
		manager, err := NewWithConfig(rooms, zeroRandom{}, ManagerConfig{TableStates: states})
		if err != nil {
			t.Fatal(err)
		}
		for _, userID := range userIDs {
			if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
				t.Fatal(err)
			}
		}
		return manager
	}
	return build(t), rooms, created, states, build
}

// 崩溃前打到一半的这手牌，重启后必须原样继续，而不是作废。
func TestHandInProgressSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	manager, _, created, states, restart := restartFixture(t, "owner", "guest", "third")
	before := startHand(t, ctx, manager, []string{"owner", "guest", "third"})
	if before.Phase != holdem.PhasePreflop {
		t.Fatalf("hand should be running, got %s", before.Phase)
	}
	// 先让一个人行动，制造出「打到一半」的状态
	before = submitFold(t, ctx, manager, created.RoomID, before, "fold-before-restart")

	manager.waitForStatePersistence()
	if _, found, err := states.Load(ctx, created.RoomID); err != nil || !found {
		t.Fatalf("a running hand must be persisted: found=%v err=%v", found, err)
	}

	// 进程重启：旧 Manager 连同全部内存状态消失
	revived := restart(t)
	after, err := revived.Snapshot(ctx, "owner", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if after.HandID != before.HandID {
		t.Fatalf("the same hand must continue: before=%s after=%s", before.HandID, after.HandID)
	}
	if after.Phase != before.Phase {
		t.Fatalf("phase changed across the restart: before=%s after=%s", before.Phase, after.Phase)
	}
	if after.CurrentAction == nil || before.CurrentAction == nil ||
		after.CurrentAction.UserID != before.CurrentAction.UserID {
		t.Fatalf("the same player must be to act: before=%#v after=%#v", before.CurrentAction, after.CurrentAction)
	}
	if after.TotalPot != before.TotalPot {
		t.Fatalf("pot changed across the restart: before=%d after=%d", before.TotalPot, after.TotalPot)
	}
	if len(after.Board) != len(before.Board) {
		t.Fatalf("board changed across the restart: before=%v after=%v", before.Board, after.Board)
	}
	for _, seat := range after.Seats {
		var matched bool
		for _, original := range before.Seats {
			if original.UserID != seat.UserID {
				continue
			}
			matched = true
			if seat.Stack != original.Stack || seat.TotalBet != original.TotalBet ||
				seat.Folded != original.Folded || seat.TimeExtensions != original.TimeExtensions {
				t.Fatalf("seat changed across the restart: before=%#v after=%#v", original, seat)
			}
		}
		if !matched {
			t.Fatalf("seat appeared out of nowhere: %#v", seat)
		}
	}
	// 本人的手牌也要还在：恢复的目的就是让这手牌照原样打完
	asOwner, err := revived.Snapshot(ctx, "owner", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if len(asOwner.HoleCards) != 2 {
		t.Fatalf("the player must get their own cards back: %#v", asOwner.HoleCards)
	}
}

// 恢复之后这手牌能正常打完，筹码守恒，并且状态记录被清掉。
func TestRestoredHandSettlesAndClearsItsState(t *testing.T) {
	ctx := context.Background()
	manager, _, created, states, restart := restartFixture(t, "owner", "guest", "third")
	inHand := startHand(t, ctx, manager, []string{"owner", "guest", "third"})
	chipsBefore := totalChipsOnTable(inHand)

	// 恢复的前提是状态已经落盘。真实崩溃时若最后一次写入没来得及完成，
	// 恢复的就是稍早的状态或直接作废，那是设计内的降级。
	manager.waitForStatePersistence()
	revived := restart(t)
	snapshot, err := revived.Snapshot(ctx, "owner", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; snapshot.Phase != holdem.PhaseWaitingNextHand && round < 8; round++ {
		snapshot = submitFold(t, ctx, revived, created.RoomID, snapshot, "fold-after-restart")
	}
	if snapshot.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("the restored hand did not settle: %s", snapshot.Phase)
	}
	if got := totalChipsOnTable(snapshot); got != chipsBefore {
		t.Fatalf("chips must be conserved across the restart: before=%d after=%d", chipsBefore, got)
	}
	if snapshot.Settlement == nil {
		t.Fatal("the restored hand must produce a settlement")
	}
	revived.waitForStatePersistence()
	if _, found, err := states.Load(ctx, created.RoomID); err != nil || found {
		t.Fatalf("the state must be cleared once the hand ends: found=%v err=%v", found, err)
	}
}

// 手间不保存：那时的权威状态在房间成员表里，多存一份只会多一处可能不一致。
func TestNothingIsStoredBetweenHands(t *testing.T) {
	ctx := context.Background()
	manager, _, created, states, _ := restartFixture(t, "owner", "guest")
	manager.waitForStatePersistence()
	if _, found, err := states.Load(ctx, created.RoomID); err != nil || found {
		t.Fatalf("a fresh table stores nothing: found=%v err=%v", found, err)
	}
	inHand := startHand(t, ctx, manager, []string{"owner", "guest"})
	settled := submitFold(t, ctx, manager, created.RoomID, inHand, "fold-1")
	if settled.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("hand did not settle: %s", settled.Phase)
	}
	manager.waitForStatePersistence()
	if _, found, err := states.Load(ctx, created.RoomID); err != nil || found {
		t.Fatalf("nothing may linger between hands: found=%v err=%v", found, err)
	}
}

// 装不回去的状态一律放弃并清除，退回「本手作废」的旧行为——绝不能拿一个
// 不确定的状态继续发牌。
func TestUnusableStateIsDiscardedInsteadOfDealt(t *testing.T) {
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.PresetStandard, 6, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.Join(ctx, room.Participant{UserID: "guest", DisplayName: "客人"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	for name, payload := range map[string]string{
		"不是合法的 JSON": `{`,
		"版本对不上":      `{"version":99,"engine":{}}`,
		"引擎状态内部矛盾":   `{"version":1,"engine":{"version":1,"phase":"PREFLOP"}}`,
		"存的是手间的状态":   `{"version":1,"engine":{"version":1,"phase":"WAITING_NEXT_HAND","config":{"TableID":"x","MaxSeats":6,"SmallBlind":10,"BigBlind":20}}}`,
	} {
		states := tablestate.NewMemoryStore()
		if err := states.Save(ctx, tablestate.Record{
			RoomID: created.RoomID, HandID: "hand_x", State: []byte(payload),
		}); err != nil {
			t.Fatal(err)
		}
		manager, err := NewWithConfig(rooms, zeroRandom{}, ManagerConfig{TableStates: states})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Join(ctx, "owner", created.RoomID); err != nil {
			t.Fatalf("%s：加入房间不能因为坏状态而失败：%v", name, err)
		}
		snapshot, err := manager.Snapshot(ctx, "owner", created.RoomID)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !isBetweenHandsPhase(snapshot.Phase) {
			t.Fatalf("%s：必须退回手间，而不是拿坏状态开牌，phase=%s", name, snapshot.Phase)
		}
		manager.waitForStatePersistence()
		manager.waitForStatePersistence()
		if _, found, err := states.Load(ctx, created.RoomID); err != nil || found {
			t.Fatalf("%s：装不回去的状态必须被清掉，否则每次进房间都重复失败", name)
		}
	}
}

// 房间规则在停机期间被改过时不恢复：拿不同的盲注继续打到一半的牌局，
// 底池与盲注会对不上，比作废一手更糟。
func TestChangedBlindsPreventRestore(t *testing.T) {
	ctx := context.Background()
	manager, _, created, states, restart := restartFixture(t, "owner", "guest")
	startHand(t, ctx, manager, []string{"owner", "guest"})

	manager.waitForStatePersistence()
	record, found, err := states.Load(ctx, created.RoomID)
	if err != nil || !found {
		t.Fatalf("the running hand should be stored: found=%v err=%v", found, err)
	}
	// 直接把存下来的盲注改掉，等价于停机期间房间规则被改
	var stored map[string]any
	if err := json.Unmarshal(record.State, &stored); err != nil {
		t.Fatal(err)
	}
	engine := stored["engine"].(map[string]any)
	config := engine["config"].(map[string]any)
	config["BigBlind"] = float64(50)
	if record.State, err = json.Marshal(stored); err != nil {
		t.Fatal(err)
	}
	if err := states.Save(ctx, record); err != nil {
		t.Fatal(err)
	}

	revived := restart(t)
	snapshot, err := revived.Snapshot(ctx, "owner", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if !isBetweenHandsPhase(snapshot.Phase) {
		t.Fatalf("blinds changed, the stored hand must not resume: %s", snapshot.Phase)
	}
	manager.waitForStatePersistence()
	revived.waitForStatePersistence()
	if _, found, err := states.Load(ctx, created.RoomID); err != nil || found {
		t.Fatal("the mismatched state must be cleared")
	}
}

// 没有配置状态存储时，行为与 0.5.0 之前完全一致：不保存，崩溃后作废。
func TestWithoutAStoreNothingChanges(t *testing.T) {
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.PresetStandard, 6, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.Join(ctx, room.Participant{UserID: "guest", DisplayName: "客人"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	manager, err := New(rooms, zeroRandom{})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{"owner", "guest"} {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	inHand := startHand(t, ctx, manager, []string{"owner", "guest"})
	if inHand.Phase != holdem.PhasePreflop {
		t.Fatalf("a table without a state store must still deal: %s", inHand.Phase)
	}
	settled := submitFold(t, ctx, manager, created.RoomID, inHand, "fold-1")
	if settled.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("hand did not settle: %s", settled.Phase)
	}
}

func isBetweenHandsPhase(phase holdem.Phase) bool {
	return phase == holdem.PhaseWaiting || phase == holdem.PhaseWaitingNextHand
}
