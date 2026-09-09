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
}
