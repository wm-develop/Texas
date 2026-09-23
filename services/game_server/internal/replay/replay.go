// Package replay 把一条已结算的牌谱还原成可以逐步播放的时间轴。
//
// 输入必须是**已经按请求者裁剪过**的牌谱（history.Store 的 PageForPlayer /
// HandForPlayer 返回的那种）：生成器从头到尾拿不到别人没亮过的底牌，也就
// 不可能把它们放进时间轴。
//
// 牌谱里存的是动作序列与最终结果，不是每一步的状态；每一步的筹码、底池、
// 本街投入都由这里按规则推出来。推完之后用牌谱记录的结束筹码逐人核对，
// 对不上就拒绝生成——宁可不给看，也不给看一份算错的录像。
package replay

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/history"
)

// ErrUnavailable 表示这手牌无法还原成可信的时间轴（数据缺失或推出来的结果
// 与牌谱记录的结果对不上）。
var ErrUnavailable = errors.New("replay unavailable")

// 步骤类型。
const (
	KindBlinds   = "blinds"   // 发牌、下盲注：第一帧
	KindAction   = "action"   // 某位玩家的一次行动
	KindStreet   = "street"   // 发出翻牌、转牌或河牌
	KindRunout   = "runout"   // 全下后发两次，两块牌面同时出现
	KindRefund   = "refund"   // 没人跟注的部分退回
	KindShowdown = "showdown" // 摊牌亮牌
	KindSettle   = "settle"   // 分池结算
)

type Timeline struct {
	HandID         string                `json:"handId"`
	RoomCode       string                `json:"roomCode"`
	StartedAt      time.Time             `json:"startedAt"`
	EndedAt        time.Time             `json:"endedAt"`
	SmallBlind     int64                 `json:"smallBlind"`
	BigBlind       int64                 `json:"bigBlind"`
	DealerSeat     int                   `json:"dealerSeat"`
	SmallBlindSeat int                   `json:"smallBlindSeat"`
	BigBlindSeat   int                   `json:"bigBlindSeat"`
	Showdown       bool                  `json:"showdown"`
	Rake           int64                 `json:"rake,omitempty"`
	Players        []Player              `json:"players"`
	Steps          []Step                `json:"steps"`
	PotAwards      []holdem.PotAward     `json:"potAwards"`
	RunoutBoards   [][]string            `json:"runoutBoards,omitempty"`
	RevealedHands  []holdem.RevealedHand `json:"revealedHands"`
}

type Player struct {
	UserID        string `json:"userId"`
	DisplayName   string `json:"displayName"`
	Seat          int    `json:"seat"`
	Position      string `json:"position"`
	StartingStack int64  `json:"startingStack"`
	EndingStack   int64  `json:"endingStack"`
	Delta         int64  `json:"delta"`
	IsViewer      bool   `json:"isViewer,omitempty"`
}

// Step 是时间轴上的一帧，带着这一刻完整的桌面状态，客户端前进后退只是换一帧，
// 不需要自己累加。
type Step struct {
	Index    int    `json:"index"`
	Kind     string `json:"kind"`
	Street   string `json:"street"`
	ActorID  string `json:"actorId,omitempty"`
	Action   string `json:"action,omitempty"`
	Amount   int64  `json:"amount,omitempty"`
	TimedOut bool   `json:"timedOut,omitempty"`
	// Board 是此刻已经发出的公共牌；发两次时见 RunoutBoards。
	Board        []string    `json:"board"`
	RunoutBoards [][]string  `json:"runoutBoards,omitempty"`
	Pot          int64       `json:"pot"`
	Seats        []SeatState `json:"seats"`
	// Awards 只在结算那一帧出现。
	Awards []holdem.PotAward `json:"awards,omitempty"`
}

type SeatState struct {
	UserID     string `json:"userId"`
	Stack      int64  `json:"stack"`
	StreetBet  int64  `json:"streetBet"`
	TotalBet   int64  `json:"totalBet"`
	Folded     bool   `json:"folded,omitempty"`
	AllIn      bool   `json:"allIn,omitempty"`
	LastAction string `json:"lastAction,omitempty"`
	// HoleCards 只在这一帧对请求者可见时出现：本人的牌每一帧都有，
	// 别人的牌只有摊牌亮出之后才有。
	HoleCards []string `json:"holeCards,omitempty"`
}

var streetOrder = []string{"preflop", "flop", "turn", "river"}

// boardCardsBy 是每条街结束时公共牌应有的张数。
var boardCardsBy = map[string]int{"preflop": 0, "flop": 3, "turn": 4, "river": 5}

type seat struct {
	player     history.PlayerResult
	stack      int64
	streetBet  int64
	totalBet   int64
	folded     bool
	allIn      bool
	lastAction string
}

type builder struct {
	hand     history.Hand
	viewerID string
	seats    []*seat
	byID     map[string]*seat
	board    []string
	street   string
	steps    []Step
	// showCards 为真后，摊牌亮出的底牌开始出现在每一帧里。
	showCards bool
	revealed  map[string][]string
	runouts   [][]string
}

// Build 把按 viewerID 裁剪过的牌谱还原成时间轴。
func Build(hand history.Hand, viewerID string) (Timeline, error) {
	if len(hand.Players) < 2 || hand.BigBlind <= 0 || hand.SmallBlind <= 0 {
		return Timeline{}, fmt.Errorf("%w: missing players or blinds", ErrUnavailable)
	}
	b := &builder{
		hand: hand, viewerID: viewerID,
		byID: make(map[string]*seat, len(hand.Players)), street: "preflop",
		revealed: make(map[string][]string, len(hand.RevealedHands)),
	}
	players := append([]history.PlayerResult(nil), hand.Players...)
	sort.Slice(players, func(left, right int) bool { return players[left].Seat < players[right].Seat })
	for _, player := range players {
		if player.Seat <= 0 || player.StartingStack < 0 {
			return Timeline{}, fmt.Errorf("%w: invalid player", ErrUnavailable)
		}
		if _, duplicate := b.byID[player.UserID]; duplicate {
			return Timeline{}, fmt.Errorf("%w: duplicate player", ErrUnavailable)
		}
		value := &seat{player: player, stack: player.StartingStack}
		b.seats = append(b.seats, value)
		b.byID[player.UserID] = value
	}
	for _, shown := range hand.RevealedHands {
		b.revealed[shown.PlayerID] = shown.HoleCards
	}
	smallBlindSeat, bigBlindSeat := blindSeats(hand, b.seats)
	if b.seatNumbered(smallBlindSeat) == nil || b.seatNumbered(bigBlindSeat) == nil {
		return Timeline{}, fmt.Errorf("%w: blind seats not found", ErrUnavailable)
	}

	if err := b.play(smallBlindSeat, bigBlindSeat); err != nil {
		return Timeline{}, err
	}

	result := Timeline{
		HandID: hand.HandID, RoomCode: hand.RoomCode, StartedAt: hand.StartedAt, EndedAt: hand.EndedAt,
		SmallBlind: hand.SmallBlind, BigBlind: hand.BigBlind, DealerSeat: hand.DealerSeat,
		SmallBlindSeat: smallBlindSeat, BigBlindSeat: bigBlindSeat, Showdown: hand.Showdown, Rake: hand.Rake,
		Steps: b.steps, PotAwards: hand.PotAwards, RunoutBoards: hand.RunoutBoards,
		RevealedHands: hand.RevealedHands,
	}
	if result.PotAwards == nil {
		result.PotAwards = []holdem.PotAward{}
	}
	if result.RevealedHands == nil {
		result.RevealedHands = []holdem.RevealedHand{}
	}
	positions := positionsFor(b.seats, hand.DealerSeat, smallBlindSeat, bigBlindSeat)
	for _, value := range b.seats {
		result.Players = append(result.Players, Player{
			UserID: value.player.UserID, DisplayName: value.player.DisplayName, Seat: value.player.Seat,
			Position: positions[value.player.Seat], StartingStack: value.player.StartingStack,
			EndingStack: value.player.EndingStack, Delta: value.player.Delta,
			IsViewer: value.player.UserID == viewerID,
		})
	}
	return result, nil
}

func (b *builder) play(smallBlindSeat, bigBlindSeat int) error {
	// 盲注：筹码不够时全下不足额，与引擎的 postBlind 一致。
	b.post(b.seatNumbered(smallBlindSeat), b.hand.SmallBlind, "small_blind")
	b.post(b.seatNumbered(bigBlindSeat), b.hand.BigBlind, "big_blind")
	b.record(Step{Kind: KindBlinds})

	actions := append([]history.Action(nil), b.hand.Actions...)
	sort.Slice(actions, func(left, right int) bool { return actions[left].Sequence < actions[right].Sequence })
	for _, action := range actions {
		if err := b.advanceTo(action.Street); err != nil {
			return err
		}
		actor := b.byID[action.UserID]
		if actor == nil {
			return fmt.Errorf("%w: action by a player not in the hand", ErrUnavailable)
		}
		if action.Committed < 0 || action.Committed > actor.stack || actor.folded {
			return fmt.Errorf("%w: action %d cannot be applied", ErrUnavailable, action.Sequence)
		}
		actor.stack -= action.Committed
		actor.streetBet += action.Committed
		actor.totalBet += action.Committed
		actor.lastAction = action.Type
		switch {
		case action.Type == string(holdem.ActionFold):
			actor.folded = true
		case action.Type == string(holdem.ActionAllIn) || (action.Committed > 0 && actor.stack == 0):
			actor.allIn = true
		}
		b.record(Step{
			Kind: KindAction, ActorID: action.UserID, Action: action.Type,
			Amount: action.Committed, TimedOut: action.TimedOut,
		})
	}

	// 行动结束后仍在局里的人不止一个，剩下的公共牌是自动发完的（全下后补发）。
	if b.remaining() > 1 {
		if len(b.hand.RunoutBoards) > 1 {
			b.runouts = b.hand.RunoutBoards
			b.street = "river"
			b.clearStreet()
			b.record(Step{Kind: KindRunout})
		} else {
			for _, street := range streetOrder {
				if boardCardsBy[street] <= len(b.board) || boardCardsBy[street] > len(b.hand.Board) {
					continue
				}
				if err := b.advanceTo(street); err != nil {
					return err
				}
			}
		}
	}
	if b.runouts == nil && len(b.board) != len(b.hand.Board) {
		return fmt.Errorf("%w: board does not match the actions", ErrUnavailable)
	}

	if err := b.refund(); err != nil {
		return err
	}
	if b.hand.Showdown {
		b.showCards = true
		b.clearStreet()
		b.record(Step{Kind: KindShowdown, Street: "showdown"})
	}
	return b.settle()
}

// advanceTo 依次发出从当前街到 target 之间的每一条街；中间没人行动的街
// （例如有人全下后）同样要有一帧，录像才连贯。
func (b *builder) advanceTo(target string) error {
	if _, known := boardCardsBy[target]; !known {
		return fmt.Errorf("%w: unknown street %q", ErrUnavailable, target)
	}
	for indexOf(b.street) < indexOf(target) {
		next := streetOrder[indexOf(b.street)+1]
		count := boardCardsBy[next]
		if count > len(b.hand.Board) {
			return fmt.Errorf("%w: board is missing %s cards", ErrUnavailable, next)
		}
		b.street = next
		b.board = append([]string(nil), b.hand.Board[:count]...)
		b.clearStreet()
		b.record(Step{Kind: KindStreet})
	}
	if indexOf(target) < indexOf(b.street) {
		return fmt.Errorf("%w: actions go back to an earlier street", ErrUnavailable)
	}
	return nil
}

// refund 退回没人跟注的那一截，与引擎结算时用的是同一个分池函数。
func (b *builder) refund() error {
	contributions := make([]holdem.Contribution, 0, len(b.seats))
	for _, value := range b.seats {
		contributions = append(contributions, holdem.Contribution{
			PlayerID: value.player.UserID, Seat: value.player.Seat,
			Amount: value.totalBet, Folded: value.folded,
		})
	}
	pots, err := holdem.BuildPots(contributions)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	for _, value := range b.seats {
		amount := pots.Refunds[value.player.UserID]
		if amount <= 0 {
			continue
		}
		value.stack += amount
		value.totalBet -= amount
		value.streetBet -= amount
		if value.streetBet < 0 {
			value.streetBet = 0
		}
		if value.stack > 0 {
			value.allIn = false
		}
		b.record(Step{Kind: KindRefund, ActorID: value.player.UserID, Amount: amount})
	}
	return nil
}

// settle 按牌谱记录的分池结果把底池发给赢家，然后逐人核对结束筹码。
func (b *builder) settle() error {
	for _, award := range b.hand.PotAwards {
		for _, payout := range award.Payouts {
			winner := b.byID[payout.PlayerID]
			if winner == nil || payout.Amount < 0 {
				return fmt.Errorf("%w: award to a player not in the hand", ErrUnavailable)
			}
			winner.stack += payout.Amount
		}
	}
	for _, value := range b.seats {
		value.streetBet = 0
		value.totalBet = 0
	}
	b.record(Step{Kind: KindSettle, Street: "showdown", Awards: b.hand.PotAwards})
	for _, value := range b.seats {
		if value.stack != value.player.EndingStack {
			return fmt.Errorf("%w: seat %d ends with %d, the record says %d",
				ErrUnavailable, value.player.Seat, value.stack, value.player.EndingStack)
		}
	}
	return nil
}

func (b *builder) post(target *seat, amount int64, label string) {
	if amount > target.stack {
		amount = target.stack
	}
	target.lastAction = label
	target.stack -= amount
	target.streetBet += amount
	target.totalBet += amount
	if target.stack == 0 && amount > 0 {
		target.allIn = true
	}
}

// clearStreet 与引擎换街时一致：本街投入归零、上一个动作清空。弃牌的人保留
// 「弃牌」，否则录像里看不出他已经出局；全下的人座位上另有全下标识。
func (b *builder) clearStreet() {
	for _, value := range b.seats {
		value.streetBet = 0
		if !value.folded {
			value.lastAction = ""
		}
	}
}

func (b *builder) remaining() int {
	count := 0
	for _, value := range b.seats {
		if !value.folded {
			count++
		}
	}
	return count
}

func (b *builder) record(step Step) {
	step.Index = len(b.steps)
	if step.Street == "" {
		step.Street = b.street
	}
	step.Board = append([]string{}, b.board...)
	if b.runouts != nil {
		step.RunoutBoards = b.runouts
		step.Board = append([]string{}, b.runouts[0]...)
	}
	for _, value := range b.seats {
		step.Pot += value.totalBet
		state := SeatState{
			UserID: value.player.UserID, Stack: value.stack, StreetBet: value.streetBet,
			TotalBet: value.totalBet, Folded: value.folded, AllIn: value.allIn, LastAction: value.lastAction,
		}
		switch {
		case value.player.UserID == b.viewerID:
			state.HoleCards = append([]string(nil), value.player.HoleCards...)
		case b.showCards:
			if cards, shown := b.revealed[value.player.UserID]; shown {
				state.HoleCards = append([]string(nil), cards...)
			}
		}
		step.Seats = append(step.Seats, state)
	}
	b.steps = append(b.steps, step)
}

func (b *builder) seatNumbered(number int) *seat {
	for _, value := range b.seats {
		if value.player.Seat == number {
			return value
		}
	}
	return nil
}

func indexOf(street string) int {
	for index, value := range streetOrder {
		if value == street {
			return index
		}
	}
	return -1
}

// blindSeats 优先用牌谱记录的盲注座位；0.8.0 之前的牌谱没记，按引擎 StartHand
// 的规则从庄位推出：两人时庄位下小盲，否则庄位之后依次是小盲、大盲。
func blindSeats(hand history.Hand, seats []*seat) (int, int) {
	numbers := make([]int, 0, len(seats))
	for _, value := range seats {
		numbers = append(numbers, value.player.Seat)
	}
	return inferBlindSeats(hand, numbers)
}

// BlindSeats 给别的包用：统计对手倾向时要知道谁下了盲注，旧牌谱同样按庄位推出。
func BlindSeats(hand history.Hand) (int, int) {
	numbers := make([]int, 0, len(hand.Players))
	for _, player := range hand.Players {
		numbers = append(numbers, player.Seat)
	}
	sort.Ints(numbers)
	return inferBlindSeats(hand, numbers)
}

func inferBlindSeats(hand history.Hand, numbers []int) (int, int) {
	if hand.SmallBlindSeat > 0 && hand.BigBlindSeat > 0 {
		return hand.SmallBlindSeat, hand.BigBlindSeat
	}
	if len(numbers) == 0 {
		return 0, 0
	}
	if len(numbers) == 2 {
		return hand.DealerSeat, nextSeat(hand.DealerSeat, numbers)
	}
	small := nextSeat(hand.DealerSeat, numbers)
	return small, nextSeat(small, numbers)
}

// nextSeat 与引擎的 nextSeatIn 一致：顺时针下一个有人的座位，绕回最小座号。
func nextSeat(from int, ascending []int) int {
	for _, number := range ascending {
		if number > from {
			return number
		}
	}
	return ascending[0]
}

// positionsFor 给每个座位标上常用的位置名。从小盲起顺时针：SB、BB，然后
// UTG、UTG+1……，最后三个依次是 HJ、CO、BTN；两人时庄位就是小盲。
func positionsFor(seats []*seat, dealerSeat, smallBlindSeat, bigBlindSeat int) map[int]string {
	numbers := make([]int, 0, len(seats))
	for _, value := range seats {
		numbers = append(numbers, value.player.Seat)
	}
	result := make(map[int]string, len(numbers))
	if len(numbers) == 2 {
		result[smallBlindSeat] = "BTN"
		result[bigBlindSeat] = "BB"
		return result
	}
	order := []int{smallBlindSeat, bigBlindSeat}
	for current := bigBlindSeat; len(order) < len(numbers); {
		current = nextSeat(current, numbers)
		order = append(order, current)
	}
	middle := order[2:]
	names := make([]string, len(middle))
	for index := range middle {
		fromEnd := len(middle) - 1 - index
		switch {
		case middle[index] == dealerSeat:
			names[index] = "BTN"
		case fromEnd == 1:
			names[index] = "CO"
		case fromEnd == 2:
			names[index] = "HJ"
		case index == 0:
			names[index] = "UTG"
		default:
			names[index] = fmt.Sprintf("UTG+%d", index)
		}
	}
	result[smallBlindSeat] = "SB"
	result[bigBlindSeat] = "BB"
	for index, number := range middle {
		result[number] = names[index]
	}
	return result
}
