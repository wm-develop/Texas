package bankroll

import (
	"context"
	"testing"
	"time"
)

func TestTransferWalletMovesEverythingRecordsBothSidesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	service, err := NewService(NewMemoryRepository(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.TopUp(ctx, "leaver", "topup-leaver", 700); err != nil {
		t.Fatal(err)
	}
	if _, err := service.TopUp(ctx, "admin", "topup-admin", 50); err != nil {
		t.Fatal(err)
	}

	result, err := service.TransferWallet(ctx, "leaver", "admin", "account_deletion:leaver", "leaver")
	if err != nil {
		t.Fatalf("TransferWallet: %v", err)
	}
	if result.WalletChips != 0 {
		t.Fatalf("leaver should be emptied, got %d", result.WalletChips)
	}
	admin, _ := service.Snapshot(ctx, "admin")
	if admin.WalletChips != 750 {
		t.Fatalf("admin wallet=%d", admin.WalletChips)
	}

	leaverEntries, _ := service.Entries(ctx, "leaver", 10)
	adminEntries, _ := service.Entries(ctx, "admin", 10)
	if leaverEntries[0].Reason != ReasonAccountDeletion || leaverEntries[0].WalletDelta != -700 ||
		leaverEntries[0].ReferenceID != "leaver" {
		t.Fatalf("leaver entry=%#v", leaverEntries[0])
	}
	if adminEntries[0].Reason != ReasonAccountDeletion || adminEntries[0].WalletDelta != 700 ||
		adminEntries[0].ReferenceID != "leaver" || adminEntries[0].WalletBalanceAfter != 750 {
		t.Fatalf("admin entry=%#v", adminEntries[0])
	}
	// 两条流水之和为 0：注销不创造也不销毁筹码
	if leaverEntries[0].WalletDelta+adminEntries[0].WalletDelta != 0 {
		t.Fatal("transfer must conserve chips")
	}

	// 幂等：同一 requestID 重放不会再次转账
	if _, err := service.TransferWallet(ctx, "leaver", "admin", "account_deletion:leaver", "leaver"); err != nil {
		t.Fatalf("replay: %v", err)
	}
	adminAgain, _ := service.Snapshot(ctx, "admin")
	if adminAgain.WalletChips != 750 {
		t.Fatalf("replay double-credited admin: %d", adminAgain.WalletChips)
	}
}

func TestTransferWalletRefusesWhileChipsAreAtATable(t *testing.T) {
	ctx := context.Background()
	service, err := NewService(NewMemoryRepository(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.TopUp(ctx, "seated", "topup", 1_000); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BuyIn(ctx, "seated", "room_1", "buyin", 400, 2_000); err != nil {
		t.Fatal(err)
	}
	_, err = service.TransferWallet(ctx, "seated", "admin", "account_deletion:seated", "seated")
	if !IsErrorCode(err, "user_in_room") {
		t.Fatalf("expected user_in_room, got %v", err)
	}
	if _, err := service.TransferWallet(ctx, "same", "same", "r", "same"); !IsErrorCode(err, "invalid_request") {
		t.Fatalf("self transfer must be rejected, got %v", err)
	}
}
