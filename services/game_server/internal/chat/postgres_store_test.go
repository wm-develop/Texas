package chat

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"texas/services/game_server/internal/postgres"
	"texas/services/game_server/migrations"
)

func TestPostgresStorePersistsMuteAndAudit(t *testing.T) {
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
	schema := fmt.Sprintf("chat_moderation_test_%d", time.Now().UnixNano())
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
	now := time.Unix(1_000, 0).UTC()
	if _, err := database.ExecContext(
		ctx,
		`INSERT INTO users (
		 user_id, username, display_name, password_hash, role, created_at, updated_at
		) VALUES
		 ('usr_admin', 'admin', '管理员', 'hash', 'admin', $1, $1),
		 ('usr_player', 'player', '玩家', 'hash', 'player', $1, $1)`,
		now,
	); err != nil {
		t.Fatalf("insert users: %v", err)
	}
	store, err := NewPostgresStore(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetMuted(ModerationChange{
		AuditEventID: "aud_chat_1", ActorUserID: "usr_admin",
		TargetUserID: "usr_player", Muted: true, ChangedAt: now,
	}); err != nil {
		t.Fatalf("SetMuted: %v", err)
	}
	reloaded, err := NewPostgresStore(database)
	if err != nil {
		t.Fatal(err)
	}
	if muted, err := reloaded.IsMuted("usr_player"); err != nil || !muted {
		t.Fatalf("persisted mute=%v err=%v", muted, err)
	}
	var eventType string
	var targetUserID string
	if err := database.QueryRowContext(
		ctx,
		`SELECT event_type, metadata->>'targetUserId'
		 FROM audit_events WHERE event_id = 'aud_chat_1'`,
	).Scan(&eventType, &targetUserID); err != nil {
		t.Fatalf("load audit: %v", err)
	}
	if eventType != "admin.chat_muted" || targetUserID != "usr_player" {
		t.Fatalf("audit event=%q target=%q", eventType, targetUserID)
	}
	if err := reloaded.SetMuted(ModerationChange{
		AuditEventID: "aud_chat_2", ActorUserID: "usr_admin",
		TargetUserID: "usr_player", Muted: false, ChangedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("clear mute: %v", err)
	}
	if muted, err := store.IsMuted("usr_player"); err != nil || muted {
		t.Fatalf("mute after clear=%v err=%v", muted, err)
	}
}
