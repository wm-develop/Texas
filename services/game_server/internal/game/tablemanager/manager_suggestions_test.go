package tablemanager

import (
	"testing"

	"texas/services/game_server/internal/game/holdem"
)

// 底池远大于剩余筹码时，多个比例档都会超过上限。此前它们被压到 MaxRaiseTo
// 却保留原标签，界面上就会出现一个写着「1/2 池」、实际是最大加注的按钮。
func TestBetSuggestionsDropFractionsCoveredByAllIn(t *testing.T) {
	players := []holdem.Player{{PlayerID: "actor", StreetBet: 0, Stack: 205}}
	options := holdem.ActionOptions{
		ToCall: 100, CanRaise: true, CanAllIn: true, MinRaiseTo: 200, MaxRaiseTo: 200,
	}
	suggestions := betSuggestions(1_000, players, "actor", options, 10)

	if len(suggestions) != 1 || suggestions[0].Label != "all_in" ||
		suggestions[0].RaiseTo != 205 {
		t.Fatalf("expected only the all-in suggestion, got %#v", suggestions)
	}
}

// 不能全下时（例如本轮已经加注过，RaiseAllowed 为 false），被压到上限的档位
// 必须保留，但标签要改成 max_raise，不能继续冒充比例档。
func TestBetSuggestionsRelabelFractionsClampedToMaximum(t *testing.T) {
	players := []holdem.Player{{PlayerID: "actor", StreetBet: 0, Stack: 205}}
	options := holdem.ActionOptions{
		ToCall: 100, CanRaise: true, CanAllIn: false, MinRaiseTo: 200, MaxRaiseTo: 200,
	}
	suggestions := betSuggestions(1_000, players, "actor", options, 10)

	if len(suggestions) != 1 {
		t.Fatalf("clamped fractions must collapse to one entry, got %#v", suggestions)
	}
	if suggestions[0].Label != "max_raise" || suggestions[0].RaiseTo != 200 {
		t.Fatalf("suggestion=%#v want max_raise at 200", suggestions[0])
	}
}

// 低于最小加注的档位仍然抬到下界并改名 min_raise，这条既有行为不能被破坏。
func TestBetSuggestionsStillCollapseLowFractionsToMinimumRaise(t *testing.T) {
	players := []holdem.Player{{PlayerID: "actor", StreetBet: 40, Stack: 1_940}}
	options := holdem.ActionOptions{
		ToCall: 60, CanRaise: true, CanAllIn: true, MinRaiseTo: 200, MaxRaiseTo: 1_980,
	}
	suggestions := betSuggestions(140, players, "actor", options, 10)

	if len(suggestions) == 0 || suggestions[0].Label != "min_raise" {
		t.Fatalf("first suggestion=%#v want min_raise", suggestions)
	}
	for _, suggestion := range suggestions {
		if suggestion.Label == "max_raise" {
			t.Fatalf("nothing should clamp to the maximum here: %#v", suggestions)
		}
	}
}
