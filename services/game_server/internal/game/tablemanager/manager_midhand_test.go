package tablemanager

import (
	"context"
	cryptorand "crypto/rand"
	"testing"
	"time"

	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/room"
	"texas/services/game_server/internal/security"
)

func startHand(t *testing.T, ctx context.Context, manager *Manager, userIDs []string) Snapshot {
	t.Helper()
	var snapshot Snapshot
	var err error
	for _, userID := range userIDs {
		snapshot, err = manager.SetReady(ctx, userID, true)
		if err != nil {
			t.Fatalf("SetReady %s: %v", userID, err)
		}
	}
	if snapshot.Phase != holdem.PhasePreflop {
		t.Fatalf("expected PREFLOP after all ready, phase=%s", snapshot.Phase)
	}
	return snapshot
}

func submitFold(t *testing.T, ctx context.Context, manager *Manager, roomID string, snapshot Snapshot, actionID string) Snapshot {
	t.Helper()
	if snapshot.CurrentAction == nil {
		t.Fatalf("no current action in snapshot phase=%s", snapshot.Phase)
	}
	actor := snapshot.CurrentAction.UserID
	_, next, err := manager.SubmitAction(ctx, actor, roomID, holdem.ActionRequest{
		ActionID: actionID, HandID: snapshot.HandID, TableRevision: snapshot.TableRevision,
		Action: holdem.ActionFold,
	})
	if err != nil {
		t.Fatalf("fold by %s: %v", actor, err)
	}
	return next
}

// A player joining the room mid-hand must not poison snapshots for the whole
// table. They wait as a pending spectator and take an engine seat once the
// hand settles.
func TestMidHandJoinKeepsSnapshotsWorkingAndSeatsPlayerNextHand(t *testing.T) {
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.PresetStandard, 3, "")
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
	for _, userID := range []string{"owner", "guest"} {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	inHand := startHand(t, ctx, manager, []string{"owner", "guest"})

	if _, err := rooms.Join(ctx, room.Participant{UserID: "third", DisplayName: "三号"}, created.Code, ""); err != nil {
		t.Fatalf("room join mid-hand: %v", err)
	}
	thirdSnapshot, err := manager.Join(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatalf("manager Join mid-hand must succeed: %v", err)
	}
	if thirdSnapshot.Phase != holdem.PhasePreflop {
		t.Fatalf("third should observe the running hand, phase=%s", thirdSnapshot.Phase)
	}

	// Every existing player's snapshot keeps working and shows the newcomer as
	// a pending, non-participating seat.
	for _, userID := range []string{"owner", "guest", "third"} {
		snapshot, err := manager.Snapshot(ctx, userID, created.RoomID)
		if err != nil {
			t.Fatalf("Snapshot %s during hand: %v", userID, err)
		}
		var pending *SeatSnapshot
		for index := range snapshot.Seats {
			if snapshot.Seats[index].UserID == "third" {
				pending = &snapshot.Seats[index]
			}
		}
		if pending == nil || pending.Participating {
			t.Fatalf("snapshot for %s should list third as pending seat, got %#v", userID, snapshot.Seats)
		}
	}
	// The newcomer must not be able to start acting or see hole cards mid-hand.
	if len(thirdSnapshot.HoleCards) != 0 {
		t.Fatalf("pending member must not receive hole cards: %#v", thirdSnapshot.HoleCards)
	}

	// Finish the hand with a fold; afterwards the newcomer takes a real seat.
	settled := submitFold(t, ctx, manager, created.RoomID, inHand, "fold-1")
	if settled.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("expected WAITING_NEXT_HAND, got %s", settled.Phase)
	}
	afterHand, err := manager.Snapshot(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatalf("Snapshot third after hand: %v", err)
	}
	if len(afterHand.Seats) != 3 {
		t.Fatalf("expected 3 seats after hand, got %#v", afterHand.Seats)
	}
	next := startHand(t, ctx, manager, []string{"owner", "guest", "third"})
	participating := 0
	for _, seat := range next.Seats {
		if seat.Participating {
			participating++
		}
	}
	if participating != 3 {
		t.Fatalf("expected 3 participants in next hand, got %d", participating)
	}
}

// A folded player may leave mid-hand. Their wallet refund happens after the
// settlement so pot chips are not paid out twice, and total chips stay
// conserved.
func TestFoldedPlayerLeavesMidHandWithDeferredConservedCashOut(t *testing.T) {
	ctx := context.Background()
	chips, err := bankroll.NewService(bankroll.NewMemoryRepository(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	users := []string{"owner", "guestA", "guestB"}
	for _, userID := range users {
		if _, err := chips.TopUp(ctx, userID, "topup-"+userID, 5_000); err != nil {
			t.Fatal(err)
		}
	}
	hasher, err := security.NewPasswordHasher(1_000, cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rooms, err := room.NewService(room.NewMemoryRepository(), hasher, room.ServiceConfig{Bankroll: chips})
	if err != nil {
		t.Fatal(err)
	}
	created, err := rooms.CreateConfigured(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.CreateOptions{
		Preset: room.PresetStandard, MaxPlayers: 3, SmallBlind: 10, BigBlind: 20,
		MaxBuyIn: 2_000, BuyIn: 1_000, RequestID: "create-owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{"guestA", "guestB"} {
		if _, err := rooms.JoinWithBuyIn(ctx, room.Participant{UserID: userID, DisplayName: userID}, room.JoinOptions{
			Code: created.Code, BuyIn: 1_000, RequestID: "join-" + userID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := NewWithConfig(rooms, zeroRandom{}, ManagerConfig{Bankroll: chips})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range users {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	inHand := startHand(t, ctx, manager, users)

	// A participating, unfolded player must still be blocked from leaving.
	actor := inHand.CurrentAction.UserID
	if _, err := manager.Leave(ctx, actor); err == nil {
		t.Fatal("unfolded participant should not leave mid-hand")
	}

	afterFold := submitFold(t, ctx, manager, created.RoomID, inHand, "fold-leaver")
	if afterFold.Phase == holdem.PhaseWaitingNextHand {
		t.Fatalf("hand should continue with two players, phase=%s", afterFold.Phase)
	}
	var leaverStack int64 = -1
	for _, seat := range afterFold.Seats {
		if seat.UserID == actor {
			leaverStack = seat.Stack
		}
	}
	if leaverStack < 0 {
		t.Fatalf("folded player missing from snapshot: %#v", afterFold.Seats)
	}
	walletBefore, err := chips.Snapshot(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}

	closed, err := manager.Leave(ctx, actor)
	if err != nil {
		t.Fatalf("folded player should leave mid-hand: %v", err)
	}
	if closed {
		t.Fatal("room must stay open")
	}
	if _, err := rooms.Current(ctx, actor); err == nil {
		t.Fatal("leaver should no longer be in a room")
	}
	// Cash-out must not have happened yet: the wallet is unchanged until the
	// settlement writes post-hand stacks.
	walletMid, err := chips.Snapshot(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	if walletMid.WalletChips != walletBefore.WalletChips {
		t.Fatalf("wallet changed before settlement: %d -> %d", walletBefore.WalletChips, walletMid.WalletChips)
	}

	// Finish the hand: the next actor folds, the last player wins. Leaving
	// bumped the table revision, so fetch a fresh snapshot first.
	current, err := manager.Snapshot(ctx, afterFold.CurrentAction.UserID, created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	settled := submitFold(t, ctx, manager, created.RoomID, current, "fold-second")
	if settled.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("expected settlement, phase=%s", settled.Phase)
	}

	walletAfter, err := chips.Snapshot(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	if walletAfter.WalletChips != walletBefore.WalletChips+leaverStack {
		t.Fatalf(
			"leaver wallet=%d, expected %d (before %d + stack %d)",
			walletAfter.WalletChips, walletBefore.WalletChips+leaverStack,
			walletBefore.WalletChips, leaverStack,
		)
	}
	if walletAfter.TableChips != 0 {
		t.Fatalf("leaver table balance should be zero, got %d", walletAfter.TableChips)
	}
	// Chip conservation across all three accounts: only the top-ups created
	// chips.
	var total int64
	for _, userID := range users {
		snapshot, err := chips.Snapshot(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		total += snapshot.WalletChips + snapshot.TableChips
	}
	if total != 15_000 {
		t.Fatalf("total chips=%d, expected 15000", total)
	}
}

// A folded requester may ask to view another folded player's hole cards, not
// just players still contesting the pot.
func TestFoldedPlayerCanRequestViewOfAnotherFoldedPlayer(t *testing.T) {
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.PresetStandard, 4, "")
	if err != nil {
		t.Fatal(err)
	}
	users := []string{"owner", "guestA", "guestB", "guestC"}
	for _, userID := range users[1:] {
		if _, err := rooms.Join(ctx, room.Participant{UserID: userID, DisplayName: userID}, created.Code, ""); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := New(rooms, zeroRandom{})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range users {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	inHand := startHand(t, ctx, manager, users)
	firstFolder := inHand.CurrentAction.UserID
	afterFirst := submitFold(t, ctx, manager, created.RoomID, inHand, "fold-a")
	secondFolder := afterFirst.CurrentAction.UserID
	afterSecond := submitFold(t, ctx, manager, created.RoomID, afterFirst, "fold-b")
	if afterSecond.Phase == holdem.PhaseWaitingNextHand {
		t.Fatalf("hand should continue with two live players, phase=%s", afterSecond.Phase)
	}

	if _, err := manager.RequestHoleCardView(ctx, firstFolder, created.RoomID, secondFolder, "view-folded"); err != nil {
		t.Fatalf("folded requester should be able to ask a folded target: %v", err)
	}
	accepted, err := manager.RespondHoleCardView(ctx, secondFolder, created.RoomID, "view-folded", true)
	if err != nil {
		t.Fatalf("folded target should be able to accept: %v", err)
	}
	_ = accepted
	requesterView, err := manager.Snapshot(ctx, firstFolder, created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, revealed := range requesterView.PrivateReveals {
		if revealed.PlayerID == secondFolder && len(revealed.HoleCards) == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("requester should see folded target's cards: %#v", requesterView.PrivateReveals)
	}
	// The reveal must stay private to the requester.
	otherView, err := manager.Snapshot(ctx, "owner", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if firstFolder != "owner" && len(otherView.PrivateReveals) != 0 {
		t.Fatalf("bystander must not see the private reveal: %#v", otherView.PrivateReveals)
	}
}

// Seat swap consent no longer requires a full table: any occupied target seat
// can be asked for a swap.
func TestSeatSwapRequestWorksWithoutFullTable(t *testing.T) {
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.PresetStandard, 6, "")
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
	for _, userID := range []string{"owner", "guest"} {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	guestSeat := 0
	snapshot, err := manager.Snapshot(ctx, "owner", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	for _, seat := range snapshot.Seats {
		if seat.UserID == "guest" {
			guestSeat = seat.Seat
		}
	}
	if _, err := manager.RequestSeatChange(ctx, "owner", created.RoomID, guestSeat, "swap-sparse"); err != nil {
		t.Fatalf("swap request on a non-full table should work: %v", err)
	}
	accepted, err := manager.RespondSeatSwap(ctx, "guest", created.RoomID, "swap-sparse", true)
	if err != nil {
		t.Fatal(err)
	}
	seats := map[string]int{}
	for _, seat := range accepted.Seats {
		seats[seat.UserID] = seat.Seat
	}
	if seats["owner"] != guestSeat {
		t.Fatalf("owner should now hold seat %d, got %#v", guestSeat, seats)
	}
}
