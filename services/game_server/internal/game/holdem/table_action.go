package holdem

import "errors"

func (table *Table) CurrentActionOptions() (ActionOptions, error) {
	player := table.players[table.currentSeat]
	if player == nil {
		return ActionOptions{}, RuleError{Code: "no_action_required"}
	}
	return table.actionOptions(player), nil
}

func (table *Table) SubmitAction(request ActionRequest) (ActionResult, error) {
	if request.ActionID == "" || request.PlayerID == "" {
		return ActionResult{}, RuleError{Code: "invalid_action"}
	}
	idempotencyKey := request.PlayerID + "\x00" + request.ActionID
	if previous, exists := table.actionResults[idempotencyKey]; exists {
		return previous, nil
	}
	if request.HandID != table.handID {
		return ActionResult{}, RuleError{Code: "stale_hand"}
	}
	if request.TableRevision != table.revision {
		return ActionResult{}, RuleError{Code: "stale_revision"}
	}
	player := table.playerByID(request.PlayerID)
	if player == nil || !player.Participating {
		return ActionResult{}, RuleError{Code: "not_seated"}
	}
	if player.Seat != table.currentSeat {
		return ActionResult{}, RuleError{Code: "not_your_turn"}
	}

	options := table.actionOptions(player)
	committed, err := table.applyAction(player, request, options)
	if err != nil {
		return ActionResult{}, err
	}
	player.LastAction = request.Action
	player.LastCommitted = committed
	player.LastActionTo = player.StreetBet
	table.revision++
	if err := table.progressAfterAction(player.Seat); err != nil {
		return ActionResult{}, err
	}

	result := ActionResult{
		ActionID:  request.ActionID,
		Action:    request.Action,
		Committed: committed,
		Phase:     table.phase,
		Revision:  table.revision,
		HandEnded: table.phase == PhaseWaitingNextHand,
	}
	table.actionResults[idempotencyKey] = result
	return result, nil
}

func (table *Table) ApplyTimeout() (ActionResult, error) {
	player := table.players[table.currentSeat]
	if player == nil {
		return ActionResult{}, RuleError{Code: "no_action_required"}
	}
	action := ActionFold
	if table.currentBet-player.StreetBet == 0 {
		action = ActionCheck
	}
	return table.SubmitAction(ActionRequest{
		ActionID:      "timeout_" + table.handID + "_" + player.PlayerID + "_" + uintToString(table.revision),
		PlayerID:      player.PlayerID,
		HandID:        table.handID,
		TableRevision: table.revision,
		Action:        action,
	})
}

func (table *Table) actionOptions(player *Player) ActionOptions {
	toCall := table.currentBet - player.StreetBet
	if toCall < 0 {
		toCall = 0
	}
	maxRaiseTo := player.StreetBet + player.Stack
	options := ActionOptions{
		ToCall:     toCall,
		CanFold:    toCall > 0,
		CanCheck:   toCall == 0,
		CanCall:    toCall > 0 && player.Stack > 0,
		CanAllIn:   player.Stack > 0 && (maxRaiseTo <= table.currentBet || player.RaiseAllowed),
		MaxRaiseTo: maxRaiseTo,
	}
	if table.currentBet == 0 {
		options.MinRaiseTo = table.config.BigBlind
		options.CanBet = player.RaiseAllowed && maxRaiseTo >= options.MinRaiseTo
	} else {
		options.MinRaiseTo = table.currentBet + table.minRaiseIncrement
		options.CanRaise = player.RaiseAllowed && maxRaiseTo >= options.MinRaiseTo
	}
	return options
}

func (table *Table) applyAction(player *Player, request ActionRequest, options ActionOptions) (int64, error) {
	switch request.Action {
	case ActionFold:
		if !options.CanFold {
			return 0, RuleError{Code: "illegal_action"}
		}
		player.Folded = true
		player.NeedsAction = false
		player.RaiseAllowed = false
		return 0, nil
	case ActionCheck:
		if !options.CanCheck {
			return 0, RuleError{Code: "illegal_action"}
		}
		player.NeedsAction = false
		player.RaiseAllowed = false
		return 0, nil
	case ActionCall:
		if !options.CanCall {
			return 0, RuleError{Code: "illegal_action"}
		}
		amount := options.ToCall
		if amount > player.Stack {
			amount = player.Stack
		}
		table.commit(player, amount)
		player.NeedsAction = false
		player.RaiseAllowed = false
		return amount, nil
	case ActionBet:
		if !options.CanBet || request.RaiseTo < options.MinRaiseTo || request.RaiseTo > options.MaxRaiseTo {
			return 0, RuleError{Code: "invalid_amount"}
		}
		return table.applyAggressiveAction(player, request.RaiseTo)
	case ActionRaise:
		if !options.CanRaise || request.RaiseTo < options.MinRaiseTo || request.RaiseTo > options.MaxRaiseTo {
			return 0, RuleError{Code: "invalid_amount"}
		}
		return table.applyAggressiveAction(player, request.RaiseTo)
	case ActionAllIn:
		if !options.CanAllIn {
			return 0, RuleError{Code: "illegal_action"}
		}
		target := options.MaxRaiseTo
		if target <= table.currentBet {
			amount := player.Stack
			table.commit(player, amount)
			player.NeedsAction = false
			player.RaiseAllowed = false
			return amount, nil
		}
		return table.applyAggressiveAction(player, target)
	default:
		return 0, RuleError{Code: "illegal_action"}
	}
}

func (table *Table) applyAggressiveAction(player *Player, target int64) (int64, error) {
	if target <= table.currentBet || target <= player.StreetBet {
		return 0, RuleError{Code: "invalid_amount"}
	}
	amount := target - player.StreetBet
	if amount > player.Stack {
		return 0, RuleError{Code: "invalid_amount"}
	}
	oldCurrentBet := table.currentBet
	fullRaise := (oldCurrentBet == 0 && target >= table.config.BigBlind) ||
		(oldCurrentBet > 0 && target-oldCurrentBet >= table.minRaiseIncrement)

	table.commit(player, amount)
	table.currentBet = target
	if fullRaise {
		table.minRaiseIncrement = target - oldCurrentBet
	}
	for _, other := range table.players {
		if other == player || !other.Participating || other.Folded || other.AllIn {
			continue
		}
		if fullRaise {
			other.NeedsAction = true
			other.RaiseAllowed = true
		} else if other.StreetBet < table.currentBet {
			other.NeedsAction = true
		}
	}
	player.NeedsAction = false
	player.RaiseAllowed = false
	return amount, nil
}

func (table *Table) progressAfterAction(afterSeat int) error {
	if table.nonFoldedCount() == 1 {
		return table.settleHand()
	}
	if table.bettingRoundComplete() {
		return table.advanceStreetOrSettle()
	}
	table.currentSeat = table.nextActionSeat(afterSeat)
	if table.currentSeat == 0 {
		return errors.New("betting round has no next actor but is incomplete")
	}
	return nil
}

func (table *Table) progressWithoutAction() error {
	if table.nonFoldedCount() == 1 {
		return table.settleHand()
	}
	if table.bettingRoundComplete() {
		return table.advanceStreetOrSettle()
	}
	if table.currentSeat == 0 {
		return errors.New("betting round requires an actor")
	}
	return nil
}

func (table *Table) bettingRoundComplete() bool {
	actionable := make([]*Player, 0, len(table.players))
	for _, player := range table.players {
		if player.Participating && !player.Folded && !player.AllIn {
			actionable = append(actionable, player)
		}
	}
	if len(actionable) == 0 {
		return true
	}
	if len(actionable) == 1 && actionable[0].StreetBet == table.currentBet {
		return true
	}
	for _, player := range actionable {
		if player.NeedsAction || player.StreetBet != table.currentBet {
			return false
		}
	}
	return true
}

func (table *Table) nonFoldedCount() int {
	count := 0
	for _, player := range table.players {
		if player.Participating && !player.Folded {
			count++
		}
	}
	return count
}

func uintToString(value uint64) string {
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[index:])
}
