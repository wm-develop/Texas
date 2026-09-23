package history

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"texas/services/game_server/internal/postgres"
	"texas/services/game_server/migrations"
)

// 生产用的是 Postgres 实现，而它的按接收者裁剪此前从未在真实库上验证过：
// history 包没有任何集成测试，内存实现的绿灯说明不了 SQL 那条路径。
func TestPostgresStoreRedactsRecentHands(t *testing.T) {
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
	schema := fmt.Sprintf("history_store_test_%d", time.Now().UnixNano())
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

	// hand_players.user_id 与 hand_actions.user_id 都有指向 users 的外键，
	// 契约里的三个玩家必须先在库里存在。
	for _, userID := range contractUserIDs {
		if _, err := database.ExecContext(
			ctx,
			`INSERT INTO users (user_id, username, display_name, password_hash)
			 VALUES ($1, $1, $1, 'hash')`,
			userID,
		); err != nil {
			t.Fatalf("create user %s: %v", userID, err)
		}
	}

	store, err := NewPostgresStore(database)
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	runRecentForPlayerContract(t, store)
	runPaginationContract(t, store)

	// 迁移 000013 给旧牌谱补盲注额：退回到 000012、按旧结构写一手，再升上去。
	// 退几步按之后又加了多少迁移算，不能写死成 1
	steps := 0
	for _, version := range migrator.Versions() {
		if version > 12 {
			steps++
		}
	}
	if _, err := migrator.Down(ctx, database, steps); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if _, err := database.ExecContext(ctx,
		`INSERT INTO rooms (room_id, room_code, owner_user_id, preset, max_players,
		 small_blind, big_blind, max_buy_in, action_seconds, status)
		 VALUES ('room_legacy', '222222', 'me', 'standard', 6, 25, 50, 5000, 30, 'closed')`); err != nil {
		t.Fatalf("legacy room: %v", err)
	}
	if _, err := database.ExecContext(ctx,
		`INSERT INTO hands (hand_id, room_id, room_code, dealer_seat, showdown, started_at, ended_at)
		 VALUES ('legacy_hand', 'room_legacy', '222222', 1, false, now(), now())`); err != nil {
		t.Fatalf("legacy hand: %v", err)
	}
	if _, err := migrator.Up(ctx, database); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
	var smallBlind, bigBlind int64
	var smallSeat, bigSeat int
	if err := database.QueryRowContext(ctx,
		`SELECT small_blind, big_blind, small_blind_seat, big_blind_seat FROM hands WHERE hand_id = 'legacy_hand'`,
	).Scan(&smallBlind, &bigBlind, &smallSeat, &bigSeat); err != nil {
		t.Fatalf("read legacy hand: %v", err)
	}
	if smallBlind != 25 || bigBlind != 50 || smallSeat != 0 || bigSeat != 0 {
		t.Fatalf("旧牌谱的盲注应当从已关闭的房间补上、座位留 0：%d/%d seats %d/%d", smallBlind, bigBlind, smallSeat, bigSeat)
	}
}
