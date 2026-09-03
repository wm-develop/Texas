package tablemanager

import (
	"context"
	"testing"

	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/room"
)

// 赢家常常赢完这手就立刻离开房间。此后房间成员表里没有他，结算文案会退化
// 成显示用户 ID，底池（按在座玩家投入累加）也会小于结算金额。
func TestSettlementKeepsWinnerNameAndPotAfterTheyLeave(t *testing.T) {
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.PresetStandard, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.Join(ctx, room.Participant{UserID: "guest", DisplayName: "好友"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	manager, err := New(rooms, zeroRandom{})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{"owner", "guest"} {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	inHand := startHand(t, ctx, manager, []string{"owner", "guest"})

	// 一人弃牌，另一人赢下这手
	settled := submitFold(t, ctx, manager, created.RoomID, inHand, "fold-1")
	if settled.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("expected WAITING_NEXT_HAND, got %s", settled.Phase)
	}
	if settled.Settlement == nil || len(settled.Settlement.PotAwards) == 0 {
		t.Fatal("settlement should carry pot awards")
	}
	var winnerID string
	var expectedPot int64
	for _, award := range settled.Settlement.PotAwards {
		expectedPot += award.Amount
		for _, payout := range award.Payouts {
			winnerID = payout.PlayerID
			if payout.DisplayName == "" {
				t.Fatalf("payout must carry the winner's name: %#v", payout)
			}
		}
	}
	if settled.TotalPot != expectedPot {
		t.Fatalf("pot on screen (%d) must match the settled amount (%d)", settled.TotalPot, expectedPot)
	}

	// 赢家离开房间后，剩下的人看到的结算文案不能退化成用户 ID
	if _, err := manager.Leave(ctx, winnerID); err != nil {
		t.Fatal(err)
	}
	remaining := "owner"
	if winnerID == "owner" {
		remaining = "guest"
	}
	after, err := manager.Snapshot(ctx, remaining, created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Settlement == nil {
		t.Fatal("settlement should still be visible while waiting for the next hand")
	}
	var seen bool
	for _, award := range after.Settlement.PotAwards {
		for _, payout := range award.Payouts {
			seen = true
			if payout.DisplayName == "" {
				t.Fatalf("winner name lost after they left the room: %#v", payout)
			}
		}
	}
	if !seen {
		t.Fatal("expected the settlement to still list payouts")
	}
	if after.TotalPot != expectedPot {
		t.Fatalf("pot must not shrink when the winner leaves: got %d, want %d", after.TotalPot, expectedPot)
	}
}
