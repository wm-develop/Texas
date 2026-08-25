package holdem

import (
	"fmt"
	"strings"
)

type Rank uint8

const (
	Two Rank = iota + 2
	Three
	Four
	Five
	Six
	Seven
	Eight
	Nine
	Ten
	Jack
	Queen
	King
	Ace
)

type Suit uint8

const (
	Clubs Suit = iota
	Diamonds
	Hearts
	Spades
)

type Card struct {
	Rank Rank `json:"rank"`
	Suit Suit `json:"suit"`
}

func (card Card) Valid() bool {
	return card.Rank >= Two && card.Rank <= Ace && card.Suit <= Spades
}

func (card Card) String() string {
	if !card.Valid() {
		return "??"
	}
	return rankString(card.Rank) + suitString(card.Suit)
}

func ParseCard(value string) (Card, error) {
	value = strings.TrimSpace(value)
	if len(value) != 2 {
		return Card{}, fmt.Errorf("invalid card %q", value)
	}

	rank, ok := parseRank(value[0])
	if !ok {
		return Card{}, fmt.Errorf("invalid card rank %q", value[0])
	}
	suit, ok := parseSuit(value[1])
	if !ok {
		return Card{}, fmt.Errorf("invalid card suit %q", value[1])
	}
	return Card{Rank: rank, Suit: suit}, nil
}

func parseRank(value byte) (Rank, bool) {
	switch value {
	case '2', '3', '4', '5', '6', '7', '8', '9':
		return Rank(value - '0'), true
	case 'T', 't':
		return Ten, true
	case 'J', 'j':
		return Jack, true
	case 'Q', 'q':
		return Queen, true
	case 'K', 'k':
		return King, true
	case 'A', 'a':
		return Ace, true
	default:
		return 0, false
	}
}

func parseSuit(value byte) (Suit, bool) {
	switch value {
	case 'c', 'C':
		return Clubs, true
	case 'd', 'D':
		return Diamonds, true
	case 'h', 'H':
		return Hearts, true
	case 's', 'S':
		return Spades, true
	default:
		return 0, false
	}
}

func rankString(rank Rank) string {
	if rank >= Two && rank <= Nine {
		return string(rune('0' + rank))
	}
	return map[Rank]string{Ten: "T", Jack: "J", Queen: "Q", King: "K", Ace: "A"}[rank]
}

func suitString(suit Suit) string {
	return [...]string{"c", "d", "h", "s"}[suit]
}
