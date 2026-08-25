package history

import (
	"testing"
	"time"

	"texas/services/game_server/internal/game/holdem"
)

func TestRecentHandsAreNewestFirstAndProtectHiddenCards(t *testing.T) {
	store := NewInMemoryStore()
	for index := 1; index <= 2; index++ {
		hand := Hand{
			HandID: "hand_" + string(rune('0'+index)), RoomID: "room", RoomCode: "123456",
			EndedAt: time.Unix(int64(index), 0), Board: []string{"As", "Kd", "Qc"},
			Players: []PlayerResult{
				{UserID: "a", DisplayName: "A", Seat: 1, StartingStack: 100, EndingStack: 90, Delta: -10, HoleCards: []string{"2c", "3c"}},
				{UserID: "b", DisplayName: "B", Seat: 2, StartingStack: 100, EndingStack: 110, Delta: 10, HoleCards: []string{"Ah", "Ad"}},
			},
			Showdown: index == 2,
		}
		if index == 2 {
			hand.RevealedHands = []holdem.RevealedHand{{PlayerID: "b", HoleCards: []string{"Ah", "Ad"}, Category: "one_pair"}}
		}
		if err := store.Append(hand); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	recent := store.RecentForPlayer("a", 20)
	if len(recent) != 2 || recent[0].HandID != "hand_2" {
		t.Fatalf("recent=%#v", recent)
	}
	if len(recent[0].Players[0].HoleCards) != 2 || len(recent[0].Players[1].HoleCards) != 2 {
		t.Fatalf("showdown cards missing: %#v", recent[0].Players)
	}
	if len(recent[1].Players[1].HoleCards) != 0 {
		t.Fatalf("hidden opponent cards leaked: %#v", recent[1].Players[1])
	}
}
