package holdem

import (
	"errors"
	"sort"
	"strings"
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
	// DisplayName 由 tablemanager 在组装快照时填入；规则引擎不认识昵称。
	// 结算文案必须自带昵称：赢家常常赢完这手就离开房间，届时房间成员表里
	// 已经没有他，靠座位反查只能退化成显示用户 ID。
	DisplayName string `json:"displayName,omitempty"`
	Amount      int64  `json:"amount"`
}

type PotAward struct {
	PotIndex        int      `json:"potIndex"`
	RunoutIndex     int      `json:"runoutIndex,omitempty"`
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

	// 上一层的有资格名单，用于合并参赛名单相同的相邻层，见下方说明。
	var previousEligibleKey string
	var hasPreviousPot bool

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

		// 边池的意义是「部分玩家无权争夺」，只有 all in 造成投入档位差异时
		// 才会产生。弃牌者投入较少同样会切出一层，但那一层与下一层的有资格
		// 名单完全相同——没人 all in 却显示出「边池 1」会让玩家莫名其妙。
		// 名单相同的相邻层合并成一个池：同一批人比同样的牌，赢家必然相同，
		// 合并后余数也只分一次，与真实牌桌一致。
		eligibleKey := strings.Join(playerIDs, ",")
		amount := layer * int64(len(participants))
		if hasPreviousPot && eligibleKey == previousEligibleKey {
			result.Pots[len(result.Pots)-1].Amount += amount
			continue
		}
		result.Pots = append(result.Pots, Pot{
			Amount:            amount,
			EligiblePlayerIDs: playerIDs,
		})
		previousEligibleKey = eligibleKey
		hasPreviousPot = true
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
