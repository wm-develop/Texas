package tablemanager

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"texas/services/game_server/internal/bankroll"
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
	if settled.LastAction == nil || settled.LastAction.ActionID != "fold_1" ||
		settled.LastAction.HandID != settled.HandID || settled.LastAction.UserID != actor ||
		settled.LastAction.Action != holdem.ActionFold ||
		settled.LastAction.TableRevision != result.Revision {
		t.Fatalf("confirmed last action=%#v result=%#v", settled.LastAction, result)
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

func TestFoldedPlayerAndFoldWinnerCanRevealUntilNextHand(t *testing.T) {
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.PresetStandard, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.Join(ctx, room.Participant{UserID: "guest", DisplayName: "好友"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(5_000, 0)
	manager, err := NewWithConfig(rooms, zeroRandom{}, ManagerConfig{
		Now: func() time.Time { return now },
		AfterFunc: func(_ time.Duration, _ func()) ScheduledTimer {
			return &fakeScheduledTimer{}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{"owner", "guest"} {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.SetReady(ctx, userID, true); err != nil {
			t.Fatal(err)
		}
	}
	started, err := manager.Snapshot(ctx, "owner", created.RoomID)
	if err != nil || started.CurrentAction == nil {
		t.Fatalf("started=%#v err=%v", started, err)
	}
	foldedUserID := started.CurrentAction.UserID
	winnerUserID := "owner"
	if foldedUserID == winnerUserID {
		winnerUserID = "guest"
	}
	_, settled, err := manager.SubmitAction(ctx, foldedUserID, created.RoomID, holdem.ActionRequest{
		ActionID: "fold-for-voluntary-reveal", HandID: started.HandID,
		TableRevision: started.TableRevision, Action: holdem.ActionFold,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !settled.CanShowHoleCards || settled.AutoReadyDeadline != now.Add(autoReadyDelay).UnixMilli() ||
		settled.OwnerUserID != "owner" {
		t.Fatalf("folded settlement snapshot=%#v", settled)
	}
	winnerSnapshot, err := manager.Snapshot(ctx, winnerUserID, created.RoomID)
	if err != nil || !winnerSnapshot.CanShowHoleCards {
		t.Fatalf("winner snapshot=%#v err=%v", winnerSnapshot, err)
	}
	if _, err := manager.ShowHoleCards(ctx, foldedUserID, created.RoomID); err != nil {
		t.Fatalf("folded reveal: %v", err)
	}
	afterWinnerReveal, err := manager.ShowHoleCards(ctx, winnerUserID, created.RoomID)
	if err != nil || len(afterWinnerReveal.VoluntaryReveals) != 2 || afterWinnerReveal.CanShowHoleCards {
		t.Fatalf("voluntary reveals=%#v err=%v", afterWinnerReveal, err)
	}
	if _, err := manager.SetReady(ctx, foldedUserID, true); err != nil {
		t.Fatal(err)
	}
	nextHand, err := manager.SetReady(ctx, winnerUserID, true)
	if err != nil || nextHand.Phase != holdem.PhasePreflop || len(nextHand.VoluntaryReveals) != 0 || nextHand.CanShowHoleCards {
		t.Fatalf("next hand snapshot=%#v err=%v", nextHand, err)
	}
}

func TestSettlementAutomaticallyReadiesAfterTenSecondsAndSupportsCancellation(t *testing.T) {
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.PresetStandard, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.Join(ctx, room.Participant{UserID: "guest", DisplayName: "好友"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(6_000, 0)
	scheduled := make(map[time.Duration]func())
	manager, err := NewWithConfig(rooms, zeroRandom{}, ManagerConfig{
		Now: func() time.Time { return now },
		AfterFunc: func(duration time.Duration, callback func()) ScheduledTimer {
			scheduled[duration] = callback
			return &fakeScheduledTimer{}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{"owner", "guest"} {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.SetReady(ctx, userID, true); err != nil {
			t.Fatal(err)
		}
	}
	started, _ := manager.Snapshot(ctx, "owner", created.RoomID)
	actor := started.CurrentAction.UserID
	_, _, err = manager.SubmitAction(ctx, actor, created.RoomID, holdem.ActionRequest{
		ActionID: "fold-before-auto-ready", HandID: started.HandID,
		TableRevision: started.TableRevision, Action: holdem.ActionFold,
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := manager.SetReady(ctx, actor, false)
	if err != nil || !cancelled.AutoReadyCancelled || cancelled.AutoReadyDeadline == 0 {
		t.Fatalf("cancelled snapshot=%#v err=%v", cancelled, err)
	}
	autoReady := scheduled[autoReadyDelay]
	if autoReady == nil {
		t.Fatal("auto-ready callback was not scheduled")
	}
	now = now.Add(autoReadyDelay)
	autoReady()
	afterDeadline, err := manager.Snapshot(ctx, actor, created.RoomID)
	if err != nil || afterDeadline.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("after deadline=%#v err=%v", afterDeadline, err)
	}
	for _, seat := range afterDeadline.Seats {
		if seat.UserID == actor && seat.Ready {
			t.Fatal("cancelled player was automatically readied")
		}
		if seat.UserID != actor && !seat.Ready {
			t.Fatal("non-cancelled player was not automatically readied")
		}
	}
	startedAgain, err := manager.SetReady(ctx, actor, true)
	if err != nil || startedAgain.Phase != holdem.PhasePreflop {
		t.Fatalf("manual ready after cancellation=%#v err=%v", startedAgain, err)
	}
	_, _, err = manager.SubmitAction(ctx, startedAgain.CurrentAction.UserID, created.RoomID, holdem.ActionRequest{
		ActionID: "fold-before-full-auto-ready", HandID: startedAgain.HandID,
		TableRevision: startedAgain.TableRevision, Action: holdem.ActionFold,
	})
	if err != nil {
		t.Fatal(err)
	}
	autoReady = scheduled[autoReadyDelay]
	now = now.Add(autoReadyDelay)
	autoReady()
	fullyAutomatic, err := manager.Snapshot(ctx, "owner", created.RoomID)
	if err != nil || fullyAutomatic.Phase != holdem.PhasePreflop || fullyAutomatic.AutoReadyDeadline != 0 {
		t.Fatalf("fully automatic next hand=%#v err=%v", fullyAutomatic, err)
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
		ToCall: 10, CanRaise: true, CanAllIn: true, MinRaiseTo: 20, MaxRaiseTo: 1000,
	}
	suggestions := betSuggestions(300, players, "actor", options, 10)
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
	for _, suggestion := range suggestions[:6] {
		if suggestion.RaiseTo%10 != 0 {
			t.Fatalf("suggestion does not use small blind denomination: %#v", suggestion)
		}
	}
	if suggestions[5].RaiseTo != 390 || suggestions[6].RaiseTo != 1000 {
		t.Fatalf("overbet/all-in suggestions=%#v", suggestions)
	}
}

func TestBetSuggestionsUsePostCallPotAndCollapseIllegalFractionsToMinimumRaise(t *testing.T) {
	players := []holdem.Player{{PlayerID: "actor", StreetBet: 20, Stack: 980}}
	options := holdem.ActionOptions{
		ToCall: 180, CanRaise: true, CanAllIn: true, MinRaiseTo: 380, MaxRaiseTo: 1000,
	}
	suggestions := betSuggestions(220, players, "actor", options, 10)
	want := []struct {
		label   string
		target  int64
		commits int64
	}{
		{"min_raise", 380, 360},
		{"half_pot", 400, 380},
		{"two_thirds_pot", 470, 450},
		{"pot", 600, 580},
		{"overbet_120", 680, 660},
		{"all_in", 1000, 980},
	}
	if len(suggestions) != len(want) {
		t.Fatalf("suggestions=%#v", suggestions)
	}
	for index, expected := range want {
		actual := suggestions[index]
		if actual.Label != expected.label || actual.RaiseTo != expected.target ||
			actual.RaiseTo-players[0].StreetBet != expected.commits {
			t.Fatalf("suggestion %d=%#v want=%#v", index, actual, expected)
		}
	}

	secondOptions := holdem.ActionOptions{
		ToCall: 100, CanRaise: true, CanAllIn: true, MinRaiseTo: 300, MaxRaiseTo: 1725,
	}
	second := betSuggestions(
		300, []holdem.Player{{PlayerID: "actor", StreetBet: 100, Stack: 1625}},
		"actor", secondOptions, 10,
	)
	if len(second) == 0 || second[0].Label != "quarter_pot" || second[0].RaiseTo != 300 {
		t.Fatalf("second preflop suggestions=%#v", second)
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

func TestConfiguredBuyInRebuyIsIdempotentAndLeaveCashesOut(t *testing.T) {
	ctx := context.Background()
	chips, err := bankroll.NewService(bankroll.NewMemoryRepository(), time.Now)
	if err != nil {
		t.Fatalf("bankroll NewService: %v", err)
	}
	for _, userID := range []string{"owner", "guest"} {
		if _, err := chips.TopUp(ctx, userID, "topup-"+userID, 5_000); err != nil {
			t.Fatalf("TopUp %s: %v", userID, err)
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
		Preset: room.PresetStandard, MaxPlayers: 2, SmallBlind: 10, BigBlind: 20,
		MaxBuyIn: 2_000, BuyIn: 1_000, RequestID: "create-owner",
	})
	if err != nil {
		t.Fatalf("CreateConfigured: %v", err)
	}
	if _, err := rooms.JoinWithBuyIn(ctx, room.Participant{UserID: "guest", DisplayName: "好友"}, room.JoinOptions{
		Code: created.Code, BuyIn: 500, RequestID: "join-guest",
	}); err != nil {
		t.Fatalf("JoinWithBuyIn: %v", err)
	}
	manager, err := NewWithConfig(rooms, zeroRandom{}, ManagerConfig{Bankroll: chips})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Join(ctx, "owner", created.RoomID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Join(ctx, "guest", created.RoomID); err != nil {
		t.Fatal(err)
	}
	first, err := manager.Rebuy(ctx, "guest", created.RoomID, "rebuy-guest", 600)
	if err != nil {
		t.Fatalf("Rebuy: %v", err)
	}
	duplicate, err := manager.Rebuy(ctx, "guest", created.RoomID, "rebuy-guest", 600)
	if err != nil {
		t.Fatalf("duplicate Rebuy: %v", err)
	}
	guestStack := func(snapshot Snapshot) int64 {
		for _, seat := range snapshot.Seats {
			if seat.UserID == "guest" {
				return seat.Stack
			}
		}
		return -1
	}
	if guestStack(first) != 1_100 || guestStack(duplicate) != 1_100 {
		t.Fatalf("rebuy stacks first=%d duplicate=%d", guestStack(first), guestStack(duplicate))
	}
	if _, err := manager.Rebuy(ctx, "guest", created.RoomID, "rebuy-over-cap", 901); errorCodeForTest(err) != "maximum_buy_in_exceeded" {
		t.Fatalf("expected cap error, got %v", err)
	}
	closed, err := manager.Leave(ctx, "guest")
	if err != nil || closed {
		t.Fatalf("guest Leave closed=%v err=%v", closed, err)
	}
	guestWallet, err := chips.Snapshot(ctx, "guest")
	if err != nil {
		t.Fatal(err)
	}
	if guestWallet.WalletChips != 5_000 || guestWallet.TableChips != 0 {
		t.Fatalf("guest cash-out=%#v", guestWallet)
	}
}

func TestOwnerLeaveCashesOutOnlyOwnerAndTransfersRoom(t *testing.T) {
	ctx := context.Background()
	chips, _ := bankroll.NewService(bankroll.NewMemoryRepository(), time.Now)
	for _, userID := range []string{"owner", "guest"} {
		if _, err := chips.TopUp(ctx, userID, "owner-transfer-topup-"+userID, 5000); err != nil {
			t.Fatal(err)
		}
	}
	hasher, _ := security.NewPasswordHasher(1_000, cryptorand.Reader)
	rooms, _ := room.NewService(room.NewMemoryRepository(), hasher, room.ServiceConfig{Bankroll: chips})
	created, err := rooms.CreateConfigured(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.CreateOptions{
		Preset: room.PresetStandard, MaxPlayers: 2, SmallBlind: 10, BigBlind: 20,
		MaxBuyIn: 2000, BuyIn: 1000, RequestID: "owner-transfer-create",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.JoinWithBuyIn(ctx, room.Participant{UserID: "guest", DisplayName: "好友"}, room.JoinOptions{
		Code: created.Code, BuyIn: 1000, RequestID: "owner-transfer-join",
	}); err != nil {
		t.Fatal(err)
	}
	manager, _ := NewWithConfig(rooms, zeroRandom{}, ManagerConfig{Bankroll: chips})
	for _, userID := range []string{"owner", "guest"} {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	closed, err := manager.Leave(ctx, "owner")
	if err != nil || closed {
		t.Fatalf("owner leave closed=%v err=%v", closed, err)
	}
	current, err := rooms.Current(ctx, "guest")
	if err != nil || current.OwnerUserID != "guest" {
		t.Fatalf("transferred room=%#v err=%v", current, err)
	}
	ownerChips, _ := chips.Snapshot(ctx, "owner")
	guestChips, _ := chips.Snapshot(ctx, "guest")
	if ownerChips.WalletChips != 5000 || ownerChips.TableChips != 0 ||
		guestChips.WalletChips != 4000 || guestChips.TableChips != 1000 {
		t.Fatalf("owner=%#v guest=%#v", ownerChips, guestChips)
	}
}

func errorCodeForTest(err error) string {
	var roomError room.Error
	if errors.As(err, &roomError) {
		return roomError.Code
	}
	var ruleError holdem.RuleError
	if errors.As(err, &ruleError) {
		return ruleError.Code
	}
	return ""
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
