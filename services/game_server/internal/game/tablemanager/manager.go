package tablemanager

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/ledger"
	"texas/services/game_server/internal/room"
)

type Manager struct {
	mu               sync.Mutex
	rooms            *room.Service
	random           holdem.IntnSource
	tables           map[string]*runtime
	now              func() time.Time
	afterFunc        func(time.Duration, func()) ScheduledTimer
	snapshotListener func(string)
	ledger           ledger.Store
	history          history.Store
}

type runtime struct {
	mu              sync.Mutex
	engine          *holdem.Table
	roomID          string
	actionSeconds   int
	deadline        time.Time
	timer           ScheduledTimer
	timerGeneration uint64
	timeExtensions  map[string]int
	handStartedAt   time.Time
	persistedHandID string
}

const (
	timeExtensionsPerHand = 2
	timeExtensionDuration = 30 * time.Second
	headsUpActionDuration = 60 * time.Second
)

type ScheduledTimer interface {
	Stop() bool
}

type ManagerConfig struct {
	Now       func() time.Time
	AfterFunc func(time.Duration, func()) ScheduledTimer
	Ledger    ledger.Store
	History   history.Store
}

type SeatSnapshot struct {
	UserID         string `json:"userId"`
	DisplayName    string `json:"displayName"`
	Seat           int    `json:"seat"`
	Stack          int64  `json:"stack"`
	Ready          bool   `json:"ready"`
	Connected      bool   `json:"connected"`
	Participating  bool   `json:"participating"`
	Folded         bool   `json:"folded"`
	AllIn          bool   `json:"allIn"`
	StreetBet      int64  `json:"streetBet"`
	TotalBet       int64  `json:"totalBet"`
	Position       string `json:"position,omitempty"`
	LastAction     string `json:"lastAction,omitempty"`
	LastCommitted  int64  `json:"lastCommitted,omitempty"`
	LastActionTo   int64  `json:"lastActionTo,omitempty"`
	TimeExtensions int    `json:"timeExtensions"`
}

type BetSuggestion struct {
	Label   string            `json:"label"`
	Action  holdem.ActionType `json:"action"`
	RaiseTo int64             `json:"raiseTo,omitempty"`
}

type ActionSnapshot struct {
	UserID      string               `json:"userId"`
	Seat        int                  `json:"seat"`
	Deadline    int64                `json:"deadline"`
	Options     holdem.ActionOptions `json:"options"`
	Suggestions []BetSuggestion      `json:"suggestions"`
}

type Snapshot struct {
	RoomID         string             `json:"roomId"`
	RoomCode       string             `json:"roomCode"`
	RoomRevision   uint64             `json:"roomRevision"`
	TableRevision  uint64             `json:"tableRevision"`
	Phase          holdem.Phase       `json:"phase"`
	HandID         string             `json:"handId,omitempty"`
	DealerSeat     int                `json:"dealerSeat,omitempty"`
	SmallBlindSeat int                `json:"smallBlindSeat,omitempty"`
	BigBlindSeat   int                `json:"bigBlindSeat,omitempty"`
	Board          []string           `json:"board"`
	HoleCards      []string           `json:"holeCards,omitempty"`
	Seats          []SeatSnapshot     `json:"seats"`
	CurrentAction  *ActionSnapshot    `json:"currentAction,omitempty"`
	TotalPot       int64              `json:"totalPot"`
	Settlement     *holdem.Settlement `json:"settlement,omitempty"`
}

func New(rooms *room.Service, random holdem.IntnSource) (*Manager, error) {
	return NewWithConfig(rooms, random, ManagerConfig{})
}

func NewWithConfig(rooms *room.Service, random holdem.IntnSource, config ManagerConfig) (*Manager, error) {
	if rooms == nil || random == nil {
		return nil, errors.New("invalid table manager configuration")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.AfterFunc == nil {
		config.AfterFunc = func(duration time.Duration, callback func()) ScheduledTimer {
			return time.AfterFunc(duration, callback)
		}
	}
	if config.Ledger == nil {
		config.Ledger = ledger.NewInMemoryStore()
	}
	if config.History == nil {
		config.History = history.NewInMemoryStore()
	}
	return &Manager{
		rooms: rooms, random: random, tables: make(map[string]*runtime),
		now: config.Now, afterFunc: config.AfterFunc,
		ledger: config.Ledger, history: config.History,
	}, nil
}

func (manager *Manager) SetSnapshotListener(listener func(roomID string)) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.snapshotListener = listener
}

func (manager *Manager) Join(ctx context.Context, userID string, roomID string) (Snapshot, error) {
	roomValue, err := manager.rooms.GetForMember(ctx, userID, roomID)
	if err != nil {
		return Snapshot{}, err
	}
	runtime, err := manager.runtimeFor(roomValue)
	if err != nil {
		return Snapshot{}, err
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if err := syncMembers(runtime.engine, roomValue); err != nil {
		return Snapshot{}, err
	}
	if err := runtime.engine.SetConnected(userID, true); err != nil {
		return Snapshot{}, err
	}
	return snapshotFor(runtime.engine, roomValue, userID, runtime.deadline, runtime.timeExtensions)
}

func (manager *Manager) Disconnect(ctx context.Context, userID string, roomID string) {
	roomValue, err := manager.rooms.GetForMember(ctx, userID, roomID)
	if err != nil {
		return
	}
	runtime := manager.existingRuntime(roomID)
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	_ = runtime.engine.SetConnected(userID, false)
	if runtime.engine.Phase() == holdem.PhaseWaiting || runtime.engine.Phase() == holdem.PhaseWaitingNextHand {
		if updated, readyErr := manager.rooms.SetReady(ctx, userID, false); readyErr == nil {
			roomValue = updated
			_ = runtime.engine.SetReady(userID, false)
		}
	}
	_ = roomValue
}

func (manager *Manager) SetReady(ctx context.Context, userID string, ready bool) (Snapshot, error) {
	roomValue, err := manager.rooms.SetReady(ctx, userID, ready)
	if err != nil {
		return Snapshot{}, err
	}
	runtime, err := manager.runtimeFor(roomValue)
	if err != nil {
		return Snapshot{}, err
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if err := syncMembers(runtime.engine, roomValue); err != nil {
		return Snapshot{}, err
	}
	if allReady(roomValue.Members) &&
		(runtime.engine.Phase() == holdem.PhaseWaiting || runtime.engine.Phase() == holdem.PhaseWaitingNextHand) {
		runtime.handStartedAt = manager.now()
		runtime.persistedHandID = ""
		if err := runtime.engine.StartHand(manager.random); err != nil {
			return Snapshot{}, err
		}
		runtime.timeExtensions = make(map[string]int)
		for _, player := range runtime.engine.Players() {
			if player.Participating {
				runtime.timeExtensions[player.PlayerID] = timeExtensionsPerHand
			}
		}
		if runtime.engine.Phase() == holdem.PhaseWaitingNextHand {
			if err := manager.persistSettlementLocked(runtime, roomValue); err != nil {
				return Snapshot{}, err
			}
			roomValue, err = manager.resetReadyLocked(ctx, runtime, roomValue)
			if err != nil {
				return Snapshot{}, err
			}
		} else {
			manager.refreshDeadlineLocked(runtime)
		}
	}
	return snapshotFor(runtime.engine, roomValue, userID, runtime.deadline, runtime.timeExtensions)
}

func (manager *Manager) SubmitAction(
	ctx context.Context,
	userID string,
	roomID string,
	request holdem.ActionRequest,
) (holdem.ActionResult, Snapshot, error) {
	roomValue, err := manager.rooms.GetForMember(ctx, userID, roomID)
	if err != nil {
		return holdem.ActionResult{}, Snapshot{}, err
	}
	runtime := manager.existingRuntime(roomID)
	if runtime == nil {
		return holdem.ActionResult{}, Snapshot{}, room.Error{Code: "table_not_started"}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	request.PlayerID = userID
	previousRevision := runtime.engine.Revision()
	result, err := runtime.engine.SubmitAction(request)
	if err != nil {
		return holdem.ActionResult{}, Snapshot{}, err
	}
	if result.HandEnded {
		if err := manager.persistSettlementLocked(runtime, roomValue); err != nil {
			return holdem.ActionResult{}, Snapshot{}, err
		}
		roomValue, err = manager.resetReadyLocked(ctx, runtime, roomValue)
		if err != nil {
			return holdem.ActionResult{}, Snapshot{}, err
		}
	}
	if runtime.engine.Revision() != previousRevision {
		manager.refreshDeadlineLocked(runtime)
	}
	snapshot, err := snapshotFor(runtime.engine, roomValue, userID, runtime.deadline, runtime.timeExtensions)
	return result, snapshot, err
}

func (manager *Manager) Snapshot(ctx context.Context, userID string, roomID string) (Snapshot, error) {
	roomValue, err := manager.rooms.GetForMember(ctx, userID, roomID)
	if err != nil {
		return Snapshot{}, err
	}
	runtime, err := manager.runtimeFor(roomValue)
	if err != nil {
		return Snapshot{}, err
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if err := syncMembers(runtime.engine, roomValue); err != nil {
		return Snapshot{}, err
	}
	return snapshotFor(runtime.engine, roomValue, userID, runtime.deadline, runtime.timeExtensions)
}

func (manager *Manager) UseTimeExtension(ctx context.Context, userID string, roomID string) (Snapshot, error) {
	roomValue, err := manager.rooms.GetForMember(ctx, userID, roomID)
	if err != nil {
		return Snapshot{}, err
	}
	runtime := manager.existingRuntime(roomID)
	if runtime == nil {
		return Snapshot{}, room.Error{Code: "table_not_started"}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.engine.CurrentPlayerID() != userID {
		return Snapshot{}, holdem.RuleError{Code: "not_your_turn"}
	}
	if runtime.deadline.IsZero() || !manager.now().Before(runtime.deadline) {
		return Snapshot{}, holdem.RuleError{Code: "time_extension_expired"}
	}
	if runtime.timeExtensions[userID] <= 0 {
		return Snapshot{}, holdem.RuleError{Code: "no_time_extensions"}
	}
	runtime.timeExtensions[userID]--
	manager.scheduleDeadlineLocked(runtime, runtime.deadline.Add(timeExtensionDuration))
	return snapshotFor(
		runtime.engine, roomValue, userID, runtime.deadline, runtime.timeExtensions,
	)
}

func (manager *Manager) runtimeFor(roomValue room.Room) (*runtime, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if existing := manager.tables[roomValue.RoomID]; existing != nil {
		return existing, nil
	}
	engine, err := holdem.NewTable(holdem.Config{
		TableID: roomValue.RoomID, MaxSeats: roomValue.MaxPlayers,
		SmallBlind: roomValue.Rules.SmallBlind, BigBlind: roomValue.Rules.BigBlind,
	})
	if err != nil {
		return nil, err
	}
	created := &runtime{
		engine: engine, roomID: roomValue.RoomID, actionSeconds: roomValue.Rules.ActionSeconds,
		timeExtensions: make(map[string]int),
	}
	manager.tables[roomValue.RoomID] = created
	return created, nil
}

func (manager *Manager) existingRuntime(roomID string) *runtime {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.tables[roomID]
}

func (manager *Manager) refreshDeadlineLocked(runtime *runtime) {
	duration := time.Duration(runtime.actionSeconds) * time.Second
	if runtime.engine.RemainingPlayerCount() == 2 {
		duration = headsUpActionDuration
	}
	manager.scheduleDeadlineLocked(runtime, manager.now().Add(duration))
}

func (manager *Manager) scheduleDeadlineLocked(runtime *runtime, deadline time.Time) {
	if runtime.timer != nil {
		runtime.timer.Stop()
		runtime.timer = nil
	}
	runtime.timerGeneration++
	runtime.deadline = time.Time{}
	if runtime.engine.CurrentSeat() == 0 {
		return
	}
	runtime.deadline = deadline
	duration := deadline.Sub(manager.now())
	if duration < 0 {
		duration = 0
	}
	generation := runtime.timerGeneration
	runtime.timer = manager.afterFunc(duration, func() {
		manager.handleTimeout(runtime.roomID, generation)
	})
}

func (manager *Manager) handleTimeout(roomID string, generation uint64) {
	runtime := manager.existingRuntime(roomID)
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	if generation != runtime.timerGeneration || runtime.engine.CurrentSeat() == 0 ||
		manager.now().Before(runtime.deadline) {
		runtime.mu.Unlock()
		return
	}
	actorUserID := runtime.engine.CurrentPlayerID()
	if runtime.timeExtensions[actorUserID] > 0 {
		runtime.timeExtensions[actorUserID]--
		manager.scheduleDeadlineLocked(runtime, manager.now().Add(timeExtensionDuration))
		runtime.mu.Unlock()
		manager.notifySnapshot(roomID)
		return
	}
	roomValue, err := manager.rooms.GetForMember(context.Background(), actorUserID, roomID)
	if err == nil {
		var result holdem.ActionResult
		result, err = runtime.engine.ApplyTimeout()
		if err == nil && result.HandEnded {
			err = manager.persistSettlementLocked(runtime, roomValue)
			if err == nil {
				roomValue, err = manager.resetReadyLocked(context.Background(), runtime, roomValue)
			}
		}
	}
	if err == nil {
		manager.refreshDeadlineLocked(runtime)
	} else {
		runtime.deadline = time.Time{}
		runtime.timer = nil
	}
	runtime.mu.Unlock()
	if err == nil {
		manager.notifySnapshot(roomID)
	}
}

func (manager *Manager) resetReadyLocked(
	ctx context.Context,
	runtime *runtime,
	roomValue room.Room,
) (room.Room, error) {
	for _, member := range roomValue.Members {
		updated, err := manager.rooms.SetReady(ctx, member.UserID, false)
		if err != nil {
			return room.Room{}, err
		}
		roomValue = updated
		_ = runtime.engine.SetReady(member.UserID, false)
	}
	return roomValue, nil
}

func (manager *Manager) persistSettlementLocked(runtime *runtime, roomValue room.Room) error {
	settlement := runtime.engine.LastSettlement()
	if settlement.HandID == "" || runtime.persistedHandID == settlement.HandID {
		return nil
	}
	if len(manager.ledger.EntriesForHand(settlement.HandID)) == 0 {
		if err := manager.ledger.Append(settlement.LedgerEntries); err != nil {
			return err
		}
	}
	if _, exists := manager.history.Hand(settlement.HandID); !exists {
		displayNames := make(map[string]string, len(roomValue.Members))
		for _, member := range roomValue.Members {
			displayNames[member.UserID] = member.DisplayName
		}
		players := runtime.engine.Players()
		ledgerByPlayer := make(map[string]ledger.Entry, len(settlement.LedgerEntries))
		for _, entry := range settlement.LedgerEntries {
			ledgerByPlayer[entry.PlayerID] = entry
		}
		record := history.Hand{
			HandID: settlement.HandID, RoomID: roomValue.RoomID, RoomCode: roomValue.Code,
			StartedAt: runtime.handStartedAt, EndedAt: manager.now(), Showdown: settlement.Showdown,
			PotAwards: settlement.PotAwards, RevealedHands: settlement.RevealedHands,
		}
		for _, card := range runtime.engine.Board() {
			record.Board = append(record.Board, card.String())
		}
		for _, player := range players {
			entry, participating := ledgerByPlayer[player.PlayerID]
			if !participating {
				continue
			}
			record.Players = append(record.Players, history.PlayerResult{
				UserID: player.PlayerID, DisplayName: displayNames[player.PlayerID], Seat: player.Seat,
				StartingStack: entry.BalanceAfter - entry.Delta, EndingStack: entry.BalanceAfter,
				Delta:     entry.Delta,
				HoleCards: []string{player.HoleCards[0].String(), player.HoleCards[1].String()},
			})
		}
		if err := manager.history.Append(record); err != nil {
			return err
		}
	}
	runtime.persistedHandID = settlement.HandID
	return nil
}

func (manager *Manager) notifySnapshot(roomID string) {
	manager.mu.Lock()
	listener := manager.snapshotListener
	manager.mu.Unlock()
	if listener != nil {
		listener(roomID)
	}
}

func syncMembers(engine *holdem.Table, roomValue room.Room) error {
	players := engine.Players()
	byID := make(map[string]holdem.Player, len(players))
	for _, player := range players {
		byID[player.PlayerID] = player
	}
	for _, member := range roomValue.Members {
		if _, exists := byID[member.UserID]; !exists {
			if err := engine.AddPlayer(member.UserID, member.Seat, roomValue.Rules.StartingChips); err != nil {
				return err
			}
			if err := engine.SetConnected(member.UserID, false); err != nil {
				return err
			}
		}
		if engine.Phase() == holdem.PhaseWaiting || engine.Phase() == holdem.PhaseWaitingNextHand {
			if err := engine.SetReady(member.UserID, member.Ready); err != nil {
				return err
			}
		}
	}
	return nil
}

func snapshotFor(
	engine *holdem.Table,
	roomValue room.Room,
	recipientUserID string,
	deadline time.Time,
	timeExtensions map[string]int,
) (Snapshot, error) {
	displayNames := make(map[string]string, len(roomValue.Members))
	ready := make(map[string]bool, len(roomValue.Members))
	for _, member := range roomValue.Members {
		displayNames[member.UserID] = member.DisplayName
		ready[member.UserID] = member.Ready
	}
	players := engine.Players()
	positions := positionsFor(players, engine.DealerSeat(), roomValue.MaxPlayers)
	result := Snapshot{
		RoomID: roomValue.RoomID, RoomCode: roomValue.Code, RoomRevision: roomValue.Revision,
		TableRevision: engine.Revision(), Phase: engine.Phase(), HandID: engine.HandID(),
		DealerSeat: engine.DealerSeat(), SmallBlindSeat: engine.SmallBlindSeat(), BigBlindSeat: engine.BigBlindSeat(),
		Board: make([]string, 0, len(engine.Board())), Seats: make([]SeatSnapshot, 0, len(players)),
	}
	for _, card := range engine.Board() {
		result.Board = append(result.Board, card.String())
	}
	for _, player := range players {
		result.Seats = append(result.Seats, SeatSnapshot{
			UserID: player.PlayerID, DisplayName: displayNames[player.PlayerID], Seat: player.Seat,
			Stack: player.Stack, Ready: ready[player.PlayerID], Connected: player.Connected,
			Participating: player.Participating, Folded: player.Folded, AllIn: player.AllIn,
			StreetBet: player.StreetBet, TotalBet: player.TotalBet,
			Position: positions[player.PlayerID], LastAction: string(player.LastAction),
			LastCommitted: player.LastCommitted, LastActionTo: player.LastActionTo,
			TimeExtensions: timeExtensions[player.PlayerID],
		})
		result.TotalPot += player.TotalBet
		if player.PlayerID == recipientUserID && player.Participating {
			result.HoleCards = []string{player.HoleCards[0].String(), player.HoleCards[1].String()}
		}
	}
	sort.Slice(result.Seats, func(left, right int) bool { return result.Seats[left].Seat < result.Seats[right].Seat })
	if engine.CurrentSeat() != 0 {
		options, err := engine.CurrentActionOptions()
		if err != nil {
			return Snapshot{}, err
		}
		result.CurrentAction = &ActionSnapshot{
			UserID: engine.CurrentPlayerID(), Seat: engine.CurrentSeat(), Options: options,
			Suggestions: betSuggestions(result.TotalPot, players, engine.CurrentPlayerID(), options),
		}
		if !deadline.IsZero() {
			result.CurrentAction.Deadline = deadline.UnixMilli()
		}
	}
	if settlement := engine.LastSettlement(); settlement.HandID != "" {
		result.Settlement = &settlement
	}
	return result, nil
}

func positionsFor(players []holdem.Player, dealerSeat int, maxSeats int) map[string]string {
	participating := make(map[int]holdem.Player)
	for _, player := range players {
		if player.Participating {
			participating[player.Seat] = player
		}
	}
	count := len(participating)
	result := make(map[string]string, count)
	if count < 2 || participating[dealerSeat].PlayerID == "" {
		return result
	}
	ordered := make([]holdem.Player, 0, count)
	for offset := 0; offset < maxSeats; offset++ {
		seat := ((dealerSeat - 1 + offset) % maxSeats) + 1
		if player, exists := participating[seat]; exists {
			ordered = append(ordered, player)
		}
	}
	if count == 2 {
		result[ordered[0].PlayerID] = "BTN/SB"
		result[ordered[1].PlayerID] = "BB"
		return result
	}
	result[ordered[0].PlayerID] = "BTN"
	result[ordered[1].PlayerID] = "SB"
	result[ordered[2].PlayerID] = "BB"
	remainingPositions := positionsAfterBigBlind(count)
	for index, position := range remainingPositions {
		result[ordered[index+3].PlayerID] = position
	}
	return result
}

func positionsAfterBigBlind(playerCount int) []string {
	switch playerCount {
	case 4:
		return []string{"UTG"}
	case 5:
		return []string{"UTG", "CO"}
	case 6:
		return []string{"UTG", "HJ", "CO"}
	case 7:
		return []string{"UTG", "MP", "HJ", "CO"}
	case 8:
		return []string{"UTG", "UTG+1", "MP", "HJ", "CO"}
	case 9:
		return []string{"UTG", "UTG+1", "MP", "LJ", "HJ", "CO"}
	case 10:
		return []string{"UTG", "UTG+1", "UTG+2", "MP", "LJ", "HJ", "CO"}
	default:
		return nil
	}
}

func betSuggestions(
	totalPot int64,
	players []holdem.Player,
	currentPlayerID string,
	options holdem.ActionOptions,
) []BetSuggestion {
	var player holdem.Player
	for _, candidate := range players {
		if candidate.PlayerID == currentPlayerID {
			player = candidate
			break
		}
	}
	result := make([]BetSuggestion, 0, 7)
	if options.CanBet || options.CanRaise {
		fractions := []struct {
			label       string
			numerator   int64
			denominator int64
		}{
			{"quarter_pot", 1, 4},
			{"third_pot", 1, 3},
			{"half_pot", 1, 2},
			{"two_thirds_pot", 2, 3},
			{"pot", 1, 1},
			{"overbet_120", 6, 5},
		}
		potAfterCall := totalPot + options.ToCall
		for _, fraction := range fractions {
			raiseBy := (potAfterCall*fraction.numerator + fraction.denominator/2) / fraction.denominator
			target := player.StreetBet + options.ToCall + raiseBy
			if target < options.MinRaiseTo {
				target = options.MinRaiseTo
			}
			if target > options.MaxRaiseTo {
				target = options.MaxRaiseTo
			}
			action := holdem.ActionRaise
			if options.CanBet {
				action = holdem.ActionBet
			}
			result = append(result, BetSuggestion{Label: fraction.label, Action: action, RaiseTo: target})
		}
	}
	if options.CanAllIn {
		result = append(result, BetSuggestion{Label: "all_in", Action: holdem.ActionAllIn, RaiseTo: options.MaxRaiseTo})
	}
	return result
}

func allReady(members []room.Member) bool {
	if len(members) < 2 {
		return false
	}
	for _, member := range members {
		if !member.Ready {
			return false
		}
	}
	return true
}
