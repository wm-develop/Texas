package holdem

import (
	"errors"
	"math/bits"
	"sort"
	"strings"

	"texas/services/game_server/internal/ledger"
)

func (table *Table) completeRunout(count int) error {
	if count != 1 && count != 2 {
		return errors.New("invalid runout count")
	}
	prefix := append([]Card(nil), table.board...)
	table.runoutBoards = make([][]Card, 0, count)
	for index := 0; index < count; index++ {
		board, err := table.dealRemainingBoard(prefix)
		if err != nil {
			return err
		}
		table.runoutBoards = append(table.runoutBoards, board)
	}
	table.board = append([]Card(nil), table.runoutBoards[0]...)
	return table.settleHand()
}

func (table *Table) dealRemainingBoard(prefix []Card) ([]Card, error) {
	board := append([]Card(nil), prefix...)
	if len(board) == 0 {
		if _, err := table.deck.Draw(1); err != nil {
			return nil, err
		}
		cards, err := table.deck.Draw(3)
		if err != nil {
			return nil, err
		}
		board = append(board, cards...)
	}
	for len(board) < 5 {
		if _, err := table.deck.Draw(1); err != nil {
			return nil, err
		}
		cards, err := table.deck.Draw(1)
		if err != nil {
			return nil, err
		}
		board = append(board, cards[0])
	}
	return board, nil
}

func (table *Table) advanceStreetOrSettle() error {
	if table.phase == PhaseRiver {
		return table.settleHand()
	}

	for _, player := range table.players {
		if player.Participating {
			player.StreetBet = 0
		}
	}
	table.currentBet = 0
	table.minRaiseIncrement = table.config.BigBlind

	switch table.phase {
	case PhasePreflop:
		if err := table.burnAndDeal(3); err != nil {
			return err
		}
		table.phase = PhaseFlop
	case PhaseFlop:
		if err := table.burnAndDeal(1); err != nil {
			return err
		}
		table.phase = PhaseTurn
	case PhaseTurn:
		if err := table.burnAndDeal(1); err != nil {
			return err
		}
		table.phase = PhaseRiver
	default:
		return errors.New("cannot advance from current phase")
	}

	table.beginBettingRound()
	table.currentSeat = table.nextActionSeat(table.dealerSeat)
	table.revision++
	return table.progressWithoutAction()
}

func (table *Table) burnAndDeal(count int) error {
	if _, err := table.deck.Draw(1); err != nil {
		return err
	}
	cards, err := table.deck.Draw(count)
	if err != nil {
		return err
	}
	table.board = append(table.board, cards...)
	return nil
}

func (table *Table) settleHand() error {
	nonFolded := make([]*Player, 0, len(table.players))
	contributions := make([]Contribution, 0, len(table.players))
	for _, player := range table.players {
		if !player.Participating {
			continue
		}
		contributions = append(contributions, Contribution{
			PlayerID: player.PlayerID,
			Seat:     player.Seat,
			Amount:   player.TotalBet,
			Folded:   player.Folded,
		})
		if !player.Folded {
			nonFolded = append(nonFolded, player)
		}
	}
	if len(nonFolded) == 0 {
		return errors.New("cannot settle without an eligible player")
	}

	potResult, err := BuildPots(contributions)
	if err != nil {
		return err
	}
	for playerID, amount := range potResult.Refunds {
		table.playerByID(playerID).Stack += amount
	}
	// 抽水在分池之前从底池里扣：整手只算一次，再按各池大小摊到每个池上，各池扣除
	// 之和恒等于总抽水。发两次时也只抽这一次，之后各池再对半分到两块牌面。
	var rakeBase int64
	for _, pot := range potResult.Pots {
		rakeBase += pot.Amount
	}
	flopSeen := len(table.board) >= 3 || len(table.runoutBoards) > 0
	rake := table.rake.Amount(rakeBase, flopSeen, table.config.BigBlind)
	shares, err := splitRake(rake, potResult.Pots)
	if err != nil {
		return err
	}
	for index, share := range shares {
		potResult.Pots[index].Amount -= share
	}

	table.phase = PhaseShowdown
	table.currentSeat = 0
	table.revision++
	var awards []PotAward
	revealedHands := make([]RevealedHand, 0, len(nonFolded))
	if len(nonFolded) == 1 {
		winner := nonFolded[0]
		for index, pot := range potResult.Pots {
			winner.Stack += pot.Amount
			awards = append(awards, PotAward{
				PotIndex:        index,
				Amount:          pot.Amount,
				WinnerPlayerIDs: []string{winner.PlayerID},
				Payouts:         []Payout{{PlayerID: winner.PlayerID, Amount: pot.Amount}},
			})
		}
	} else {
		boards := table.runoutBoards
		if len(boards) == 0 {
			boards = [][]Card{append([]Card(nil), table.board...)}
		}
		categories := make(map[string][]string, len(nonFolded))
		for runoutIndex, board := range boards {
			if len(board) != 5 {
				return errors.New("showdown requires five board cards")
			}
			contenders := make([]Contender, 0, len(nonFolded))
			for _, player := range nonFolded {
				cards := append([]Card(nil), board...)
				cards = append(cards, player.HoleCards[:]...)
				hand, evaluateErr := Evaluate(cards)
				if evaluateErr != nil {
					return evaluateErr
				}
				contenders = append(contenders, Contender{
					PlayerID: player.PlayerID, Seat: player.Seat, Hand: hand,
				})
				categories[player.PlayerID] = append(categories[player.PlayerID], hand.Category.String())
			}
			for potIndex, pot := range potResult.Pots {
				amount := pot.Amount / int64(len(boards))
				if int64(runoutIndex) < pot.Amount%int64(len(boards)) {
					amount++
				}
				if amount == 0 {
					continue
				}
				awardResult, awardErr := AwardPots(
					[]Pot{{Amount: amount, EligiblePlayerIDs: pot.EligiblePlayerIDs}},
					contenders, table.dealerSeat, table.config.MaxSeats,
				)
				if awardErr != nil {
					return awardErr
				}
				award := awardResult.Pots[0]
				award.PotIndex = potIndex
				if len(boards) > 1 {
					award.RunoutIndex = runoutIndex + 1
				}
				awards = append(awards, award)
				for playerID, payout := range awardResult.TotalByPlayer {
					table.playerByID(playerID).Stack += payout
				}
			}
		}
		for _, player := range nonFolded {
			revealedHands = append(revealedHands, RevealedHand{
				PlayerID:  player.PlayerID,
				HoleCards: []string{player.HoleCards[0].String(), player.HoleCards[1].String()},
				Category:  strings.Join(categories[player.PlayerID], " / "),
			})
		}
	}

	table.phase = PhaseSettlement
	table.revision++
	settlement := Settlement{
		HandID:         table.handID,
		PotAwards:      awards,
		Rake:           rake,
		RakeBase:       rakeBase,
		Refunds:        potResult.Refunds,
		StacksByPlayer: make(map[string]int64, len(table.players)),
		Showdown:       len(nonFolded) > 1,
		RevealedHands:  revealedHands,
	}
	if len(table.runoutBoards) > 1 {
		settlement.RunoutBoards = make([][]string, len(table.runoutBoards))
		for index, board := range table.runoutBoards {
			for _, card := range board {
				settlement.RunoutBoards[index] = append(settlement.RunoutBoards[index], card.String())
			}
		}
	}
	for _, player := range table.players {
		settlement.StacksByPlayer[player.PlayerID] = player.Stack
	}
	playerIDs := make([]string, 0, len(table.handStartStacks))
	for playerID := range table.handStartStacks {
		playerIDs = append(playerIDs, playerID)
	}
	sort.Strings(playerIDs)
	var ledgerDelta int64
	for _, playerID := range playerIDs {
		player := table.playerByID(playerID)
		delta := player.Stack - table.handStartStacks[playerID]
		ledgerDelta += delta
		settlement.LedgerEntries = append(settlement.LedgerEntries, ledger.Entry{
			EntryID:      table.handID + ":" + playerID,
			HandID:       table.handID,
			PlayerID:     playerID,
			Delta:        delta,
			BalanceAfter: player.Stack,
		})
	}
	// 各人输赢之和加上抽水必须为零：抽水是唯一离开牌桌的筹码。
	if ledgerDelta+rake != 0 {
		return errors.New("settlement ledger does not conserve chips")
	}
	table.lastSettlement = settlement
	table.phase = PhaseWaitingNextHand
	table.revision++
	for seat, player := range table.players {
		if player.LeaveAfterHand {
			delete(table.players, seat)
		}
	}
	return nil
}

// splitRake 把一手的总抽水按各池大小摊开。各池先取 rake × 池 / 总额 的整数部分，
// 剩下的零头按小数部分从大到小逐枚补上（并列时靠前的池优先），因此各池之和恰好
// 等于总抽水，任何一个池也不会被扣成负数。
//
// 不能从主池起依次扣：短码全下的人只能争主池，那样他一个人替边池付了全部抽水，
// 主池甚至会被扣到 0，牌最大却一分拿不到。
func splitRake(rake int64, pots []Pot) ([]int64, error) {
	shares := make([]int64, len(pots))
	if rake == 0 {
		return shares, nil
	}
	var total int64
	for _, pot := range pots {
		total += pot.Amount
	}
	if rake < 0 || rake > total {
		return nil, errors.New("rake exceeds the pot")
	}
	remainders := make([]uint64, len(pots))
	assigned := int64(0)
	for index, pot := range pots {
		if pot.Amount < 0 {
			return nil, errors.New("negative pot")
		}
		// rake × 池 用 128 位算：建房不限盲注与带入，两个 int64 直接相乘在大底池里
		// 会溢出，溢出后各池之和仍等于总抽水，三处守恒校验都发现不了，
		// 却会把筹码从一个池挪到另一个池。rake ≤ total，商不超过池本身，放得进 64 位。
		high, low := bits.Mul64(uint64(rake), uint64(pot.Amount))
		quotient, remainder := bits.Div64(high, low, uint64(total))
		shares[index] = int64(quotient)
		remainders[index] = remainder
		assigned += shares[index]
	}
	// 每个池至多差不到 1，零头总数小于池数，这个循环最多跑 len(pots) 轮
	given := make([]bool, len(pots))
	for left := rake - assigned; left > 0; left-- {
		best := -1
		for index := range pots {
			if given[index] || shares[index] >= pots[index].Amount {
				continue
			}
			if best < 0 || remainders[index] > remainders[best] {
				best = index
			}
		}
		if best < 0 {
			return nil, errors.New("rake exceeds the pot")
		}
		shares[best]++
		given[best] = true
	}
	return shares, nil
}
