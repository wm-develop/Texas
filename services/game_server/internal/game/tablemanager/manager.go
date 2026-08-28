package tablemanager

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"texas/services/game_server/internal/bankroll"
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
	bankroll         *bankroll.Service
}

type runtime struct {
	mu                       sync.Mutex
	engine                   *holdem.Table
	roomID                   string
	actionSeconds            int
	deadline                 time.Time
	timer                    ScheduledTimer
	timerGeneration          uint64
	readyTimer               ScheduledTimer
	readyTimerGeneration     uint64
	autoReadyDeadline        time.Time
	autoReadyCancelled       map[string]bool
	voluntarilyRevealedHands map[string]holdem.RevealedHand
	timeExtensions           map[string]int
	handStartedAt            time.Time
	persistedHandID          string
	actions                  []history.Action
	lastAction               *ConfirmedActionSnapshot
}

const (
	timeExtensionsPerHand = 2
	timeExtensionDuration = 30 * time.Second
	headsUpActionDuration = 60 * time.Second
	autoReadyDelay        = 10 * time.Second
)

type ScheduledTimer interface {
	Stop() bool
}

type ManagerConfig struct {
	Now       func() time.Time
	AfterFunc func(time.Duration, func()) ScheduledTimer
	Ledger    ledger.Store
	History   history.Store
	Bankroll  *bankroll.Service
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

type ConfirmedActionSnapshot struct {
	ActionID      string            `json:"actionId"`
	HandID        string            `json:"handId"`
	UserID        string            `json:"userId"`
	Action        holdem.ActionType `json:"action"`
	TableRevision uint64            `json:"tableRevision"`
}

type Snapshot struct {
	RoomID             string                   `json:"roomId"`
	RoomCode           string                   `json:"roomCode"`
	OwnerUserID        string                   `json:"ownerUserId"`
	RoomRevision       uint64                   `json:"roomRevision"`
	TableRevision      uint64                   `json:"tableRevision"`
	Phase              holdem.Phase             `json:"phase"`
	HandID             string                   `json:"handId,omitempty"`
	DealerSeat         int                      `json:"dealerSeat,omitempty"`
	SmallBlindSeat     int                      `json:"smallBlindSeat,omitempty"`
	BigBlindSeat       int                      `json:"bigBlindSeat,omitempty"`
	Board              []string                 `json:"board"`
	HoleCards          []string                 `json:"holeCards,omitempty"`
	Seats              []SeatSnapshot           `json:"seats"`
	CurrentAction      *ActionSnapshot          `json:"currentAction,omitempty"`
	LastAction         *ConfirmedActionSnapshot `json:"lastAction,omitempty"`
	TotalPot           int64                    `json:"totalPot"`
	Settlement         *holdem.Settlement       `json:"settlement,omitempty"`
	VoluntaryReveals   []holdem.RevealedHand    `json:"voluntaryReveals,omitempty"`
	CanShowHoleCards   bool                     `json:"canShowHoleCards"`
	AutoReadyDeadline  int64                    `json:"autoReadyDeadline,omitempty"`
	AutoReadyCancelled bool                     `json:"autoReadyCancelled"`
	MaxBuyIn           int64                    `json:"maxBuyIn"`
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
		bankroll: config.Bankroll,
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
	return snapshotForRuntime(runtime, roomValue, userID)
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
		if !runtime.autoReadyDeadline.IsZero() {
			runtime.autoReadyCancelled[userID] = true
		}
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
	if !runtime.autoReadyDeadline.IsZero() {
		if ready {
			delete(runtime.autoReadyCancelled, userID)
		} else {
			runtime.autoReadyCancelled[userID] = true
		}
	}
	if allReady(roomValue.Members) &&
		(runtime.engine.Phase() == holdem.PhaseWaiting || runtime.engine.Phase() == holdem.PhaseWaitingNextHand) {
		manager.clearAutoReadyLocked(runtime)
		runtime.voluntarilyRevealedHands = make(map[string]holdem.RevealedHand)
		runtime.handStartedAt = manager.now()
		runtime.actions = nil
		runtime.lastAction = nil
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
			manager.scheduleAutoReadyLocked(runtime)
		} else {
			manager.refreshDeadlineLocked(runtime)
		}
	}
	return snapshotForRuntime(runtime, roomValue, userID)
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
	street := runtime.engine.Phase()
	result, err := runtime.engine.SubmitAction(request)
	if err != nil {
		return holdem.ActionResult{}, Snapshot{}, err
	}
	if runtime.engine.Revision() != previousRevision {
		runtime.actions = append(runtime.actions, history.Action{
			ActionID: result.ActionID, UserID: userID, Sequence: len(runtime.actions) + 1,
			Street: strings.ToLower(string(street)), Type: string(result.Action),
			Committed: result.Committed, RaiseTo: request.RaiseTo, CreatedAt: manager.now(),
		})
		runtime.lastAction = &ConfirmedActionSnapshot{
			ActionID: result.ActionID, HandID: request.HandID, UserID: userID,
			Action: result.Action, TableRevision: result.Revision,
		}
	}
	if result.HandEnded {
		if err := manager.persistSettlementLocked(runtime, roomValue); err != nil {
			return holdem.ActionResult{}, Snapshot{}, err
		}
		roomValue, err = manager.resetReadyLocked(ctx, runtime, roomValue)
		if err != nil {
			return holdem.ActionResult{}, Snapshot{}, err
		}
		manager.scheduleAutoReadyLocked(runtime)
	}
	if runtime.engine.Revision() != previousRevision {
		manager.refreshDeadlineLocked(runtime)
	}
	snapshot, err := snapshotForRuntime(runtime, roomValue, userID)
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
	return snapshotForRuntime(runtime, roomValue, userID)
}

func (manager *Manager) ShowHoleCards(ctx context.Context, userID string, roomID string) (Snapshot, error) {
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
	if _, exists := runtime.voluntarilyRevealedHands[userID]; exists {
		return snapshotForRuntime(runtime, roomValue, userID)
	}
	player, eligible := voluntaryRevealPlayer(runtime.engine, userID)
	if !eligible {
		return Snapshot{}, holdem.RuleError{Code: "hole_cards_not_revealable"}
	}
	runtime.voluntarilyRevealedHands[userID] = holdem.RevealedHand{
		PlayerID:  userID,
		HoleCards: []string{player.HoleCards[0].String(), player.HoleCards[1].String()},
		Category:  "voluntary",
	}
	return snapshotForRuntime(runtime, roomValue, userID)
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
	return snapshotForRuntime(runtime, roomValue, userID)
}

func (manager *Manager) Rebuy(
	ctx context.Context,
	userID string,
	roomID string,
	requestID string,
	amount int64,
) (Snapshot, error) {
	if manager.bankroll == nil {
		return Snapshot{}, room.Error{Code: "service_unavailable"}
	}
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
	if runtime.engine.Phase() != holdem.PhaseWaiting && runtime.engine.Phase() != holdem.PhaseWaitingNextHand {
		return Snapshot{}, holdem.RuleError{Code: "hand_in_progress"}
	}
	var current int64 = -1
	for _, player := range runtime.engine.Players() {
		if player.PlayerID == userID {
			current = player.Stack
			break
		}
	}
	if current < 0 {
		return Snapshot{}, holdem.RuleError{Code: "not_seated"}
	}
	if amount <= 0 || current > roomValue.Rules.MaxBuyIn-amount {
		return Snapshot{}, room.Error{Code: "maximum_buy_in_exceeded"}
	}
	position, err := manager.bankroll.Rebuy(ctx, userID, roomID, requestID, amount, roomValue.Rules.MaxBuyIn)
	if err != nil {
		var bankrollError bankroll.Error
		if errors.As(err, &bankrollError) {
			return Snapshot{}, room.Error{Code: bankrollError.Code}
		}
		return Snapshot{}, err
	}
	delta := position.TableChips - current
	if delta == 0 {
		return snapshotForRuntime(runtime, roomValue, userID)
	}
	if delta != amount {
		return Snapshot{}, room.Error{Code: "table_balance_mismatch"}
	}
	if err := runtime.engine.AddChips(userID, delta); err != nil {
		return Snapshot{}, err
	}
	roomValue, err = manager.rooms.SetStack(ctx, roomID, userID, position.TableChips)
	if err != nil {
		return Snapshot{}, err
	}
	return snapshotForRuntime(runtime, roomValue, userID)
}

// Leave returns the player's table balance to their wallet before removing the
// room membership. The room owner transfers to the earliest remaining member;
// the room closes only when its final member leaves. A player cannot
// cash out while a hand is active because their chips may still be committed.
func (manager *Manager) Leave(ctx context.Context, userID string) (bool, error) {
	roomValue, err := manager.rooms.Current(ctx, userID)
	if err != nil {
		return false, err
	}
	runtime := manager.existingRuntime(roomValue.RoomID)
	if runtime != nil {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		if runtime.engine.Phase() != holdem.PhaseWaiting && runtime.engine.Phase() != holdem.PhaseWaitingNextHand {
			return false, holdem.RuleError{Code: "hand_in_progress"}
		}
	}
	if manager.bankroll != nil {
		requestID := "cashout:" + roomValue.RoomID + ":" + userID
		if _, err := manager.bankroll.CashOut(ctx, userID, roomValue.RoomID, requestID); err != nil {
			return false, err
		}
	}
	closed, err := manager.rooms.Leave(ctx, userID)
	if err != nil {
		return false, err
	}
	if runtime != nil {
		_ = runtime.engine.RequestLeave(userID)
		if closed && runtime.timer != nil {
			runtime.timer.Stop()
		}
	}
	if closed {
		if runtime != nil && runtime.readyTimer != nil {
			runtime.readyTimer.Stop()
		}
		manager.mu.Lock()
		delete(manager.tables, roomValue.RoomID)
		manager.mu.Unlock()
	}
	return closed, nil
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
		timeExtensions: make(map[string]int), autoReadyCancelled: make(map[string]bool),
		voluntarilyRevealedHands: make(map[string]holdem.RevealedHand),
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

func (manager *Manager) scheduleAutoReadyLocked(runtime *runtime) {
	if runtime.readyTimer != nil {
		runtime.readyTimer.Stop()
	}
	runtime.readyTimerGeneration++
	runtime.autoReadyDeadline = manager.now().Add(autoReadyDelay)
	runtime.autoReadyCancelled = make(map[string]bool)
	generation := runtime.readyTimerGeneration
	runtime.readyTimer = manager.afterFunc(autoReadyDelay, func() {
		manager.handleAutoReady(runtime.roomID, generation)
	})
}

func (manager *Manager) clearAutoReadyLocked(runtime *runtime) {
	if runtime.readyTimer != nil {
		runtime.readyTimer.Stop()
		runtime.readyTimer = nil
	}
	runtime.readyTimerGeneration++
	runtime.autoReadyDeadline = time.Time{}
	runtime.autoReadyCancelled = make(map[string]bool)
}

func (manager *Manager) handleAutoReady(roomID string, generation uint64) {
	runtime := manager.existingRuntime(roomID)
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	if generation != runtime.readyTimerGeneration ||
		runtime.engine.Phase() != holdem.PhaseWaitingNextHand ||
		manager.now().Before(runtime.autoReadyDeadline) {
		runtime.mu.Unlock()
		return
	}
	connected := make(map[string]bool)
	for _, player := range runtime.engine.Players() {
		connected[player.PlayerID] = player.Connected
	}
	cancelled := make(map[string]bool, len(runtime.autoReadyCancelled))
	for userID, value := range runtime.autoReadyCancelled {
		cancelled[userID] = value
	}
	runtime.readyTimer = nil
	runtime.autoReadyDeadline = time.Time{}
	runtime.mu.Unlock()

	var roomValue room.Room
	for userID := range connected {
		value, err := manager.rooms.GetForMember(context.Background(), userID, roomID)
		if err == nil {
			roomValue = value
			break
		}
	}
	if roomValue.RoomID == "" {
		return
	}
	for _, member := range roomValue.Members {
		if member.Stack <= 0 || !connected[member.UserID] || cancelled[member.UserID] {
			continue
		}
		if _, err := manager.SetReady(context.Background(), member.UserID, true); err != nil {
			return
		}
	}
	manager.notifySnapshot(roomID)
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
		street := runtime.engine.Phase()
		result, err = runtime.engine.ApplyTimeout()
		if err == nil {
			runtime.actions = append(runtime.actions, history.Action{
				ActionID: result.ActionID, UserID: actorUserID, Sequence: len(runtime.actions) + 1,
				Street: strings.ToLower(string(street)), Type: string(result.Action), Committed: result.Committed,
				CreatedAt: manager.now(),
			})
			runtime.lastAction = &ConfirmedActionSnapshot{
				ActionID: result.ActionID, HandID: runtime.engine.HandID(), UserID: actorUserID,
				Action: result.Action, TableRevision: result.Revision,
			}
		}
		if err == nil && result.HandEnded {
			err = manager.persistSettlementLocked(runtime, roomValue)
			if err == nil {
				roomValue, err = manager.resetReadyLocked(context.Background(), runtime, roomValue)
				if err == nil {
					manager.scheduleAutoReadyLocked(runtime)
				}
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
	if manager.bankroll != nil {
		if err := manager.bankroll.ApplySettlement(
			context.Background(), roomValue.RoomID, settlement.HandID,
			settlement.StacksByPlayer, roomValue.Rules.MaxBuyIn,
		); err != nil {
			return err
		}
		if _, err := manager.rooms.UpdateStacks(context.Background(), roomValue.RoomID, settlement.StacksByPlayer); err != nil {
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
			DealerSeat: runtime.engine.DealerSeat(),
			StartedAt:  runtime.handStartedAt, EndedAt: manager.now(), Showdown: settlement.Showdown,
			PotAwards: settlement.PotAwards, RevealedHands: settlement.RevealedHands,
			Actions: append([]history.Action(nil), runtime.actions...),
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
			if err := engine.AddPlayer(member.UserID, member.Seat, member.Stack); err != nil {
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

func snapshotForRuntime(runtime *runtime, roomValue room.Room, recipientUserID string) (Snapshot, error) {
	result, err := snapshotFor(
		runtime.engine, roomValue, recipientUserID, runtime.deadline, runtime.timeExtensions,
	)
	if err != nil {
		return Snapshot{}, err
	}
	result.OwnerUserID = roomValue.OwnerUserID
	if runtime.lastAction != nil {
		lastAction := *runtime.lastAction
		result.LastAction = &lastAction
	}
	result.CanShowHoleCards = canVoluntarilyReveal(runtime.engine, recipientUserID) &&
		runtime.voluntarilyRevealedHands[recipientUserID].PlayerID == ""
	if !runtime.autoReadyDeadline.IsZero() {
		result.AutoReadyDeadline = runtime.autoReadyDeadline.UnixMilli()
		result.AutoReadyCancelled = runtime.autoReadyCancelled[recipientUserID]
	}
	userIDs := make([]string, 0, len(runtime.voluntarilyRevealedHands))
	for userID := range runtime.voluntarilyRevealedHands {
		userIDs = append(userIDs, userID)
	}
	sort.Strings(userIDs)
	for _, userID := range userIDs {
		hand := runtime.voluntarilyRevealedHands[userID]
		hand.HoleCards = append([]string(nil), hand.HoleCards...)
		result.VoluntaryReveals = append(result.VoluntaryReveals, hand)
	}
	return result, nil
}

func voluntaryRevealPlayer(engine *holdem.Table, userID string) (holdem.Player, bool) {
	for _, player := range engine.Players() {
		if player.PlayerID != userID || !player.Participating || engine.HandID() == "" {
			continue
		}
		if player.Folded {
			return player, true
		}
		settlement := engine.LastSettlement()
		if engine.Phase() == holdem.PhaseWaitingNextHand && !settlement.Showdown &&
			settlement.HandID == engine.HandID() {
			for _, award := range settlement.PotAwards {
				for _, winnerID := range award.WinnerPlayerIDs {
					if winnerID == userID {
						return player, true
					}
				}
			}
		}
	}
	return holdem.Player{}, false
}

func canVoluntarilyReveal(engine *holdem.Table, userID string) bool {
	_, eligible := voluntaryRevealPlayer(engine, userID)
	return eligible
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
		MaxBuyIn: roomValue.Rules.MaxBuyIn,
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
			Suggestions: betSuggestions(
				result.TotalPot, players, engine.CurrentPlayerID(), options, roomValue.Rules.SmallBlind,
			),
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
	smallBlind int64,
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
		seenTargets := make(map[int64]struct{}, len(fractions))
		for _, fraction := range fractions {
			raiseBy := (potAfterCall*fraction.numerator + fraction.denominator/2) / fraction.denominator
			target := roundToNearestUnit(player.StreetBet+options.ToCall+raiseBy, smallBlind)
			label := fraction.label
			if target < options.MinRaiseTo {
				target = options.MinRaiseTo
				label = "min_raise"
			}
			if target > options.MaxRaiseTo {
				target = options.MaxRaiseTo
			}
			if _, exists := seenTargets[target]; exists {
				continue
			}
			seenTargets[target] = struct{}{}
			action := holdem.ActionRaise
			if options.CanBet {
				action = holdem.ActionBet
			}
			result = append(result, BetSuggestion{Label: label, Action: action, RaiseTo: target})
		}
	}
	if options.CanAllIn {
		result = append(result, BetSuggestion{
			Label: "all_in", Action: holdem.ActionAllIn, RaiseTo: player.StreetBet + player.Stack,
		})
	}
	return result
}

func roundToNearestUnit(value, unit int64) int64 {
	if unit <= 1 {
		return value
	}
	return ((value + unit/2) / unit) * unit
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
