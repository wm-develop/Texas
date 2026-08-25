package holdem

import "testing"

type zeroRandom struct{}

func (zeroRandom) Intn(int) (int, error) { return 0, nil }

func TestDeckContainsEveryCardExactlyOnce(t *testing.T) {
	deck, err := NewShuffledDeck(zeroRandom{})
	if err != nil {
		t.Fatalf("NewShuffledDeck: %v", err)
	}
	cards, err := deck.Draw(52)
	if err != nil {
		t.Fatalf("Draw: %v", err)
	}
	seen := make(map[Card]struct{}, 52)
	for _, card := range cards {
		if !card.Valid() {
			t.Fatalf("invalid card: %v", card)
		}
		if _, exists := seen[card]; exists {
			t.Fatalf("duplicate card: %v", card)
		}
		seen[card] = struct{}{}
	}
	if len(seen) != 52 || deck.Remaining() != 0 {
		t.Fatalf("unique=%d remaining=%d", len(seen), deck.Remaining())
	}
	if _, err := deck.Draw(1); err == nil {
		t.Fatal("expected exhausted deck draw to fail")
	}
}

func TestDeckRejectsInvalidRandomSource(t *testing.T) {
	if _, err := NewShuffledDeck(nil); err == nil {
		t.Fatal("expected nil random source to fail")
	}
}
