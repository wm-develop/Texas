package holdem

import (
	"errors"
	"sort"
)

type Contribution struct {
	PlayerID string
	Seat     int
	Amount   int64
	Folded   bool
}

type Pot struct {
	Amount            int64
	EligiblePlayerIDs []string
}

type PotBuildResult struct {
	Pots    []Pot
	Refunds map[string]int64
}

type Contender struct {
	PlayerID string
	Seat     int
	Hand     HandValue
}

type Payout struct {
	PlayerID string `json:"playerId"`
	Amount   int64  `json:"amount"`
}

type PotAward struct {
	PotIndex        int      `json:"potIndex"`
	Amount          int64    `json:"amount"`
	WinnerPlayerIDs []string `json:"winnerPlayerIds"`
	Payouts         []Payout `json:"payouts"`
}

type AwardResult struct {
	Pots          []PotAward
	TotalByPlayer map[string]int64
}

func BuildPots(contributions []Contribution) (PotBuildResult, error) {
	result := PotBuildResult{Refunds: make(map[string]int64)}
	if len(contributions) == 0 {
		return result, nil
	}

	levels := make([]int64, 0, len(contributions))
	seenLevels := make(map[int64]struct{}, len(contributions))
	seenPlayers := make(map[string]struct{}, len(contributions))
	seenSeats := make(map[int]struct{}, len(contributions))
	for _, contribution := range contributions {
		if contribution.PlayerID == "" || contribution.Seat <= 0 || contribution.Amount < 0 {
			return PotBuildResult{}, errors.New("invalid contribution")
		}
		if _, exists := seenPlayers[contribution.PlayerID]; exists {
			return PotBuildResult{}, errors.New("duplicate contributing player")
		}
		if _, exists := seenSeats[contribution.Seat]; exists {
			return PotBuildResult{}, errors.New("duplicate contributing seat")
		}
		seenPlayers[contribution.PlayerID] = struct{}{}
		seenSeats[contribution.Seat] = struct{}{}
		if contribution.Amount == 0 {
			continue
		}
		if _, exists := seenLevels[contribution.Amount]; !exists {
			levels = append(levels, contribution.Amount)
			seenLevels[contribution.Amount] = struct{}{}
		}
	}
	sort.Slice(levels, func(left, right int) bool { return levels[left] < levels[right] })

	var previousLevel int64
	for _, level := range levels {
		participants := make([]Contribution, 0, len(contributions))
		eligible := make([]Contribution, 0, len(contributions))
		for _, contribution := range contributions {
			if contribution.Amount < level {
				continue
			}
			participants = append(participants, contribution)
			if !contribution.Folded {
				eligible = append(eligible, contribution)
			}
		}

		layer := level - previousLevel
		previousLevel = level
		if layer <= 0 || len(participants) == 0 {
			continue
		}
		if len(participants) == 1 {
			result.Refunds[participants[0].PlayerID] += layer
			continue
		}
		if len(eligible) == 0 {
			return PotBuildResult{}, errors.New("pot has no eligible player")
		}

		sort.Slice(eligible, func(left, right int) bool {
			return eligible[left].Seat < eligible[right].Seat
		})
		playerIDs := make([]string, len(eligible))
		for index, contribution := range eligible {
			playerIDs[index] = contribution.PlayerID
		}
		result.Pots = append(result.Pots, Pot{
			Amount:            layer * int64(len(participants)),
			EligiblePlayerIDs: playerIDs,
		})
	}

	return result, nil
}

func AwardPots(pots []Pot, contenders []Contender, dealerSeat int, maxSeats int) (AwardResult, error) {
	result := AwardResult{TotalByPlayer: make(map[string]int64)}
	if maxSeats < 2 || dealerSeat <= 0 || dealerSeat > maxSeats {
		return AwardResult{}, errors.New("invalid table seats")
	}

	byPlayer := make(map[string]Contender, len(contenders))
	seenSeats := make(map[int]struct{}, len(contenders))
	for _, contender := range contenders {
		if contender.PlayerID == "" || contender.Seat <= 0 || contender.Seat > maxSeats {
			return AwardResult{}, errors.New("invalid contender")
		}
		if _, exists := byPlayer[contender.PlayerID]; exists {
			return AwardResult{}, errors.New("duplicate contender")
		}
		if _, exists := seenSeats[contender.Seat]; exists {
			return AwardResult{}, errors.New("duplicate contender seat")
		}
		byPlayer[contender.PlayerID] = contender
		seenSeats[contender.Seat] = struct{}{}
	}

	for potIndex, pot := range pots {
		if pot.Amount <= 0 || len(pot.EligiblePlayerIDs) == 0 {
			return AwardResult{}, errors.New("invalid pot")
		}

		eligible := make([]Contender, 0, len(pot.EligiblePlayerIDs))
		seenEligible := make(map[string]struct{}, len(pot.EligiblePlayerIDs))
		for _, playerID := range pot.EligiblePlayerIDs {
			if _, exists := seenEligible[playerID]; exists {
				return AwardResult{}, errors.New("duplicate eligible player")
			}
			contender, exists := byPlayer[playerID]
			if !exists {
				return AwardResult{}, errors.New("missing eligible hand")
			}
			seenEligible[playerID] = struct{}{}
			eligible = append(eligible, contender)
		}

		best := eligible[0].Hand
		winners := []Contender{eligible[0]}
		for _, contender := range eligible[1:] {
			switch contender.Hand.Compare(best) {
			case 1:
				best = contender.Hand
				winners = []Contender{contender}
			case 0:
				winners = append(winners, contender)
			}
		}
		sort.Slice(winners, func(left, right int) bool {
			return clockwiseDistance(dealerSeat, winners[left].Seat, maxSeats) <
				clockwiseDistance(dealerSeat, winners[right].Seat, maxSeats)
		})

		share := pot.Amount / int64(len(winners))
		remainder := pot.Amount % int64(len(winners))
		award := PotAward{
			PotIndex:        potIndex,
			Amount:          pot.Amount,
			WinnerPlayerIDs: make([]string, len(winners)),
			Payouts:         make([]Payout, len(winners)),
		}
		for index, winner := range winners {
			amount := share
			if int64(index) < remainder {
				amount++
			}
			award.WinnerPlayerIDs[index] = winner.PlayerID
			award.Payouts[index] = Payout{PlayerID: winner.PlayerID, Amount: amount}
			result.TotalByPlayer[winner.PlayerID] += amount
		}
		result.Pots = append(result.Pots, award)
	}

	return result, nil
}

func clockwiseDistance(dealerSeat int, seat int, maxSeats int) int {
	distance := (seat - dealerSeat + maxSeats) % maxSeats
	if distance == 0 {
		return maxSeats
	}
	return distance
}
