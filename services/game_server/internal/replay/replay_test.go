package replay

import (
	"errors"
	"testing"
	"time"

	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/history"
)

// 三人桌：庄位 3、小盲 1、大盲 2。3 号加注到 60，1 号弃牌，2 号弃牌，
// 3 号收下盲注，没人跟注的 40 退回给他。
func foldOutHand() history.Hand {
	now := time.Unix(1_800_000_000, 0).UTC()
	return history.Hand{
		HandID: "hand_fold", RoomID: "room", RoomCode: "123456", DealerSeat: 3,
		StartedAt: now, EndedAt: now.Add(time.Minute),
		SmallBlind: 10, BigBlind: 20,
		Players: []history.PlayerResult{
			{UserID: "sb", DisplayName: "小盲", Seat: 1, StartingStack: 1000, EndingStack: 990, Delta: -10, HoleCards: []string{"2c", "7d"}},
			{UserID: "bb", DisplayName: "大盲", Seat: 2, StartingStack: 1000, EndingStack: 980, Delta: -20},
			{UserID: "btn", DisplayName: "庄", Seat: 3, StartingStack: 1000, EndingStack: 1030, Delta: 30},
		},
		Actions: []history.Action{
			{UserID: "btn", Sequence: 1, Street: "preflop", Type: "raise", Committed: 60, RaiseTo: 60},
			{UserID: "sb", Sequence: 2, Street: "preflop", Type: "fold", TimedOut: true},
			{UserID: "bb", Sequence: 3, Street: "preflop", Type: "fold"},
		},
		PotAwards: []holdem.PotAward{{
			Amount: 50, WinnerPlayerIDs: []string{"btn"},
			Payouts: []holdem.Payout{{PlayerID: "btn", Amount: 50}},
		}},
	}
}

func TestFoldOutTimeline(t *testing.T) {
	timeline, err := Build(foldOutHand(), "sb")
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]string, 0, len(timeline.Steps))
	for _, step := range timeline.Steps {
		kinds = append(kinds, step.Kind)
	}
	want := []string{KindBlinds, KindAction, KindAction, KindAction, KindRefund, KindSettle}
	if len(kinds) != len(want) {
		t.Fatalf("kinds=%v", kinds)
	}
	for index := range want {
		if kinds[index] != want[index] {
			t.Fatalf("kinds=%v want %v", kinds, want)
		}
	}
	if timeline.SmallBlindSeat != 1 || timeline.BigBlindSeat != 2 {
		t.Fatalf("blinds %d/%d", timeline.SmallBlindSeat, timeline.BigBlindSeat)
	}
	if first := timeline.Steps[0]; first.Pot != 30 {
		t.Fatalf("pot after blinds=%d", first.Pot)
	}
	if !timeline.Steps[2].TimedOut {
		t.Fatal("the timed-out fold must be marked")
	}
	refund := timeline.Steps[4]
	if refund.ActorID != "btn" || refund.Amount != 40 || refund.Pot != 50 {
		t.Fatalf("refund=%+v", refund)
	}
	positions := map[string]string{}
	for _, player := range timeline.Players {
		positions[player.UserID] = player.Position
	}
	if positions["sb"] != "SB" || positions["bb"] != "BB" || positions["btn"] != "BTN" {
		t.Fatalf("positions=%v", positions)
	}
	// 没摊牌就结束：别人的牌一帧都不出现
	for _, step := range timeline.Steps {
		for _, seat := range step.Seats {
			if seat.UserID != "sb" && len(seat.HoleCards) != 0 {
				t.Fatalf("step %d shows %s's cards", step.Index, seat.UserID)
			}
		}
	}
}

// 换街后弃牌的人仍标着弃牌，其他人（包括全下的人）的上一个动作清空，
// 与引擎一致，免得客户端显示「跟注至 0」。
func TestStreetChangeClearsActionsExceptFold(t *testing.T) {
	hand := foldOutHand()
	hand.Board = []string{"Ah", "Kh", "Qh"}
	hand.Actions = []history.Action{
		{UserID: "btn", Sequence: 1, Street: "preflop", Type: "call", Committed: 20},
		{UserID: "sb", Sequence: 2, Street: "preflop", Type: "fold"},
		{UserID: "bb", Sequence: 3, Street: "preflop", Type: "check"},
		{UserID: "bb", Sequence: 4, Street: "flop", Type: "check"},
		{UserID: "btn", Sequence: 5, Street: "flop", Type: "bet", Committed: 20, RaiseTo: 20},
		{UserID: "bb", Sequence: 6, Street: "flop", Type: "fold"},
	}
	hand.Players[1].EndingStack, hand.Players[1].Delta = 980, -20
	hand.Players[2].EndingStack, hand.Players[2].Delta = 1030, 30
	hand.PotAwards[0].Amount = 50
	hand.PotAwards[0].Payouts[0].Amount = 50
	timeline, err := Build(hand, "sb")
	if err != nil {
		t.Fatal(err)
	}
	flop := timeline.Steps[4]
	if flop.Kind != KindStreet || flop.Street != "flop" || len(flop.Board) != 3 {
		t.Fatalf("flop step=%+v", flop)
	}
	for _, seat := range flop.Seats {
		want := ""
		if seat.UserID == "sb" {
			want = "fold"
		}
		if seat.LastAction != want || seat.StreetBet != 0 {
			t.Fatalf("seat %s after the street change: %+v", seat.UserID, seat)
		}
	}
}

// 推出来的结果与牌谱对不上时拒绝生成，而不是给出一份算错的录像。
func TestInconsistentRecordIsRejected(t *testing.T) {
	for name, mutate := range map[string]func(*history.Hand){
		"结束筹码对不上":  func(hand *history.Hand) { hand.Players[2].EndingStack++ },
		"投入超过筹码":   func(hand *history.Hand) { hand.Actions[0].Committed = 5_000 },
		"动作者不在牌谱里": func(hand *history.Hand) { hand.Actions[1].UserID = "ghost" },
		"缺少盲注额":    func(hand *history.Hand) { hand.BigBlind = 0 },
		"公共牌不够":    func(hand *history.Hand) { hand.Actions[2].Street = "turn" },
	} {
		hand := foldOutHand()
		mutate(&hand)
		if _, err := Build(hand, "sb"); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s: err=%v", name, err)
		}
	}
}

// 两人桌庄位下小盲；旧牌谱没记盲注座位时同样这样推。
func TestHeadsUpLegacyBlindSeats(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	hand := history.Hand{
		HandID: "hu", RoomID: "room", DealerSeat: 4, StartedAt: now, EndedAt: now,
		SmallBlind: 10, BigBlind: 20,
		Players: []history.PlayerResult{
			{UserID: "a", Seat: 2, StartingStack: 500, EndingStack: 510, Delta: 10},
			{UserID: "b", Seat: 4, StartingStack: 500, EndingStack: 490, Delta: -10},
		},
		Actions: []history.Action{{UserID: "b", Sequence: 1, Street: "preflop", Type: "fold"}},
		PotAwards: []holdem.PotAward{{
			Amount: 20, WinnerPlayerIDs: []string{"a"},
			Payouts: []holdem.Payout{{PlayerID: "a", Amount: 20}},
		}},
	}
	timeline, err := Build(hand, "a")
	if err != nil {
		t.Fatal(err)
	}
	if timeline.SmallBlindSeat != 4 || timeline.BigBlindSeat != 2 {
		t.Fatalf("heads-up blinds %d/%d, want 4/2", timeline.SmallBlindSeat, timeline.BigBlindSeat)
	}
}

// 翻牌前全下后自动发完五张：全下的人换街后上一个动作同样清空（座位上另有全下
// 标识），不留下一个金额已归零的「跟注」「全下」。
func TestAllInPlayerActionIsClearedOnTheNextStreet(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	hand := history.Hand{
		HandID: "allin", RoomID: "room", DealerSeat: 1, StartedAt: now, EndedAt: now,
		SmallBlind: 10, BigBlind: 20, Showdown: true,
		Board: []string{"2c", "7d", "9h", "Js", "Kd"},
		Players: []history.PlayerResult{
			{UserID: "a", Seat: 1, StartingStack: 100, EndingStack: 200, Delta: 100, HoleCards: []string{"As", "Ah"}},
			{UserID: "b", Seat: 2, StartingStack: 100, EndingStack: 0, Delta: -100, HoleCards: []string{"3c", "4c"}},
		},
		Actions: []history.Action{
			{UserID: "a", Sequence: 1, Street: "preflop", Type: "all_in", Committed: 90, RaiseTo: 100},
			{UserID: "b", Sequence: 2, Street: "preflop", Type: "call", Committed: 80},
		},
		PotAwards: []holdem.PotAward{{
			Amount: 200, WinnerPlayerIDs: []string{"a"},
			Payouts: []holdem.Payout{{PlayerID: "a", Amount: 200}},
		}},
		RevealedHands: []holdem.RevealedHand{
			{PlayerID: "a", HoleCards: []string{"As", "Ah"}, Category: "one_pair"},
			{PlayerID: "b", HoleCards: []string{"3c", "4c"}, Category: "high_card"},
		},
	}
	timeline, err := Build(hand, "a")
	if err != nil {
		t.Fatal(err)
	}
	var streets int
	for _, step := range timeline.Steps {
		if step.Kind != KindStreet {
			continue
		}
		streets++
		for _, seat := range step.Seats {
			if seat.LastAction != "" || !seat.AllIn {
				t.Fatalf("%s at %s: %+v", seat.UserID, step.Street, seat)
			}
		}
	}
	if streets != 3 {
		t.Fatalf("expected flop, turn and river to be dealt, got %d street steps", streets)
	}
}
