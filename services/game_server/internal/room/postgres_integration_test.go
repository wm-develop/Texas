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
	"texas/services/game_server/internal/game/tablemanager"
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
	// 观战位的持久化：seat_number = 0 必须能写入并读回，两名观战者共用 0 不能撞上
	// 座位唯一索引，观战设置四列要完整往返。内存仓储测不出这些。
	spectating, err := rooms.EnterSpectate(ctx, "guest")
	if err != nil {
		t.Fatalf("EnterSpectate(guest): %v", err)
	}
	if _, err := rooms.EnterSpectate(ctx, "owner"); err != nil {
		t.Fatalf("EnterSpectate(owner) alongside another spectator: %v", err)
	}
	reloaded, err := rooms.Current(ctx, "guest")
	if err != nil {
		t.Fatalf("Current after spectating: %v", err)
	}
	for _, member := range reloaded.Members {
		if !member.Spectating || member.Seat != 0 {
			t.Fatalf("spectating member must reload with seat 0: %#v", member)
		}
		if member.UserID == "guest" && member.Stack != 500 {
			t.Fatalf("spectator keeps their table chips: %#v", member)
		}
	}
	if reloaded.Revision <= spectating.Revision-1 {
		t.Fatalf("revision should advance: %#v", reloaded)
	}
	wantSettings := room.SpectatorSettings{FeeBigBlinds: 0, VoiceAllowed: false, ChatAllowed: true, EmoteAllowed: false}
	if _, err := rooms.UpdateSpectatorSettings(ctx, "owner", wantSettings); err != nil {
		t.Fatalf("UpdateSpectatorSettings: %v", err)
	}
	if reloaded, err = rooms.Current(ctx, "owner"); err != nil || reloaded.Spectator != wantSettings {
		t.Fatalf("spectator settings did not round-trip: %#v err=%v", reloaded.Spectator, err)
	}
	// 观战者不占座位：两人都在观战位时，第三人必须能带入并坐下——此前的
	// 满员计数 count(*) 把观战者也算了进去，2 人房两人观战就把新人挡在门外。
	// CreateConfigured 会把人数强制成 10（客户端传的 maxPlayers 已弃用），
	// 这里直接把库里的上限改成 2，让满员判断真正被触发。
	if _, err := database.ExecContext(ctx, `UPDATE rooms SET max_players = 2 WHERE room_id = $1`, created.RoomID); err != nil {
		t.Fatalf("shrink max_players: %v", err)
	}
	if err := accounts.CreateUser(ctx, account.User{
		UserID: "third", Username: "third", DisplayName: "玩家third",
		PasswordHash: "hash", CreatedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("CreateUser(third): %v", err)
	}
	if _, err := chips.TopUp(ctx, "third", "topup:third", 5_000); err != nil {
		t.Fatalf("TopUp(third): %v", err)
	}
	withThird, err := rooms.JoinWithBuyIn(ctx, room.Participant{
		UserID: "third", DisplayName: "玩家third",
	}, room.JoinOptions{Code: created.Code, BuyIn: 500, RequestID: "join-third"})
	if err != nil {
		t.Fatalf("JoinWithBuyIn while both members spectate must succeed: %v", err)
	}
	if len(withThird.SeatedMembers()) != 1 || len(withThird.SpectatorMembers()) != 2 {
		t.Fatalf("one seated, two spectating: %#v", withThird.Members)
	}
	// 现在只剩一个座位：先让 owner 坐回去，guest 再上桌就该被拒
	if _, err := rooms.TakeSeat(ctx, "owner"); err != nil {
		t.Fatalf("TakeSeat(owner): %v", err)
	}
	if _, err := rooms.TakeSeat(ctx, "guest"); err == nil {
		t.Fatal("two seats occupied, guest must be refused")
	}
	if _, err := chips.CashOut(ctx, "third", created.RoomID, "cashout-third"); err != nil {
		t.Fatalf("CashOut(third): %v", err)
	}
	if _, err := rooms.Leave(ctx, "third"); err != nil {
		t.Fatalf("Leave(third): %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE rooms SET max_players = 10 WHERE room_id = $1`, created.RoomID); err != nil {
		t.Fatalf("restore max_players: %v", err)
	}
	// 两人回到座位，房间恢复成 2 人满员，后面的牌局流程照旧
	for _, userID := range []string{"owner", "guest"} {
		if _, err := rooms.TakeSeat(ctx, userID); err != nil {
			t.Fatalf("TakeSeat(%s): %v", userID, err)
		}
	}
	if reloaded, err = rooms.Current(ctx, "owner"); err != nil || len(reloaded.SeatedMembers()) != 2 || len(reloaded.SpectatorMembers()) != 0 {
		t.Fatalf("both should be seated again: %#v err=%v", reloaded.Members, err)
	}
	if _, err := rooms.UpdateSpectatorSettings(ctx, "owner", room.DefaultSpectatorSettings()); err != nil {
		t.Fatalf("restore spectator settings: %v", err)
	}
	ownerPosition, err := chips.Rebuy(ctx, "owner", created.RoomID, "rebuy-owner", 500, 2_000)
	if err != nil || ownerPosition.TableChips != 1_500 {
		t.Fatalf("Rebuy position=%#v err=%v", ownerPosition, err)
	}
	ledgerStore, _ := ledger.NewPostgresStore(database)
	historyStore, _ := history.NewPostgresStore(database)
	tables, err := tablemanager.NewWithConfig(rooms, postgresZeroRandom{}, tablemanager.ManagerConfig{
		Now: func() time.Time { return now }, Ledger: ledgerStore,
		History: historyStore, Bankroll: chips,
	})
	if err != nil {
		t.Fatalf("table manager: %v", err)
	}
	if _, err := tables.Join(ctx, "owner", created.RoomID); err != nil {
		t.Fatalf("owner table join: %v", err)
	}
	if _, err := tables.Join(ctx, "guest", created.RoomID); err != nil {
		t.Fatalf("guest table join: %v", err)
	}
	if _, err := tables.SetReady(ctx, "owner", true); err != nil {
		t.Fatalf("owner ready: %v", err)
	}
	started, err := tables.SetReady(ctx, "guest", true)
	if err != nil || started.CurrentAction == nil {
		t.Fatalf("guest ready snapshot=%#v err=%v", started, err)
	}
	actor := started.CurrentAction.UserID
	actorSnapshot, err := tables.Snapshot(ctx, actor, created.RoomID)
	if err != nil {
		t.Fatalf("actor snapshot: %v", err)
	}
	_, folded, err := tables.SubmitAction(ctx, actor, created.RoomID, holdem.ActionRequest{
		ActionID: "postgres-preflop-fold", HandID: actorSnapshot.HandID,
		TableRevision: actorSnapshot.TableRevision, Action: holdem.ActionFold,
	})
	if err != nil {
		t.Fatalf("preflop fold settlement: %v", err)
	}
	if folded.Settlement == nil || folded.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("preflop fold snapshot=%#v", folded)
	}
	if persisted, found := historyStore.Hand(folded.HandID); !found {
		t.Fatal("preflop fold history was not persisted")
	} else if len(persisted.Board) != 0 {
		t.Fatalf("preflop fold board=%#v", persisted.Board)
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

	if err := ledgerStore.Append([]ledger.Entry{
		{EntryID: "ledger-owner", HandID: "history-hand", PlayerID: "owner", Delta: 100, BalanceAfter: 1_200},
		{EntryID: "ledger-guest", HandID: "history-hand", PlayerID: "guest", Delta: -100, BalanceAfter: 800},
	}); err != nil {
		t.Fatalf("append ledger before history: %v", err)
	}
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
	if recent := historyStore.RecentForPlayer("owner", 10); len(recent) != 2 || recent[0].HandID != "history-hand" || recent[0].DealerSeat != 1 || len(recent[0].Actions) != 1 {
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

type postgresZeroRandom struct{}

func (postgresZeroRandom) Intn(int) (int, error) { return 0, nil }
