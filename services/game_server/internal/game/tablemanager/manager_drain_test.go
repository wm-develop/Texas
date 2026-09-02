package tablemanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/room"
)

func drainFixture(t *testing.T) (context.Context, *Manager, room.Room, *[]string) {
	t.Helper()
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.PresetStandard, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.Join(ctx, room.Participant{UserID: "guest", DisplayName: "好友"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	manager, err := New(rooms, zeroRandom{})
	if err != nil {
		t.Fatal(err)
	}
	notified := &[]string{}
	manager.SetSnapshotListener(func(roomID string) { *notified = append(*notified, roomID) })
	for _, userID := range []string{"owner", "guest"} {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, manager, created, notified
}

// 优雅停机的核心承诺：进行中的手打完，之后不再开新局。
func TestDrainFinishesCurrentHandAndBlocksNewOnes(t *testing.T) {
	ctx, manager, created, notified := drainFixture(t)
	inHand := startHand(t, ctx, manager, []string{"owner", "guest"})
	if manager.TablesInHand() != 1 {
		t.Fatalf("TablesInHand=%d, expected 1 during a hand", manager.TablesInHand())
	}
	if manager.Draining() {
		t.Fatal("must not be draining before BeginDrain")
	}

	before := len(*notified)
	manager.BeginDrain()
	manager.BeginDrain() // 幂等
	if !manager.Draining() {
		t.Fatal("Draining must report true after BeginDrain")
	}
	if len(*notified) <= before {
		t.Fatal("BeginDrain must broadcast a snapshot so clients can show the reason")
	}

	// 进行中的手不受影响，等待必须超时
	waitContext, cancel := context.WithTimeout(ctx, 60*time.Millisecond)
	err := manager.WaitForHandBoundary(waitContext)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForHandBoundary during a hand: err=%v, expected deadline exceeded", err)
	}
	mid, err := manager.Snapshot(ctx, "owner", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if !mid.Draining || mid.Phase != holdem.PhasePreflop {
		t.Fatalf("mid-hand snapshot should keep the hand and flag draining: %#v", mid.Phase)
	}

	// 手内动作照常，手结束后进入空档
	settled := submitFold(t, ctx, manager, created.RoomID, inHand, "fold-1")
	if settled.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("expected WAITING_NEXT_HAND, got %s", settled.Phase)
	}
	if !settled.Draining {
		t.Fatal("settled snapshot must still flag draining")
	}
	if settled.AutoReadyDeadline != 0 {
		t.Fatal("no auto-ready countdown may be scheduled while draining")
	}
	if manager.TablesInHand() != 0 {
		t.Fatalf("TablesInHand=%d after settlement", manager.TablesInHand())
	}
	if err := manager.WaitForHandBoundary(ctx); err != nil {
		t.Fatalf("WaitForHandBoundary after settlement: %v", err)
	}

	// 不允许开新局，但允许取消准备
	_, err = manager.SetReady(ctx, "owner", true)
	var ruleError holdem.RuleError
	if !errors.As(err, &ruleError) || ruleError.Code != "server_draining" {
		t.Fatalf("SetReady(true) while draining: err=%v, expected server_draining", err)
	}
	if _, err := manager.SetReady(ctx, "owner", false); err != nil {
		t.Fatalf("SetReady(false) must stay allowed while draining: %v", err)
	}
	if manager.TablesInHand() != 0 {
		t.Fatal("no hand may start while draining")
	}
}

// 排空开始时已经在倒计时的自动准备必须取消，否则倒计时结束只会撞上拒绝。
func TestBeginDrainCancelsPendingAutoReady(t *testing.T) {
	ctx, manager, created, _ := drainFixture(t)
	inHand := startHand(t, ctx, manager, []string{"owner", "guest"})
	settled := submitFold(t, ctx, manager, created.RoomID, inHand, "fold-1")
	if settled.AutoReadyDeadline == 0 {
		t.Fatal("precondition: settlement schedules an auto-ready countdown")
	}

	manager.BeginDrain()
	after, err := manager.Snapshot(ctx, "guest", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if after.AutoReadyDeadline != 0 || !after.Draining {
		t.Fatalf("after BeginDrain: deadline=%d draining=%v", after.AutoReadyDeadline, after.Draining)
	}
}

// 没有牌桌或所有牌桌都在空档时，等待立即返回，停机不会白等。
func TestWaitForHandBoundaryReturnsImmediatelyWhenIdle(t *testing.T) {
	ctx, manager, _, _ := drainFixture(t)
	manager.BeginDrain()
	waitContext, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	started := time.Now()
	if err := manager.WaitForHandBoundary(waitContext); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 500*time.Millisecond {
		t.Fatal("idle tables must not delay shutdown")
	}
}
