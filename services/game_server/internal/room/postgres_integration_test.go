package room_test

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"os"
	"testing"
	"time"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/chat"
	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/ledger"
	"texas/services/game_server/internal/postgres"
	"texas/services/game_server/internal/room"
	"texas/services/game_server/internal/security"
	"texas/services/game_server/migrations"
)

func TestPostgresPhase3PersistenceFlow(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	database.SetMaxOpenConns(1)
	schema := fmt.Sprintf("phase3_flow_test_%d", time.Now().UnixNano())
	if _, err := database.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
		_ = database.Close()
	})
	if _, err := database.ExecContext(ctx, `SET search_path TO `+schema); err != nil {
		t.Fatalf("set search path: %v", err)
	}
	migrator, err := postgres.NewMigrator(migrations.Files)
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	if _, err := migrator.Up(ctx, database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Unix(10_000, 0).UTC()
	accounts, err := account.NewPostgresRepository(database)
	if err != nil {
		t.Fatalf("account repository: %v", err)
	}
	for index, userID := range []string{"owner", "guest"} {
		if err := accounts.CreateUser(ctx, account.User{
			UserID: userID, Username: userID, DisplayName: "玩家" + userID,
			PasswordHash: "hash", CreatedAt: now.Add(time.Duration(index) * time.Second),
		}); err != nil {
			t.Fatalf("CreateUser(%s): %v", userID, err)
		}
	}
	bankrollRepository, err := bankroll.NewPostgresRepository(database)
	if err != nil {
		t.Fatalf("bankroll repository: %v", err)
	}
	chips, err := bankroll.NewService(bankrollRepository, func() time.Time { return now })
	if err != nil {
		t.Fatalf("bankroll service: %v", err)
	}
	for _, userID := range []string{"owner", "guest"} {
		if _, err := chips.TopUp(ctx, userID, "topup:"+userID, 5_000); err != nil {
			t.Fatalf("TopUp(%s): %v", userID, err)
		}
	}
	roomsRepository, err := room.NewPostgresRepository(database)
	if err != nil {
		t.Fatalf("room repository: %v", err)
	}
	hasher, err := security.NewPasswordHasher(1_000, cryptorand.Reader)
	if err != nil {
		t.Fatalf("password hasher: %v", err)
	}
	rooms, err := room.NewService(roomsRepository, hasher, room.ServiceConfig{
		Now: func() time.Time { return now }, Bankroll: chips,
	})
	if err != nil {
		t.Fatalf("room service: %v", err)
	}
	created, err := rooms.CreateConfigured(ctx, room.Participant{
		UserID: "owner", DisplayName: "玩家owner",
	}, room.CreateOptions{
		Preset: room.PresetCasual, MaxPlayers: 2, SmallBlind: 10, BigBlind: 20,
		MaxBuyIn: 2_000, BuyIn: 1_000, RequestID: "create-room",
	})
	if err != nil {
		t.Fatalf("CreateConfigured: %v", err)
	}
	joined, err := rooms.JoinWithBuyIn(ctx, room.Participant{
		UserID: "guest", DisplayName: "玩家guest",
	}, room.JoinOptions{Code: created.Code, BuyIn: 500, RequestID: "join-room"})
	if err != nil || len(joined.Members) != 2 {
		t.Fatalf("JoinWithBuyIn room=%#v err=%v", joined, err)
	}
	ownerPosition, err := chips.Rebuy(ctx, "owner", created.RoomID, "rebuy-owner", 500, 2_000)
	if err != nil || ownerPosition.TableChips != 1_500 {
		t.Fatalf("Rebuy position=%#v err=%v", ownerPosition, err)
	}
	if err := chips.ApplySettlement(ctx, created.RoomID, "hand-1", map[string]int64{
		"owner": 1_200, "guest": 800,
	}, 2_000); err != nil {
		t.Fatalf("ApplySettlement: %v", err)
	}
	if _, err := chips.CashOut(ctx, "guest", created.RoomID, "cashout-guest"); err != nil {
		t.Fatalf("CashOut: %v", err)
	}
	if closed, err := rooms.Leave(ctx, "guest"); err != nil || closed {
		t.Fatalf("guest Leave closed=%v err=%v", closed, err)
	}
	guestWallet, err := chips.Snapshot(ctx, "guest")
	if err != nil || guestWallet.WalletChips != 5_300 || guestWallet.TableID != "" {
		t.Fatalf("guest wallet=%#v err=%v", guestWallet, err)
	}

	ledgerStore, _ := ledger.NewPostgresStore(database)
	if err := ledgerStore.Append([]ledger.Entry{
		{EntryID: "ledger-owner", HandID: "history-hand", PlayerID: "owner", Delta: 100, BalanceAfter: 1_200},
		{EntryID: "ledger-guest", HandID: "history-hand", PlayerID: "guest", Delta: -100, BalanceAfter: 800},
	}); err != nil {
		t.Fatalf("append ledger before history: %v", err)
	}
	historyStore, _ := history.NewPostgresStore(database)
	if err := historyStore.Append(history.Hand{
		HandID: "history-hand", RoomID: created.RoomID, RoomCode: created.Code,
		DealerSeat: 1, StartedAt: now, EndedAt: now.Add(time.Minute),
		Board: []string{"AS", "KH", "QD", "JC", "TS"}, Showdown: true,
		Players: []history.PlayerResult{
			{UserID: "owner", DisplayName: "玩家owner", Seat: 1, StartingStack: 1_100, EndingStack: 1_200, Delta: 100, HoleCards: []string{"2S", "2H"}},
			{UserID: "guest", DisplayName: "玩家guest", Seat: 2, StartingStack: 900, EndingStack: 800, Delta: -100, HoleCards: []string{"3S", "3H"}},
		},
		Actions: []history.Action{{
			ActionID: "action-1", UserID: "owner", Sequence: 1, Street: "preflop",
			Type: "call", Committed: 20, CreatedAt: now.Add(10 * time.Second),
		}},
		PotAwards: []holdem.PotAward{}, RevealedHands: []holdem.RevealedHand{},
	}); err != nil {
		t.Fatalf("append history: %v", err)
	}
	if loaded, found := historyStore.Hand("history-hand"); !found {
		t.Fatal("persisted history could not be loaded by hand id")
	} else if len(loaded.Players) != 2 || len(loaded.Actions) != 1 {
		t.Fatalf("loaded history=%#v", loaded)
	}
	if recent := historyStore.RecentForPlayer("owner", 10); len(recent) != 1 || recent[0].DealerSeat != 1 || len(recent[0].Actions) != 1 {
		t.Fatalf("recent history=%#v", recent)
	}
	chatStore, _ := chat.NewPostgresStore(database)
	message := chat.Message{
		MessageID: "message-1", ClientMessageID: "client-1", UserID: "owner",
		DisplayName: "玩家owner", TableID: created.RoomID, Kind: chat.KindText,
		Content: "好牌", SentAt: now,
	}
	if saved, err := chatStore.Save(message); err != nil || saved.MessageID != message.MessageID {
		t.Fatalf("save chat message=%#v err=%v", saved, err)
	}
	if saved, err := chatStore.Save(message); err != nil || saved.MessageID != message.MessageID {
		t.Fatalf("repeat chat message=%#v err=%v", saved, err)
	}
}
