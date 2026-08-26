package account

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"texas/services/game_server/internal/postgres"
	"texas/services/game_server/migrations"
)

func TestNewPostgresRepositoryRejectsMissingDatabase(t *testing.T) {
	if _, err := NewPostgresRepository(nil); err == nil {
		t.Fatal("expected missing database error")
	}
}

func TestPostgresRepositoryPersistsUsersWalletsAndRotatedSessions(t *testing.T) {
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
	schema := fmt.Sprintf("account_repository_test_%d", time.Now().UnixNano())
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
	repository, err := NewPostgresRepository(database)
	if err != nil {
		t.Fatalf("NewPostgresRepository: %v", err)
	}
	now := time.Unix(1_000, 0).UTC()
	user := User{
		UserID: "usr_1", Username: "Player_1", DisplayName: "玩家一",
		PasswordHash: "password-hash", CreatedAt: now,
	}
	if err := repository.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	loaded, err := repository.UserByUsername(ctx, "PLAYER_1")
	if err != nil || loaded.UserID != user.UserID {
		t.Fatalf("UserByUsername user=%#v err=%v", loaded, err)
	}
	var walletChips int64
	if err := database.QueryRowContext(
		ctx, `SELECT wallet_chips FROM account_wallets WHERE user_id = $1`, user.UserID,
	).Scan(&walletChips); err != nil || walletChips != 0 {
		t.Fatalf("wallet chips=%d err=%v", walletChips, err)
	}
	if err := repository.CreateUser(ctx, User{
		UserID: "usr_2", Username: "player_1", DisplayName: "重复账号",
		PasswordHash: "password-hash", CreatedAt: now,
	}); err == nil {
		t.Fatal("case-insensitive duplicate username was accepted")
	}

	first := Session{
		SessionID: "ses_1", UserID: user.UserID,
		AccessTokenHash: "access-1", RefreshTokenHash: "refresh-1",
		AccessExpiresAt: now.Add(time.Hour), RefreshExpiresAt: now.Add(24 * time.Hour), CreatedAt: now,
	}
	if err := repository.SaveSession(ctx, first); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if loaded, err := repository.SessionByAccessHash(ctx, first.AccessTokenHash); err != nil || loaded.SessionID != first.SessionID {
		t.Fatalf("SessionByAccessHash session=%#v err=%v", loaded, err)
	}
	rotated := first
	rotated.AccessTokenHash = "access-2"
	rotated.RefreshTokenHash = "refresh-2"
	if err := repository.SaveSession(ctx, rotated); err != nil {
		t.Fatalf("rotate session: %v", err)
	}
	if _, err := repository.SessionByAccessHash(ctx, first.AccessTokenHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old access hash error=%v", err)
	}
	if loaded, err := repository.SessionByRefreshHash(ctx, rotated.RefreshTokenHash); err != nil || loaded.SessionID != rotated.SessionID {
		t.Fatalf("SessionByRefreshHash session=%#v err=%v", loaded, err)
	}
	if err := repository.DeleteSession(ctx, rotated.SessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := repository.SessionByRefreshHash(ctx, rotated.RefreshTokenHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted refresh hash error=%v", err)
	}
}
