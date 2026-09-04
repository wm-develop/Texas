package tablemanager

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/ledger"
	"texas/services/game_server/internal/room"
)

type Manager struct {
	mu sync.Mutex
	// draining 置位后不再开新局，用于优雅停机等待所有牌桌到达手间空档。
	draining         atomic.Bool
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
	draining                 *atomic.Bool
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
	holeCardViewRequests     map[string]holeCardViewRequest
	privateHoleCardViews     map[string]map[string]holdem.RevealedHand
	seatSwapRequests         map[string]seatSwapRequest
	timeExtensions           map[string]int
	handStartedAt            time.Time
	persistedHandID          string
	actions                  []history.Action
	lastAction               *ConfirmedActionSnapshot
	// online 记录当前保持 WebSocket 连接的用户，独立于引擎座位。
	// 牌局进行中加入的玩家在结算前没有引擎座位，Join 时的 SetConnected 落空；
	// 若入座时一律写成断线，他会一直显示「已断线」，服务端自动准备也会跳过他。
	online map[string]bool
	// pendingCashOuts holds userID → display name for folded players who left
	// the room mid-hand. Their wallet refund runs after the settlement writes
	// the post-hand stacks; the display name keeps hand history readable.
	pendingCashOuts map[string]string
	// knownDisplayNames 记住本房间见过的昵称。赢家常常赢完这手就离开房间，
	// 届时房间成员表里已经没有他，结算文案会退化成显示用户 ID。
	knownDisplayNames map[string]string
	// 观战位。pendingSpectate / pendingSeat 记录牌局进行中提交的切换意向，
	// 手结束时统一应用；spectatorAccess 记录本手已付费、可以看牌的观战者；
	// spectatorFees 是本手看牌费的收付明细，随快照下发给结算区展示。
	pendingSpectate map[string]bool
	pendingSeat     map[string]bool
	spectatorAccess map[string]bool
	spectatorFees   *SpectatorFeeSnapshot
}

const (
	timeExtensionsPerHand  = 2
	timeExtensionDuration  = 30 * time.Second
	standardActionDuration = 30 * time.Second
	headsUpActionDuration  = 60 * time.Second
	autoReadyDelay         = 10 * time.Second
	// 发两次的结算在客户端分两块牌面先后展示（第一块停留 5 秒再切换），
	// 自动准备相应延长，保证玩家能看完两块牌面的结果。
	runoutAutoReadyDelay = 15 * time.Second
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
	// HoleCards 只对本手有效付费的观战者填充；普通玩家永远拿不到别人的手牌。
	HoleCards []string `json:"holeCards,omitempty"`
	// PendingSpectate 表示该玩家已申请本手结束后进入观战。
	PendingSpectate bool   `json:"pendingSpectate,omitempty"`
	UserID          string `json:"userId"`
	DisplayName     string `json:"displayName"`
	Seat            int    `json:"seat"`
	Stack           int64  `json:"stack"`
	Ready           bool   `json:"ready"`
	Connected       bool   `json:"connected"`
	Participating   bool   `json:"participating"`
	Folded          bool   `json:"folded"`
	AllIn           bool   `json:"allIn"`
	StreetBet       int64  `json:"streetBet"`
	TotalBet        int64  `json:"totalBet"`
	Position        string `json:"position,omitempty"`
	LastAction      string `json:"lastAction,omitempty"`
	LastCommitted   int64  `json:"lastCommitted,omitempty"`
	LastActionTo    int64  `json:"lastActionTo,omitempty"`
	TimeExtensions  int    `json:"timeExtensions"`
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

type HoleCardViewRequestSnapshot struct {
	RequestID       string `json:"requestId"`
	RequesterUserID string `json:"requesterUserId"`
	TargetUserID    string `json:"targetUserId"`
}

type SeatSwapRequestSnapshot struct {
	RequestID       string `json:"requestId"`
	RequesterUserID string `json:"requesterUserId"`
	TargetUserID    string `json:"targetUserId"`
}

type RunoutChoiceSnapshot struct {
	EligiblePlayerIDs []string       `json:"eligiblePlayerIds"`
	Choices           map[string]int `json:"choices"`
	Deadline          int64          `json:"deadline"`
}

type holeCardViewRequest struct {
	RequestID, RequesterUserID, TargetUserID, HandID string
}

type seatSwapRequest struct {
	RequestID, RequesterUserID, TargetUserID string
}

type Snapshot struct {
	RoomID             string                        `json:"roomId"`
	RoomCode           string                        `json:"roomCode"`
	OwnerUserID        string                        `json:"ownerUserId"`
	RoomRevision       uint64                        `json:"roomRevision"`
	TableRevision      uint64                        `json:"tableRevision"`
	Phase              holdem.Phase                  `json:"phase"`
	HandID             string                        `json:"handId,omitempty"`
	DealerSeat         int                           `json:"dealerSeat,omitempty"`
	SmallBlindSeat     int                           `json:"smallBlindSeat,omitempty"`
	BigBlindSeat       int                           `json:"bigBlindSeat,omitempty"`
	Board              []string                      `json:"board"`
	HoleCards          []string                      `json:"holeCards,omitempty"`
	Seats              []SeatSnapshot                `json:"seats"`
	CurrentAction      *ActionSnapshot               `json:"currentAction,omitempty"`
	LastAction         *ConfirmedActionSnapshot      `json:"lastAction,omitempty"`
	TotalPot           int64                         `json:"totalPot"`
	Settlement         *holdem.Settlement            `json:"settlement,omitempty"`
	VoluntaryReveals   []holdem.RevealedHand         `json:"voluntaryReveals,omitempty"`
	PrivateReveals     []holdem.RevealedHand         `json:"privateReveals,omitempty"`
	HoleCardRequests   []HoleCardViewRequestSnapshot `json:"holeCardViewRequests,omitempty"`
	SeatSwapRequests   []SeatSwapRequestSnapshot     `json:"seatSwapRequests,omitempty"`
	RunoutChoice       *RunoutChoiceSnapshot         `json:"runoutChoice,omitempty"`
	CanShowHoleCards   bool                          `json:"canShowHoleCards"`
	AutoReadyDeadline  int64                         `json:"autoReadyDeadline,omitempty"`
	AutoReadyCancelled bool                          `json:"autoReadyCancelled"`
	MaxBuyIn           int64                         `json:"maxBuyIn"`
	// Draining 为 true 表示服务端正在优雅停机：本手结束后不再开新局。
	Draining bool `json:"draining,omitempty"`
	// JoinLocked 为 true 表示房主已关闭房间入口。
	JoinLocked bool `json:"joinLocked,omitempty"`
	// Spectators 是观战位上的成员；Seats 仍只含上桌玩家。
	Spectators []SpectatorSnapshot `json:"spectators,omitempty"`
	// SpectatorSettings 是房主对观战位的设置，客户端据此显示或隐藏入口。
	SpectatorSettings room.SpectatorSettings `json:"spectatorSettings"`
	// SpectatorFees 是最近一手看牌费的收付明细，只在该手结算展示期间下发。
	SpectatorFees *SpectatorFeeSnapshot `json:"spectatorFees,omitempty"`
	// Spectating 表示接收者本人在观战位。
	Spectating bool `json:"spectating"`
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
	runtime.online[userID] = true
	if err := syncMembers(runtime.engine, roomValue, runtime.online); err != nil {
		return Snapshot{}, err
	}
	if err := runtime.engine.SetConnected(userID, true); err != nil {
		// A member joining mid-hand has no engine seat yet; they connect as a
		// pending spectator and take their seat when the hand settles.
		var ruleError holdem.RuleError
		if !errors.As(err, &ruleError) || ruleError.Code != "not_seated" {
			return Snapshot{}, err
		}
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
	delete(runtime.online, userID)
	_ = runtime.engine.SetConnected(userID, false)
	if runtime.engine.Phase() == holdem.PhaseWaiting || runtime.engine.Phase() == holdem.PhaseWaitingNextHand {
		// A short platform/network reconnect must not be treated as the player
		// explicitly cancelling automatic ready. This is especially common on
		// HarmonyOS when the app briefly changes lifecycle state.
		if updated, readyErr := manager.rooms.SetReady(ctx, userID, false); readyErr == nil {
			roomValue = updated
			_ = runtime.engine.SetReady(userID, false)
		}
	}
	_ = roomValue
}

func (manager *Manager) SetReady(ctx context.Context, userID string, ready bool) (Snapshot, error) {
	// 停机排空期间不接受准备：一旦全员就绪就会开新局，而新局撑不到进程退出。
	// 取消准备仍然允许，重启后玩家重新点准备即可。
	if ready && manager.draining.Load() {
		return Snapshot{}, holdem.RuleError{Code: "server_draining"}
	}
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
	if err := syncMembers(runtime.engine, roomValue, runtime.online); err != nil {
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
		runtime.holeCardViewRequests = make(map[string]holeCardViewRequest)
		runtime.privateHoleCardViews = make(map[string]map[string]holdem.RevealedHand)
		runtime.seatSwapRequests = make(map[string]seatSwapRequest)
		runtime.handStartedAt = manager.now()
		runtime.actions = nil
		runtime.lastAction = nil
		runtime.persistedHandID = ""
		// 看牌费必须在发牌之前收：引擎在 StartHand 里记下每人的起始筹码作为
		// 结算守恒校验的基线，发牌后再往玩家筹码里加钱会让 Σ(结束−起始) ≠ 0。
		roomValue, err = manager.collectSpectatorFeesLocked(ctx, runtime, roomValue)
		if err != nil {
			return Snapshot{}, err
		}
		if err := runtime.engine.StartHand(manager.random); err != nil {
			return Snapshot{}, err
		}
		if runtime.spectatorFees != nil {
			runtime.spectatorFees.HandID = runtime.engine.HandID()
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
		runtime.holeCardViewRequests = make(map[string]holeCardViewRequest)
		runtime.privateHoleCardViews = make(map[string]map[string]holdem.RevealedHand)
		if err := manager.persistSettlementLocked(runtime, roomValue); err != nil {
			return holdem.ActionResult{}, Snapshot{}, err
		}
		roomValue, err = manager.resetReadyLocked(ctx, runtime, roomValue)
		if err != nil {
			return holdem.ActionResult{}, Snapshot{}, err
		}
		manager.scheduleAutoReadyLocked(runtime)
		manager.refreshDeadlineLocked(runtime)
	}
	if runtime.engine.Revision() != previousRevision {
		manager.refreshDeadlineLocked(runtime)
	}
	snapshot, err := snapshotForRuntime(runtime, roomValue, userID)
	return result, snapshot, err
}

func (manager *Manager) SubmitRunoutChoice(
	ctx context.Context, userID, roomID string, count int,
) (Snapshot, error) {
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
	settled, err := runtime.engine.ChooseRunoutCount(userID, count)
	if err != nil {
		return Snapshot{}, err
	}
	if settled {
		if err := manager.persistSettlementLocked(runtime, roomValue); err != nil {
			return Snapshot{}, err
		}
		roomValue, err = manager.resetReadyLocked(ctx, runtime, roomValue)
		if err != nil {
			return Snapshot{}, err
		}
		manager.scheduleAutoReadyLocked(runtime)
		manager.refreshDeadlineLocked(runtime)
	} else {
		manager.refreshDeadlineLocked(runtime)
	}
	return snapshotForRuntime(runtime, roomValue, userID)
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
	if err := syncMembers(runtime.engine, roomValue, runtime.online); err != nil {
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

func (manager *Manager) RequestHoleCardView(
	ctx context.Context, requesterUserID, roomID, targetUserID, requestID string,
) (Snapshot, error) {
	roomValue, err := manager.rooms.GetForMember(ctx, requesterUserID, roomID)
	if err != nil {
		return Snapshot{}, err
	}
	runtime := manager.existingRuntime(roomID)
	if runtime == nil {
		return Snapshot{}, room.Error{Code: "table_not_started"}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if requestID == "" || requesterUserID == targetUserID ||
		runtime.engine.Phase() == holdem.PhaseWaiting || runtime.engine.Phase() == holdem.PhaseWaitingNextHand {
		return Snapshot{}, holdem.RuleError{Code: "hole_card_view_not_available"}
	}
	var requester, target *holdem.Player
	players := runtime.engine.Players()
	for index := range players {
		player := &players[index]
		if player.PlayerID == requesterUserID {
			requester = player
		}
		if player.PlayerID == targetUserID {
			target = player
		}
	}
	if requester == nil || !requester.Participating || !requester.Folded || target == nil ||
		!target.Participating || target.HoleCards[0].Rank == 0 || target.HoleCards[1].Rank == 0 {
		return Snapshot{}, holdem.RuleError{Code: "hole_card_view_not_available"}
	}
	for _, pending := range runtime.holeCardViewRequests {
		if pending.RequesterUserID == requesterUserID && pending.TargetUserID == targetUserID {
			return snapshotForRuntime(runtime, roomValue, requesterUserID)
		}
	}
	runtime.holeCardViewRequests[requestID] = holeCardViewRequest{
		RequestID: requestID, RequesterUserID: requesterUserID,
		TargetUserID: targetUserID, HandID: runtime.engine.HandID(),
	}
	return snapshotForRuntime(runtime, roomValue, requesterUserID)
}

func (manager *Manager) RespondHoleCardView(
	ctx context.Context, targetUserID, roomID, requestID string, accept bool,
) (Snapshot, error) {
	roomValue, err := manager.rooms.GetForMember(ctx, targetUserID, roomID)
	if err != nil {
		return Snapshot{}, err
	}
	runtime := manager.existingRuntime(roomID)
	if runtime == nil {
		return Snapshot{}, room.Error{Code: "table_not_started"}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	pending, exists := runtime.holeCardViewRequests[requestID]
	if !exists || pending.TargetUserID != targetUserID || pending.HandID != runtime.engine.HandID() {
		return Snapshot{}, holdem.RuleError{Code: "hole_card_view_request_not_found"}
	}
	delete(runtime.holeCardViewRequests, requestID)
	if accept {
		for _, player := range runtime.engine.Players() {
			if player.PlayerID != targetUserID || !player.Participating {
				continue
			}
			if runtime.privateHoleCardViews[pending.RequesterUserID] == nil {
				runtime.privateHoleCardViews[pending.RequesterUserID] = make(map[string]holdem.RevealedHand)
			}
			runtime.privateHoleCardViews[pending.RequesterUserID][targetUserID] = holdem.RevealedHand{
				PlayerID:  targetUserID,
				HoleCards: []string{player.HoleCards[0].String(), player.HoleCards[1].String()},
				Category:  "private_view",
			}
			break
		}
	}
	return snapshotForRuntime(runtime, roomValue, targetUserID)
}

func (manager *Manager) RequestSeatChange(
	ctx context.Context, requesterUserID, roomID string, targetSeat int, requestID string,
) (Snapshot, error) {
	roomValue, err := manager.rooms.GetForMember(ctx, requesterUserID, roomID)
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
	var targetUserID string
	for _, member := range roomValue.Members {
		if member.Seat == targetSeat {
			targetUserID = member.UserID
			break
		}
	}
	if targetUserID == "" {
		roomValue, err = manager.rooms.MoveSeat(ctx, requesterUserID, targetSeat)
		if err != nil {
			return Snapshot{}, err
		}
		if err := runtime.engine.MovePlayer(requesterUserID, targetSeat); err != nil {
			return Snapshot{}, err
		}
		return snapshotForRuntime(runtime, roomValue, requesterUserID)
	}
	if targetUserID == requesterUserID || requestID == "" {
		return Snapshot{}, holdem.RuleError{Code: "invalid_seat_swap"}
	}
	runtime.seatSwapRequests[requestID] = seatSwapRequest{
		RequestID: requestID, RequesterUserID: requesterUserID, TargetUserID: targetUserID,
	}
	return snapshotForRuntime(runtime, roomValue, requesterUserID)
}

func (manager *Manager) RespondSeatSwap(
	ctx context.Context, targetUserID, roomID, requestID string, accept bool,
) (Snapshot, error) {
	roomValue, err := manager.rooms.GetForMember(ctx, targetUserID, roomID)
	if err != nil {
		return Snapshot{}, err
	}
	runtime := manager.existingRuntime(roomID)
	if runtime == nil {
		return Snapshot{}, room.Error{Code: "table_not_started"}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	pending, exists := runtime.seatSwapRequests[requestID]
	if !exists || pending.TargetUserID != targetUserID {
		return Snapshot{}, holdem.RuleError{Code: "seat_swap_request_not_found"}
	}
	delete(runtime.seatSwapRequests, requestID)
	if accept {
		if runtime.engine.Phase() != holdem.PhaseWaiting && runtime.engine.Phase() != holdem.PhaseWaitingNextHand {
			return Snapshot{}, holdem.RuleError{Code: "hand_in_progress"}
		}
		roomValue, err = manager.rooms.SwapSeats(ctx, pending.RequesterUserID, targetUserID)
		if err != nil {
			return Snapshot{}, err
		}
		if err := runtime.engine.SwapPlayers(pending.RequesterUserID, targetUserID); err != nil {
			return Snapshot{}, err
		}
	}
	return snapshotForRuntime(runtime, roomValue, targetUserID)
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
	var current int64 = -1
	for _, player := range runtime.engine.Players() {
		if player.PlayerID == userID {
			current = player.Stack
			break
		}
	}
	// 观战者不在引擎里，筹码记在房间成员上。他们不参与牌局，补码不必等手间——
	// 看牌费不够时正需要马上补上，好赶上下一手。
	spectating := false
	if current < 0 {
		for _, member := range roomValue.Members {
			if member.UserID == userID && member.Spectating {
				current = member.Stack
				spectating = true
				break
			}
		}
	}
	if current < 0 {
		return Snapshot{}, holdem.RuleError{Code: "not_seated"}
	}
	if !spectating &&
		runtime.engine.Phase() != holdem.PhaseWaiting && runtime.engine.Phase() != holdem.PhaseWaitingNextHand {
		return Snapshot{}, holdem.RuleError{Code: "hand_in_progress"}
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
	if !spectating {
		if err := runtime.engine.AddChips(userID, delta); err != nil {
			return Snapshot{}, err
		}
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
	deferCashOut := false
	if runtime != nil {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		if runtime.engine.Phase() != holdem.PhaseWaiting && runtime.engine.Phase() != holdem.PhaseWaitingNextHand {
			var player *holdem.Player
			players := runtime.engine.Players()
			for index := range players {
				if players[index].PlayerID == userID {
					player = &players[index]
					break
				}
			}
			switch {
			case player == nil || !player.Participating:
				// Pending members and players sitting out this hand hold no
				// chips in the pot; they can settle and leave immediately.
			case player.Folded:
				// A folded player's stack is fixed until settlement, but the
				// bankroll table balance still reflects the pre-hand stack.
				// Defer the cash-out until the settlement writes the post-hand
				// stacks, otherwise chips committed to the pot would be
				// returned twice.
				deferCashOut = true
			default:
				return false, holdem.RuleError{Code: "hand_in_progress"}
			}
		}
	}
	if manager.bankroll != nil && !deferCashOut {
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
		delete(runtime.pendingSpectate, userID)
		delete(runtime.pendingSeat, userID)
		_ = runtime.engine.RequestLeave(userID)
		if deferCashOut && !closed {
			displayName := ""
			for _, member := range roomValue.Members {
				if member.UserID == userID {
					displayName = member.DisplayName
					break
				}
			}
			if runtime.pendingCashOuts == nil {
				runtime.pendingCashOuts = make(map[string]string)
			}
			runtime.pendingCashOuts[userID] = displayName
		}
		if closed && runtime.timer != nil {
			runtime.timer.Stop()
		}
	}
	if closed {
		if deferCashOut && manager.bankroll != nil {
			// The room can only close mid-hand in degenerate cases; without an
			// upcoming settlement the deferred cash-out must run now so the
			// table balance is not stranded.
			requestID := "cashout:" + roomValue.RoomID + ":" + userID
			_, _ = manager.bankroll.CashOut(ctx, userID, roomValue.RoomID, requestID)
		}
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
		engine: engine, draining: &manager.draining,
		roomID: roomValue.RoomID, actionSeconds: roomValue.Rules.ActionSeconds,
		timeExtensions: make(map[string]int), autoReadyCancelled: make(map[string]bool),
		voluntarilyRevealedHands: make(map[string]holdem.RevealedHand),
		holeCardViewRequests:     make(map[string]holeCardViewRequest),
		privateHoleCardViews:     make(map[string]map[string]holdem.RevealedHand),
		seatSwapRequests:         make(map[string]seatSwapRequest),
		online:                   make(map[string]bool),
	}
	manager.tables[roomValue.RoomID] = created
	return created, nil
}

// ActiveTables 返回当前进程内持有的运行中牌桌数量，供指标暴露使用。
// BeginDrain 进入停机排空：不再开新局、取消所有牌桌的自动准备倒计时，
// 并向每桌广播一次快照，让客户端立即显示原因。进行中的手不受影响。
// 重复调用是空操作。
func (manager *Manager) BeginDrain() {
	if manager.draining.Swap(true) {
		return
	}
	manager.mu.Lock()
	tables := make(map[string]*runtime, len(manager.tables))
	for roomID, table := range manager.tables {
		tables[roomID] = table
	}
	manager.mu.Unlock()
	for roomID, table := range tables {
		table.mu.Lock()
		manager.clearAutoReadyLocked(table)
		table.mu.Unlock()
		manager.notifySnapshot(roomID)
	}
}

// Draining 报告是否处于停机排空。
func (manager *Manager) Draining() bool { return manager.draining.Load() }

// TablesInHand 返回仍有一手牌在进行中的牌桌数。
func (manager *Manager) TablesInHand() int {
	manager.mu.Lock()
	tables := make([]*runtime, 0, len(manager.tables))
	for _, table := range manager.tables {
		tables = append(tables, table)
	}
	manager.mu.Unlock()
	count := 0
	for _, table := range tables {
		table.mu.Lock()
		phase := table.engine.Phase()
		table.mu.Unlock()
		if phase != holdem.PhaseWaiting && phase != holdem.PhaseWaitingNextHand {
			count++
		}
	}
	return count
}

// WaitForHandBoundary 阻塞到所有牌桌都处于手间空档，或 ctx 结束。
// 手内行动有服务端权威超时推进，因此这里只需轮询，不会永久等待。
func (manager *Manager) WaitForHandBoundary(ctx context.Context) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if manager.TablesInHand() == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (manager *Manager) ActiveTables() int {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return len(manager.tables)
}

func (manager *Manager) existingRuntime(roomID string) *runtime {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.tables[roomID]
}

func (manager *Manager) refreshDeadlineLocked(runtime *runtime) {
	duration := standardActionDuration
	if runtime.engine.Phase() == holdem.PhaseRunoutChoice {
		manager.scheduleDeadlineLocked(runtime, manager.now().Add(duration))
		return
	}
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
	if runtime.engine.CurrentSeat() == 0 && runtime.engine.Phase() != holdem.PhaseRunoutChoice {
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
	// 排空期间不安排自动准备，否则倒计时结束后只会得到一串 server_draining。
	if manager.draining.Load() {
		manager.clearAutoReadyLocked(runtime)
		return
	}
	if runtime.readyTimer != nil {
		runtime.readyTimer.Stop()
	}
	delay := autoReadyDelay
	if len(runtime.engine.LastSettlement().RunoutBoards) == 2 {
		delay = runoutAutoReadyDelay
	}
	runtime.readyTimerGeneration++
	runtime.autoReadyDeadline = manager.now().Add(delay)
	runtime.autoReadyCancelled = make(map[string]bool)
	generation := runtime.readyTimerGeneration
	runtime.readyTimer = manager.afterFunc(delay, func() {
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
	if generation != runtime.timerGeneration || manager.now().Before(runtime.deadline) {
		runtime.mu.Unlock()
		return
	}
	if runtime.engine.Phase() == holdem.PhaseRunoutChoice {
		state := runtime.engine.RunoutChoiceState()
		var roomValue room.Room
		var err error
		for _, userID := range state.EligiblePlayerIDs {
			roomValue, err = manager.rooms.GetForMember(context.Background(), userID, roomID)
			if err == nil {
				break
			}
		}
		if err == nil {
			err = runtime.engine.ResolveRunoutChoiceTimeout()
		}
		if err == nil {
			err = manager.persistSettlementLocked(runtime, roomValue)
		}
		if err == nil {
			roomValue, err = manager.resetReadyLocked(context.Background(), runtime, roomValue)
		}
		if err == nil {
			manager.scheduleAutoReadyLocked(runtime)
			runtime.timerGeneration++
			runtime.deadline = time.Time{}
			runtime.timer = nil
		} else {
			runtime.deadline = time.Time{}
			runtime.timer = nil
		}
		runtime.mu.Unlock()
		if err == nil {
			manager.notifySnapshot(roomID)
		}
		return
	}
	if runtime.engine.CurrentSeat() == 0 {
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
	// 每一手结束都会经过这里，是应用「上桌 / 进观战」切换意向的统一入口。
	roomValue, err := manager.applyPendingSeatChangesLocked(ctx, runtime, roomValue)
	if err != nil {
		return room.Room{}, err
	}
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
		// Folded players who left mid-hand now have their post-hand stack in
		// the table balance; move it back to their wallet idempotently.
		for userID := range runtime.pendingCashOuts {
			requestID := "cashout:" + roomValue.RoomID + ":" + userID
			if _, err := manager.bankroll.CashOut(context.Background(), userID, roomValue.RoomID, requestID); err != nil {
				return err
			}
		}
	}
	if _, exists := manager.history.Hand(settlement.HandID); !exists {
		displayNames := make(map[string]string, len(roomValue.Members)+len(runtime.pendingCashOuts))
		for userID, displayName := range runtime.pendingCashOuts {
			displayNames[userID] = displayName
		}
		for _, member := range roomValue.Members {
			displayNames[member.UserID] = member.DisplayName
		}
		if runtime.knownDisplayNames == nil {
			runtime.knownDisplayNames = make(map[string]string, len(displayNames))
		}
		for userID, displayName := range displayNames {
			runtime.knownDisplayNames[userID] = displayName
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
			RunoutBoards: settlement.RunoutBoards,
			Actions:      append([]history.Action(nil), runtime.actions...),
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
	runtime.pendingCashOuts = nil
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

func syncMembers(engine *holdem.Table, roomValue room.Room, online map[string]bool) error {
	players := engine.Players()
	byID := make(map[string]holdem.Player, len(players))
	for _, player := range players {
		byID[player.PlayerID] = player
	}
	betweenHands := engine.Phase() == holdem.PhaseWaiting || engine.Phase() == holdem.PhaseWaitingNextHand
	for _, member := range roomValue.Members {
		// 观战者不占引擎座位。
		if member.Spectating {
			continue
		}
		if _, exists := byID[member.UserID]; !exists {
			// New members can only take an engine seat between hands. A member
			// joining mid-hand stays as a pending spectator until the current
			// hand settles; failing here would break snapshots for the whole
			// table until the hand ends.
			if !betweenHands {
				continue
			}
			if err := engine.AddPlayer(member.UserID, member.Seat, member.Stack); err != nil {
				return err
			}
			// 中途加入者在结算后才入座，此时他多半早已连着；按真实在线状态落座，
			// 否则他会一直显示「已断线」，服务端自动准备也会跳过他。
			if err := engine.SetConnected(member.UserID, online[member.UserID]); err != nil {
				return err
			}
		}
		if betweenHands {
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
	result.Draining = runtime.draining != nil && runtime.draining.Load()
	result.JoinLocked = roomValue.JoinLocked
	annotateSettlement(runtime, roomValue, &result)
	annotateSpectators(runtime, roomValue, recipientUserID, &result)
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
	if runtime.engine.Phase() == holdem.PhaseRunoutChoice {
		state := runtime.engine.RunoutChoiceState()
		result.RunoutChoice = &RunoutChoiceSnapshot{
			EligiblePlayerIDs: append([]string(nil), state.EligiblePlayerIDs...),
			Choices:           make(map[string]int, len(state.Choices)),
			Deadline:          runtime.deadline.UnixMilli(),
		}
		for userID, choice := range state.Choices {
			result.RunoutChoice.Choices[userID] = choice
		}
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
	privateUserIDs := make([]string, 0, len(runtime.privateHoleCardViews[recipientUserID]))
	for userID := range runtime.privateHoleCardViews[recipientUserID] {
		privateUserIDs = append(privateUserIDs, userID)
	}
	sort.Strings(privateUserIDs)
	for _, userID := range privateUserIDs {
		hand := runtime.privateHoleCardViews[recipientUserID][userID]
		hand.HoleCards = append([]string(nil), hand.HoleCards...)
		result.PrivateReveals = append(result.PrivateReveals, hand)
	}
	for _, pending := range runtime.holeCardViewRequests {
		if pending.TargetUserID == recipientUserID {
			result.HoleCardRequests = append(result.HoleCardRequests, HoleCardViewRequestSnapshot{
				RequestID: pending.RequestID, RequesterUserID: pending.RequesterUserID,
				TargetUserID: pending.TargetUserID,
			})
		}
	}
	for _, pending := range runtime.seatSwapRequests {
		if pending.TargetUserID == recipientUserID {
			result.SeatSwapRequests = append(result.SeatSwapRequests, SeatSwapRequestSnapshot{
				RequestID: pending.RequestID, RequesterUserID: pending.RequesterUserID,
				TargetUserID: pending.TargetUserID,
			})
		}
	}
	sort.Slice(result.HoleCardRequests, func(left, right int) bool {
		return result.HoleCardRequests[left].RequestID < result.HoleCardRequests[right].RequestID
	})
	sort.Slice(result.SeatSwapRequests, func(left, right int) bool {
		return result.SeatSwapRequests[left].RequestID < result.SeatSwapRequests[right].RequestID
	})
	return result, nil
}

func voluntaryRevealPlayer(engine *holdem.Table, userID string) (holdem.Player, bool) {
	// Voluntary public reveal belongs to the settlement window. Folded players
	// must not be able to expose their cards while a hand is still in progress.
	if engine.Phase() != holdem.PhaseWaitingNextHand {
		return holdem.Player{}, false
	}
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
	// Members who joined mid-hand have no engine seat yet. Surface them as
	// pending spectators so every client can see the newcomer waiting for the
	// next hand.
	engineSeats := make(map[int]struct{}, len(players))
	engineUsers := make(map[string]struct{}, len(players))
	for _, player := range players {
		engineSeats[player.Seat] = struct{}{}
		engineUsers[player.PlayerID] = struct{}{}
	}
	for _, member := range roomValue.Members {
		if member.Spectating {
			continue
		}
		if _, seated := engineUsers[member.UserID]; seated {
			continue
		}
		if _, occupied := engineSeats[member.Seat]; occupied {
			continue
		}
		result.Seats = append(result.Seats, SeatSnapshot{
			UserID: member.UserID, DisplayName: member.DisplayName, Seat: member.Seat,
			Stack: member.Stack, Ready: member.Ready, Connected: true,
		})
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
	allInTarget := player.StreetBet + player.Stack
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
				// 能全下时，被压到上限的档位与全下额度相差不到一个小盲
				// （MaxRaiseTo 就是全下额度向下取整的结果），保留它只会出现
				// 「满池 260」和「全下 265」两个几乎同额、标签却完全不同的按钮。
				if options.CanAllIn && allInTarget-options.MaxRaiseTo < smallBlind {
					continue
				}
				// 不能全下时保留该档位，但标签必须跟着改：否则按钮写着
				// 「1/2 池」，实际却是玩家能加的最大额，与半个底池毫无关系。
				target = options.MaxRaiseTo
				label = "max_raise"
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
			Label: "all_in", Action: holdem.ActionAllIn, RaiseTo: allInTarget,
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

// allReady 只看上桌的玩家：观战者没有准备态，也不计入开局人数。
func allReady(members []room.Member) bool {
	seated := 0
	for _, member := range members {
		if member.Spectating {
			continue
		}
		seated++
		if !member.Ready {
			return false
		}
	}
	return seated >= 2
}

// KickMember 由房主把一名成员移出房间。
//
// 复用玩家自己离桌的同一条路径（Leave），而不是另写一套移除逻辑：那条路径
// 已经处理了弃牌者的延迟返还、房主转移、最后一人离开时关闭房间等状态，
// 另写一份必然会漏掉其中某几种。
//
// 只在手间允许踢人：牌局进行中把人踢走会牵扯底池归属与行动顺序，
// 是筹码不守恒的高风险来源。
func (manager *Manager) KickMember(ctx context.Context, ownerUserID, targetUserID string) (bool, error) {
	roomValue, err := manager.rooms.Current(ctx, ownerUserID)
	if err != nil {
		return false, err
	}
	if roomValue.OwnerUserID != ownerUserID {
		return false, room.Error{Code: "owner_required"}
	}
	if targetUserID == ownerUserID {
		return false, room.Error{Code: "cannot_remove_self"}
	}
	var found bool
	for _, member := range roomValue.Members {
		if member.UserID == targetUserID {
			found = true
		}
	}
	if !found {
		return false, room.Error{Code: "member_not_found"}
	}
	if runtime := manager.existingRuntime(roomValue.RoomID); runtime != nil {
		runtime.mu.Lock()
		phase := runtime.engine.Phase()
		runtime.mu.Unlock()
		if phase != holdem.PhaseWaiting && phase != holdem.PhaseWaitingNextHand {
			return false, room.Error{Code: "hand_in_progress"}
		}
	}
	return manager.Leave(ctx, targetUserID)
}

// annotateSettlement 给结算明细补上昵称，并把底池对齐到结算总额。
//
// 两处都因为同一件事出错：赢家常常赢完这手就立刻离开房间。此后房间成员表
// 和引擎座位里都没有他，于是结算文案退化成显示用户 ID，而底池（原本按在座
// 玩家的投入累加）也会小于实际金额。昵称改为在结算时固化并随快照下发；
// 底池在结算展示期间改用各池金额之和——发两次时每个池均分到各块牌面，
// 所有 award 之和仍然等于本手总底池。
func annotateSettlement(runtime *runtime, roomValue room.Room, result *Snapshot) {
	if result.Settlement == nil || result.Settlement.HandID == "" {
		return
	}
	if runtime.knownDisplayNames == nil {
		runtime.knownDisplayNames = make(map[string]string, len(roomValue.Members))
	}
	for _, member := range roomValue.Members {
		runtime.knownDisplayNames[member.UserID] = member.DisplayName
	}

	var settled int64
	awards := make([]holdem.PotAward, len(result.Settlement.PotAwards))
	for index, award := range result.Settlement.PotAwards {
		settled += award.Amount
		payouts := make([]holdem.Payout, len(award.Payouts))
		for payoutIndex, payout := range award.Payouts {
			if name := runtime.knownDisplayNames[payout.PlayerID]; name != "" {
				payout.DisplayName = name
			}
			payouts[payoutIndex] = payout
		}
		award.Payouts = payouts
		awards[index] = award
	}
	settlement := *result.Settlement
	settlement.PotAwards = awards
	result.Settlement = &settlement
	if settled > 0 {
		result.TotalPot = settled
	}
}

// SpectatorSnapshot 是观战位上的一名成员。
type SpectatorSnapshot struct {
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
	Connected   bool   `json:"connected"`
	Stack       int64  `json:"stack"`
	// CanSeeHoleCards 表示本手已付费（或免费模式），能看到所有人的手牌。
	CanSeeHoleCards bool `json:"canSeeHoleCards"`
	// PendingSeat 表示已申请本手结束后上桌。
	PendingSeat bool `json:"pendingSeat,omitempty"`
}

// SpectatorFeeShare 是看牌费的一笔收或付。
type SpectatorFeeShare struct {
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
	Amount      int64  `json:"amount"`
}

// SpectatorFeeSnapshot 是一手牌的看牌费明细：谁付了多少、每名上桌玩家分到多少。
type SpectatorFeeSnapshot struct {
	HandID          string              `json:"handId"`
	FeePerSpectator int64               `json:"feePerSpectator"`
	Payers          []SpectatorFeeShare `json:"payers"`
	Recipients      []SpectatorFeeShare `json:"recipients"`
}

func ensureSpectatorMaps(runtime *runtime) {
	if runtime.pendingSpectate == nil {
		runtime.pendingSpectate = make(map[string]bool)
	}
	if runtime.pendingSeat == nil {
		runtime.pendingSeat = make(map[string]bool)
	}
	if runtime.spectatorAccess == nil {
		runtime.spectatorAccess = make(map[string]bool)
	}
}

func isBetweenHands(engine *holdem.Table) bool {
	return engine.Phase() == holdem.PhaseWaiting || engine.Phase() == holdem.PhaseWaitingNextHand
}

// EnterSpectate 让一名上桌玩家进入观战位。
//
// 手间立即生效；牌局进行中且本人参与本手时只记录意向，手结束时统一应用——
// 桌上玩家不能在牌局中途走人，这与自己离桌是同一条规则。
func (manager *Manager) EnterSpectate(ctx context.Context, userID string, roomID string) (Snapshot, error) {
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
	ensureSpectatorMaps(runtime)
	for _, member := range roomValue.Members {
		if member.UserID == userID && member.Spectating {
			return snapshotForRuntime(runtime, roomValue, userID)
		}
	}
	participating := false
	for _, player := range runtime.engine.Players() {
		if player.PlayerID == userID {
			participating = player.Participating
			break
		}
	}
	if isBetweenHands(runtime.engine) || !participating {
		roomValue, err = manager.rooms.EnterSpectate(ctx, userID)
		if err != nil {
			return Snapshot{}, err
		}
		// 手间调用会直接释放引擎座位；不在引擎里的成员报 not_seated，可忽略。
		_ = runtime.engine.RequestLeave(userID)
		delete(runtime.pendingSeat, userID)
		if err := syncMembers(runtime.engine, roomValue, runtime.online); err != nil {
			return Snapshot{}, err
		}
		return snapshotForRuntime(runtime, roomValue, userID)
	}
	if len(roomValue.SpectatorMembers()) >= room.MaximumSpectators {
		return Snapshot{}, room.Error{Code: "spectators_full"}
	}
	runtime.pendingSpectate[userID] = true
	return snapshotForRuntime(runtime, roomValue, userID)
}

// TakeSeat 让观战者上桌。手间立即入座；牌局进行中只记录意向，手结束时应用。
// 座位满时立即拒绝，不排队等位。
func (manager *Manager) TakeSeat(ctx context.Context, userID string, roomID string) (Snapshot, error) {
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
	ensureSpectatorMaps(runtime)
	spectating := false
	for _, member := range roomValue.Members {
		if member.UserID == userID {
			spectating = member.Spectating
			break
		}
	}
	if !spectating {
		return snapshotForRuntime(runtime, roomValue, userID)
	}
	if len(roomValue.SeatedMembers()) >= roomValue.MaxPlayers {
		return Snapshot{}, room.Error{Code: "room_full"}
	}
	if isBetweenHands(runtime.engine) {
		roomValue, err = manager.rooms.TakeSeat(ctx, userID)
		if err != nil {
			return Snapshot{}, err
		}
		delete(runtime.pendingSpectate, userID)
		if err := syncMembers(runtime.engine, roomValue, runtime.online); err != nil {
			return Snapshot{}, err
		}
		return snapshotForRuntime(runtime, roomValue, userID)
	}
	runtime.pendingSeat[userID] = true
	return snapshotForRuntime(runtime, roomValue, userID)
}

// UpdateSpectatorSettings 由房主调整观战位设置，立即生效并随快照广播。
func (manager *Manager) UpdateSpectatorSettings(
	ctx context.Context,
	ownerUserID string,
	roomID string,
	settings room.SpectatorSettings,
) (Snapshot, error) {
	roomValue, err := manager.rooms.UpdateSpectatorSettings(ctx, ownerUserID, settings)
	if err != nil {
		return Snapshot{}, err
	}
	if roomValue.RoomID != roomID {
		return Snapshot{}, room.Error{Code: "permission_denied"}
	}
	runtime, err := manager.runtimeFor(roomValue)
	if err != nil {
		return Snapshot{}, err
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return snapshotForRuntime(runtime, roomValue, ownerUserID)
}

// applyPendingSeatChangesLocked 在手结束时应用切换意向：先让要观战的人离座
// （释放座位），再让要上桌的人入座。任何一条意向失败（已离开房间、观战位满、
// 座位满）都直接放弃，不排队。
func (manager *Manager) applyPendingSeatChangesLocked(
	ctx context.Context,
	runtime *runtime,
	roomValue room.Room,
) (room.Room, error) {
	ensureSpectatorMaps(runtime)
	if len(runtime.pendingSpectate) == 0 && len(runtime.pendingSeat) == 0 {
		return roomValue, nil
	}
	for userID := range runtime.pendingSpectate {
		delete(runtime.pendingSpectate, userID)
		updated, err := manager.rooms.EnterSpectate(ctx, userID)
		if err != nil {
			continue
		}
		roomValue = updated
		_ = runtime.engine.RequestLeave(userID)
	}
	for userID := range runtime.pendingSeat {
		delete(runtime.pendingSeat, userID)
		updated, err := manager.rooms.TakeSeat(ctx, userID)
		if err != nil {
			continue
		}
		roomValue = updated
	}
	if err := syncMembers(runtime.engine, roomValue, runtime.online); err != nil {
		return room.Room{}, err
	}
	return roomValue, nil
}

// collectSpectatorFeesLocked 在全员准备完成、发牌之前收取看牌费。
//
// 规则：费用 = 房主设置的大盲倍数 × 当前大盲；余额不足的观战者本手不收费
// 也不给看牌，而不是收走他剩下的全部筹码；费用为 0 时人人可看。收到的钱按
// 即将参与本手的上桌玩家（已准备且有筹码，与 StartHand 的判定一致）平分，
// 余数从上一手庄位左侧起顺时针逐枚分配——那正是本手庄位的起点，与底池余数
// 规则同向。
//
// 这是牌桌内部的筹码转移，总量不变。持久化走与结算相同的 ApplySettlement，
// 幂等键为「spectator_fee:after:上一手手号」：本手手号在 StartHand 之前还不
// 存在，而同一个开局若因持久化失败重试，必须复用同一个键才不会重复扣费。
// 先持久化再改引擎——持久化失败时引擎与账本都保持原样，只是本手没人看得到牌。
func (manager *Manager) collectSpectatorFeesLocked(
	ctx context.Context,
	runtime *runtime,
	roomValue room.Room,
) (room.Room, error) {
	ensureSpectatorMaps(runtime)
	runtime.spectatorAccess = make(map[string]bool)
	runtime.spectatorFees = nil
	spectators := roomValue.SpectatorMembers()
	if len(spectators) == 0 {
		return roomValue, nil
	}
	fee := int64(roomValue.Spectator.FeeBigBlinds) * roomValue.Rules.BigBlind
	if fee <= 0 {
		for _, spectator := range spectators {
			runtime.spectatorAccess[spectator.UserID] = true
		}
		return roomValue, nil
	}
	var payers []room.Member
	for _, spectator := range spectators {
		if spectator.Stack >= fee {
			payers = append(payers, spectator)
			runtime.spectatorAccess[spectator.UserID] = true
		}
	}
	if len(payers) == 0 {
		return roomValue, nil
	}
	var participants []holdem.Player
	for _, player := range runtime.engine.Players() {
		if player.Ready && player.Stack > 0 {
			participants = append(participants, player)
		}
	}
	if len(participants) == 0 {
		return roomValue, nil
	}
	sort.Slice(participants, func(left, right int) bool {
		return participants[left].Seat < participants[right].Seat
	})
	start := 0
	for index, player := range participants {
		if player.Seat > runtime.engine.DealerSeat() {
			start = index
			break
		}
	}
	ordered := append(participants[start:], participants[:start]...)

	memberStack := make(map[string]int64, len(roomValue.Members))
	displayName := make(map[string]string, len(roomValue.Members))
	for _, member := range roomValue.Members {
		memberStack[member.UserID] = member.Stack
		displayName[member.UserID] = member.DisplayName
	}
	total := fee * int64(len(payers))
	share := total / int64(len(ordered))
	remainder := total % int64(len(ordered))
	balances := make(map[string]int64, len(payers)+len(ordered))
	// HandID 在 StartHand 成功后由调用方补上。
	record := &SpectatorFeeSnapshot{FeePerSpectator: fee}
	feeKey := "spectator_fee:after:" + runtime.engine.HandID()
	for _, payer := range payers {
		balances[payer.UserID] = payer.Stack - fee
		record.Payers = append(record.Payers, SpectatorFeeShare{
			UserID: payer.UserID, DisplayName: payer.DisplayName, Amount: fee,
		})
	}
	for index, player := range ordered {
		amount := share
		if int64(index) < remainder {
			amount++
		}
		if amount == 0 {
			continue
		}
		balances[player.PlayerID] = memberStack[player.PlayerID] + amount
		record.Recipients = append(record.Recipients, SpectatorFeeShare{
			UserID: player.PlayerID, DisplayName: displayName[player.PlayerID], Amount: amount,
		})
	}
	if manager.bankroll != nil {
		if err := manager.bankroll.ApplySettlement(
			ctx, roomValue.RoomID, feeKey, balances, roomValue.Rules.MaxBuyIn,
		); err != nil {
			return roomValue, err
		}
	}
	for _, recipient := range record.Recipients {
		if err := runtime.engine.CreditChips(recipient.UserID, recipient.Amount); err != nil {
			return roomValue, err
		}
	}
	updated, err := manager.rooms.UpdateStacks(ctx, roomValue.RoomID, balances)
	if err != nil {
		return roomValue, err
	}
	runtime.spectatorFees = record
	return updated, nil
}

// annotateSpectators 给快照补上观战位信息，并按接收者裁剪手牌可见性：
// 只有本手有效付费（或免费模式）的观战者才拿到所有参与者的手牌。
func annotateSpectators(runtime *runtime, roomValue room.Room, recipientUserID string, result *Snapshot) {
	ensureSpectatorMaps(runtime)
	result.SpectatorSettings = roomValue.Spectator
	free := roomValue.Spectator.FeeBigBlinds <= 0
	recipientSpectating := false
	for _, member := range roomValue.SpectatorMembers() {
		if member.UserID == recipientUserID {
			recipientSpectating = true
		}
		result.Spectators = append(result.Spectators, SpectatorSnapshot{
			UserID: member.UserID, DisplayName: member.DisplayName,
			Connected: runtime.online[member.UserID], Stack: member.Stack,
			CanSeeHoleCards: free || runtime.spectatorAccess[member.UserID],
			PendingSeat:     runtime.pendingSeat[member.UserID],
		})
	}
	result.Spectating = recipientSpectating
	for index := range result.Seats {
		if runtime.pendingSpectate[result.Seats[index].UserID] {
			result.Seats[index].PendingSpectate = true
		}
	}
	// 每次开局收费时都会重置这份明细，因此它始终属于当前这一手（含其结算展示期）。
	result.SpectatorFees = runtime.spectatorFees
	if !recipientSpectating || !(free || runtime.spectatorAccess[recipientUserID]) {
		return
	}
	cards := make(map[string][]string)
	for _, player := range runtime.engine.Players() {
		if player.Participating && player.HoleCards[0].Rank != 0 && player.HoleCards[1].Rank != 0 {
			cards[player.PlayerID] = []string{player.HoleCards[0].String(), player.HoleCards[1].String()}
		}
	}
	for index := range result.Seats {
		if hole, ok := cards[result.Seats[index].UserID]; ok {
			result.Seats[index].HoleCards = hole
		}
	}
}
