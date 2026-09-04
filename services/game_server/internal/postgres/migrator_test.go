package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"texas/services/game_server/migrations"
)

func TestEmbeddedMigrationCatalogIsOrderedAndReversible(t *testing.T) {
	migrator, err := NewMigrator(migrations.Files)
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	versions := migrator.Versions()
	if len(versions) != 10 || versions[0] != 1 || versions[1] != 2 || versions[2] != 3 || versions[3] != 4 || versions[4] != 5 || versions[5] != 6 || versions[6] != 7 || versions[7] != 8 || versions[8] != 9 || versions[9] != 10 {
		t.Fatalf("versions=%v", versions)
	}
}

func TestMigrationCatalogRejectsMissingDownFile(t *testing.T) {
	_, err := NewMigrator(fstest.MapFS{
		"000001_example.up.sql": {Data: []byte("CREATE TABLE example (id bigint PRIMARY KEY);")},
	})
	if err == nil {
		t.Fatal("expected missing down migration error")
	}
}

func TestMigrationCatalogRejectsEmbeddedTransactionControl(t *testing.T) {
	_, err := NewMigrator(fstest.MapFS{
		"000001_example.up.sql":   {Data: []byte("BEGIN;\nCREATE TABLE example (id bigint PRIMARY KEY);\nCOMMIT;")},
		"000001_example.down.sql": {Data: []byte("DROP TABLE example;")},
	})
	if err == nil {
		t.Fatal("expected transaction control error")
	}
}

func TestMigratorUpgradeRepeatAndRollbackAgainstPostgres(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	database.SetMaxOpenConns(1)
	schema := fmt.Sprintf("migration_test_%d", time.Now().UnixNano())
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
	migrator, err := NewMigrator(migrations.Files)
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	// 版本数以嵌入目录为准：写死数字会在每次新增迁移后过期——这条用例只有
	// 配了 TEST_DATABASE_URL 才跑，过期了很久都没人发现。
	total := len(migrator.Versions())
	if count, err := migrator.Up(ctx, database); err != nil || count != total {
		t.Fatalf("first Up count=%d want=%d err=%v", count, total, err)
	}
	if count, err := migrator.Up(ctx, database); err != nil || count != 0 {
		t.Fatalf("repeated Up count=%d err=%v", count, err)
	}
	var usersTable sql.NullString
	if err := database.QueryRowContext(ctx, `SELECT to_regclass('users')`).Scan(&usersTable); err != nil || !usersTable.Valid {
		t.Fatalf("users table=%#v err=%v", usersTable, err)
	}
	if count, err := migrator.Down(ctx, database, total); err != nil || count != total {
		t.Fatalf("Down count=%d want=%d err=%v", count, total, err)
	}
	if err := database.QueryRowContext(ctx, `SELECT to_regclass('users')`).Scan(&usersTable); err != nil || usersTable.Valid {
		t.Fatalf("users table after rollback=%#v err=%v", usersTable, err)
	}
}
