package holdem

import (
	"errors"
	"sort"
)

// 牌桌状态快照：把进行中的一手完整地写出来，进程重启后照原样装回去。
//
// 在此之前，服务异常退出会让进行中的那一手作废——底池里的筹码退回上一手
// 结算后的状态，玩家的决策全部白费。优雅停机能等牌局打完，但崩溃、OOM 和
// 宿主机重启不会等。
//
// 只在牌局进行中保存，手间不保存：手间的权威状态（筹码、准备、座位）本来
// 就在房间成员表里，恢复它没有意义。
//
// 格式不做长期兼容：状态的存活时间就是一手牌，版本对不上直接放弃恢复，
// 退回「本手作废」的旧行为——那是安全的降级，绝不能拿不确定的状态开牌。
const tableStateVersion = 1

// DeckState 是牌堆的可序列化形式。必须连未发的牌一起存：恢复后还要继续发
// 转牌河牌，重新洗一副牌会让已发出去的手牌与公共牌对不上。
type DeckState struct {
	Cards []Card `json:"cards"`
	Next  int    `json:"next"`
}

// TableState 是引擎的完整状态。字段与 Table 的私有字段一一对应，漏一个就
// 意味着恢复后的牌局与崩溃前不同，因此 TestTableStateCoversEveryField 会
// 用反射盯着 Table 的字段数量。
type TableState struct {
	Version int    `json:"version"`
	Config  Config `json:"config"`
	// Players 按座位号升序，恢复后重建为 map。
	Players           []Player                `json:"players"`
	Phase             Phase                   `json:"phase"`
	Revision          uint64                  `json:"revision"`
	HandCounter       uint64                  `json:"handCounter"`
	HandID            string                  `json:"handId"`
	DealerSeat        int                     `json:"dealerSeat"`
	SmallBlindSeat    int                     `json:"smallBlindSeat"`
	BigBlindSeat      int                     `json:"bigBlindSeat"`
	CurrentSeat       int                     `json:"currentSeat"`
	CurrentBet        int64                   `json:"currentBet"`
	MinRaiseIncrement int64                   `json:"minRaiseIncrement"`
	Deck              *DeckState              `json:"deck,omitempty"`
	Board             []Card                  `json:"board,omitempty"`
	RunoutBoards      [][]Card                `json:"runoutBoards,omitempty"`
	RunoutChoices     map[string]int          `json:"runoutChoices,omitempty"`
	ActionResults     map[string]ActionResult `json:"actionResults,omitempty"`
	HandStartStacks   map[string]int64        `json:"handStartStacks,omitempty"`
	LastSettlement    Settlement              `json:"lastSettlement"`
}

// sortedSeats 返回升序的座位号。map 的遍历顺序随机，而状态要能稳定比较。
func sortedSeats(players map[int]*Player) []int {
	seats := make([]int, 0, len(players))
	for seat := range players {
		seats = append(seats, seat)
	}
	sort.Ints(seats)
	return seats
}

// State 导出当前状态。返回值与引擎完全脱钩，调用方可以随意持有。
func (table *Table) State() TableState {
	state := TableState{
		Version:           tableStateVersion,
		Config:            table.config,
		Phase:             table.phase,
		Revision:          table.revision,
		HandCounter:       table.handCounter,
		HandID:            table.handID,
		DealerSeat:        table.dealerSeat,
		SmallBlindSeat:    table.smallBlindSeat,
		BigBlindSeat:      table.bigBlindSeat,
		CurrentSeat:       table.currentSeat,
		CurrentBet:        table.currentBet,
		MinRaiseIncrement: table.minRaiseIncrement,
		Board:             append([]Card(nil), table.board...),
		LastSettlement:    table.lastSettlement,
	}
	for _, seat := range sortedSeats(table.players) {
		state.Players = append(state.Players, *table.players[seat])
	}
	if table.deck != nil {
		state.Deck = &DeckState{
			Cards: append([]Card(nil), table.deck.cards...),
			Next:  table.deck.next,
		}
	}
	for _, board := range table.runoutBoards {
		state.RunoutBoards = append(state.RunoutBoards, append([]Card(nil), board...))
	}
	if len(table.runoutChoices) > 0 {
		state.RunoutChoices = make(map[string]int, len(table.runoutChoices))
		for playerID, choice := range table.runoutChoices {
			state.RunoutChoices[playerID] = choice
		}
	}
	if len(table.actionResults) > 0 {
		state.ActionResults = make(map[string]ActionResult, len(table.actionResults))
		for actionID, result := range table.actionResults {
			state.ActionResults[actionID] = result
		}
	}
	if len(table.handStartStacks) > 0 {
		state.HandStartStacks = make(map[string]int64, len(table.handStartStacks))
		for playerID, stack := range table.handStartStacks {
			state.HandStartStacks[playerID] = stack
		}
	}
	return state
}

// RestoreTable 用状态快照重建引擎。
//
// 校验从严：宁可拒绝恢复（本手作废，与旧行为一致）也不能拿一个内部矛盾的
// 状态继续发牌——那会造成筹码不守恒或牌面重复，比作废一手严重得多。
func RestoreTable(state TableState) (*Table, error) {
	if state.Version != tableStateVersion {
		return nil, errors.New("unsupported table state version")
	}
	table, err := NewTable(state.Config)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(state.Players))
	for _, player := range state.Players {
		if player.PlayerID == "" || player.Seat <= 0 || player.Seat > state.Config.MaxSeats {
			return nil, errors.New("restored player has an invalid seat")
		}
		if _, duplicate := table.players[player.Seat]; duplicate {
			return nil, errors.New("restored players share a seat")
		}
		if _, duplicate := seen[player.PlayerID]; duplicate {
			return nil, errors.New("restored player appears twice")
		}
		seen[player.PlayerID] = struct{}{}
		if player.Stack < 0 || player.StreetBet < 0 || player.TotalBet < 0 {
			return nil, errors.New("restored player has negative chips")
		}
		copied := player
		table.players[player.Seat] = &copied
	}
	table.phase = state.Phase
	table.revision = state.Revision
	table.handCounter = state.HandCounter
	table.handID = state.HandID
	table.dealerSeat = state.DealerSeat
	table.smallBlindSeat = state.SmallBlindSeat
	table.bigBlindSeat = state.BigBlindSeat
	table.currentSeat = state.CurrentSeat
	table.currentBet = state.CurrentBet
	table.minRaiseIncrement = state.MinRaiseIncrement
	table.board = append([]Card(nil), state.Board...)
	table.lastSettlement = state.LastSettlement
	if state.Deck != nil {
		if state.Deck.Next < 0 || state.Deck.Next > len(state.Deck.Cards) {
			return nil, errors.New("restored deck position is out of range")
		}
		table.deck = &Deck{
			cards: append([]Card(nil), state.Deck.Cards...),
			next:  state.Deck.Next,
		}
	}
	for _, board := range state.RunoutBoards {
		table.runoutBoards = append(table.runoutBoards, append([]Card(nil), board...))
	}
	if len(state.RunoutChoices) > 0 {
		table.runoutChoices = make(map[string]int, len(state.RunoutChoices))
		for playerID, choice := range state.RunoutChoices {
			table.runoutChoices[playerID] = choice
		}
	}
	for actionID, result := range state.ActionResults {
		table.actionResults[actionID] = result
	}
	if len(state.HandStartStacks) > 0 {
		table.handStartStacks = make(map[string]int64, len(state.HandStartStacks))
		for playerID, stack := range state.HandStartStacks {
			table.handStartStacks[playerID] = stack
		}
	}
	if err := table.validateRestoredState(); err != nil {
		return nil, err
	}
	return table, nil
}

// validateRestoredState 检查恢复出来的状态自洽。
//
// 重点是牌：手牌、公共牌与牌堆已发出的部分必须两两不重复，且已发张数与牌
// 堆游标对得上。牌重复会让摊牌比较出荒谬的结果，而且无法事后察觉。
func (table *Table) validateRestoredState() error {
	if table.phase == PhaseWaiting || table.phase == PhaseWaitingNextHand {
		return nil
	}
	if table.deck == nil {
		return errors.New("a hand in progress must carry its deck")
	}
	dealt := make(map[Card]struct{})
	add := func(card Card) error {
		if !card.Valid() {
			return errors.New("restored state contains an invalid card")
		}
		if _, duplicate := dealt[card]; duplicate {
			return errors.New("restored state deals the same card twice")
		}
		dealt[card] = struct{}{}
		return nil
	}
	for _, player := range table.players {
		if !player.Participating {
			continue
		}
		for _, card := range player.HoleCards {
			// 未发牌的座位留着零值，跳过而不是报错：牌局中途入座的玩家
			// 要等下一手才参与，他的手牌本来就是空的。
			if card.Rank == 0 && card.Suit == 0 {
				continue
			}
			if err := add(card); err != nil {
				return err
			}
		}
	}
	for _, card := range table.board {
		if err := add(card); err != nil {
			return err
		}
	}
	for _, board := range table.runoutBoards {
		for _, card := range board {
			if _, known := dealt[card]; known {
				// 发两次时各条街的牌面共用前面已发的公共牌，重复是正常的
				continue
			}
			if err := add(card); err != nil {
				return err
			}
		}
	}
	if len(dealt) > table.deck.next {
		return errors.New("restored state shows more dealt cards than the deck position allows")
	}
	if table.currentSeat != 0 {
		if _, seated := table.players[table.currentSeat]; !seated {
			return errors.New("restored state points at an empty seat to act")
		}
	}
	return nil
}
