package holdem

import (
	"reflect"
	"testing"
)

func TestBuildPotsCreatesMainSidePotsAndRefund(t *testing.T) {
	result, err := BuildPots([]Contribution{
		{PlayerID: "a", Seat: 1, Amount: 100},
		{PlayerID: "b", Seat: 2, Amount: 200},
		{PlayerID: "c", Seat: 3, Amount: 300},
	})
	if err != nil {
		t.Fatalf("BuildPots: %v", err)
	}

	wantPots := []Pot{
		{Amount: 300, EligiblePlayerIDs: []string{"a", "b", "c"}},
		{Amount: 200, EligiblePlayerIDs: []string{"b", "c"}},
	}
	if !reflect.DeepEqual(result.Pots, wantPots) {
		t.Fatalf("pots = %#v, want %#v", result.Pots, wantPots)
	}
	if result.Refunds["c"] != 100 {
		t.Fatalf("c refund = %d, want 100", result.Refunds["c"])
	}
}

func TestBuildPotsIncludesFoldedMoneyButExcludesEligibility(t *testing.T) {
	result, err := BuildPots([]Contribution{
		{PlayerID: "a", Seat: 1, Amount: 100, Folded: true},
		{PlayerID: "b", Seat: 2, Amount: 100},
		{PlayerID: "c", Seat: 3, Amount: 100},
	})
	if err != nil {
		t.Fatalf("BuildPots: %v", err)
	}

	want := []Pot{{Amount: 300, EligiblePlayerIDs: []string{"b", "c"}}}
	if !reflect.DeepEqual(result.Pots, want) {
		t.Fatalf("pots = %#v, want %#v", result.Pots, want)
	}
}

func TestBuildPotsCreatesMultipleAllInLayers(t *testing.T) {
	result, err := BuildPots([]Contribution{
		{PlayerID: "a", Seat: 1, Amount: 50},
		{PlayerID: "b", Seat: 2, Amount: 100},
		{PlayerID: "c", Seat: 3, Amount: 200},
		{PlayerID: "d", Seat: 4, Amount: 200, Folded: true},
	})
	if err != nil {
		t.Fatalf("BuildPots: %v", err)
	}

	want := []Pot{
		{Amount: 200, EligiblePlayerIDs: []string{"a", "b", "c"}},
		{Amount: 150, EligiblePlayerIDs: []string{"b", "c"}},
		{Amount: 200, EligiblePlayerIDs: []string{"c"}},
	}
	if !reflect.DeepEqual(result.Pots, want) {
		t.Fatalf("pots = %#v, want %#v", result.Pots, want)
	}
	if len(result.Refunds) != 0 {
		t.Fatalf("refunds = %#v, want empty", result.Refunds)
	}
}

func TestBuildPotsRejectsInvalidContributions(t *testing.T) {
	tests := [][]Contribution{
		{{PlayerID: "a", Seat: 1, Amount: -1}},
		{{PlayerID: "a", Seat: 1, Amount: 10}, {PlayerID: "a", Seat: 2, Amount: 10}},
		{{PlayerID: "a", Seat: 1, Amount: 10}, {PlayerID: "b", Seat: 1, Amount: 10}},
		{{PlayerID: "a", Seat: 1, Amount: 10, Folded: true}, {PlayerID: "b", Seat: 2, Amount: 10, Folded: true}},
	}
	for _, contributions := range tests {
		if _, err := BuildPots(contributions); err == nil {
			t.Fatalf("BuildPots(%#v) should fail", contributions)
		}
	}
}

func TestAwardPotsEvaluatesEachSidePotIndependently(t *testing.T) {
	aceHigh := mustValue(t, "As Kd 9c 7h 4s 3d 2c")
	pair := mustValue(t, "Qc Qd As 9h 8c 3d 2s")
	straight := mustValue(t, "9c 8d 7s 6h 5c Ah Kd")

	result, err := AwardPots(
		[]Pot{
			{Amount: 300, EligiblePlayerIDs: []string{"a", "b", "c"}},
			{Amount: 200, EligiblePlayerIDs: []string{"b", "c"}},
		},
		[]Contender{
			{PlayerID: "a", Seat: 1, Hand: straight},
			{PlayerID: "b", Seat: 2, Hand: pair},
			{PlayerID: "c", Seat: 3, Hand: aceHigh},
		},
		10,
		10,
	)
	if err != nil {
		t.Fatalf("AwardPots: %v", err)
	}
	if result.TotalByPlayer["a"] != 300 || result.TotalByPlayer["b"] != 200 {
		t.Fatalf("totals = %#v, want a=300 b=200", result.TotalByPlayer)
	}
}

func TestAwardPotsSplitsOddChipClockwiseFromDealer(t *testing.T) {
	tied := mustValue(t, "As Ks Qs Js Ts 3d 2c")
	result, err := AwardPots(
		[]Pot{{Amount: 101, EligiblePlayerIDs: []string{"near", "far"}}},
		[]Contender{
			{PlayerID: "far", Seat: 8, Hand: tied},
			{PlayerID: "near", Seat: 2, Hand: tied},
		},
		10,
		10,
	)
	if err != nil {
		t.Fatalf("AwardPots: %v", err)
	}
	if result.TotalByPlayer["near"] != 51 || result.TotalByPlayer["far"] != 50 {
		t.Fatalf("totals = %#v, want near=51 far=50", result.TotalByPlayer)
	}
	if !reflect.DeepEqual(result.Pots[0].WinnerPlayerIDs, []string{"near", "far"}) {
		t.Fatalf("winner order = %#v", result.Pots[0].WinnerPlayerIDs)
	}
}

func TestPotConstructionConservesChips(t *testing.T) {
	contributions := []Contribution{
		{PlayerID: "a", Seat: 1, Amount: 17},
		{PlayerID: "b", Seat: 2, Amount: 83, Folded: true},
		{PlayerID: "c", Seat: 3, Amount: 201},
		{PlayerID: "d", Seat: 4, Amount: 83},
	}
	result, err := BuildPots(contributions)
	if err != nil {
		t.Fatalf("BuildPots: %v", err)
	}

	var contributed int64
	for _, contribution := range contributions {
		contributed += contribution.Amount
	}
	var accounted int64
	for _, pot := range result.Pots {
		accounted += pot.Amount
	}
	for _, refund := range result.Refunds {
		accounted += refund
	}
	if accounted != contributed {
		t.Fatalf("accounted = %d, contributed = %d", accounted, contributed)
	}
}

func mustValue(t *testing.T, cards string) HandValue {
	t.Helper()
	value, err := Evaluate(mustCards(t, cards))
	if err != nil {
		t.Fatalf("Evaluate(%q): %v", cards, err)
	}
	return value
}

// 边池的意义是「部分玩家无权争夺」，只有 all in 造成投入档位差异时才产生。
// 弃牌者投入较少同样会切出一层，但那一层与下一层的有资格名单完全相同；
// 此前它们各自成池，玩家会在没人 all in 的牌局里看到「边池 1」。
func TestFoldedPlayersDoNotCreateSidePots(t *testing.T) {
	result, err := BuildPots([]Contribution{
		{PlayerID: "folded", Seat: 1, Amount: 100, Folded: true},
		{PlayerID: "caller", Seat: 2, Amount: 500},
		{PlayerID: "raiser", Seat: 3, Amount: 500},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Pots) != 1 {
		t.Fatalf("no one is all in, so there must be a single pot, got %#v", result.Pots)
	}
	if result.Pots[0].Amount != 1_100 {
		t.Fatalf("the folded player's chips belong to the main pot, got %d", result.Pots[0].Amount)
	}
	if len(result.Pots[0].EligiblePlayerIDs) != 2 {
		t.Fatalf("only the two live players may win it: %#v", result.Pots[0].EligiblePlayerIDs)
	}
}

// 多人在不同街弃牌同样不该切出边池。
func TestMultipleFoldLevelsStillProduceOnePot(t *testing.T) {
	result, err := BuildPots([]Contribution{
		{PlayerID: "blind", Seat: 1, Amount: 10, Folded: true},
		{PlayerID: "early", Seat: 2, Amount: 100, Folded: true},
		{PlayerID: "middle", Seat: 3, Amount: 260, Folded: true},
		{PlayerID: "hero", Seat: 4, Amount: 600},
		{PlayerID: "villain", Seat: 5, Amount: 600},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Pots) != 1 {
		t.Fatalf("folds must not create side pots, got %#v", result.Pots)
	}
	if result.Pots[0].Amount != 1_570 {
		t.Fatalf("every chip belongs to the single pot, got %d", result.Pots[0].Amount)
	}
}

// All in 造成的档位差异仍然必须切出边池：短码玩家赢不走他没投过的钱。
func TestAllInStillCreatesSidePots(t *testing.T) {
	result, err := BuildPots([]Contribution{
		{PlayerID: "short", Seat: 1, Amount: 100},
		{PlayerID: "mid", Seat: 2, Amount: 500},
		{PlayerID: "deep", Seat: 3, Amount: 500},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Pots) != 2 {
		t.Fatalf("expected a main pot plus one side pot, got %#v", result.Pots)
	}
	if result.Pots[0].Amount != 300 || len(result.Pots[0].EligiblePlayerIDs) != 3 {
		t.Fatalf("main pot should be 100 from each of the three: %#v", result.Pots[0])
	}
	if result.Pots[1].Amount != 800 || len(result.Pots[1].EligiblePlayerIDs) != 2 {
		t.Fatalf("the short stack must not be eligible for the side pot: %#v", result.Pots[1])
	}
}

// 边池只按「谁有资格」切，弃牌者混在 all in 之间也不会多切一层。
func TestFoldsBetweenAllInLevelsDoNotAddPots(t *testing.T) {
	result, err := BuildPots([]Contribution{
		{PlayerID: "folded", Seat: 1, Amount: 250, Folded: true},
		{PlayerID: "short", Seat: 2, Amount: 100},
		{PlayerID: "mid", Seat: 3, Amount: 600},
		{PlayerID: "deep", Seat: 4, Amount: 600},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 100 一层（四人有份，三人有资格）；100→600 之间弃牌者只跟到 250，
	// 但 250 那一层和 600 那一层的有资格名单都是 mid+deep，应当合并。
	if len(result.Pots) != 2 {
		t.Fatalf("expected exactly two pots, got %#v", result.Pots)
	}
	if result.Pots[0].Amount != 400 {
		t.Fatalf("main pot = 100 from four players, got %d", result.Pots[0].Amount)
	}
	if result.Pots[1].Amount != 1_150 {
		t.Fatalf("side pot should absorb the folded player's extra chips, got %d", result.Pots[1].Amount)
	}
	var total int64
	for _, pot := range result.Pots {
		total += pot.Amount
	}
	for _, refund := range result.Refunds {
		total += refund
	}
	if total != 1_550 {
		t.Fatalf("chips must be conserved, got %d", total)
	}
}
