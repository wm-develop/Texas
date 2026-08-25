package holdem

import (
	"errors"
	"sort"
)

type Category uint8

const (
	HighCard Category = iota
	OnePair
	TwoPair
	ThreeOfAKind
	Straight
	Flush
	FullHouse
	FourOfAKind
	StraightFlush
)

var categoryNames = [...]string{
	"high_card",
	"one_pair",
	"two_pair",
	"three_of_a_kind",
	"straight",
	"flush",
	"full_house",
	"four_of_a_kind",
	"straight_flush",
}

func (category Category) String() string {
	if int(category) >= len(categoryNames) {
		return "unknown"
	}
	return categoryNames[category]
}

type HandValue struct {
	Category Category `json:"category"`
	Tiebreak [5]Rank  `json:"tiebreak"`
}

func (value HandValue) Compare(other HandValue) int {
	if value.Category < other.Category {
		return -1
	}
	if value.Category > other.Category {
		return 1
	}
	for index := range value.Tiebreak {
		if value.Tiebreak[index] < other.Tiebreak[index] {
			return -1
		}
		if value.Tiebreak[index] > other.Tiebreak[index] {
			return 1
		}
	}
	return 0
}

func Evaluate(cards []Card) (HandValue, error) {
	if len(cards) < 5 || len(cards) > 7 {
		return HandValue{}, errors.New("holdem evaluation requires five to seven cards")
	}
	seen := make(map[Card]struct{}, len(cards))
	for _, card := range cards {
		if !card.Valid() {
			return HandValue{}, errors.New("invalid card")
		}
		if _, exists := seen[card]; exists {
			return HandValue{}, errors.New("duplicate card")
		}
		seen[card] = struct{}{}
	}

	var best HandValue
	hasBest := false
	for first := 0; first < len(cards)-4; first++ {
		for second := first + 1; second < len(cards)-3; second++ {
			for third := second + 1; third < len(cards)-2; third++ {
				for fourth := third + 1; fourth < len(cards)-1; fourth++ {
					for fifth := fourth + 1; fifth < len(cards); fifth++ {
						candidate := evaluateFive([5]Card{
							cards[first], cards[second], cards[third], cards[fourth], cards[fifth],
						})
						if !hasBest || candidate.Compare(best) > 0 {
							best = candidate
							hasBest = true
						}
					}
				}
			}
		}
	}
	return best, nil
}

func evaluateFive(cards [5]Card) HandValue {
	counts := make(map[Rank]int, 5)
	ranks := make([]Rank, 0, 5)
	flush := true
	for index, card := range cards {
		counts[card.Rank]++
		ranks = append(ranks, card.Rank)
		if index > 0 && card.Suit != cards[0].Suit {
			flush = false
		}
	}
	sortRanksDescending(ranks)
	straightHigh := detectStraightHigh(counts)

	if flush && straightHigh != 0 {
		return HandValue{Category: StraightFlush, Tiebreak: [5]Rank{straightHigh}}
	}

	groups := groupedRanks(counts)
	if groups[0].count == 4 {
		return HandValue{Category: FourOfAKind, Tiebreak: [5]Rank{groups[0].rank, groups[1].rank}}
	}
	if groups[0].count == 3 && groups[1].count == 2 {
		return HandValue{Category: FullHouse, Tiebreak: [5]Rank{groups[0].rank, groups[1].rank}}
	}
	if flush {
		return HandValue{Category: Flush, Tiebreak: rankArray(ranks)}
	}
	if straightHigh != 0 {
		return HandValue{Category: Straight, Tiebreak: [5]Rank{straightHigh}}
	}
	if groups[0].count == 3 {
		return HandValue{
			Category: ThreeOfAKind,
			Tiebreak: [5]Rank{groups[0].rank, groups[1].rank, groups[2].rank},
		}
	}
	if groups[0].count == 2 && groups[1].count == 2 {
		return HandValue{
			Category: TwoPair,
			Tiebreak: [5]Rank{groups[0].rank, groups[1].rank, groups[2].rank},
		}
	}
	if groups[0].count == 2 {
		return HandValue{
			Category: OnePair,
			Tiebreak: [5]Rank{
				groups[0].rank,
				groups[1].rank,
				groups[2].rank,
				groups[3].rank,
			},
		}
	}
	return HandValue{Category: HighCard, Tiebreak: rankArray(ranks)}
}

type rankGroup struct {
	rank  Rank
	count int
}

func groupedRanks(counts map[Rank]int) []rankGroup {
	groups := make([]rankGroup, 0, len(counts))
	for rank, count := range counts {
		groups = append(groups, rankGroup{rank: rank, count: count})
	}
	sort.Slice(groups, func(left, right int) bool {
		if groups[left].count != groups[right].count {
			return groups[left].count > groups[right].count
		}
		return groups[left].rank > groups[right].rank
	})
	return groups
}

func detectStraightHigh(counts map[Rank]int) Rank {
	if len(counts) != 5 {
		return 0
	}
	if counts[Ace] == 1 && counts[Two] == 1 && counts[Three] == 1 && counts[Four] == 1 && counts[Five] == 1 {
		return Five
	}
	var minimum Rank = Ace
	var maximum Rank = Two
	for rank := range counts {
		if rank < minimum {
			minimum = rank
		}
		if rank > maximum {
			maximum = rank
		}
	}
	if maximum-minimum == 4 {
		return maximum
	}
	return 0
}

func sortRanksDescending(ranks []Rank) {
	sort.Slice(ranks, func(left, right int) bool { return ranks[left] > ranks[right] })
}

func rankArray(ranks []Rank) [5]Rank {
	var result [5]Rank
	copy(result[:], ranks)
	return result
}
