package bankroll

import (
	"context"
	"testing"
	"time"
)

func TestVirtualTopUpAndTableTransfersAreIdempotent(t *testing.T) {
	service, err := NewService(NewMemoryRepository(), func() time.Time { return time.Unix(100, 0) })
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	first, err := service.TopUp(ctx, "u1", "topup-1", 5000)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := service.TopUp(ctx, "u1", "topup-1", 5000)
	if err != nil {
		t.Fatal(err)
	}
	if first != duplicate || first.WalletChips != 5000 {
		t.Fatalf("unexpected top-up: %#v %#v", first, duplicate)
	}

	bought, err := service.BuyIn(ctx, "u1", "table1", "buy-1", 2000, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if bought.WalletChips != 3000 || bought.TableChips != 2000 {
		t.Fatalf("unexpected buy-in: %#v", bought)
	}
	if _, err := service.Rebuy(ctx, "u1", "table1", "rebuy-too-large", 1001, 3000); !IsErrorCode(err, "maximum_buy_in_exceeded") {
		t.Fatalf("expected cap error, got %v", err)
	}

	rebought, err := service.Rebuy(ctx, "u1", "table1", "rebuy-1", 1000, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if rebought.WalletChips != 2000 || rebought.TableChips != 3000 {
		t.Fatalf("unexpected rebuy: %#v", rebought)
	}

	cashed, err := service.CashOut(ctx, "u1", "table1", "cash-1")
	if err != nil {
		t.Fatal(err)
	}
	if cashed.WalletChips != 5000 || cashed.TableChips != 0 {
		t.Fatalf("unexpected cash-out: %#v", cashed)
	}
	entries, err := service.Entries(ctx, "u1", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}
}

func TestSettlementConservesTableChipsAndIsIdempotent(t *testing.T) {
	repository := NewMemoryRepository()
	service, _ := NewService(repository, time.Now)
	ctx := context.Background()
	for _, userID := range []string{"u1", "u2"} {
		if _, err := service.TopUp(ctx, userID, "top-"+userID, 1000); err != nil {
			t.Fatal(err)
		}
		if _, err := service.BuyIn(ctx, userID, "table1", "buy-"+userID, 1000, 2000); err != nil {
			t.Fatal(err)
		}
	}
	balances := map[string]int64{"u1": 1500, "u2": 500}
	if err := service.ApplySettlement(ctx, "table1", "hand1", balances, 2000); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplySettlement(ctx, "table1", "hand1", balances, 2000); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplySettlement(ctx, "table1", "hand2", map[string]int64{"u1": 1600, "u2": 500}, 2000); !IsErrorCode(err, "table_chips_not_conserved") {
		t.Fatalf("expected conservation error, got %v", err)
	}
	for userID, want := range balances {
		tableSnapshot := repository.snapshotForTest(userID, "table1")
		if tableSnapshot.TableChips != want {
			t.Fatalf("%s table chips=%d want %d", userID, tableSnapshot.TableChips, want)
		}
	}
}

func TestAdministratorWalletSetIsExactIdempotentAndBlockedAtTable(t *testing.T) {
	service, err := NewService(NewMemoryRepository(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := service.SetWallet(ctx, "u1", "admin-set-1", 4321)
	if err != nil || first.WalletChips != 4321 {
		t.Fatalf("SetWallet first=%#v err=%v", first, err)
	}
	duplicate, err := service.SetWallet(ctx, "u1", "admin-set-1", 9999)
	if err != nil || duplicate != first {
		t.Fatalf("SetWallet duplicate=%#v err=%v", duplicate, err)
	}
	if _, err := service.BuyIn(ctx, "u1", "table1", "buy-admin-test", 1000, 5000); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetWallet(ctx, "u1", "admin-set-2", 8000); !IsErrorCode(err, "user_in_room") {
		t.Fatalf("SetWallet while at table error=%v", err)
	}
}

func (repository *MemoryRepository) snapshotForTest(userID, tableID string) Snapshot {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.snapshotLocked(userID, tableID)
}
