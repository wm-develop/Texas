package holdem

import (
	"encoding/json"
	"reflect"
	"testing"
)

// 引擎新增字段却忘了写进状态，是这套机制最可能出的错，而且症状隐蔽：恢复
// 出来的牌局看着正常，只有那个字段悄悄回到零值。字段数量对不上就在这里拦住。
func TestTableStateCoversEveryEngineField(t *testing.T) {
	engineFields := reflect.TypeOf(Table{}).NumField()
	// TableState 比引擎多一个 Version
	stateFields := reflect.TypeOf(TableState{}).NumField() - 1
	if engineFields != stateFields {
		t.Fatalf(
			"Table 有 %d 个字段而 TableState 覆盖了 %d 个："+
				"新增引擎字段时必须同步 State() 与 RestoreTable()，否则恢复后的牌局与崩溃前不同",
			engineFields, stateFields,
		)
	}
}

func stateTestTable(t *testing.T) *Table {
	t.Helper()
	table, err := NewTable(Config{TableID: "room_state", MaxSeats: 6, SmallBlind: 10, BigBlind: 20})
	if err != nil {
		t.Fatal(err)
	}
	for seat, playerID := range map[int]string{1: "alice", 2: "bob", 3: "carol"} {
		if err := table.AddPlayer(playerID, seat, 2_000); err != nil {
			t.Fatal(err)
		}
		if err := table.SetReady(playerID, true); err != nil {
			t.Fatal(err)
		}
	}
	return table
}

// 状态往返之后，引擎必须与原来完全一致——包括未发的牌。
func TestTableStateRoundTripPreservesEverything(t *testing.T) {
	table := stateTestTable(t)
	if err := table.StartHand(CryptoRandom{}); err != nil {
		t.Fatal(err)
	}
	// 走到翻牌圈，让状态里有公共牌、有下注、有动作记录
	for table.Phase() == PhasePreflop {
		seat := table.CurrentSeat()
		player := table.players[seat]
		if _, err := table.SubmitAction(ActionRequest{
			ActionID:      "preflop-" + player.PlayerID,
			PlayerID:      player.PlayerID,
			HandID:        table.HandID(),
			TableRevision: table.Revision(),
			Action:        ActionCall,
		}); err != nil {
			// 大盲无需跟注时改为过牌
			if _, checkErr := table.SubmitAction(ActionRequest{
				ActionID:      "preflop-check-" + player.PlayerID,
				PlayerID:      player.PlayerID,
				HandID:        table.HandID(),
				TableRevision: table.Revision(),
				Action:        ActionCheck,
			}); checkErr != nil {
				t.Fatalf("preflop action: %v / %v", err, checkErr)
			}
		}
	}
	if table.Phase() != PhaseFlop {
		t.Fatalf("expected the flop, got %s", table.Phase())
	}

	encoded, err := json.Marshal(table.State())
	if err != nil {
		t.Fatal(err)
	}
	var decoded TableState
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreTable(decoded)
	if err != nil {
		t.Fatalf("RestoreTable: %v", err)
	}

	if !reflect.DeepEqual(table.State(), restored.State()) {
		t.Fatalf("state changed across the round trip:\nbefore=%#v\nafter =%#v", table.State(), restored.State())
	}
	// 牌堆连未发的牌一起恢复：接下来发的转牌必须与原引擎一致
	original, err := table.deck.Draw(1)
	if err != nil {
		t.Fatal(err)
	}
	same, err := restored.deck.Draw(1)
	if err != nil {
		t.Fatal(err)
	}
	if original[0] != same[0] {
		t.Fatalf("restored deck deals a different card: %v vs %v", original[0], same[0])
	}
}

// 恢复后这一手能照常打完，并且结算通过引擎自己的守恒校验。
func TestRestoredHandPlaysToSettlement(t *testing.T) {
	table := stateTestTable(t)
	if err := table.StartHand(CryptoRandom{}); err != nil {
		t.Fatal(err)
	}
	var before int64
	for _, player := range table.players {
		before += player.Stack + player.TotalBet
	}

	restored, err := RestoreTable(table.State())
	if err != nil {
		t.Fatal(err)
	}
	for restored.Phase() != PhaseWaitingNextHand {
		seat := restored.CurrentSeat()
		if seat == 0 {
			t.Fatalf("no seat to act in phase %s", restored.Phase())
		}
		player := restored.players[seat]
		if _, err := restored.SubmitAction(ActionRequest{
			ActionID:      "fold-" + player.PlayerID + string(restored.Phase()),
			PlayerID:      player.PlayerID,
			HandID:        restored.HandID(),
			TableRevision: restored.Revision(),
			Action:        ActionFold,
		}); err != nil {
			t.Fatalf("fold on the restored table: %v", err)
		}
	}
	var after int64
	for _, player := range restored.players {
		after += player.Stack
	}
	if after != before {
		t.Fatalf("chips must be conserved across restore and settlement: before=%d after=%d", before, after)
	}
	if restored.lastSettlement.HandID == "" {
		t.Fatal("the restored hand must produce a settlement")
	}
}

// 内部矛盾的状态一律拒绝：宁可本手作废，也不能拿它继续发牌。
func TestRestoreRejectsInconsistentState(t *testing.T) {
	table := stateTestTable(t)
	if err := table.StartHand(CryptoRandom{}); err != nil {
		t.Fatal(err)
	}
	base := table.State()

	broken := func(mutate func(*TableState)) TableState {
		copied := base
		copied.Players = append([]Player(nil), base.Players...)
		if base.Deck != nil {
			deck := *base.Deck
			deck.Cards = append([]Card(nil), base.Deck.Cards...)
			copied.Deck = &deck
		}
		mutate(&copied)
		return copied
	}

	cases := map[string]TableState{
		"版本不匹配": broken(func(state *TableState) { state.Version = 99 }),
		"两名玩家同一个座位": broken(func(state *TableState) {
			state.Players[1].Seat = state.Players[0].Seat
		}),
		"同一个人出现两次": broken(func(state *TableState) {
			state.Players[1].PlayerID = state.Players[0].PlayerID
		}),
		"筹码为负": broken(func(state *TableState) { state.Players[0].Stack = -1 }),
		"手牌重复": broken(func(state *TableState) {
			state.Players[1].HoleCards = state.Players[0].HoleCards
		}),
		"牌堆游标越界":   broken(func(state *TableState) { state.Deck.Next = len(state.Deck.Cards) + 1 }),
		"进行中却没有牌堆": broken(func(state *TableState) { state.Deck = nil }),
		"轮到一个空座位":  broken(func(state *TableState) { state.CurrentSeat = 6 }),
	}
	for name, state := range cases {
		if _, err := RestoreTable(state); err == nil {
			t.Fatalf("%s：必须拒绝恢复", name)
		}
	}
}

// 手间的状态不带牌堆也能恢复：那时没有进行中的一手要还原。
func TestRestoreBetweenHandsNeedsNoDeck(t *testing.T) {
	table := stateTestTable(t)
	state := table.State()
	if state.Phase != PhaseWaiting {
		t.Fatalf("fixture should sit between hands, got %s", state.Phase)
	}
	if _, err := RestoreTable(state); err != nil {
		t.Fatalf("restoring a table between hands must work: %v", err)
	}
}
