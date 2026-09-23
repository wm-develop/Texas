package history

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"texas/services/game_server/internal/game/holdem"
)

// contractUserIDs 是契约测试里出现的玩家。Postgres 的手牌表对 users 有外键，
// 集成测试要先把他们建出来。
var contractUserIDs = []string{"me", "shown", "folded"}

// runRecentForPlayerContract 在任意 Store 实现上验证按接收者裁剪的契约。
//
// 这条契约此前只写在两个实现的代码里，没有任何测试盯着它：新加一个实现、
// 或重构时顺手改掉 forRecipient 的调用，泄露不会有任何征兆。而泄露的后果
// 是对手从未亮过的底牌被公开——德州扑克里最严重的一类信息泄露。
func runRecentForPlayerContract(t *testing.T, store Store) {
	t.Helper()
	endedAt := time.Now().UTC().Truncate(time.Second)
	hand := Hand{
		HandID: "contract_hand", RoomID: "room_contract", RoomCode: "654321",
		DealerSeat: 1, StartedAt: endedAt.Add(-time.Minute), EndedAt: endedAt,
		Board: []string{"AS", "KH", "QD", "JC", "TS"}, Showdown: true,
		Players: []PlayerResult{
			{UserID: "me", DisplayName: "我", Seat: 1, StartingStack: 1_000, EndingStack: 1_100, Delta: 100, HoleCards: []string{"2S", "2H"}},
			{UserID: "shown", DisplayName: "摊牌者", Seat: 2, StartingStack: 1_000, EndingStack: 950, Delta: -50, HoleCards: []string{"3S", "3H"}},
			{UserID: "folded", DisplayName: "弃牌者", Seat: 3, StartingStack: 1_000, EndingStack: 950, Delta: -50, HoleCards: []string{"4S", "4H"}},
		},
		Actions: []Action{{
			ActionID: "a1", UserID: "me", Sequence: 1, Street: "preflop",
			Type: "call", Committed: 20, CreatedAt: endedAt.Add(-30 * time.Second),
		}, {
			ActionID: "a2", UserID: "folded", Sequence: 2, Street: "preflop",
			Type: "fold", CreatedAt: endedAt.Add(-20 * time.Second), TimedOut: true,
		}},
		SmallBlind: 10, BigBlind: 20, SmallBlindSeat: 2, BigBlindSeat: 3,
		PotAwards: []holdem.PotAward{},
		RevealedHands: []holdem.RevealedHand{
			{PlayerID: "shown", HoleCards: []string{"3S", "3H"}, Category: "一对"},
		},
	}
	if err := store.Append(hand); err != nil {
		t.Fatalf("Append: %v", err)
	}

	for _, viewer := range []string{"me", "shown", "folded"} {
		hands := store.RecentForPlayer(viewer, 10)
		if len(hands) != 1 {
			t.Fatalf("以 %s 的视角应查到一手，得到 %d 手", viewer, len(hands))
		}
		for _, player := range hands[0].Players {
			switch {
			case player.UserID == viewer:
				if len(player.HoleCards) != 2 {
					t.Fatalf("%s 必须能看到自己的牌：%v", viewer, player.HoleCards)
				}
			case player.UserID == "shown":
				// 摊牌亮出来的牌是公开信息
				if len(player.HoleCards) != 2 {
					t.Fatalf("以 %s 的视角看不到已摊牌的牌：%v", viewer, player.HoleCards)
				}
			default:
				if len(player.HoleCards) != 0 {
					t.Fatalf(
						"以 %s 的视角看到了 %s 从未亮过的底牌 %v——"+
							"RecentForPlayer 必须用 forRecipient 裁剪",
						viewer, player.UserID, player.HoleCards,
					)
				}
			}
		}
	}

	// 不在这手牌里的人查不到它
	if hands := store.RecentForPlayer("outsider", 10); len(hands) != 0 {
		t.Fatalf("局外人不该查到这手牌：%#v", hands)
	}

	// 单手读取与翻页同样按接收者裁剪；局外人查不到
	for _, viewer := range []string{"me", "shown", "folded"} {
		single, err := store.HandForPlayer(viewer, hand.HandID)
		if err != nil {
			t.Fatalf("HandForPlayer(%s): %v", viewer, err)
		}
		page, err := store.PageForPlayer(viewer, "", 10)
		if err != nil || len(page) != 1 {
			t.Fatalf("PageForPlayer(%s)=%d hands err=%v", viewer, len(page), err)
		}
		for _, value := range []Hand{single, page[0]} {
			for _, player := range value.Players {
				if player.UserID != viewer && player.UserID != "shown" && len(player.HoleCards) != 0 {
					t.Fatalf("以 %s 的视角看到了 %s 从未亮过的底牌 %v", viewer, player.UserID, player.HoleCards)
				}
			}
		}
	}
	if _, err := store.HandForPlayer("outsider", hand.HandID); !errors.Is(err, ErrHandNotFound) {
		t.Fatalf("局外人不该查到这手牌：err=%v", err)
	}
	if _, err := store.HandForPlayer("me", "no_such_hand"); !errors.Is(err, ErrHandNotFound) {
		t.Fatalf("不存在的手号应该是 ErrHandNotFound：err=%v", err)
	}
	// 游标是局外人的手，同样当作没有：不能靠翻页确认别人打过哪手
	if _, err := store.PageForPlayer("outsider", hand.HandID, 10); !errors.Is(err, ErrHandNotFound) {
		t.Fatalf("局外人拿别人的手号当游标：err=%v", err)
	}

	// 回放需要的字段要能原样读回
	single, _ := store.HandForPlayer("me", hand.HandID)
	if single.SmallBlind != 10 || single.BigBlind != 20 || single.SmallBlindSeat != 2 || single.BigBlindSeat != 3 {
		t.Fatalf("盲注没有原样读回：%+v", single)
	}
	if len(single.Actions) != 2 || single.Actions[0].TimedOut || !single.Actions[1].TimedOut {
		t.Fatalf("超时标记没有原样读回：%+v", single.Actions)
	}

	// 内部读取仍然拿到完整记录：复盘与纠纷裁定依赖它
	full, found := store.Hand(hand.HandID)
	if !found {
		t.Fatal("Hand 应该能读回刚写入的记录")
	}
	for _, player := range full.Players {
		if len(player.HoleCards) != 2 {
			t.Fatalf("Hand 必须返回未裁剪的完整记录：%#v", player)
		}
	}
}

func TestInMemoryStoreRedactsRecentHands(t *testing.T) {
	runRecentForPlayerContract(t, NewInMemoryStore())
}

// runPaginationContract 验证牌局记录翻页：新的在前，游标之后接着往旧的翻，
// 不重复、不遗漏；同一时刻结束的几手也分得清先后。
func runPaginationContract(t *testing.T, store Store) {
	t.Helper()
	base := time.Now().UTC().Truncate(time.Second).Add(time.Hour)
	var want []string
	for index := 0; index < 7; index++ {
		// 第 3、4 手同一时刻结束
		endedAt := base.Add(time.Duration(index) * time.Minute)
		if index == 4 {
			endedAt = base.Add(3 * time.Minute)
		}
		hand := Hand{
			HandID: fmt.Sprintf("page_hand_%d", index), RoomID: "room_page", RoomCode: "111111",
			DealerSeat: 1, StartedAt: endedAt.Add(-time.Minute), EndedAt: endedAt,
			Players: []PlayerResult{
				{UserID: "me", DisplayName: "我", Seat: 1, StartingStack: 1_000, EndingStack: 1_010, Delta: 10},
				{UserID: "shown", DisplayName: "对手", Seat: 2, StartingStack: 1_000, EndingStack: 990, Delta: -10},
			},
			PotAwards: []holdem.PotAward{},
		}
		if err := store.Append(hand); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	var got []string
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		page, err := store.PageForPlayer("me", cursor, 3)
		if err != nil {
			t.Fatalf("PageForPlayer(cursor=%q): %v", cursor, err)
		}
		if len(page) == 0 {
			break
		}
		for _, hand := range page {
			if hand.RoomID == "room_page" {
				got = append(got, hand.HandID)
			}
			cursor = hand.HandID
		}
	}
	seen := make(map[string]bool)
	for _, handID := range got {
		if seen[handID] {
			t.Fatalf("翻页重复出现 %s：%v", handID, got)
		}
		seen[handID] = true
	}
	for index := 0; index < 7; index++ {
		want = append(want, fmt.Sprintf("page_hand_%d", index))
	}
	if len(got) != len(want) {
		t.Fatalf("翻页漏了：得到 %v", got)
	}
	if got[0] != "page_hand_6" || got[len(got)-1] != "page_hand_0" {
		t.Fatalf("顺序应当是新的在前：%v", got)
	}
}

func TestInMemoryStorePaginates(t *testing.T) {
	runPaginationContract(t, NewInMemoryStore())
}
