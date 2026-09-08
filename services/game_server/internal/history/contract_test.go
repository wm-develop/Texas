package history

import (
	"testing"
	"time"

	"texas/services/game_server/internal/game/holdem"
)

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
		}},
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
