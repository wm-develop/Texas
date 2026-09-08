package history

import (
	"errors"
	"sort"
	"sync"
	"time"

	"texas/services/game_server/internal/game/holdem"
)

type PlayerResult struct {
	UserID        string   `json:"userId"`
	DisplayName   string   `json:"displayName"`
	Seat          int      `json:"seat"`
	StartingStack int64    `json:"startingStack"`
	EndingStack   int64    `json:"endingStack"`
	Delta         int64    `json:"delta"`
	HoleCards     []string `json:"holeCards,omitempty"`
}

type Action struct {
	ActionID  string    `json:"actionId"`
	UserID    string    `json:"userId"`
	Sequence  int       `json:"sequence"`
	Street    string    `json:"street"`
	Type      string    `json:"type"`
	Committed int64     `json:"committed"`
	RaiseTo   int64     `json:"raiseTo"`
	CreatedAt time.Time `json:"createdAt"`
}

type Hand struct {
	HandID        string                `json:"handId"`
	RoomID        string                `json:"roomId"`
	RoomCode      string                `json:"roomCode"`
	DealerSeat    int                   `json:"dealerSeat"`
	StartedAt     time.Time             `json:"startedAt"`
	EndedAt       time.Time             `json:"endedAt"`
	Board         []string              `json:"board"`
	RunoutBoards  [][]string            `json:"runoutBoards,omitempty"`
	Players       []PlayerResult        `json:"players"`
	Actions       []Action              `json:"actions"`
	PotAwards     []holdem.PotAward     `json:"potAwards"`
	Showdown      bool                  `json:"showdown"`
	RevealedHands []holdem.RevealedHand `json:"revealedHands"`
}

// Store 保存已结算的手牌。
//
// 存下来的记录带着**所有参与者**的底牌：复盘、对账与纠纷裁定都需要完整记录。
// 因此两个读取方法的可见性契约完全不同，实现新的 Store 时必须分清：
//
//   - Hand 返回未裁剪的完整记录，只能用于服务端内部（例如判断某手是否已落库）。
//     它的返回值绝不能直接发给客户端。
//   - RecentForPlayer 返回**按接收者裁剪**的记录：只有 userID 自己的底牌，以及
//     那一手真的亮出来过的底牌（RevealedHands）会保留，其余一律清空。
//     用 forRecipient 完成裁剪，别自己写一遍。
//
// 漏掉裁剪就等于公开了对手从未亮过的牌——在德州扑克里，知道对手弃了什么牌
// 就能反推他的打法范围，这是最严重的一类信息泄露。RunRecentForPlayerContract
// 会在任何实现上验证这条契约。
type Store interface {
	Append(hand Hand) error
	Hand(handID string) (Hand, bool)
	RecentForPlayer(userID string, limit int) []Hand
}

type InMemoryStore struct {
	mu      sync.RWMutex
	byID    map[string]Hand
	ordered []string
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{byID: make(map[string]Hand)}
}

func (store *InMemoryStore) Append(hand Hand) error {
	if err := validate(hand); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.byID[hand.HandID]; exists {
		return errors.New("hand history already exists")
	}
	store.byID[hand.HandID] = cloneHand(hand)
	store.ordered = append(store.ordered, hand.HandID)
	sort.SliceStable(store.ordered, func(left, right int) bool {
		return store.byID[store.ordered[left]].EndedAt.After(store.byID[store.ordered[right]].EndedAt)
	})
	return nil
}

func (store *InMemoryStore) Hand(handID string) (Hand, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	hand, ok := store.byID[handID]
	return cloneHand(hand), ok
}

func (store *InMemoryStore) RecentForPlayer(userID string, limit int) []Hand {
	store.mu.RLock()
	defer store.mu.RUnlock()
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	result := make([]Hand, 0, limit)
	for _, handID := range store.ordered {
		hand := store.byID[handID]
		if !containsPlayer(hand, userID) {
			continue
		}
		result = append(result, forRecipient(hand, userID))
		if len(result) == limit {
			break
		}
	}
	return result
}

func validate(hand Hand) error {
	if hand.HandID == "" || hand.RoomID == "" || hand.EndedAt.IsZero() || len(hand.Players) < 2 {
		return errors.New("invalid hand history")
	}
	seen := make(map[string]struct{}, len(hand.Players))
	var totalDelta int64
	for _, player := range hand.Players {
		if player.UserID == "" || player.Seat <= 0 || player.EndingStack < 0 {
			return errors.New("invalid hand player result")
		}
		if _, exists := seen[player.UserID]; exists {
			return errors.New("duplicate hand player")
		}
		seen[player.UserID] = struct{}{}
		totalDelta += player.Delta
	}
	if totalDelta != 0 {
		return errors.New("hand history does not conserve chips")
	}
	return nil
}

func containsPlayer(hand Hand, userID string) bool {
	for _, player := range hand.Players {
		if player.UserID == userID {
			return true
		}
	}
	return false
}

func forRecipient(hand Hand, userID string) Hand {
	result := cloneHand(hand)
	revealed := make(map[string]struct{}, len(result.RevealedHands))
	for _, value := range result.RevealedHands {
		revealed[value.PlayerID] = struct{}{}
	}
	for index := range result.Players {
		player := &result.Players[index]
		if player.UserID != userID {
			if _, public := revealed[player.UserID]; !public {
				player.HoleCards = nil
			}
		}
	}
	return result
}

func cloneHand(hand Hand) Hand {
	result := hand
	result.Board = append([]string(nil), hand.Board...)
	result.RunoutBoards = make([][]string, len(hand.RunoutBoards))
	for index, board := range hand.RunoutBoards {
		result.RunoutBoards[index] = append([]string(nil), board...)
	}
	result.Players = make([]PlayerResult, len(hand.Players))
	for index, player := range hand.Players {
		result.Players[index] = player
		result.Players[index].HoleCards = append([]string(nil), player.HoleCards...)
	}
	result.Actions = append([]Action(nil), hand.Actions...)
	result.PotAwards = make([]holdem.PotAward, len(hand.PotAwards))
	for index, award := range hand.PotAwards {
		result.PotAwards[index] = award
		result.PotAwards[index].WinnerPlayerIDs = append(
			[]string(nil),
			award.WinnerPlayerIDs...,
		)
		result.PotAwards[index].Payouts = append(
			[]holdem.Payout(nil),
			award.Payouts...,
		)
	}
	result.RevealedHands = make([]holdem.RevealedHand, len(hand.RevealedHands))
	for index, revealed := range hand.RevealedHands {
		result.RevealedHands[index] = revealed
		result.RevealedHands[index].HoleCards = append([]string(nil), revealed.HoleCards...)
	}
	return result
}
