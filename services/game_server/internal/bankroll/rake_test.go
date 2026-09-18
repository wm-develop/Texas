package bankroll

import (
	"context"
	"testing"
	"time"
)

// 抽水在结算的同一步里从牌桌转进收款人的钱包：守恒校验为「结算前 = 结算后 + 抽水」，
// 收款人多一条 rake 流水，按房间可汇总；同一手重复结算不会重复入账。
func TestSettlementMovesTheRakeToTheRecipientWallet(t *testing.T) {
	ctx := context.Background()
	service, err := NewService(NewMemoryRepository(), func() time.Time { return time.Unix(100, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{"u1", "u2"} {
		if _, err := service.TopUp(ctx, userID, "topup-"+userID, 5_000); err != nil {
			t.Fatal(err)
		}
		if _, err := service.BuyIn(ctx, userID, "table1", "buyin-"+userID, 1_000, 2_000); err != nil {
			t.Fatal(err)
		}
	}
	rake := Rake{RecipientUserID: "admin", Amount: 25}
	balances := map[string]int64{"u1": 1_175, "u2": 800}
	if err := service.ApplySettlementWithRake(ctx, "table1", "hand1", balances, 2_000, rake); err != nil {
		t.Fatalf("settlement with rake: %v", err)
	}
	if err := service.ApplySettlementWithRake(ctx, "table1", "hand1", balances, 2_000, rake); err != nil {
		t.Fatalf("repeated settlement: %v", err)
	}
	admin, err := service.Snapshot(ctx, "admin")
	if err != nil || admin.WalletChips != 25 {
		t.Fatalf("admin wallet=%#v err=%v", admin, err)
	}
	summary, err := service.RakeByRoom(ctx)
	if err != nil || len(summary) != 1 || summary[0].RoomID != "table1" || summary[0].Total != 25 || summary[0].Hands != 1 {
		t.Fatalf("rake summary=%#v err=%v", summary, err)
	}
	// 账对不上时拒绝：少报或多报抽水都不行
	for _, amount := range []int64{24, 26} {
		err := service.ApplySettlementWithRake(ctx, "table1", "hand2", map[string]int64{"u1": 1_150, "u2": 800},
			2_000, Rake{RecipientUserID: "admin", Amount: amount})
		if !IsErrorCode(err, "table_chips_not_conserved") {
			t.Fatalf("rake %d must be rejected, err=%v", amount, err)
		}
	}
	// 有抽水却没有收款人、或抽水为负，一律拒绝
	for _, invalid := range []Rake{{Amount: 25}, {RecipientUserID: "admin", Amount: -1}} {
		err := service.ApplySettlementWithRake(ctx, "table1", "hand3", map[string]int64{"u1": 1_150, "u2": 800}, 2_000, invalid)
		if !IsErrorCode(err, "invalid_table_balance") {
			t.Fatalf("invalid rake %#v must be rejected, err=%v", invalid, err)
		}
	}
	entries, err := service.Entries(ctx, "admin", 10)
	if err != nil || len(entries) != 1 || entries[0].Reason != ReasonRake || entries[0].WalletDelta != 25 ||
		entries[0].TableID != "table1" || entries[0].ReferenceID != "hand1" {
		t.Fatalf("admin entries=%#v err=%v", entries, err)
	}
}
