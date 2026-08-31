package holdem

import (
	"errors"
	"testing"
)

func TestHeadsUpHandChecksDownAndConservesChips(t *testing.T) {
	table := mustTable(t, Config{MaxSeats: 10, SmallBlind: 5, BigBlind: 10})
	mustAddReady(t, table, "dealer", 2, 100)
	mustAddReady(t, table, "big", 7, 100)
	if err := table.StartHand(zeroRandom{}); err != nil {
		t.Fatalf("StartHand: %v", err)
	}

	if table.DealerSeat() != 2 || table.SmallBlindSeat() != 2 || table.BigBlindSeat() != 7 {
		t.Fatalf("positions dealer=%d small=%d big=%d", table.DealerSeat(), table.SmallBlindSeat(), table.BigBlindSeat())
	}
	if table.CurrentSeat() != 2 {
		t.Fatalf("preflop current seat = %d, want dealer seat 2", table.CurrentSeat())
	}
	if table.TotalChips() != 200 {
		t.Fatalf("chips during hand = %d, want 200", table.TotalChips())
	}

	mustAct(t, table, "preflop-call", ActionCall, 0)
	mustAct(t, table, "preflop-check", ActionCheck, 0)
	if table.Phase() != PhaseFlop || table.CurrentSeat() != 7 || len(table.Board()) != 3 {
		t.Fatalf("flop state phase=%s current=%d board=%d", table.Phase(), table.CurrentSeat(), len(table.Board()))
	}

	for index := 0; index < 6; index++ {
		mustAct(t, table, "postflop-"+uintToString(uint64(index)), ActionCheck, 0)
	}
	if table.Phase() != PhaseWaitingNextHand {
		t.Fatalf("phase = %s, want waiting next hand", table.Phase())
	}
	if len(table.Board()) != 5 || table.TotalChips() != 200 {
		t.Fatalf("board=%d chips=%d", len(table.Board()), table.TotalChips())
	}
	settlement := table.LastSettlement()
	if settlement.HandID == "" || len(settlement.PotAwards) == 0 || len(settlement.LedgerEntries) != 2 {
		t.Fatalf("missing settlement: %#v", settlement)
	}
	if !settlement.Showdown || len(settlement.RevealedHands) != 2 {
		t.Fatalf("missing showdown reveal: %#v", settlement)
	}
	if settlement.LedgerEntries[0].Delta+settlement.LedgerEntries[1].Delta != 0 {
		t.Fatalf("ledger does not conserve chips: %#v", settlement.LedgerEntries)
	}
}

func TestHandIDDoesNotRepeatWhenTableRuntimeIsRecreated(t *testing.T) {
	start := func() string {
		table := mustTable(t, Config{
			TableID: "persistent-room", MaxSeats: 2, SmallBlind: 10, BigBlind: 20,
		})
		mustAddReady(t, table, "one", 1, 1000)
		mustAddReady(t, table, "two", 2, 1000)
		if err := table.StartHand(zeroRandom{}); err != nil {
			t.Fatalf("StartHand: %v", err)
		}
		return table.HandID()
	}

	first := start()
	second := start()
	if first == second {
		t.Fatalf("recreated table reused hand id %q", first)
	}
}

func TestFoldAwardsPotWithoutShowdown(t *testing.T) {
	table := mustTable(t, Config{MaxSeats: 10, SmallBlind: 5, BigBlind: 10})
	mustAddReady(t, table, "small", 1, 100)
	mustAddReady(t, table, "big", 2, 100)
	if err := table.StartHand(zeroRandom{}); err != nil {
		t.Fatalf("StartHand: %v", err)
	}
	mustAct(t, table, "fold", ActionFold, 0)

	players := playersByID(table.Players())
	if players["small"].Stack != 95 || players["big"].Stack != 105 {
		t.Fatalf("stacks small=%d big=%d", players["small"].Stack, players["big"].Stack)
	}
	if len(table.Board()) != 0 || table.TotalChips() != 200 {
		t.Fatalf("board=%d chips=%d", len(table.Board()), table.TotalChips())
	}
	settlement := table.LastSettlement()
	if settlement.Showdown || len(settlement.RevealedHands) != 0 {
		t.Fatalf("fold settlement leaked cards: %#v", settlement)
	}
}

func TestAllInBlindsOfferRunoutChoiceAndDefaultToOnce(t *testing.T) {
	table := mustTable(t, Config{MaxSeats: 2, SmallBlind: 5, BigBlind: 10})
	mustAddReady(t, table, "a", 1, 5)
	mustAddReady(t, table, "b", 2, 5)
	if err := table.StartHand(zeroRandom{}); err != nil {
		t.Fatalf("StartHand: %v", err)
	}
	if table.Phase() != PhaseRunoutChoice || len(table.Board()) != 0 {
		t.Fatalf("phase=%s board=%d", table.Phase(), len(table.Board()))
	}
	if err := table.ResolveRunoutChoiceTimeout(); err != nil {
		t.Fatalf("ResolveRunoutChoiceTimeout: %v", err)
	}
	if table.Phase() != PhaseWaitingNextHand || len(table.Board()) != 5 {
		t.Fatalf("settled phase=%s board=%d", table.Phase(), len(table.Board()))
	}
	if table.TotalChips() != 10 {
		t.Fatalf("chips = %d, want 10", table.TotalChips())
	}
}

func TestHeadsUpAllInRunsTwiceOnlyWhenBothChooseTwice(t *testing.T) {
	table := mustTable(t, Config{MaxSeats: 2, SmallBlind: 5, BigBlind: 10})
	mustAddReady(t, table, "a", 1, 5)
	mustAddReady(t, table, "b", 2, 5)
	if err := table.StartHand(zeroRandom{}); err != nil {
		t.Fatal(err)
	}
	settled, err := table.ChooseRunoutCount("a", 2)
	if err != nil || settled {
		t.Fatalf("first choice settled=%v err=%v", settled, err)
	}
	settled, err = table.ChooseRunoutCount("b", 2)
	if err != nil || !settled || table.Phase() != PhaseWaitingNextHand {
		t.Fatalf("second choice settled=%v phase=%s err=%v", settled, table.Phase(), err)
	}
	result := table.LastSettlement()
	if len(result.RunoutBoards) != 2 || len(result.RunoutBoards[0]) != 5 || len(result.RunoutBoards[1]) != 5 {
		t.Fatalf("runout boards=%#v", result.RunoutBoards)
	}
	var awarded int64
	for _, award := range result.PotAwards {
		if award.RunoutIndex != 1 && award.RunoutIndex != 2 {
			t.Fatalf("award missing runout index: %#v", award)
		}
		awarded += award.Amount
	}
	if awarded != 10 || table.TotalChips() != 10 {
		t.Fatalf("awarded=%d chips=%d", awarded, table.TotalChips())
	}
}

func TestRunoutChoiceWaitsUntilOpponentCallsAllIn(t *testing.T) {
	table := mustTable(t, Config{MaxSeats: 2, SmallBlind: 5, BigBlind: 10})
	mustAddReady(t, table, "small", 1, 100)
	mustAddReady(t, table, "big", 2, 100)
	if err := table.StartHand(zeroRandom{}); err != nil {
		t.Fatal(err)
	}

	mustAct(t, table, "small-all-in", ActionAllIn, 0)
	if table.Phase() != PhasePreflop || table.CurrentSeat() != 2 {
		t.Fatalf("choice opened before opponent responded: phase=%s current=%d", table.Phase(), table.CurrentSeat())
	}

	mustAct(t, table, "big-call-all-in", ActionCall, 0)
	if table.Phase() != PhaseRunoutChoice || table.CurrentSeat() != 0 {
		t.Fatalf("choice not opened after betting closed: phase=%s current=%d", table.Phase(), table.CurrentSeat())
	}
}

func TestRunoutChoiceIsSkippedWhenOpponentFoldsToAllIn(t *testing.T) {
	table := mustTable(t, Config{MaxSeats: 2, SmallBlind: 5, BigBlind: 10})
	mustAddReady(t, table, "small", 1, 100)
	mustAddReady(t, table, "big", 2, 100)
	if err := table.StartHand(zeroRandom{}); err != nil {
		t.Fatal(err)
	}

	mustAct(t, table, "small-all-in", ActionAllIn, 0)
	mustAct(t, table, "big-fold", ActionFold, 0)
	if table.Phase() != PhaseWaitingNextHand {
		t.Fatalf("fold should settle immediately instead of opening choice: phase=%s", table.Phase())
	}
}

func TestPlayerMayFoldWhenCheckIsAvailable(t *testing.T) {
	table := mustTable(t, Config{MaxSeats: 2, SmallBlind: 5, BigBlind: 10})
	player := &Player{PlayerID: "actor", Stack: 100}
	options := table.actionOptions(player)
	if !options.CanCheck || !options.CanFold || options.ToCall != 0 {
		t.Fatalf("options=%#v", options)
	}
}

func TestShortAllInDoesNotReopenRaise(t *testing.T) {
	table := mustTable(t, Config{MaxSeats: 3, SmallBlind: 5, BigBlind: 10})
	mustAddReady(t, table, "dealer", 1, 100)
	mustAddReady(t, table, "small", 2, 100)
	mustAddReady(t, table, "big", 3, 15)
	if err := table.StartHand(zeroRandom{}); err != nil {
		t.Fatalf("StartHand: %v", err)
	}
	mustAct(t, table, "dealer-call", ActionCall, 0)
	mustAct(t, table, "small-call", ActionCall, 0)
	mustAct(t, table, "big-short-all-in", ActionAllIn, 0)

	if table.CurrentSeat() != 1 {
		t.Fatalf("current seat = %d, want 1", table.CurrentSeat())
	}
	options, err := table.CurrentActionOptions()
	if err != nil {
		t.Fatalf("CurrentActionOptions: %v", err)
	}
	if options.ToCall != 5 || options.CanRaise || options.CanAllIn {
		t.Fatalf("options after short all-in = %#v", options)
	}
}

func TestShortStackCannotCallAndMustFoldOrGoAllIn(t *testing.T) {
	table := mustTable(t, Config{MaxSeats: 2, SmallBlind: 5, BigBlind: 10})
	mustAddReady(t, table, "short", 1, 7)
	mustAddReady(t, table, "big", 2, 100)
	if err := table.StartHand(zeroRandom{}); err != nil {
		t.Fatalf("StartHand: %v", err)
	}
	options, err := table.CurrentActionOptions()
	if err != nil {
		t.Fatalf("CurrentActionOptions: %v", err)
	}
	if options.ToCall != 5 || options.CanCall || !options.CanFold || !options.CanAllIn ||
		options.CanCheck || options.CanBet || options.CanRaise {
		t.Fatalf("short-stack options = %#v", options)
	}
	_, err = table.SubmitAction(ActionRequest{
		ActionID: "misleading-call", PlayerID: "short", HandID: table.HandID(),
		TableRevision: table.Revision(), Action: ActionCall,
	})
	var ruleErr RuleError
	if !errors.As(err, &ruleErr) || ruleErr.Code != "illegal_action" {
		t.Fatalf("short call error = %v", err)
	}
}

func TestFullRaiseReopensRaise(t *testing.T) {
	table := mustTable(t, Config{MaxSeats: 3, SmallBlind: 5, BigBlind: 10})
	mustAddReady(t, table, "dealer", 1, 100)
	mustAddReady(t, table, "small", 2, 100)
	mustAddReady(t, table, "big", 3, 100)
	if err := table.StartHand(zeroRandom{}); err != nil {
		t.Fatalf("StartHand: %v", err)
	}
	mustAct(t, table, "dealer-call", ActionCall, 0)
	mustAct(t, table, "small-call", ActionCall, 0)
	mustAct(t, table, "big-raise", ActionRaise, 20)

	options, err := table.CurrentActionOptions()
	if err != nil {
		t.Fatalf("CurrentActionOptions: %v", err)
	}
	if !options.CanRaise || options.MinRaiseTo != 30 {
		t.Fatalf("options after full raise = %#v", options)
	}
}

func TestRegularRaiseUsesSmallBlindUnitWhileAllInMayUseRemainder(t *testing.T) {
	table := mustTable(t, Config{MaxSeats: 2, SmallBlind: 10, BigBlind: 20})
	mustAddReady(t, table, "dealer", 1, 103)
	mustAddReady(t, table, "big", 2, 100)
	if err := table.StartHand(zeroRandom{}); err != nil {
		t.Fatalf("StartHand: %v", err)
	}
	options, err := table.CurrentActionOptions()
	if err != nil {
		t.Fatalf("CurrentActionOptions: %v", err)
	}
	if options.MinRaiseTo != 40 || options.MaxRaiseTo != 100 || !options.CanAllIn {
		t.Fatalf("options=%#v", options)
	}
	player := table.players[table.CurrentSeat()]
	_, err = table.SubmitAction(ActionRequest{
		ActionID: "invalid-unit", PlayerID: player.PlayerID, HandID: table.HandID(),
		TableRevision: table.Revision(), Action: ActionRaise, RaiseTo: 45,
	})
	var ruleError RuleError
	if !errors.As(err, &ruleError) || ruleError.Code != "invalid_amount" {
		t.Fatalf("non-unit raise error=%v", err)
	}
	result := mustAct(t, table, "all-in-remainder", ActionAllIn, 0)
	if result.Committed != 93 {
		t.Fatalf("all-in committed=%d want=93", result.Committed)
	}
}

func TestActionIdempotencyAndStaleRevision(t *testing.T) {
	table := mustTable(t, Config{MaxSeats: 2, SmallBlind: 5, BigBlind: 10})
	mustAddReady(t, table, "small", 1, 100)
	mustAddReady(t, table, "big", 2, 100)
	if err := table.StartHand(zeroRandom{}); err != nil {
		t.Fatalf("StartHand: %v", err)
	}

	request := ActionRequest{
		ActionID:      "same-action",
		PlayerID:      "small",
		HandID:        table.HandID(),
		TableRevision: table.Revision(),
		Action:        ActionCall,
	}
	first, err := table.SubmitAction(request)
	if err != nil {
		t.Fatalf("SubmitAction: %v", err)
	}
	second, err := table.SubmitAction(request)
	if err != nil {
		t.Fatalf("duplicate SubmitAction: %v", err)
	}
	if first != second {
		t.Fatalf("duplicate result = %#v, want %#v", second, first)
	}
	if playersByID(table.Players())["small"].Stack != 90 {
		t.Fatal("duplicate action committed chips twice")
	}

	_, err = table.SubmitAction(ActionRequest{
		ActionID:      "stale",
		PlayerID:      "big",
		HandID:        table.HandID(),
		TableRevision: request.TableRevision,
		Action:        ActionCheck,
	})
	var ruleErr RuleError
	if !errors.As(err, &ruleErr) || ruleErr.Code != "stale_revision" {
		t.Fatalf("error = %v, want stale_revision", err)
	}
}

func TestTimeoutChecksOrFolds(t *testing.T) {
	table := mustTable(t, Config{MaxSeats: 2, SmallBlind: 5, BigBlind: 10})
	mustAddReady(t, table, "small", 1, 100)
	mustAddReady(t, table, "big", 2, 100)
	if err := table.StartHand(zeroRandom{}); err != nil {
		t.Fatalf("StartHand: %v", err)
	}
	result, err := table.ApplyTimeout()
	if err != nil {
		t.Fatalf("ApplyTimeout: %v", err)
	}
	if result.Action != ActionFold || table.Phase() != PhaseWaitingNextHand {
		t.Fatalf("timeout result=%#v phase=%s", result, table.Phase())
	}
}

func TestDisconnectKeepsHandAndLeaveWaitsForSettlement(t *testing.T) {
	table := mustTable(t, Config{MaxSeats: 2, SmallBlind: 5, BigBlind: 10})
	mustAddReady(t, table, "leaving", 1, 100)
	mustAddReady(t, table, "staying", 2, 100)
	if err := table.StartHand(zeroRandom{}); err != nil {
		t.Fatalf("StartHand: %v", err)
	}
	if err := table.SetConnected("leaving", false); err != nil {
		t.Fatalf("SetConnected: %v", err)
	}
	if player := table.playerByID("leaving"); player == nil || !player.Participating || player.Folded {
		t.Fatalf("disconnect changed hand state: %#v", player)
	}
	if err := table.RequestLeave("leaving"); err != nil {
		t.Fatalf("RequestLeave: %v", err)
	}
	if table.playerByID("leaving") == nil {
		t.Fatal("player was removed before hand settlement")
	}
	if _, err := table.ApplyTimeout(); err != nil {
		t.Fatalf("ApplyTimeout: %v", err)
	}
	if table.playerByID("leaving") != nil {
		t.Fatal("player was not removed after hand settlement")
	}
	if table.LastSettlement().StacksByPlayer["leaving"] != 95 {
		t.Fatalf("settlement lost departing player's stack: %#v", table.LastSettlement())
	}
}

func mustTable(t *testing.T, config Config) *Table {
	t.Helper()
	if config.TableID == "" {
		config.TableID = "test_table"
	}
	table, err := NewTable(config)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	return table
}

func mustAddReady(t *testing.T, table *Table, playerID string, seat int, stack int64) {
	t.Helper()
	if err := table.AddPlayer(playerID, seat, stack); err != nil {
		t.Fatalf("AddPlayer: %v", err)
	}
	if err := table.SetReady(playerID, true); err != nil {
		t.Fatalf("SetReady: %v", err)
	}
}

func mustAct(t *testing.T, table *Table, actionID string, action ActionType, raiseTo int64) ActionResult {
	t.Helper()
	player := table.players[table.CurrentSeat()]
	if player == nil {
		t.Fatal("no current player")
	}
	result, err := table.SubmitAction(ActionRequest{
		ActionID:      actionID,
		PlayerID:      player.PlayerID,
		HandID:        table.HandID(),
		TableRevision: table.Revision(),
		Action:        action,
		RaiseTo:       raiseTo,
	})
	if err != nil {
		t.Fatalf("SubmitAction(%s): %v", action, err)
	}
	return result
}

func playersByID(players []Player) map[string]Player {
	result := make(map[string]Player, len(players))
	for _, player := range players {
		result[player.PlayerID] = player
	}
	return result
}
