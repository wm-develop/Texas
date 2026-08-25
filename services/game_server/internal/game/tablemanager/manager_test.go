package tablemanager

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/ledger"
	"texas/services/game_server/internal/room"
	"texas/services/game_server/internal/security"
)

type zeroRandom struct{}

func (zeroRandom) Intn(int) (int, error) { return 0, nil }

type fakeScheduledTimer struct{ stopped bool }

func (timer *fakeScheduledTimer) Stop() bool {
	wasActive := !timer.stopped
	timer.stopped = true
	return wasActive
}

func TestManagerStartsRealHandAndProducesPrivateSnapshots(t *testing.T) {
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.PresetStandard, 2, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := rooms.Join(ctx, room.Participant{UserID: "guest", DisplayName: "好友"}, created.Code, ""); err != nil {
		t.Fatalf("Join: %v", err)
	}
	ledgerStore := ledger.NewInMemoryStore()
	historyStore := history.NewInMemoryStore()
	manager, err := NewWithConfig(rooms, zeroRandom{}, ManagerConfig{
		Ledger: ledgerStore, History: historyStore,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := manager.Join(ctx, "owner", created.RoomID); err != nil {
		t.Fatalf("owner Join: %v", err)
	}
	if _, err := manager.Join(ctx, "guest", created.RoomID); err != nil {
		t.Fatalf("guest Join: %v", err)
	}
	ownerWaiting, err := manager.SetReady(ctx, "owner", true)
	if err != nil || ownerWaiting.Phase != holdem.PhaseWaiting {
		t.Fatalf("owner ready snapshot=%#v error=%v", ownerWaiting, err)
	}
	guestSnapshot, err := manager.SetReady(ctx, "guest", true)
	if err != nil {
		t.Fatalf("guest ready: %v", err)
	}
	if guestSnapshot.Phase != holdem.PhasePreflop || len(guestSnapshot.HoleCards) != 2 || guestSnapshot.CurrentAction == nil {
		t.Fatalf("guest snapshot=%#v", guestSnapshot)
	}
	ownerSnapshot, err := manager.Snapshot(ctx, "owner", created.RoomID)
	if err != nil || len(ownerSnapshot.HoleCards) != 2 {
		t.Fatalf("owner snapshot=%#v error=%v", ownerSnapshot, err)
	}
	ownerJSON, err := json.Marshal(ownerSnapshot)
	if err != nil {
		t.Fatalf("Marshal owner snapshot: %v", err)
	}
	for _, guestCard := range guestSnapshot.HoleCards {
		if strings.Contains(string(ownerJSON), `"`+guestCard+`"`) {
			t.Fatalf("owner snapshot leaked guest card %s: %s", guestCard, ownerJSON)
		}
	}
	if !strings.Contains(string(ownerJSON), `"position":"BTN/SB"`) ||
		!strings.Contains(string(ownerJSON), `"toCall":`) ||
		strings.Contains(string(ownerJSON), `"ToCall":`) {
		t.Fatalf("snapshot V2 JSON contract missing: %s", ownerJSON)
	}

	actor := ownerSnapshot.CurrentAction.UserID
	actorSnapshot := ownerSnapshot
	if actor == "guest" {
		actorSnapshot = guestSnapshot
	}
	result, settled, err := manager.SubmitAction(ctx, actor, created.RoomID, holdem.ActionRequest{
		ActionID:      "fold_1",
		HandID:        actorSnapshot.HandID,
		TableRevision: actorSnapshot.TableRevision,
		Action:        holdem.ActionFold,
	})
	if err != nil {
		t.Fatalf("SubmitAction: %v", err)
	}
	if !result.HandEnded || settled.Phase != holdem.PhaseWaitingNextHand || settled.Settlement == nil {
		t.Fatalf("result=%#v snapshot=%#v", result, settled)
	}
	for _, seat := range settled.Seats {
		if seat.Ready {
			t.Fatalf("ready was not reset after settlement: %#v", settled.Seats)
		}
	}
	if entries := ledgerStore.EntriesForHand(settled.HandID); len(entries) != 2 {
		t.Fatalf("persisted ledger entries=%#v", entries)
	}
	recent := historyStore.RecentForPlayer(actor, 20)
	if len(recent) != 1 || recent[0].HandID != settled.HandID {
		t.Fatalf("recent hands=%#v", recent)
	}
	for _, player := range recent[0].Players {
		if player.UserID != actor && len(player.HoleCards) != 0 {
			t.Fatalf("folded opponent cards leaked in history: %#v", player)
		}
	}
}

func TestManagerRejectsNonMember(t *testing.T) {
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.PresetCasual, 2, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	manager, err := New(rooms, zeroRandom{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := manager.Join(ctx, "outsider", created.RoomID); err == nil {
		t.Fatal("non-member joined table")
	}
}

func TestPositionsCoverEveryTableSizeFromTwoToTen(t *testing.T) {
	expected := map[int][]string{
		2:  {"BTN/SB", "BB"},
		3:  {"BTN", "SB", "BB"},
		4:  {"BTN", "SB", "BB", "UTG"},
		5:  {"BTN", "SB", "BB", "UTG", "CO"},
		6:  {"BTN", "SB", "BB", "UTG", "HJ", "CO"},
		7:  {"BTN", "SB", "BB", "UTG", "MP", "HJ", "CO"},
		8:  {"BTN", "SB", "BB", "UTG", "UTG+1", "MP", "HJ", "CO"},
		9:  {"BTN", "SB", "BB", "UTG", "UTG+1", "MP", "LJ", "HJ", "CO"},
		10: {"BTN", "SB", "BB", "UTG", "UTG+1", "UTG+2", "MP", "LJ", "HJ", "CO"},
	}
	for playerCount := 2; playerCount <= 10; playerCount++ {
		players := make([]holdem.Player, 0, playerCount)
		for seat := 1; seat <= playerCount; seat++ {
			players = append(players, holdem.Player{
				PlayerID: "player_" + string(rune('A'+seat-1)), Seat: seat, Participating: true,
			})
		}
		positions := positionsFor(players, 1, playerCount)
		for index, wanted := range expected[playerCount] {
			if got := positions[players[index].PlayerID]; got != wanted {
				t.Fatalf("players=%d seat=%d position=%q want=%q", playerCount, index+1, got, wanted)
			}
		}
	}
}

func TestPositionsSkipEmptySeatsClockwise(t *testing.T) {
	players := []holdem.Player{
		{PlayerID: "dealer", Seat: 2, Participating: true},
		{PlayerID: "small", Seat: 5, Participating: true},
		{PlayerID: "big", Seat: 9, Participating: true},
		{PlayerID: "under", Seat: 10, Participating: true},
	}
	positions := positionsFor(players, 2, 10)
	if positions["dealer"] != "BTN" || positions["small"] != "SB" ||
		positions["big"] != "BB" || positions["under"] != "UTG" {
		t.Fatalf("positions=%#v", positions)
	}
}

func TestBetSuggestionsIncludeRequestedPotFractionsAndAllIn(t *testing.T) {
	players := []holdem.Player{{PlayerID: "actor", StreetBet: 10, Stack: 990}}
	options := holdem.ActionOptions{
		ToCall: 10, CanRaise: true, CanAllIn: true, MinRaiseTo: 40, MaxRaiseTo: 1000,
	}
	suggestions := betSuggestions(30, players, "actor", options)
	labels := []string{"quarter_pot", "third_pot", "half_pot", "two_thirds_pot", "pot", "overbet_120", "all_in"}
	if len(suggestions) != len(labels) {
		t.Fatalf("suggestions=%#v", suggestions)
	}
	for index, label := range labels {
		if suggestions[index].Label != label {
			t.Fatalf("suggestion %d=%#v want label %s", index, suggestions[index], label)
		}
		if suggestions[index].RaiseTo < options.MinRaiseTo && suggestions[index].Action != holdem.ActionAllIn {
			t.Fatalf("suggestion below minimum: %#v", suggestions[index])
		}
	}
	if suggestions[5].RaiseTo != 68 || suggestions[6].RaiseTo != 1000 {
		t.Fatalf("overbet/all-in suggestions=%#v", suggestions)
	}
}

func TestActionDeadlineTimesOutThroughAuthoritativeStateMachine(t *testing.T) {
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.PresetStandard, 2, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := rooms.Join(ctx, room.Participant{UserID: "guest", DisplayName: "好友"}, created.Code, ""); err != nil {
		t.Fatalf("Join: %v", err)
	}
	now := time.Unix(1_000, 0)
	var scheduled func()
	manager, err := NewWithConfig(rooms, zeroRandom{}, ManagerConfig{
		Now: func() time.Time { return now },
		AfterFunc: func(_ time.Duration, callback func()) ScheduledTimer {
			scheduled = callback
			return &fakeScheduledTimer{}
		},
	})
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	if _, err := manager.Join(ctx, "owner", created.RoomID); err != nil {
		t.Fatalf("owner Join: %v", err)
	}
	if _, err := manager.Join(ctx, "guest", created.RoomID); err != nil {
		t.Fatalf("guest Join: %v", err)
	}
	if _, err := manager.SetReady(ctx, "owner", true); err != nil {
		t.Fatalf("owner ready: %v", err)
	}
	started, err := manager.SetReady(ctx, "guest", true)
	if err != nil {
		t.Fatalf("guest ready: %v", err)
	}
	if started.CurrentAction == nil || started.CurrentAction.Deadline != now.Add(60*time.Second).UnixMilli() || scheduled == nil {
		t.Fatalf("started snapshot=%#v scheduled=%v", started, scheduled != nil)
	}
	actorUserID := started.CurrentAction.UserID
	extended, err := manager.UseTimeExtension(ctx, actorUserID, created.RoomID)
	if err != nil {
		t.Fatalf("UseTimeExtension: %v", err)
	}
	if extended.CurrentAction == nil || extended.CurrentAction.Deadline != now.Add(90*time.Second).UnixMilli() {
		t.Fatalf("manual extension snapshot=%#v", extended)
	}
	for _, seat := range extended.Seats {
		if seat.UserID == actorUserID && seat.TimeExtensions != 1 {
			t.Fatalf("manual extension count=%d", seat.TimeExtensions)
		}
	}
	notified := make(chan string, 1)
	manager.SetSnapshotListener(func(roomID string) { notified <- roomID })
	now = now.Add(91 * time.Second)
	scheduled()
	select {
	case roomID := <-notified:
		if roomID != created.RoomID {
			t.Fatalf("notified room=%s", roomID)
		}
	default:
		t.Fatal("automatic extension did not publish a table snapshot")
	}
	automaticallyExtended, err := manager.Snapshot(ctx, "owner", created.RoomID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if automaticallyExtended.CurrentAction == nil ||
		automaticallyExtended.CurrentAction.Deadline != now.Add(30*time.Second).UnixMilli() {
		t.Fatalf("automatic extension snapshot=%#v", automaticallyExtended)
	}
	for _, seat := range automaticallyExtended.Seats {
		if seat.UserID == actorUserID && seat.TimeExtensions != 0 {
			t.Fatalf("automatic extension count=%d", seat.TimeExtensions)
		}
	}
	now = now.Add(31 * time.Second)
	scheduled()
	select {
	case <-notified:
	default:
		t.Fatal("final timeout did not publish a table snapshot")
	}
	settled, err := manager.Snapshot(ctx, "owner", created.RoomID)
	if err != nil {
		t.Fatalf("settled Snapshot: %v", err)
	}
	if settled.Phase != holdem.PhaseWaitingNextHand || settled.Settlement == nil || settled.CurrentAction != nil {
		t.Fatalf("timeout snapshot=%#v", settled)
	}
}

func TestActionDeadlineBecomesSixtySecondsWhenThreePlayersBecomeTwo(t *testing.T) {
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: "one", DisplayName: "一号"}, room.PresetStandard, 3, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, participant := range []room.Participant{
		{UserID: "two", DisplayName: "二号"},
		{UserID: "three", DisplayName: "三号"},
	} {
		if _, err := rooms.Join(ctx, participant, created.Code, ""); err != nil {
			t.Fatalf("Join %s: %v", participant.UserID, err)
		}
	}
	now := time.Unix(2_000, 0)
	manager, err := NewWithConfig(rooms, zeroRandom{}, ManagerConfig{
		Now: func() time.Time { return now },
		AfterFunc: func(_ time.Duration, _ func()) ScheduledTimer {
			return &fakeScheduledTimer{}
		},
	})
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	for _, userID := range []string{"one", "two", "three"} {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatalf("Join table %s: %v", userID, err)
		}
	}
	if _, err := manager.SetReady(ctx, "one", true); err != nil {
		t.Fatalf("ready one: %v", err)
	}
	if _, err := manager.SetReady(ctx, "two", true); err != nil {
		t.Fatalf("ready two: %v", err)
	}
	started, err := manager.SetReady(ctx, "three", true)
	if err != nil {
		t.Fatalf("ready three: %v", err)
	}
	if started.CurrentAction == nil || started.CurrentAction.Deadline != now.Add(20*time.Second).UnixMilli() {
		t.Fatalf("three-player deadline=%#v", started.CurrentAction)
	}
	actor := started.CurrentAction.UserID
	_, afterFold, err := manager.SubmitAction(ctx, actor, created.RoomID, holdem.ActionRequest{
		ActionID: "fold-to-heads-up", HandID: started.HandID,
		TableRevision: started.TableRevision, Action: holdem.ActionFold,
	})
	if err != nil {
		t.Fatalf("SubmitAction: %v", err)
	}
	if afterFold.CurrentAction == nil || afterFold.CurrentAction.Deadline != now.Add(60*time.Second).UnixMilli() {
		t.Fatalf("heads-up deadline=%#v", afterFold.CurrentAction)
	}
}

func testRoomService(t *testing.T) *room.Service {
	t.Helper()
	hasher, err := security.NewPasswordHasher(1_000, cryptorand.Reader)
	if err != nil {
		t.Fatalf("NewPasswordHasher: %v", err)
	}
	service, err := room.NewService(room.NewMemoryRepository(), hasher, room.ServiceConfig{})
	if err != nil {
		t.Fatalf("room NewService: %v", err)
	}
	return service
}
