package review

import (
	"hash/fnv"
	"math/rand"
	"sort"

	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/replay"
)

// Facts 是交给模型的全部局面数据。原则是「事实由服务端算，模型只负责解释与
// 判断」：底池、赔率、筹码深度、胜率这些数都在这里算好，模型不必也不该自己算。
//
// 这里不出现任何昵称、用户 ID、房间码；对手一律用位置称呼。没亮过的底牌永远
// 不在这里——输入的牌谱已经按请求者裁剪过，本来就拿不到。
type Facts struct {
	Game      GameFacts       `json:"game"`
	Hero      HeroFacts       `json:"hero"`
	Seats     []SeatFacts     `json:"seats"`
	Actions   []ActionFacts   `json:"actions"`
	Decisions []DecisionFacts `json:"decisions"`
	Opponents []OpponentFacts `json:"opponents"`
	Hindsight HindsightFacts  `json:"hindsight"`
}

type GameFacts struct {
	SmallBlind int64 `json:"smallBlind"`
	BigBlind   int64 `json:"bigBlind"`
	Players    int   `json:"players"`
}

type HeroFacts struct {
	Position      string   `json:"position"`
	HoleCards     []string `json:"holeCards"`
	StartingStack int64    `json:"startingStack"`
	StackInBB     float64  `json:"stackInBigBlinds"`
}

type SeatFacts struct {
	Position      string  `json:"position"`
	StartingStack int64   `json:"startingStack"`
	StackInBB     float64 `json:"stackInBigBlinds"`
	IsHero        bool    `json:"isHero,omitempty"`
}

// ActionFacts 是这一手所有人的公开动作，按发生顺序。
type ActionFacts struct {
	Step      int    `json:"step"`
	Street    string `json:"street"`
	Position  string `json:"position"`
	Action    string `json:"action"`
	Amount    int64  `json:"amount"`
	StreetBet int64  `json:"streetBetAfter"`
	PotAfter  int64  `json:"potAfter"`
	TimedOut  bool   `json:"timedOut,omitempty"`
	IsHero    bool   `json:"isHero,omitempty"`
}

// DecisionFacts 是本人一个决策点的局面，全部是决策当时就已知的信息。
type DecisionFacts struct {
	Step         int      `json:"step"`
	Street       string   `json:"street"`
	Board        []string `json:"board"`
	HandCategory string   `json:"madeHand,omitempty"`
	// BestFive 是本人此刻最好的五张牌；HoleCardsUsed 是其中来自本人底牌的那几张，
	// BoardPlays 表示公共牌本身就是这个牌力、底牌没用上。只给牌型名时模型会自己
	// 推断牌力，实测把 A2 在 KK553 上的两对说成「AA KK」。
	BestFive      []string `json:"bestFive,omitempty"`
	HoleCardsUsed []string `json:"holeCardsUsed,omitempty"`
	BoardPlays    bool     `json:"boardPlays,omitempty"`
	FlushDraw     bool     `json:"flushDraw,omitempty"`
	PotBefore     int64    `json:"potBefore"`
	// WinnablePot 是本人最多能赢到的那部分底池：本人筹码比对手的投入少时，超出
	// 本人能跟的部分会退回或进边池，本人赢不到。只在比 PotBefore 小时给出，底池
	// 赔率按它算——否则短码跟全下时赔率会被严重低估。
	WinnablePot      int64    `json:"winnablePot,omitempty"`
	ToCall           int64    `json:"toCall"`
	PotOdds          float64  `json:"potOddsPercent,omitempty"`
	EffectiveStack   int64    `json:"effectiveStack"`
	EffectiveStackBB float64  `json:"effectiveStackInBigBlinds"`
	SPR              float64  `json:"stackToPotRatio"`
	OpponentsInHand  []string `json:"opponentsInHand"`
	EquityVsRandom   float64  `json:"equityVsRandomHandsPercent"`
	Action           string   `json:"action"`
	Amount           int64    `json:"amount"`
	StreetBetAfter   int64    `json:"streetBetAfter"`
	TimedOut         bool     `json:"timedOut,omitempty"`
}

// OpponentFacts 是对手的打法倾向，只从请求者与他同桌打过的牌局统计，只看公开动作。
type OpponentFacts struct {
	Position   string  `json:"position"`
	HandsSeen  int     `json:"handsTogether"`
	VPIP       float64 `json:"vpipPercent"`
	PFR        float64 `json:"pfrPercent"`
	Aggression float64 `json:"postflopAggressionFactor"`
	// 激进度的原始次数：没有跟注时激进度只能写成下注次数，1 次下注与「攻守均衡」
	// 的 1.0 分不开，给出次数让模型自己看样本
	PostflopBetsRaises int     `json:"postflopBetsAndRaises"`
	PostflopCalls      int     `json:"postflopCalls"`
	WTSD               float64 `json:"wentToShowdownPercent"`
}

// HindsightFacts 是事后才知道的信息：只能用于「结果回顾」，不能拿来评价当时的决策。
type HindsightFacts struct {
	Showdown     bool            `json:"showdown"`
	FinalBoard   []string        `json:"finalBoard"`
	RunoutBoards [][]string      `json:"runoutBoards,omitempty"`
	Revealed     []RevealedFacts `json:"revealedAtShowdown"`
	Results      []ResultFacts   `json:"results"`
	Rake         int64           `json:"rake,omitempty"`
}

type RevealedFacts struct {
	Position  string   `json:"position"`
	HoleCards []string `json:"holeCards"`
	Category  string   `json:"category"`
	// BestFive 是按最终公共牌算出的最好五张；发两次时每块牌面不同，不给。
	BestFive []string `json:"bestFive,omitempty"`
}

type ResultFacts struct {
	Position string `json:"position"`
	Delta    int64  `json:"delta"`
}

// equitySamples 是蒙特卡洛的次数：两千次时胜率误差在两个百分点左右，够判断。
const equitySamples = 2000

// maximumEquityOpponents 限制参与模拟的对手数，多人底池时胜率本来就只作参考。
const maximumEquityOpponents = 5

// BuildFacts 从裁剪过的牌谱与回放时间轴整理出交给模型的数据。history 是请求者
// 近期打过的牌局（同样已裁剪），用来统计对手倾向。
func BuildFacts(hand history.Hand, timeline replay.Timeline, viewerID string, recent []history.Hand) Facts {
	position := make(map[string]string, len(timeline.Players))
	for _, player := range timeline.Players {
		position[player.UserID] = player.Position
	}
	bigBlind := float64(timeline.BigBlind)
	inBB := func(chips int64) float64 { return round1(float64(chips) / bigBlind) }

	facts := Facts{
		Game: GameFacts{SmallBlind: timeline.SmallBlind, BigBlind: timeline.BigBlind, Players: len(timeline.Players)},
		Hindsight: HindsightFacts{
			Showdown: timeline.Showdown, FinalBoard: append([]string{}, hand.Board...),
			RunoutBoards: hand.RunoutBoards, Rake: hand.Rake,
			Revealed: []RevealedFacts{}, Results: []ResultFacts{},
		},
		Actions: []ActionFacts{}, Decisions: []DecisionFacts{}, Opponents: []OpponentFacts{},
	}
	var heroCards []string
	for _, player := range hand.Players {
		if player.UserID == viewerID {
			heroCards = append([]string{}, player.HoleCards...)
		}
	}
	for _, player := range timeline.Players {
		facts.Seats = append(facts.Seats, SeatFacts{
			Position: player.Position, StartingStack: player.StartingStack,
			StackInBB: inBB(player.StartingStack), IsHero: player.UserID == viewerID,
		})
		facts.Hindsight.Results = append(facts.Hindsight.Results, ResultFacts{Position: player.Position, Delta: player.Delta})
		if player.UserID == viewerID {
			facts.Hero = HeroFacts{
				Position: player.Position, HoleCards: heroCards,
				StartingStack: player.StartingStack, StackInBB: inBB(player.StartingStack),
			}
		}
	}
	for _, shown := range hand.RevealedHands {
		revealed := RevealedFacts{
			Position: position[shown.PlayerID], HoleCards: shown.HoleCards, Category: shown.Category,
		}
		if len(hand.RunoutBoards) <= 1 && len(hand.Board) == 5 {
			if best, ok := bestHand(parseCards(shown.HoleCards), parseCards(hand.Board)); ok {
				revealed.BestFive = best.five
			}
		}
		facts.Hindsight.Revealed = append(facts.Hindsight.Revealed, revealed)
	}

	for index, step := range timeline.Steps {
		if step.Kind != replay.KindAction {
			continue
		}
		actor := seatIn(step, step.ActorID)
		facts.Actions = append(facts.Actions, ActionFacts{
			Step: step.Index, Street: step.Street, Position: position[step.ActorID], Action: step.Action,
			Amount: step.Amount, StreetBet: actor.StreetBet, PotAfter: step.Pot, TimedOut: step.TimedOut,
			IsHero: step.ActorID == viewerID,
		})
		if step.ActorID != viewerID || index == 0 {
			continue
		}
		facts.Decisions = append(facts.Decisions, decisionAt(timeline, index, viewerID, heroCards, position, inBB))
	}

	facts.Opponents = tendencies(viewerID, timeline, recent)
	return facts
}

func decisionAt(
	timeline replay.Timeline, index int, viewerID string, heroCards []string,
	position map[string]string, inBB func(int64) float64,
) DecisionFacts {
	before := timeline.Steps[index-1]
	step := timeline.Steps[index]
	hero := seatIn(before, viewerID)
	var highest, largestOpponent, largestOpponentBehind int64
	// 翻牌前的待跟注额至少是一个完整大盲：大盲筹码不足全下时，引擎仍按完整
	// 大盲要求别人跟注
	if step.Street == "preflop" {
		highest = timeline.BigBlind
	}
	var opponents []string
	for _, seat := range before.Seats {
		if seat.StreetBet > highest {
			highest = seat.StreetBet
		}
		if seat.UserID == viewerID || seat.Folded {
			continue
		}
		opponents = append(opponents, position[seat.UserID])
		// 对手这一手还能投入的上限：剩余筹码加上本街已经投的
		if reach := seat.Stack + seat.StreetBet; reach > largestOpponent {
			largestOpponent = reach
		}
		if seat.Stack > largestOpponentBehind {
			largestOpponentBehind = seat.Stack
		}
	}
	toCall := highest - hero.StreetBet
	if toCall > hero.Stack {
		toCall = hero.Stack
	}
	effective := hero.Stack + hero.StreetBet
	if largestOpponent < effective {
		effective = largestOpponent
	}
	decision := DecisionFacts{
		Step: step.Index, Street: step.Street, Board: append([]string{}, before.Board...),
		PotBefore: before.Pot, ToCall: toCall, EffectiveStack: effective, EffectiveStackBB: inBB(effective),
		OpponentsInHand: opponents, Action: step.Action, Amount: step.Amount,
		StreetBetAfter: seatIn(step, viewerID).StreetBet, TimedOut: step.TimedOut,
	}
	heroMax := hero.TotalBet + hero.Stack
	var winnable, contributed int64
	for _, seat := range before.Seats {
		winnable += min(seat.TotalBet, heroMax)
		contributed += seat.TotalBet
	}
	// 各座位投入对不上底池时（不该发生）宁可按整个底池算，不给出错的数
	if contributed == before.Pot && winnable < before.Pot {
		decision.WinnablePot = winnable
	} else {
		winnable = before.Pot
	}
	if toCall > 0 {
		decision.PotOdds = round1(float64(toCall) * 100 / float64(winnable+toCall))
	}
	// SPR 用双方还没投进底池的筹码：本街已经投的算在底池里，不能两边都算
	if behind := min(hero.Stack, largestOpponentBehind); before.Pot > 0 {
		decision.SPR = round1(float64(behind) / float64(before.Pot))
	}
	hole, board := parseCards(heroCards), parseCards(before.Board)
	if len(hole) == 2 && len(board) >= 3 {
		if best, ok := bestHand(hole, board); ok {
			decision.HandCategory = best.value.Category.String()
			decision.BestFive, decision.HoleCardsUsed = best.five, best.holeUsed
			decision.BoardPlays = len(best.holeUsed) == 0
			decision.FlushDraw = len(board) < 5 && best.value.Category < holdem.Flush && hasFourToAFlush(hole, board)
		}
	}
	if len(hole) == 2 && len(opponents) > 0 {
		decision.EquityVsRandom = equity(hole, board, len(opponents), timeline.HandID, step.Index)
	}
	return decision
}

// attachFacts 把服务端算好的局面与每一步的精确数字挂到模型的点评上，客户端
// 与模型的判断一起显示；这些数字不经过模型。
func attachFacts(result *Result, facts Facts) {
	result.Situation = &Situation{
		HeroPosition: facts.Hero.Position, HoleCards: facts.Hero.HoleCards, Players: facts.Game.Players,
		SmallBlind: facts.Game.SmallBlind, BigBlind: facts.Game.BigBlind, Seats: facts.Seats,
	}
	byStep := make(map[int]DecisionFacts, len(facts.Decisions))
	for _, decision := range facts.Decisions {
		byStep[decision.Step] = decision
	}
	for index := range result.Decisions {
		if decision, ok := byStep[result.Decisions[index].Step]; ok {
			result.Decisions[index].Facts = &decision
		}
	}
}

type madeHand struct {
	value    holdem.HandValue
	five     []string
	holeUsed []string
}

// bestHand 在底牌加公共牌里找最好的五张。同样大的组合里取用底牌最少的那个：
// 公共牌本身就是这个牌力时，底牌其实没起作用。
func bestHand(hole, board []holdem.Card) (madeHand, bool) {
	cards := append(append([]holdem.Card{}, hole...), board...)
	if len(hole) != 2 || len(cards) < 5 || len(cards) > 7 {
		return madeHand{}, false
	}
	var best madeHand
	bestHoles, found := 0, false
	choose := make([]int, 5)
	var walk func(start, depth int)
	walk = func(start, depth int) {
		if depth == 5 {
			five := make([]holdem.Card, 5)
			holes := 0
			for index, position := range choose {
				five[index] = cards[position]
				if position < 2 {
					holes++
				}
			}
			value, err := holdem.Evaluate(five)
			if err != nil {
				return
			}
			compared := value.Compare(best.value)
			if !found || compared > 0 || (compared == 0 && holes < bestHoles) {
				best.value, bestHoles, found = value, holes, true
				best.five, best.holeUsed = nil, nil
				for index, position := range choose {
					best.five = append(best.five, five[index].String())
					if position < 2 {
						best.holeUsed = append(best.holeUsed, cards[position].String())
					}
				}
			}
			return
		}
		for position := start; position <= len(cards)-(5-depth); position++ {
			choose[depth] = position
			walk(position+1, depth+1)
		}
	}
	walk(0, 0)
	if found {
		best.five = sortByRank(best.five, best.value)
	}
	return best, found
}

// sortByRank 按点数从大到小排好最好的五张，A 当 1 用的顺子（A2345）把 A 放到
// 最后：交给模型的牌按牌力的读法排，不容易被误读。
func sortByRank(five []string, value holdem.HandValue) []string {
	cards := parseCards(five)
	if len(cards) != len(five) {
		return five
	}
	wheel := (value.Category == holdem.Straight || value.Category == holdem.StraightFlush) &&
		value.Tiebreak[0] == holdem.Five
	rank := func(card holdem.Card) int {
		if wheel && card.Rank == holdem.Ace {
			return 1
		}
		return int(card.Rank)
	}
	sort.SliceStable(cards, func(left, right int) bool { return rank(cards[left]) > rank(cards[right]) })
	sorted := make([]string, len(cards))
	for index, card := range cards {
		sorted[index] = card.String()
	}
	return sorted
}

func seatIn(step replay.Step, userID string) replay.SeatState {
	for _, seat := range step.Seats {
		if seat.UserID == userID {
			return seat
		}
	}
	return replay.SeatState{}
}

func parseCards(values []string) []holdem.Card {
	cards := make([]holdem.Card, 0, len(values))
	for _, value := range values {
		card, err := holdem.ParseCard(value)
		if err != nil {
			return nil
		}
		cards = append(cards, card)
	}
	return cards
}

func hasFourToAFlush(hole, board []holdem.Card) bool {
	counts := make(map[holdem.Suit]int)
	for _, card := range append(append([]holdem.Card{}, hole...), board...) {
		counts[card.Suit]++
	}
	for _, card := range hole {
		if counts[card.Suit] == 4 {
			return true
		}
	}
	return false
}

// equity 用蒙特卡洛估算本人对若干手随机底牌的胜率（平分按份额计）。随机数种子
// 由手号与步号决定：同一手每次算出来都一样，复盘结果可以缓存、可以复现。
func equity(hole, board []holdem.Card, opponents int, handID string, step int) float64 {
	if opponents > maximumEquityOpponents {
		opponents = maximumEquityOpponents
	}
	used := make(map[holdem.Card]bool, 7)
	for _, card := range append(append([]holdem.Card{}, hole...), board...) {
		used[card] = true
	}
	deck := make([]holdem.Card, 0, 52)
	for rank := holdem.Two; rank <= holdem.Ace; rank++ {
		for suit := holdem.Clubs; suit <= holdem.Spades; suit++ {
			card := holdem.Card{Rank: rank, Suit: suit}
			if !used[card] {
				deck = append(deck, card)
			}
		}
	}
	seed := fnv.New64a()
	_, _ = seed.Write([]byte(handID))
	source := rand.New(rand.NewSource(int64(seed.Sum64()) + int64(step)))
	need := 5 - len(board)
	var share float64
	for sample := 0; sample < equitySamples; sample++ {
		// 只洗需要用到的那几张
		for index := 0; index < need+2*opponents; index++ {
			swap := index + source.Intn(len(deck)-index)
			deck[index], deck[swap] = deck[swap], deck[index]
		}
		fullBoard := append(append([]holdem.Card{}, board...), deck[:need]...)
		mine, _ := holdem.Evaluate(append(append([]holdem.Card{}, hole...), fullBoard...))
		best, winners := 1, 1
		for opponent := 0; opponent < opponents; opponent++ {
			cards := deck[need+2*opponent : need+2*opponent+2]
			theirs, _ := holdem.Evaluate(append(append([]holdem.Card{}, cards...), fullBoard...))
			switch compared := mine.Compare(theirs); {
			case compared < 0:
				best = 0
			case compared == 0:
				winners++
			}
			if best == 0 {
				break
			}
		}
		if best == 1 {
			share += 1 / float64(winners)
		}
	}
	return round1(share * 100 / equitySamples)
}

// tendencies 从请求者近期打过的牌局里，统计这一手每个对手的公开打法。只数两人
// 同时在场的牌局；入池与加注看翻牌前，激进度看翻牌后，摊牌率看看到翻牌的那些手。
func tendencies(viewerID string, timeline replay.Timeline, recent []history.Hand) []OpponentFacts {
	result := []OpponentFacts{}
	for _, player := range timeline.Players {
		if player.UserID == viewerID {
			continue
		}
		var hands, vpip, pfr, aggressive, calls, sawFlop, showdowns int
		for _, hand := range recent {
			if !inHand(hand, viewerID) || !inHand(hand, player.UserID) {
				continue
			}
			hands++
			voluntary, raised, folded, foldedPreflop := false, false, false, false
			for _, action := range classifyAllIns(hand) {
				if action.UserID != player.UserID {
					continue
				}
				switch action.Type {
				case "fold":
					folded = true
					if action.Street == "preflop" {
						foldedPreflop = true
					}
				case "call":
					if action.Street == "preflop" {
						voluntary = true
					} else {
						calls++
					}
				case "bet", "raise", "all_in":
					if action.Street == "preflop" {
						voluntary, raised = true, true
					} else {
						aggressive++
					}
				}
			}
			if voluntary {
				vpip++
			}
			if raised {
				pfr++
			}
			if !foldedPreflop && len(hand.Board) >= 3 {
				sawFlop++
				if hand.Showdown && !folded {
					showdowns++
				}
			}
		}
		opponent := OpponentFacts{
			Position: player.Position, HandsSeen: hands, PostflopBetsRaises: aggressive, PostflopCalls: calls,
		}
		if hands > 0 {
			opponent.VPIP = round1(float64(vpip) * 100 / float64(hands))
			opponent.PFR = round1(float64(pfr) * 100 / float64(hands))
		}
		if calls > 0 {
			opponent.Aggression = round1(float64(aggressive) / float64(calls))
		} else if aggressive > 0 {
			opponent.Aggression = float64(aggressive)
		}
		if sawFlop > 0 {
			opponent.WTSD = round1(float64(showdowns) * 100 / float64(sawFlop))
		}
		result = append(result, opponent)
	}
	sort.SliceStable(result, func(left, right int) bool { return result[left].Position < result[right].Position })
	return result
}

// classifyAllIns 把全下按它实际的作用改写成加注或跟注：没超过当前最高投入的
// 全下只是跟不足额，算成加注会把短码玩家的翻前加注率与激进度抬高。盲注不是
// 动作，按盲注座位补上（旧牌谱没记盲注座位，按与回放相同的规则从庄位推出）。
func classifyAllIns(hand history.Hand) []history.Action {
	result := make([]history.Action, len(hand.Actions))
	street := ""
	var committed map[string]int64
	var highest int64
	for index, action := range hand.Actions {
		if action.Street != street {
			street, committed, highest = action.Street, map[string]int64{}, 0
			if street == "preflop" {
				highest = hand.BigBlind
				smallBlindSeat, bigBlindSeat := replay.BlindSeats(hand)
				for _, player := range hand.Players {
					switch player.Seat {
					case smallBlindSeat:
						committed[player.UserID] += hand.SmallBlind
					case bigBlindSeat:
						committed[player.UserID] += hand.BigBlind
					}
				}
			}
		}
		committed[action.UserID] += action.Committed
		if action.Type == "all_in" {
			if committed[action.UserID] > highest {
				action.Type = "raise"
			} else {
				action.Type = "call"
			}
		}
		if committed[action.UserID] > highest {
			highest = committed[action.UserID]
		}
		result[index] = action
	}
	return result
}

func inHand(hand history.Hand, userID string) bool {
	for _, player := range hand.Players {
		if player.UserID == userID {
			return true
		}
	}
	return false
}

func round1(value float64) float64 {
	if value < 0 {
		return -round1(-value)
	}
	return float64(int64(value*10+0.5)) / 10
}
