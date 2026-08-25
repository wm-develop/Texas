package holdem

import (
	"errors"
	"fmt"
	"regexp"
	"sort"

	"texas/services/game_server/internal/ledger"
)

type Phase string

const (
	PhaseWaiting         Phase = "WAITING"
	PhaseStarting        Phase = "STARTING"
	PhasePreflop         Phase = "PREFLOP"
	PhaseFlop            Phase = "FLOP"
	PhaseTurn            Phase = "TURN"
	PhaseRiver           Phase = "RIVER"
	PhaseShowdown        Phase = "SHOWDOWN"
	PhaseSettlement      Phase = "SETTLEMENT"
	PhaseWaitingNextHand Phase = "WAITING_NEXT_HAND"
)

type ActionType string

const (
	ActionFold  ActionType = "fold"
	ActionCheck ActionType = "check"
	ActionCall  ActionType = "call"
	ActionBet   ActionType = "bet"
	ActionRaise ActionType = "raise"
	ActionAllIn ActionType = "all_in"
)

type Config struct {
	TableID    string
	MaxSeats   int
	SmallBlind int64
	BigBlind   int64
}

var validTableID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

type RuleError struct {
	Code string
}

func (ruleError RuleError) Error() string { return ruleError.Code }

type Player struct {
	PlayerID  string
	Seat      int
	Stack     int64
	Ready     bool
	Connected bool

	Participating  bool
	LeaveAfterHand bool
	Folded         bool
	AllIn          bool
	HoleCards      [2]Card
	StreetBet      int64
	TotalBet       int64
	NeedsAction    bool
	RaiseAllowed   bool
	LastAction     ActionType
	LastCommitted  int64
	LastActionTo   int64
}

type ActionOptions struct {
	ToCall     int64 `json:"toCall"`
	CanFold    bool  `json:"canFold"`
	CanCheck   bool  `json:"canCheck"`
	CanCall    bool  `json:"canCall"`
	CanBet     bool  `json:"canBet"`
	CanRaise   bool  `json:"canRaise"`
	CanAllIn   bool  `json:"canAllIn"`
	MinRaiseTo int64 `json:"minRaiseTo"`
	MaxRaiseTo int64 `json:"maxRaiseTo"`
}

type ActionRequest struct {
	ActionID      string
	PlayerID      string
	HandID        string
	TableRevision uint64
	Action        ActionType
	RaiseTo       int64
}

type ActionResult struct {
	ActionID  string
	Action    ActionType
	Committed int64
	Phase     Phase
	Revision  uint64
	HandEnded bool
}

type Settlement struct {
	HandID         string           `json:"handId"`
	PotAwards      []PotAward       `json:"potAwards"`
	Refunds        map[string]int64 `json:"refunds"`
	StacksByPlayer map[string]int64 `json:"stacksByPlayer"`
	LedgerEntries  []ledger.Entry   `json:"ledgerEntries"`
	Showdown       bool             `json:"showdown"`
	RevealedHands  []RevealedHand   `json:"revealedHands"`
}

type RevealedHand struct {
	PlayerID  string   `json:"playerId"`
	HoleCards []string `json:"holeCards"`
	Category  string   `json:"category"`
}

type Table struct {
	config  Config
	players map[int]*Player

	phase             Phase
	revision          uint64
	handCounter       uint64
	handID            string
	dealerSeat        int
	smallBlindSeat    int
	bigBlindSeat      int
	currentSeat       int
	currentBet        int64
	minRaiseIncrement int64
	deck              *Deck
	board             []Card
	actionResults     map[string]ActionResult
	handStartStacks   map[string]int64
	lastSettlement    Settlement
}

func NewTable(config Config) (*Table, error) {
	if !validTableID.MatchString(config.TableID) ||
		config.MaxSeats < 2 || config.MaxSeats > 10 ||
		config.SmallBlind <= 0 || config.BigBlind < config.SmallBlind ||
		config.BigBlind%config.SmallBlind != 0 {
		return nil, errors.New("invalid table configuration")
	}
	return &Table{
		config:        config,
		players:       make(map[int]*Player),
		phase:         PhaseWaiting,
		actionResults: make(map[string]ActionResult),
	}, nil
}

func (table *Table) AddPlayer(playerID string, seat int, stack int64) error {
	if table.phase != PhaseWaiting && table.phase != PhaseWaitingNextHand {
		return RuleError{Code: "hand_in_progress"}
	}
	if playerID == "" || seat <= 0 || seat > table.config.MaxSeats || stack <= 0 {
		return RuleError{Code: "invalid_player"}
	}
	if _, exists := table.players[seat]; exists {
		return RuleError{Code: "seat_occupied"}
	}
	for _, player := range table.players {
		if player.PlayerID == playerID {
			return RuleError{Code: "player_already_seated"}
		}
	}
	table.players[seat] = &Player{PlayerID: playerID, Seat: seat, Stack: stack, Connected: true}
	table.revision++
	return nil
}

func (table *Table) SetConnected(playerID string, connected bool) error {
	player := table.playerByID(playerID)
	if player == nil {
		return RuleError{Code: "not_seated"}
	}
	if player.Connected != connected {
		player.Connected = connected
		table.revision++
	}
	return nil
}

func (table *Table) RequestLeave(playerID string) error {
	player := table.playerByID(playerID)
	if player == nil {
		return RuleError{Code: "not_seated"}
	}
	if player.Participating && table.phase != PhaseWaiting && table.phase != PhaseWaitingNextHand {
		player.Connected = false
		player.Ready = false
		player.LeaveAfterHand = true
		table.revision++
		return nil
	}
	delete(table.players, player.Seat)
	table.revision++
	return nil
}

func (table *Table) SetReady(playerID string, ready bool) error {
	if table.phase != PhaseWaiting && table.phase != PhaseWaitingNextHand {
		return RuleError{Code: "hand_in_progress"}
	}
	player := table.playerByID(playerID)
	if player == nil {
		return RuleError{Code: "not_seated"}
	}
	if player.Ready != ready {
		player.Ready = ready
		table.revision++
	}
	return nil
}

func (table *Table) AddChips(playerID string, amount int64) error {
	if table.phase != PhaseWaiting && table.phase != PhaseWaitingNextHand {
		return RuleError{Code: "hand_in_progress"}
	}
	player := table.playerByID(playerID)
	if player == nil {
		return RuleError{Code: "not_seated"}
	}
	if amount <= 0 || player.Stack > maximumTableChips-amount {
		return RuleError{Code: "invalid_chip_amount"}
	}
	player.Stack += amount
	player.Ready = false
	table.revision++
	return nil
}

const maximumTableChips int64 = 9_000_000_000_000_000

func (table *Table) StartHand(random IntnSource) error {
	if table.phase != PhaseWaiting && table.phase != PhaseWaitingNextHand {
		return RuleError{Code: "hand_in_progress"}
	}
	activeSeats := table.readySeats()
	if len(activeSeats) < 2 {
		return RuleError{Code: "not_enough_ready_players"}
	}
	if random == nil {
		return errors.New("random source is required")
	}

	if table.dealerSeat == 0 {
		index, err := random.Intn(len(activeSeats))
		if err != nil || index < 0 || index >= len(activeSeats) {
			return errors.New("choose first dealer: invalid random result")
		}
		table.dealerSeat = activeSeats[index]
	} else {
		table.dealerSeat = nextSeatIn(table.dealerSeat, activeSeats, table.config.MaxSeats)
	}

	deck, err := NewShuffledDeck(random)
	if err != nil {
		return fmt.Errorf("shuffle deck: %w", err)
	}
	table.deck = deck
	table.board = nil
	table.handCounter++
	table.handID = fmt.Sprintf("%s_hand_%d", table.config.TableID, table.handCounter)
	table.actionResults = make(map[string]ActionResult)
	table.handStartStacks = make(map[string]int64, len(activeSeats))
	table.lastSettlement = Settlement{}
	table.phase = PhaseStarting
	table.revision++

	for _, player := range table.players {
		player.Participating = player.Ready && player.Stack > 0
		player.LeaveAfterHand = false
		player.Folded = false
		player.AllIn = false
		player.HoleCards = [2]Card{}
		player.StreetBet = 0
		player.TotalBet = 0
		player.NeedsAction = false
		player.RaiseAllowed = false
		player.LastAction = ""
		player.LastCommitted = 0
		player.LastActionTo = 0
		if player.Participating {
			table.handStartStacks[player.PlayerID] = player.Stack
		}
	}
	if err := table.dealHoleCards(); err != nil {
		return err
	}

	if len(activeSeats) == 2 {
		table.smallBlindSeat = table.dealerSeat
		table.bigBlindSeat = nextSeatIn(table.dealerSeat, activeSeats, table.config.MaxSeats)
	} else {
		table.smallBlindSeat = nextSeatIn(table.dealerSeat, activeSeats, table.config.MaxSeats)
		table.bigBlindSeat = nextSeatIn(table.smallBlindSeat, activeSeats, table.config.MaxSeats)
	}
	table.postBlind(table.smallBlindSeat, table.config.SmallBlind)
	table.postBlind(table.bigBlindSeat, table.config.BigBlind)

	table.phase = PhasePreflop
	table.currentBet = table.config.BigBlind
	table.minRaiseIncrement = table.config.BigBlind
	table.beginBettingRound()
	table.currentSeat = table.nextActionSeat(table.bigBlindSeat)
	table.revision++
	return table.progressWithoutAction()
}

func (table *Table) Phase() Phase        { return table.phase }
func (table *Table) TableID() string     { return table.config.TableID }
func (table *Table) Revision() uint64    { return table.revision }
func (table *Table) HandID() string      { return table.handID }
func (table *Table) DealerSeat() int     { return table.dealerSeat }
func (table *Table) SmallBlindSeat() int { return table.smallBlindSeat }
func (table *Table) BigBlindSeat() int   { return table.bigBlindSeat }
func (table *Table) CurrentSeat() int    { return table.currentSeat }
func (table *Table) CurrentPlayerID() string {
	player := table.players[table.currentSeat]
	if player == nil {
		return ""
	}
	return player.PlayerID
}
func (table *Table) Board() []Card              { return append([]Card(nil), table.board...) }
func (table *Table) LastSettlement() Settlement { return cloneSettlement(table.lastSettlement) }

func (table *Table) Players() []Player {
	seats := table.allSeats()
	result := make([]Player, 0, len(seats))
	for _, seat := range seats {
		result = append(result, *table.players[seat])
	}
	return result
}

func (table *Table) TotalChips() int64 {
	var total int64
	for _, player := range table.players {
		total += player.Stack
		if table.phase != PhaseWaiting && table.phase != PhaseWaitingNextHand {
			total += player.TotalBet
		}
	}
	return total
}

func (table *Table) RemainingPlayerCount() int {
	return table.nonFoldedCount()
}

func (table *Table) dealHoleCards() error {
	seats := table.participatingSeats()
	start := nextSeatIn(table.dealerSeat, seats, table.config.MaxSeats)
	ordered := rotateSeatsFrom(start, seats)
	for cardIndex := 0; cardIndex < 2; cardIndex++ {
		for _, seat := range ordered {
			cards, err := table.deck.Draw(1)
			if err != nil {
				return err
			}
			table.players[seat].HoleCards[cardIndex] = cards[0]
		}
	}
	return nil
}

func (table *Table) postBlind(seat int, amount int64) {
	player := table.players[seat]
	if amount > player.Stack {
		amount = player.Stack
	}
	table.commit(player, amount)
}

func (table *Table) beginBettingRound() {
	for _, player := range table.players {
		player.LastAction = ""
		player.LastCommitted = 0
		player.LastActionTo = 0
		if player.Participating && !player.Folded && !player.AllIn {
			player.NeedsAction = true
			player.RaiseAllowed = true
		} else {
			player.NeedsAction = false
			player.RaiseAllowed = false
		}
	}
}

func (table *Table) commit(player *Player, amount int64) {
	if amount < 0 || amount > player.Stack {
		panic("invalid internal chip commitment")
	}
	player.Stack -= amount
	player.StreetBet += amount
	player.TotalBet += amount
	player.AllIn = player.Stack == 0
}

func (table *Table) playerByID(playerID string) *Player {
	for _, player := range table.players {
		if player.PlayerID == playerID {
			return player
		}
	}
	return nil
}

func (table *Table) allSeats() []int {
	seats := make([]int, 0, len(table.players))
	for seat := range table.players {
		seats = append(seats, seat)
	}
	sort.Ints(seats)
	return seats
}

func (table *Table) readySeats() []int {
	seats := make([]int, 0, len(table.players))
	for seat, player := range table.players {
		if player.Ready && player.Stack > 0 {
			seats = append(seats, seat)
		}
	}
	sort.Ints(seats)
	return seats
}

func (table *Table) participatingSeats() []int {
	seats := make([]int, 0, len(table.players))
	for seat, player := range table.players {
		if player.Participating {
			seats = append(seats, seat)
		}
	}
	sort.Ints(seats)
	return seats
}

func (table *Table) nextActionSeat(after int) int {
	for offset := 1; offset <= table.config.MaxSeats; offset++ {
		seat := ((after - 1 + offset) % table.config.MaxSeats) + 1
		player := table.players[seat]
		if player != nil && player.Participating && !player.Folded && !player.AllIn && player.NeedsAction {
			return seat
		}
	}
	return 0
}

func nextSeatIn(after int, seats []int, maxSeats int) int {
	for offset := 1; offset <= maxSeats; offset++ {
		candidate := ((after - 1 + offset) % maxSeats) + 1
		for _, seat := range seats {
			if seat == candidate {
				return seat
			}
		}
	}
	return 0
}

func rotateSeatsFrom(start int, seats []int) []int {
	result := make([]int, 0, len(seats))
	for _, seat := range seats {
		if seat >= start {
			result = append(result, seat)
		}
	}
	for _, seat := range seats {
		if seat < start {
			result = append(result, seat)
		}
	}
	return result
}

func cloneSettlement(value Settlement) Settlement {
	result := value
	result.PotAwards = append([]PotAward(nil), value.PotAwards...)
	result.Refunds = make(map[string]int64, len(value.Refunds))
	for playerID, amount := range value.Refunds {
		result.Refunds[playerID] = amount
	}
	result.StacksByPlayer = make(map[string]int64, len(value.StacksByPlayer))
	for playerID, amount := range value.StacksByPlayer {
		result.StacksByPlayer[playerID] = amount
	}
	result.LedgerEntries = append([]ledger.Entry(nil), value.LedgerEntries...)
	result.RevealedHands = make([]RevealedHand, len(value.RevealedHands))
	for index, hand := range value.RevealedHands {
		result.RevealedHands[index] = hand
		result.RevealedHands[index].HoleCards = append([]string(nil), hand.HoleCards...)
	}
	return result
}
