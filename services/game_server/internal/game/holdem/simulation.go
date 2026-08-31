package holdem

import (
	"fmt"
	"math/rand"
)

type SeededRandom struct {
	random *rand.Rand
}

func NewSeededRandom(seed int64) *SeededRandom {
	return &SeededRandom{random: rand.New(rand.NewSource(seed))}
}

func (random *SeededRandom) Intn(max int) (int, error) {
	if random == nil || random.random == nil || max <= 0 {
		return 0, fmt.Errorf("invalid deterministic random request")
	}
	return random.random.Intn(max), nil
}

type SimulationResult struct {
	Seed               int64
	Hands              int
	Actions            int
	MaximumHandActions int
}

func SimulateHands(handCount int, seed int64) (SimulationResult, error) {
	if handCount <= 0 {
		return SimulationResult{}, fmt.Errorf("hand count must be positive")
	}
	random := NewSeededRandom(seed)
	result := SimulationResult{Seed: seed}

	for handIndex := 0; handIndex < handCount; handIndex++ {
		playerOffset, err := random.Intn(9)
		if err != nil {
			return result, err
		}
		playerCount := playerOffset + 2
		table, err := NewTable(Config{
			TableID:    fmt.Sprintf("sim_%d", handIndex),
			MaxSeats:   10,
			SmallBlind: 5,
			BigBlind:   10,
		})
		if err != nil {
			return result, err
		}
		for index := 0; index < playerCount; index++ {
			playerID := fmt.Sprintf("player_%d", index+1)
			if err := table.AddPlayer(playerID, index+1, 1000); err != nil {
				return result, fmt.Errorf("seed %d hand %d add player: %w", seed, handIndex, err)
			}
			if err := table.SetReady(playerID, true); err != nil {
				return result, fmt.Errorf("seed %d hand %d ready player: %w", seed, handIndex, err)
			}
		}
		initialChips := int64(playerCount * 1000)
		if err := table.StartHand(random); err != nil {
			return result, fmt.Errorf("seed %d hand %d start: %w", seed, handIndex, err)
		}
		if err := validateDealtCards(table); err != nil {
			return result, fmt.Errorf("seed %d hand %d cards after deal: %w", seed, handIndex, err)
		}

		handActions := 0
		for table.Phase() != PhaseWaitingNextHand {
			if handActions >= 500 {
				return result, fmt.Errorf("seed %d hand %d exceeded action limit", seed, handIndex)
			}
			if table.TotalChips() != initialChips {
				return result, fmt.Errorf(
					"seed %d hand %d action %d chips=%d want=%d",
					seed, handIndex, handActions, table.TotalChips(), initialChips,
				)
			}
			for _, player := range table.Players() {
				if player.Stack < 0 || player.StreetBet < 0 || player.TotalBet < 0 {
					return result, fmt.Errorf("seed %d hand %d has negative chips", seed, handIndex)
				}
			}
			if table.Phase() == PhaseRunoutChoice {
				if err := table.ResolveRunoutChoiceTimeout(); err != nil {
					return result, fmt.Errorf("seed %d hand %d resolve runout: %w", seed, handIndex, err)
				}
				continue
			}

			options, err := table.CurrentActionOptions()
			if err != nil {
				return result, fmt.Errorf("seed %d hand %d options: %w", seed, handIndex, err)
			}
			action, raiseTo, err := chooseSimulatedAction(random, options)
			if err != nil {
				return result, fmt.Errorf("seed %d hand %d choose action: %w", seed, handIndex, err)
			}
			_, err = table.SubmitAction(ActionRequest{
				ActionID:      fmt.Sprintf("sim_%d", handActions),
				PlayerID:      table.CurrentPlayerID(),
				HandID:        table.HandID(),
				TableRevision: table.Revision(),
				Action:        action,
				RaiseTo:       raiseTo,
			})
			if err != nil {
				return result, fmt.Errorf(
					"seed %d hand %d action %d (%s to %d): %w",
					seed, handIndex, handActions, action, raiseTo, err,
				)
			}
			if err := validateDealtCards(table); err != nil {
				return result, fmt.Errorf("seed %d hand %d action %d cards: %w", seed, handIndex, handActions, err)
			}
			handActions++
			result.Actions++
		}

		if table.TotalChips() != initialChips {
			return result, fmt.Errorf(
				"seed %d hand %d settled chips=%d want=%d",
				seed, handIndex, table.TotalChips(), initialChips,
			)
		}
		if table.LastSettlement().HandID == "" {
			return result, fmt.Errorf("seed %d hand %d has no settlement", seed, handIndex)
		}
		if handActions > result.MaximumHandActions {
			result.MaximumHandActions = handActions
		}
		result.Hands++
	}
	return result, nil
}

func validateDealtCards(table *Table) error {
	seen := make(map[Card]struct{}, 25)
	for _, card := range table.Board() {
		if _, exists := seen[card]; exists {
			return fmt.Errorf("duplicate board card %s", card)
		}
		seen[card] = struct{}{}
	}
	for _, player := range table.Players() {
		if !player.Participating {
			continue
		}
		for _, card := range player.HoleCards {
			if !card.Valid() {
				return fmt.Errorf("invalid hole card for %s", player.PlayerID)
			}
			if _, exists := seen[card]; exists {
				return fmt.Errorf("duplicate hole card %s", card)
			}
			seen[card] = struct{}{}
		}
	}
	return nil
}

func chooseSimulatedAction(random IntnSource, options ActionOptions) (ActionType, int64, error) {
	roll, err := random.Intn(100)
	if err != nil {
		return "", 0, err
	}
	if options.ToCall == 0 {
		if options.CanBet && roll < 12 {
			return ActionBet, options.MinRaiseTo, nil
		}
		if options.CanAllIn && roll == 99 {
			return ActionAllIn, 0, nil
		}
		if options.CanCheck {
			return ActionCheck, 0, nil
		}
	}
	if options.CanFold && roll < 15 {
		return ActionFold, 0, nil
	}
	if options.CanRaise && roll >= 85 && roll < 95 {
		return ActionRaise, options.MinRaiseTo, nil
	}
	if options.CanAllIn && roll >= 95 {
		return ActionAllIn, 0, nil
	}
	if options.CanCall {
		return ActionCall, 0, nil
	}
	return "", 0, fmt.Errorf("no legal simulated action: %#v", options)
}
