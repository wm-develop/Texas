package room_test

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
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
	"texas/services/game_server/internal/tablestate"
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
	// 进行中牌局的状态：外键挂在 rooms 上，只有真实库能验证级联与 upsert。
	tableStates, err := tablestate.NewPostgresStore(database)
	if err != nil {
		t.Fatalf("table state store: %v", err)
	}
	if err := tableStates.Save(ctx, tablestate.Record{
		RoomID: created.RoomID, HandID: "hand_state_1", Revision: 7,
		State: []byte(`{"version":1}`), UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save table state: %v", err)
	}
	// 同一房间再存一次必须覆盖而不是插入第二行
	if err := tableStates.Save(ctx, tablestate.Record{
		RoomID: created.RoomID, HandID: "hand_state_1", Revision: 8,
		State: []byte(`{"version":1,"engine":{}}`), UpdatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("overwrite table state: %v", err)
	}
	storedState, found, err := tableStates.Load(ctx, created.RoomID)
	if err != nil || !found {
		t.Fatalf("load table state: found=%v err=%v", found, err)
	}
	// jsonb 会重排键，按解析后的内容比较而不是比较字符串
	var decodedState map[string]any
	if err := json.Unmarshal(storedState.State, &decodedState); err != nil {
		t.Fatalf("stored state is not valid JSON: %v", err)
	}
	if storedState.Revision != 8 || decodedState["version"] != float64(1) {
		t.Fatalf("table state did not round-trip: %#v %#v", storedState, decodedState)
	}
	if removed, err := tableStates.DeleteOlderThan(ctx, now.Add(2*time.Second)); err != nil || removed != 1 {
		t.Fatalf("stale cleanup removed=%d err=%v", removed, err)
	}
	if _, found, err := tableStates.Load(ctx, created.RoomID); err != nil || found {
		t.Fatalf("cleaned state must be gone: found=%v err=%v", found, err)
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

// 线上事故的回归用例，只有真实库能复现。
//
// 房主弃牌后中途离开，桌上筹码没有退回钱包；重新进入再离开，又丢了一次。根因之一
// 是弃牌中途离开时成员记录被立刻删除，而 PostgreSQL 的桌上筹码就记在
// room_members.table_chips 上：那一行一删，筹码连同记录一起消失且不留流水，本手
// 结算又因为找不到这一行而整手回滚。内存仓储把余额另存一处，所以单元测试全绿。
// 根因之二是离桌退还的幂等编号按「房间 + 用户」固定，同一房间第二次离开被当成
// 重复请求跳过。这里两条都走一遍，并且特意让离开者在这一手里下过注（小盲）。
func TestPostgresFoldedMidHandLeaveKeepsChipsAndSecondLeaveCashesOut(t *testing.T) {
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
	schema := fmt.Sprintf("leave_rejoin_test_%d", time.Now().UnixNano())
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

	now := time.Unix(20_000, 0).UTC()
	accounts, err := account.NewPostgresRepository(database)
	if err != nil {
		t.Fatalf("account repository: %v", err)
	}
	users := []string{"owner", "guest", "third"}
	for index, userID := range users {
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
	for _, userID := range users {
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
	// 入座时间逐次递增：离桌退还的编号按入座时间区分两次入座。
	tick := now
	rooms, err := room.NewService(roomsRepository, hasher, room.ServiceConfig{
		Now: func() time.Time {
			tick = tick.Add(time.Second)
			return tick
		},
		Bankroll: chips,
	})
	if err != nil {
		t.Fatalf("room service: %v", err)
	}
	created, err := rooms.CreateConfigured(ctx, room.Participant{UserID: "owner", DisplayName: "玩家owner"}, room.CreateOptions{
		Preset: room.PresetCasual, SmallBlind: 10, BigBlind: 20,
		MaxBuyIn: 2_000, BuyIn: 1_000, RequestID: "create-room",
	})
	if err != nil {
		t.Fatalf("CreateConfigured: %v", err)
	}
	for _, userID := range []string{"guest", "third"} {
		if _, err := rooms.JoinWithBuyIn(ctx, room.Participant{UserID: userID, DisplayName: "玩家" + userID}, room.JoinOptions{
			Code: created.Code, BuyIn: 1_000, RequestID: "join-" + userID,
		}); err != nil {
			t.Fatalf("JoinWithBuyIn(%s): %v", userID, err)
		}
	}
	ledgerStore, err := ledger.NewPostgresStore(database)
	if err != nil {
		t.Fatalf("ledger store: %v", err)
	}
	historyStore, err := history.NewPostgresStore(database)
	if err != nil {
		t.Fatalf("history store: %v", err)
	}
	tables, err := tablemanager.NewWithConfig(rooms, postgresZeroRandom{}, tablemanager.ManagerConfig{
		Now: func() time.Time { return now }, Ledger: ledgerStore, History: historyStore, Bankroll: chips,
	})
	if err != nil {
		t.Fatalf("table manager: %v", err)
	}
	for _, userID := range users {
		if _, err := tables.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatalf("table join %s: %v", userID, err)
		}
	}
	var started tablemanager.Snapshot
	for _, userID := range users {
		if started, err = tables.SetReady(ctx, userID, true); err != nil {
			t.Fatalf("ready %s: %v", userID, err)
		}
	}
	if started.CurrentAction == nil || started.Phase != holdem.PhasePreflop {
		t.Fatalf("hand did not start: %#v", started)
	}
	handID := started.HandID

	// 枪口位跟注留在局里；下一位是小盲，已经下了注，让他弃牌后离开。
	first := started.CurrentAction.UserID
	_, afterCall, err := tables.SubmitAction(ctx, first, created.RoomID, holdem.ActionRequest{
		ActionID: "call-first", HandID: handID, TableRevision: started.TableRevision, Action: holdem.ActionCall,
	})
	if err != nil || afterCall.CurrentAction == nil {
		t.Fatalf("call: %#v err=%v", afterCall.Phase, err)
	}
	leaver := afterCall.CurrentAction.UserID
	var committed int64
	for _, seat := range afterCall.Seats {
		if seat.UserID == leaver {
			committed = seat.TotalBet
		}
	}
	if committed <= 0 {
		t.Fatalf("the leaver should have posted a blind: %#v", afterCall.Seats)
	}
	_, afterFold, err := tables.SubmitAction(ctx, leaver, created.RoomID, holdem.ActionRequest{
		ActionID: "fold-leaver", HandID: handID, TableRevision: afterCall.TableRevision, Action: holdem.ActionFold,
	})
	if err != nil || afterFold.Phase == holdem.PhaseWaitingNextHand {
		t.Fatalf("fold: phase=%s err=%v", afterFold.Phase, err)
	}
	if closed, err := tables.Leave(ctx, leaver); err != nil || closed {
		t.Fatalf("folded player must be able to leave mid-hand: closed=%v err=%v", closed, err)
	}

	// 成员记录与桌上筹码必须还在：这正是事故里被提前删掉的那一行。
	var heldChips int64
	if err := database.QueryRowContext(ctx,
		`SELECT table_chips FROM room_members WHERE room_id = $1 AND user_id = $2`,
		created.RoomID, leaver,
	).Scan(&heldChips); err != nil {
		t.Fatalf("the member row must survive until settlement: %v", err)
	}
	if heldChips != 1_000 {
		t.Fatalf("table chips before settlement=%d", heldChips)
	}
	if !tables.LeavePending(leaver, created.RoomID) {
		t.Fatal("leave should be pending")
	}

	// 剩下两人把这一手打完。结算必须成功——事故里它因为找不到离开者那一行而回滚。
	snapshot, err := tables.Snapshot(ctx, first, created.RoomID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	for steps := 0; snapshot.Phase != holdem.PhaseWaitingNextHand; steps++ {
		if steps > 20 || snapshot.CurrentAction == nil {
			t.Fatalf("hand did not finish: phase=%s", snapshot.Phase)
		}
		action := holdem.ActionFold
		if snapshot.CurrentAction.Options.CanCheck {
			action = holdem.ActionCheck
		}
		_, snapshot, err = tables.SubmitAction(ctx, snapshot.CurrentAction.UserID, created.RoomID, holdem.ActionRequest{
			ActionID: fmt.Sprintf("finish-%d", steps), HandID: snapshot.HandID,
			TableRevision: snapshot.TableRevision, Action: action,
		})
		if err != nil {
			t.Fatalf("settlement must succeed with a departed player in the hand: %v", err)
		}
	}

	wallet, err := chips.Snapshot(ctx, leaver)
	if err != nil {
		t.Fatalf("leaver snapshot: %v", err)
	}
	if wallet.WalletChips != 5_000-committed || wallet.TableChips != 0 {
		t.Fatalf("leaver must get back exactly the post-fold stack: committed=%d snapshot=%#v", committed, wallet)
	}
	var memberRows int
	if err := database.QueryRowContext(ctx,
		`SELECT count(*) FROM room_members WHERE room_id = $1 AND user_id = $2`, created.RoomID, leaver,
	).Scan(&memberRows); err != nil || memberRows != 0 {
		t.Fatalf("leaver must be removed after settlement: rows=%d err=%v", memberRows, err)
	}
	persisted, found := historyStore.Hand(handID)
	if !found {
		t.Fatal("the hand must be written to history")
	}
	var historyTotal int64
	leaverInHistory := false
	for _, player := range persisted.Players {
		historyTotal += player.Delta
		if player.UserID == leaver && player.Delta == -committed {
			leaverInHistory = true
		}
	}
	if !leaverInHistory || historyTotal != 0 || len(persisted.Players) != 3 {
		t.Fatalf("history players=%#v", persisted.Players)
	}

	// 同一房间重新进入再离开：第二次离开也必须退还。
	if _, err := rooms.JoinWithBuyIn(ctx, room.Participant{UserID: leaver, DisplayName: "玩家" + leaver}, room.JoinOptions{
		Code: created.Code, BuyIn: 1_500, RequestID: "rejoin-" + leaver,
	}); err != nil {
		t.Fatalf("rejoin: %v", err)
	}
	if _, err := tables.Join(ctx, leaver, created.RoomID); err != nil {
		t.Fatalf("rejoin table: %v", err)
	}
	if closed, err := tables.Leave(ctx, leaver); err != nil || closed {
		t.Fatalf("second leave: closed=%v err=%v", closed, err)
	}
	wallet, err = chips.Snapshot(ctx, leaver)
	if err != nil {
		t.Fatalf("leaver snapshot after second leave: %v", err)
	}
	if wallet.WalletChips != 5_000-committed || wallet.TableChips != 0 {
		t.Fatalf("second leave must cash out too: %#v", wallet)
	}
	var cashOuts int
	if err := database.QueryRowContext(ctx,
		`SELECT count(*) FROM bankroll_entries WHERE user_id = $1 AND room_id = $2 AND reason = 'cash_out'`,
		leaver, created.RoomID,
	).Scan(&cashOuts); err != nil || cashOuts != 2 {
		t.Fatalf("expected one cash-out per stay, got %d err=%v", cashOuts, err)
	}
	result, err := chips.RoomResult(ctx, leaver, created.RoomID)
	if err != nil || result.BoughtIn != 2_500 || result.ReturnedToWallet != 2_500-committed || result.Net != -committed {
		t.Fatalf("room result must continue across stays: %#v err=%v", result, err)
	}

	// 全体筹码守恒：只有充值创造过筹码。
	var total int64
	for _, userID := range users {
		position, err := chips.Snapshot(ctx, userID)
		if err != nil {
			t.Fatalf("snapshot %s: %v", userID, err)
		}
		total += position.WalletChips + position.TableChips
	}
	if total != 15_000 {
		t.Fatalf("total chips=%d, expected 15000", total)
	}
}

// 抽水在真实库上的全链路：迁移 000012 加的列能往返，结算与抽水入账在同一事务里完成，
// 管理员多一条 rake 流水，牌谱记下每手抽水，按房间能汇总（房间关闭后房间码仍取得到），
// 聊天表接受 system 类型的公告。内存仓储测不出列名、约束与 SUM 的类型这些问题。
func TestPostgresRakeFlow(t *testing.T) {
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
	schema := fmt.Sprintf("rake_flow_test_%d", time.Now().UnixNano())
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

	now := time.Unix(30_000, 0).UTC()
	accounts, err := account.NewPostgresRepository(database)
	if err != nil {
		t.Fatalf("account repository: %v", err)
	}
	players := []string{"owner", "guest", "third"}
	for index, userID := range append([]string{"admin"}, players...) {
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
	for _, userID := range players {
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
	created, err := rooms.CreateConfigured(ctx, room.Participant{UserID: "owner", DisplayName: "玩家owner"}, room.CreateOptions{
		Preset: room.PresetCasual, SmallBlind: 10, BigBlind: 20,
		MaxBuyIn: 2_000, BuyIn: 1_000, RequestID: "create-room",
	})
	if err != nil {
		t.Fatalf("CreateConfigured: %v", err)
	}
	if created.Rake != (room.RakeSettings{}) {
		t.Fatalf("new rooms must not rake: %#v", created.Rake)
	}
	for _, userID := range []string{"guest", "third"} {
		if _, err := rooms.JoinWithBuyIn(ctx, room.Participant{UserID: userID, DisplayName: "玩家" + userID}, room.JoinOptions{
			Code: created.Code, BuyIn: 1_000, RequestID: "join-" + userID,
		}); err != nil {
			t.Fatalf("JoinWithBuyIn(%s): %v", userID, err)
		}
	}

	// 规则写进五个新列并原样读回；库里的约束拦住越界值
	settings := room.RakeSettings{Enabled: true, BasisPoints: 250, Cap: 100, PostflopEnabled: true, PostflopAmount: 20}
	if _, changed, err := rooms.UpdateRakeSettings(ctx, created.RoomID, settings); err != nil || !changed {
		t.Fatalf("UpdateRakeSettings changed=%v err=%v", changed, err)
	}
	reloaded, err := rooms.Current(ctx, "owner")
	if err != nil || reloaded.Rake != settings {
		t.Fatalf("rake settings did not round-trip: %#v err=%v", reloaded.Rake, err)
	}
	if _, err := database.ExecContext(ctx,
		`UPDATE rooms SET rake_basis_points = 1001 WHERE room_id = $1`, created.RoomID); err == nil {
		t.Fatal("the database must reject a rake above 10%")
	}
	if _, _, err := rooms.UpdateRakeSettings(ctx, created.RoomID, room.RakeSettings{
		Enabled: true, PostflopEnabled: true, PostflopAmount: 21,
	}); err == nil {
		t.Fatal("a postflop charge above one big blind must be rejected")
	}
	open, err := rooms.ListOpen(ctx)
	if err != nil || len(open) != 1 || open[0].RoomID != created.RoomID || len(open[0].Members) != 3 {
		t.Fatalf("ListOpen=%#v err=%v", open, err)
	}

	// 系统公告：聊天表的 kind 约束要接受 system
	chatStore, err := chat.NewPostgresStore(database)
	if err != nil {
		t.Fatalf("chat store: %v", err)
	}
	nextMessage := 0
	chatService, err := chat.NewServiceWithStore(chat.Policy{
		MaximumRunes: 200, MaximumPerWindow: 5, RateWindow: time.Second, HistoryLimit: 50,
	}, func() time.Time { return now }, func() string {
		nextMessage++
		return fmt.Sprintf("rake_flow_message_%d", nextMessage)
	}, chatStore)
	if err != nil {
		t.Fatalf("chat service: %v", err)
	}
	announced, err := chatService.Announce("admin", "系统公告", created.RoomID, "管理员已调整本房间的抽水")
	if err != nil || announced.Kind != chat.KindSystem {
		t.Fatalf("Announce=%#v err=%v", announced, err)
	}
	if history := chatService.History(created.RoomID, 10); len(history) != 1 || history[0].Kind != chat.KindSystem {
		t.Fatalf("chat history=%#v", history)
	}

	// 打一手到摊牌
	ledgerStore, err := ledger.NewPostgresStore(database)
	if err != nil {
		t.Fatalf("ledger store: %v", err)
	}
	historyStore, err := history.NewPostgresStore(database)
	if err != nil {
		t.Fatalf("history store: %v", err)
	}
	tables, err := tablemanager.NewWithConfig(rooms, postgresZeroRandom{}, tablemanager.ManagerConfig{
		Now: func() time.Time { return now }, Ledger: ledgerStore, History: historyStore, Bankroll: chips,
		RakeRecipient: func(context.Context) (string, error) { return "admin", nil },
	})
	if err != nil {
		t.Fatalf("table manager: %v", err)
	}
	for _, userID := range players {
		if _, err := tables.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatalf("table join %s: %v", userID, err)
		}
	}
	var snapshot tablemanager.Snapshot
	for _, userID := range players {
		if snapshot, err = tables.SetReady(ctx, userID, true); err != nil {
			t.Fatalf("ready %s: %v", userID, err)
		}
	}
	handID := snapshot.HandID
	for steps := 0; snapshot.Phase != holdem.PhaseWaitingNextHand; steps++ {
		if steps > 30 || snapshot.CurrentAction == nil {
			t.Fatalf("hand did not finish: phase=%s", snapshot.Phase)
		}
		action := holdem.ActionCall
		if snapshot.CurrentAction.Options.CanCheck {
			action = holdem.ActionCheck
		}
		_, snapshot, err = tables.SubmitAction(ctx, snapshot.CurrentAction.UserID, created.RoomID, holdem.ActionRequest{
			ActionID: fmt.Sprintf("rake-step-%d", steps), HandID: snapshot.HandID,
			TableRevision: snapshot.TableRevision, Action: action,
		})
		if err != nil {
			t.Fatalf("settlement with rake must succeed: %v", err)
		}
	}
	// 三人各投 20，底池 60：2.5% 向下取整是 1，发过翻牌再加 20
	if snapshot.Settlement == nil || snapshot.Settlement.Rake != 21 {
		t.Fatalf("settlement=%#v", snapshot.Settlement)
	}
	admin, err := chips.Snapshot(ctx, "admin")
	if err != nil || admin.WalletChips != 21 {
		t.Fatalf("admin wallet=%#v err=%v", admin, err)
	}
	var rakeRows int
	var rakeAmount int64
	if err := database.QueryRowContext(ctx,
		`SELECT count(*), COALESCE(sum(wallet_delta), 0) FROM bankroll_entries
		 WHERE reason = 'rake' AND user_id = 'admin' AND room_id = $1 AND reference_id = $2`,
		created.RoomID, handID,
	).Scan(&rakeRows, &rakeAmount); err != nil || rakeRows != 1 || rakeAmount != 21 {
		t.Fatalf("rake entries rows=%d amount=%d err=%v", rakeRows, rakeAmount, err)
	}
	// 生产对账脚本（deploy/backup/texas-verify.sql 的 settlement_per_hand）按这个口径判平衡：
	// 同一手的结算流水 table_delta 之和加上抽水流水 wallet_delta 必须为 0
	var unbalanced int
	if err := database.QueryRowContext(ctx,
		`SELECT count(*) FROM (
			SELECT reference_id FROM bankroll_entries
			WHERE reason IN ('hand_settlement', 'rake')
			GROUP BY reference_id
			HAVING COALESCE(sum(table_delta) FILTER (WHERE reason = 'hand_settlement'), 0)
				+ COALESCE(sum(wallet_delta) FILTER (WHERE reason = 'rake'), 0) <> 0
		) AS per_hand`,
	).Scan(&unbalanced); err != nil || unbalanced != 0 {
		t.Fatalf("reconciliation would flag %d hands, err=%v", unbalanced, err)
	}
	persisted, found := historyStore.Hand(handID)
	if !found || persisted.Rake != 21 {
		t.Fatalf("history rake=%d found=%v", persisted.Rake, found)
	}
	var total int64
	for _, userID := range players {
		position, err := chips.Snapshot(ctx, userID)
		if err != nil {
			t.Fatalf("snapshot %s: %v", userID, err)
		}
		total += position.WalletChips + position.TableChips
	}
	if total+admin.WalletChips != 15_000 {
		t.Fatalf("players hold %d and the admin %d, expected 15000 in total", total, admin.WalletChips)
	}

	summary, err := chips.RakeByRoom(ctx)
	if err != nil || len(summary) != 1 || summary[0].RoomID != created.RoomID || summary[0].RoomCode != created.Code ||
		summary[0].Closed || summary[0].Hands != 1 || summary[0].Total != 21 {
		t.Fatalf("rake summary=%#v err=%v", summary, err)
	}
	// 房间关闭后仍能按房间码查到累计
	for _, userID := range players {
		if _, err := tables.Leave(ctx, userID); err != nil {
			t.Fatalf("Leave(%s): %v", userID, err)
		}
	}
	summary, err = chips.RakeByRoom(ctx)
	if err != nil || len(summary) != 1 || summary[0].RoomCode != created.Code || !summary[0].Closed || summary[0].Total != 21 {
		t.Fatalf("rake summary after the room closed=%#v err=%v", summary, err)
	}
	if open, err = rooms.ListOpen(ctx); err != nil || len(open) != 0 {
		t.Fatalf("closed rooms must not be listed: %#v err=%v", open, err)
	}
}
