package tablemanager

import (
	"context"
	"testing"

	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/room"
)

// 0 筹码的观战者上桌必须被拒：引擎不接受 0 筹码入座，放行会让「已入座却不在
// 引擎里」的成员被持久化，此后每次同步成员都失败，整桌人都动不了。
func TestTakeSeatWithoutChipsIsRefusedAndTableKeepsWorking(t *testing.T) {
	ctx := context.Background()
	manager, rooms, created := spectatorFixture(t, 3, "owner", "guest", "third")
	if _, err := manager.EnterSpectate(ctx, "third", created.RoomID); err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.SetStack(ctx, created.RoomID, "third", 0); err != nil {
		t.Fatal(err)
	}
	_, err := manager.TakeSeat(ctx, "third", created.RoomID)
	if roomErr, ok := err.(room.Error); !ok || roomErr.Code != "insufficient_chips" {
		t.Fatalf("taking a seat with no chips must be refused with insufficient_chips, got %v", err)
	}
	// 牌桌照常：其他人能准备、能开局，观战者能重连
	inHand := startHand(t, ctx, manager, []string{"owner", "guest"})
	if inHand.Phase != holdem.PhasePreflop {
		t.Fatalf("table must keep working, phase=%s", inHand.Phase)
	}
	if _, err := manager.Join(ctx, "third", created.RoomID); err != nil {
		t.Fatalf("spectator reconnect must work: %v", err)
	}
	// 补码后才能上桌
	if _, err := rooms.SetStack(ctx, created.RoomID, "third", 500); err != nil {
		t.Fatal(err)
	}
	pending, err := manager.TakeSeat(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatalf("with chips the seat request must be accepted (pending mid-hand): %v", err)
	}
	if third := spectatorOf(pending, "third"); third == nil || !third.PendingSeat {
		t.Fatalf("mid-hand take-seat should be pending: %#v", third)
	}
}

// 看牌费把观战者掏空到 0 之后，他手中的「上桌」意向在手结束时必须被放弃，
// 而不是把一个 0 筹码的人塞进座位卡死牌桌。
func TestPendingSeatIsDroppedWhenFeesDrainedTheSpectator(t *testing.T) {
	ctx := context.Background()
	manager, rooms, created := spectatorFixture(t, 3, "owner", "guest", "third")
	fee := int64(room.DefaultSpectatorFeeBigBlinds) * created.Rules.BigBlind
	if _, err := manager.EnterSpectate(ctx, "third", created.RoomID); err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.SetStack(ctx, created.RoomID, "third", fee); err != nil {
		t.Fatal(err)
	}
	inHand := startHand(t, ctx, manager, []string{"owner", "guest"})
	paid, err := manager.Snapshot(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if third := spectatorOf(paid, "third"); third == nil || third.Stack != 0 {
		t.Fatalf("fee should have drained the spectator: %#v", third)
	}
	if _, err := manager.TakeSeat(ctx, "third", created.RoomID); err != nil {
		t.Fatalf("recording the intent mid-hand is fine: %v", err)
	}
	settled := submitFold(t, ctx, manager, created.RoomID, inHand, "fold-1")
	if settled.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("hand did not settle: %s", settled.Phase)
	}
	after, err := manager.Snapshot(ctx, "owner", created.RoomID)
	if err != nil {
		t.Fatalf("snapshot after settlement must not fail: %v", err)
	}
	if seatOf(after, "third") != nil || spectatorOf(after, "third") == nil {
		t.Fatalf("a broke spectator must stay on the bench: seats=%#v", after.Seats)
	}
	// 下一手照常开
	if next := startHand(t, ctx, manager, []string{"owner", "guest"}); next.Phase != holdem.PhasePreflop {
		t.Fatalf("next hand must start, phase=%s", next.Phase)
	}
}

// 免费模式的看牌权也按手发放：手间进观战看不到刚结束那手的盖牌，
// 牌局中途进来的也看不到现场，只有开局时在观战位的人才能看本手。
func TestFreeModeDoesNotRevealTheFinishedOrRunningHand(t *testing.T) {
	ctx := context.Background()
	manager, rooms, created := spectatorFixture(t, 4, "owner", "guest", "third")
	free := room.DefaultSpectatorSettings()
	free.FeeBigBlinds = 0
	if _, err := rooms.UpdateSpectatorSettings(ctx, "owner", free); err != nil {
		t.Fatal(err)
	}
	inHand := startHand(t, ctx, manager, []string{"owner", "guest", "third"})

	// fourth 牌局中途才加入房间（有座位但不参与本手）：立刻进观战，不能看现场
	if _, err := rooms.Join(ctx, room.Participant{UserID: "fourth", DisplayName: "玩家fourth"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Join(ctx, "fourth", created.RoomID); err != nil {
		t.Fatal(err)
	}
	midHand, err := manager.EnterSpectate(ctx, "fourth", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if !midHand.Spectating {
		t.Fatal("a non-participant may switch at once")
	}
	if spectator := spectatorOf(midHand, "fourth"); spectator == nil || spectator.CanSeeHoleCards {
		t.Fatalf("free mode must not expose the running hand to a mid-hand entrant: %#v", spectator)
	}
	for _, seat := range midHand.Seats {
		if len(seat.HoleCards) != 0 {
			t.Fatalf("hole cards leaked mid-hand: %#v", seat)
		}
	}

	// 一名参与者弃牌；手结束后他进观战，不能看到这一手别人盖掉的牌
	actor := inHand.CurrentAction.UserID
	snapshot := submitFold(t, ctx, manager, created.RoomID, inHand, "fold-1")
	for round := 0; snapshot.Phase != holdem.PhaseWaitingNextHand && round < 6; round++ {
		snapshot = submitFold(t, ctx, manager, created.RoomID, snapshot, "fold")
	}
	if snapshot.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("hand did not settle: %s", snapshot.Phase)
	}
	afterHand, err := manager.EnterSpectate(ctx, actor, created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if spectator := spectatorOf(afterHand, actor); spectator == nil || spectator.CanSeeHoleCards {
		t.Fatalf("entering the bench between hands must not reveal the finished hand: %#v", spectator)
	}
	for _, seat := range afterHand.Seats {
		if len(seat.HoleCards) != 0 {
			t.Fatalf("finished hand's hole cards leaked: %#v", seat)
		}
	}
	// 他可以直接坐回去；下一手开局时在观战位的人（fourth）才拿到看牌权
	if _, err := manager.TakeSeat(ctx, actor, created.RoomID); err != nil {
		t.Fatal(err)
	}
	remaining := []string{"owner", "guest", "third"}
	startHand(t, ctx, manager, remaining)
	next, err := manager.Snapshot(ctx, "fourth", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if spectator := spectatorOf(next, "fourth"); spectator == nil || !spectator.CanSeeHoleCards {
		t.Fatalf("free mode grants access at the start of the next hand: %#v", spectator)
	}
	for _, seat := range next.Seats {
		if seat.Participating && len(seat.HoleCards) != 2 {
			t.Fatalf("free spectator present at hand start must see every participant's cards: %#v", seat)
		}
	}
}

// 换座不能牵扯观战者：0 号位不是目标，观战者也不能发起。
func TestSeatChangesNeverInvolveSpectators(t *testing.T) {
	ctx := context.Background()
	manager, rooms, created := spectatorFixture(t, 3, "owner", "guest", "third")
	if _, err := manager.EnterSpectate(ctx, "third", created.RoomID); err != nil {
		t.Fatal(err)
	}
	_, err := manager.RequestSeatChange(ctx, "owner", created.RoomID, 0, "swap-0")
	if roomErr, ok := err.(room.Error); !ok || roomErr.Code != "invalid_seat" {
		t.Fatalf("seat 0 must be rejected as a target, got %v", err)
	}
	_, err = manager.RequestSeatChange(ctx, "third", created.RoomID, 1, "swap-spectator")
	if roomErr, ok := err.(room.Error); !ok || roomErr.Code != "spectator_cannot_move" {
		t.Fatalf("a spectator must not request seat changes, got %v", err)
	}
	// 服务层同样拒绝：即使有人绕过牌桌管理直接交换
	_, err = rooms.SwapSeats(ctx, "owner", "third")
	if roomErr, ok := err.(room.Error); !ok || roomErr.Code != "spectator_cannot_move" {
		t.Fatalf("SwapSeats with a spectator must be refused, got %v", err)
	}
	// 正常的换座仍然可用
	if _, err := manager.RequestSeatChange(ctx, "owner", created.RoomID, 3, "move-free"); err != nil {
		t.Fatalf("moving to a free seat must still work: %v", err)
	}
}

// 观战者不在牌局里，房主在牌局进行中也可以把他移出；上桌玩家仍只能在手间移出。
func TestOwnerMayRemoveSpectatorDuringHand(t *testing.T) {
	ctx := context.Background()
	manager, _, created := spectatorFixture(t, 3, "owner", "guest", "third")
	if _, err := manager.EnterSpectate(ctx, "third", created.RoomID); err != nil {
		t.Fatal(err)
	}
	inHand := startHand(t, ctx, manager, []string{"owner", "guest"})
	if _, err := manager.KickMember(ctx, "owner", "guest"); err == nil {
		t.Fatal("a seated player must not be removable mid-hand")
	}
	if _, err := manager.KickMember(ctx, "owner", "third"); err != nil {
		t.Fatalf("a spectator may be removed mid-hand: %v", err)
	}
	after, err := manager.Snapshot(ctx, "owner", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if spectatorOf(after, "third") != nil || after.Phase != inHand.Phase {
		t.Fatalf("spectator gone, hand untouched: spectators=%#v phase=%s", after.Spectators, after.Phase)
	}
}

// 唯一没准备的人进了观战位：剩下的人已全部准备好，牌局必须立刻开始，
// 而不是一直等一个已经不在座位上的人。
func TestEnteringSpectateStartsTheHandWhenOthersAreReady(t *testing.T) {
	ctx := context.Background()
	manager, _, created := spectatorFixture(t, 3, "owner", "guest", "third")
	for _, userID := range []string{"owner", "guest"} {
		if _, err := manager.SetReady(ctx, userID, true); err != nil {
			t.Fatal(err)
		}
	}
	waiting, err := manager.Snapshot(ctx, "owner", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Phase != holdem.PhaseWaiting {
		t.Fatalf("third has not readied, hand must not have started: %s", waiting.Phase)
	}
	started, err := manager.EnterSpectate(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if started.Phase != holdem.PhasePreflop {
		t.Fatalf("with everyone else ready the hand must start at once, phase=%s", started.Phase)
	}
	if spectator := spectatorOf(started, "third"); spectator == nil || !spectator.CanSeeHoleCards {
		t.Fatalf("the new spectator was on the bench at hand start and pays like anyone else: %#v", spectator)
	}
}
