package holdem

import (
	cryptorand "crypto/rand"
	"errors"
	"math/big"
)

type IntnSource interface {
	Intn(max int) (int, error)
}

type CryptoRandom struct{}

func (CryptoRandom) Intn(max int) (int, error) {
	if max <= 0 {
		return 0, errors.New("random upper bound must be positive")
	}
	value, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0, err
	}
	return int(value.Int64()), nil
}

type Deck struct {
	cards []Card
	next  int
}

func NewShuffledDeck(random IntnSource) (*Deck, error) {
	if random == nil {
		return nil, errors.New("random source is required")
	}
	cards := make([]Card, 0, 52)
	for suit := Clubs; suit <= Spades; suit++ {
		for rank := Two; rank <= Ace; rank++ {
			cards = append(cards, Card{Rank: rank, Suit: suit})
		}
	}
	for index := len(cards) - 1; index > 0; index-- {
		swapIndex, err := random.Intn(index + 1)
		if err != nil {
			return nil, err
		}
		if swapIndex < 0 || swapIndex > index {
			return nil, errors.New("random source returned an out-of-range value")
		}
		cards[index], cards[swapIndex] = cards[swapIndex], cards[index]
	}
	return &Deck{cards: cards}, nil
}

func (deck *Deck) Draw(count int) ([]Card, error) {
	if deck == nil || count < 0 || deck.next+count > len(deck.cards) {
		return nil, errors.New("not enough cards in deck")
	}
	result := append([]Card(nil), deck.cards[deck.next:deck.next+count]...)
	deck.next += count
	return result, nil
}

func (deck *Deck) Remaining() int {
	if deck == nil {
		return 0
	}
	return len(deck.cards) - deck.next
}
