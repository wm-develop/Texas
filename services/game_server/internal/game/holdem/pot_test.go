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
