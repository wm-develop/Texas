package tablemanager

import (
	"context"
	"fmt"
	"testing"

	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/ledger"
	"texas/services/game_server/internal/room"
)

func TestTenVirtualClientsCompleteOneHundredHandsWithoutStateDrift(t *testing.T) {
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{
		UserID: "player_01", DisplayName: "玩家01",
	}, room.PresetStandard, 10, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	userIDs := []string{"player_01"}
	for index := 2; index <= 10; index++ {
		userID := fmt.Sprintf("player_%02d", index)
		userIDs = append(userIDs, userID)
		if _, err := rooms.Join(ctx, room.Participant{
			UserID: userID, DisplayName: fmt.Sprintf("玩家%02d", index),
		}, created.Code, ""); err != nil {
			t.Fatalf("Join %s: %v", userID, err)
		}
	}
	ledgerStore := ledger.NewInMemoryStore()
	historyStore := history.NewInMemoryStore()
	manager, err := NewWithConfig(rooms, zeroRandom{}, ManagerConfig{
		Ledger: ledgerStore, History: historyStore,
	})
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	for _, userID := range userIDs {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatalf("table Join %s: %v", userID, err)
		}
	}

	seenHands := make(map[string]struct{}, 100)
	for handIndex := 0; handIndex < 100; handIndex++ {
		var snapshot Snapshot
		for _, userID := range userIDs {
			snapshot, err = manager.SetReady(ctx, userID, true)
			if err != nil {
				t.Fatalf("hand %d ready %s: %v", handIndex, userID, err)
			}
		}
		if snapshot.Phase != holdem.PhasePreflop || snapshot.CurrentAction == nil {
			t.Fatalf("hand %d did not start: %#v", handIndex, snapshot)
		}

		for actionIndex := 0; snapshot.CurrentAction != nil; actionIndex++ {
			if actionIndex > 20 {
				t.Fatalf("hand %d exceeded action limit", handIndex)
			}
			actor := snapshot.CurrentAction.UserID
			action := holdem.ActionFold
			if snapshot.CurrentAction.Options.CanCheck {
				action = holdem.ActionCheck
			}
			_, snapshot, err = manager.SubmitAction(ctx, actor, created.RoomID, holdem.ActionRequest{
				ActionID: fmt.Sprintf("hand-%d-action-%d", handIndex, actionIndex),
				HandID:   snapshot.HandID, TableRevision: snapshot.TableRevision, Action: action,
			})
			if err != nil {
				t.Fatalf("hand %d action %d: %v", handIndex, actionIndex, err)
			}
		}
		if snapshot.Phase != holdem.PhaseWaitingNextHand || snapshot.Settlement == nil {
			t.Fatalf("hand %d did not settle: %#v", handIndex, snapshot)
		}
		if _, duplicate := seenHands[snapshot.HandID]; duplicate {
			t.Fatalf("duplicate hand ID %s", snapshot.HandID)
		}
		seenHands[snapshot.HandID] = struct{}{}
		entries := ledgerStore.EntriesForHand(snapshot.HandID)
		if len(entries) != 10 {
			t.Fatalf("hand %d ledger entries=%d", handIndex, len(entries))
		}
		var delta int64
		for _, entry := range entries {
			delta += entry.Delta
		}
		if delta != 0 {
			t.Fatalf("hand %d ledger delta=%d", handIndex, delta)
		}
	}

	for _, userID := range userIDs {
		if recent := historyStore.RecentForPlayer(userID, 100); len(recent) != 100 {
			t.Fatalf("%s recent hands=%d", userID, len(recent))
		}
	}
	finalSnapshot, err := manager.Snapshot(ctx, userIDs[0], created.RoomID)
	if err != nil {
		t.Fatalf("final Snapshot: %v", err)
	}
	var total int64
	for _, seat := range finalSnapshot.Seats {
		total += seat.Stack
	}
	if total != 10*created.Rules.StartingChips {
		t.Fatalf("final chips=%d want=%d", total, 10*created.Rules.StartingChips)
	}
}
