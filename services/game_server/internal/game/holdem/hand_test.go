package holdem

import "testing"

func TestEvaluateCategories(t *testing.T) {
	tests := []struct {
		name     string
		cards    string
		category Category
		first    Rank
	}{
		{name: "straight flush", cards: "As Ks Qs Js Ts 2d 3c", category: StraightFlush, first: Ace},
		{name: "four of a kind", cards: "Ah Ad Ac As Kd 2c 3h", category: FourOfAKind, first: Ace},
		{name: "full house", cards: "Kh Kd Kc 2s 2d 9h 8c", category: FullHouse, first: King},
		{name: "flush", cards: "Ah Jh 8h 4h 2h Kc Qd", category: Flush, first: Ace},
		{name: "straight", cards: "9c 8d 7s 6h 5c Ah Kd", category: Straight, first: Nine},
		{name: "wheel straight", cards: "As 2d 3c 4h 5s Kh Qd", category: Straight, first: Five},
		{name: "three of a kind", cards: "Qc Qd Qs Ah 9c 3d 2s", category: ThreeOfAKind, first: Queen},
		{name: "two pair", cards: "Jc Jd 8s 8h Ac 3d 2s", category: TwoPair, first: Jack},
		{name: "one pair", cards: "Tc Td As Kh 8c 3d 2s", category: OnePair, first: Ten},
		{name: "high card", cards: "As Jd 9c 7h 4s 3d 2c", category: HighCard, first: Ace},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, err := Evaluate(mustCards(t, test.cards))
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if value.Category != test.category {
				t.Fatalf("category = %s, want %s", value.Category, test.category)
			}
			if value.Tiebreak[0] != test.first {
				t.Fatalf("first tiebreak = %d, want %d", value.Tiebreak[0], test.first)
			}
		})
	}
}

func TestEvaluateUsesKickers(t *testing.T) {
	aceKicker, err := Evaluate(mustCards(t, "Qc Qd As 9h 8c 3d 2s"))
	if err != nil {
		t.Fatalf("Evaluate ace kicker: %v", err)
	}
	kingKicker, err := Evaluate(mustCards(t, "Qh Qs Ks 9d 8h 3c 2d"))
	if err != nil {
		t.Fatalf("Evaluate king kicker: %v", err)
	}
	if aceKicker.Compare(kingKicker) <= 0 {
		t.Fatal("ace kicker should beat king kicker")
	}
}

func TestEvaluateBoardCanPlayForTie(t *testing.T) {
	first, err := Evaluate(mustCards(t, "2c 3d As Ks Qs Js Ts"))
	if err != nil {
		t.Fatalf("Evaluate first hand: %v", err)
	}
	second, err := Evaluate(mustCards(t, "Ah Ad As Ks Qs Js Ts"))
	if err != nil {
		t.Fatalf("Evaluate second hand: %v", err)
	}
	if first.Compare(second) != 0 {
		t.Fatalf("board royal flush should tie: first=%v second=%v", first, second)
	}
}

func TestEvaluateRejectsInvalidInput(t *testing.T) {
	if _, err := Evaluate(mustCards(t, "As Ks Qs Js")); err == nil {
		t.Fatal("expected too few cards to fail")
	}
	if _, err := Evaluate(mustCards(t, "As As Qs Js Ts")); err == nil {
		t.Fatal("expected duplicate card to fail")
	}
}

func TestParseCardRoundTrip(t *testing.T) {
	card, err := ParseCard("Ah")
	if err != nil {
		t.Fatalf("ParseCard: %v", err)
	}
	if card.String() != "Ah" {
		t.Fatalf("String = %q, want Ah", card.String())
	}
}

func mustCards(t *testing.T, value string) []Card {
	t.Helper()
	parts := splitCards(value)
	cards := make([]Card, 0, len(parts))
	for _, part := range parts {
		card, err := ParseCard(part)
		if err != nil {
			t.Fatalf("ParseCard(%q): %v", part, err)
		}
		cards = append(cards, card)
	}
	return cards
}

func splitCards(value string) []string {
	var result []string
	for index := 0; index+1 < len(value); index += 3 {
		result = append(result, value[index:index+2])
	}
	return result
}
