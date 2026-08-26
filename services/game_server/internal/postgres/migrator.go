package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

const migrationTableSQL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     bigint PRIMARY KEY,
    name        text NOT NULL,
    checksum    text NOT NULL,
    applied_at  timestamptz NOT NULL DEFAULT now()
)`

const migrationAdvisoryLock int64 = 0x5445584153

type migration struct {
	version  int64
	name     string
	upSQL    string
	downSQL  string
	checksum string
}

type Migrator struct {
	migrations []migration
	byVersion  map[int64]migration
}

func NewMigrator(files fs.FS) (*Migrator, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	type pair struct {
		name string
		up   string
		down string
	}
	pairs := make(map[int64]pair)
	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".up.sql") && !strings.HasSuffix(entry.Name(), ".down.sql")) {
			continue
		}
		version, name, direction, err := parseMigrationName(entry.Name())
		if err != nil {
			return nil, err
		}
		content, err := fs.ReadFile(files, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		if containsTransactionControl(string(content)) {
			return nil, fmt.Errorf("migration %s must not contain BEGIN or COMMIT", entry.Name())
		}
		value := pairs[version]
		if value.name != "" && value.name != name {
			return nil, fmt.Errorf("migration version %d has conflicting names", version)
		}
		value.name = name
		if direction == "up" {
			if value.up != "" {
				return nil, fmt.Errorf("migration version %d has duplicate up files", version)
			}
			value.up = string(content)
		} else {
			if value.down != "" {
				return nil, fmt.Errorf("migration version %d has duplicate down files", version)
			}
			value.down = string(content)
		}
		pairs[version] = value
	}
	if len(pairs) == 0 {
		return nil, errors.New("no migrations found")
	}
	versions := make([]int64, 0, len(pairs))
	for version := range pairs {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(left, right int) bool { return versions[left] < versions[right] })
	migrator := &Migrator{
		migrations: make([]migration, 0, len(versions)),
		byVersion:  make(map[int64]migration, len(versions)),
	}
	for _, version := range versions {
		pair := pairs[version]
		if strings.TrimSpace(pair.up) == "" || strings.TrimSpace(pair.down) == "" {
			return nil, fmt.Errorf("migration version %d must have matching up and down files", version)
		}
		digest := sha256.Sum256([]byte(pair.up))
		value := migration{
			version: version, name: pair.name, upSQL: pair.up, downSQL: pair.down,
			checksum: hex.EncodeToString(digest[:]),
		}
		migrator.migrations = append(migrator.migrations, value)
		migrator.byVersion[version] = value
	}
	return migrator, nil
}

func (migrator *Migrator) Versions() []int64 {
	result := make([]int64, 0, len(migrator.migrations))
	for _, value := range migrator.migrations {
		result = append(result, value.version)
	}
	return result
}

func (migrator *Migrator) Validate(ctx context.Context, database *sql.DB) error {
	var tableName sql.NullString
	if err := database.QueryRowContext(ctx, `SELECT to_regclass('schema_migrations')::text`).Scan(&tableName); err != nil {
		return fmt.Errorf("locate schema migrations: %w", err)
	}
	if !tableName.Valid {
		return errors.New("database schema is not initialized; run cmd/migrate up")
	}
	rows, err := database.QueryContext(
		ctx, `SELECT version, name, checksum FROM schema_migrations ORDER BY version`,
	)
	if err != nil {
		return fmt.Errorf("load schema migrations: %w", err)
	}
	defer rows.Close()
	applied := make(map[int64]migration, len(migrator.migrations))
	for rows.Next() {
		var version int64
		var name, checksum string
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			return fmt.Errorf("scan schema migration: %w", err)
		}
		expected, exists := migrator.byVersion[version]
		if !exists {
			return fmt.Errorf("database contains unknown migration version %d", version)
		}
		if expected.name != name || expected.checksum != checksum {
			return fmt.Errorf("migration version %d differs from the embedded catalog", version)
		}
		applied[version] = expected
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate schema migrations: %w", err)
	}
	for _, expected := range migrator.migrations {
		if _, exists := applied[expected.version]; !exists {
			return fmt.Errorf("migration version %d is not applied; run cmd/migrate up", expected.version)
		}
	}
	return nil
}

func (migrator *Migrator) Up(ctx context.Context, database *sql.DB) (int, error) {
	if err := ensureMigrationTable(ctx, database); err != nil {
		return 0, err
	}
	applied, err := loadAppliedMigrations(ctx, database)
	if err != nil {
		return 0, err
	}
	if err := migrator.validateApplied(applied); err != nil {
		return 0, err
	}
	appliedVersions := make(map[int64]struct{}, len(applied))
	for _, value := range applied {
		appliedVersions[value.version] = struct{}{}
	}
	count := 0
	for _, value := range migrator.migrations {
		if _, exists := appliedVersions[value.version]; exists {
			continue
		}
		transaction, err := database.BeginTx(ctx, nil)
		if err != nil {
			return count, fmt.Errorf("begin migration %d: %w", value.version, err)
		}
		if _, err := transaction.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationAdvisoryLock); err != nil {
			_ = transaction.Rollback()
			return count, fmt.Errorf("lock migration %d: %w", value.version, err)
		}
		var existingName, existingChecksum string
		err = transaction.QueryRowContext(
			ctx, `SELECT name, checksum FROM schema_migrations WHERE version = $1`, value.version,
		).Scan(&existingName, &existingChecksum)
		if err == nil {
			if existingName != value.name || existingChecksum != value.checksum {
				_ = transaction.Rollback()
				return count, fmt.Errorf("applied migration %d does not match the embedded migration", value.version)
			}
			if err := transaction.Commit(); err != nil {
				return count, fmt.Errorf("commit migration check %d: %w", value.version, err)
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			_ = transaction.Rollback()
			return count, fmt.Errorf("check migration %d: %w", value.version, err)
		}
		if _, err := transaction.ExecContext(ctx, value.upSQL); err != nil {
			_ = transaction.Rollback()
			return count, fmt.Errorf("apply migration %d: %w", value.version, err)
		}
		if _, err := transaction.ExecContext(
			ctx,
			`INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`,
			value.version, value.name, value.checksum,
		); err != nil {
			_ = transaction.Rollback()
			return count, fmt.Errorf("record migration %d: %w", value.version, err)
		}
		if err := transaction.Commit(); err != nil {
			return count, fmt.Errorf("commit migration %d: %w", value.version, err)
		}
		count++
	}
	return count, nil
}

func (migrator *Migrator) Down(ctx context.Context, database *sql.DB, steps int) (int, error) {
	if steps <= 0 {
		return 0, errors.New("down migration steps must be positive")
	}
	if err := ensureMigrationTable(ctx, database); err != nil {
		return 0, err
	}
	count := 0
	for count < steps {
		transaction, err := database.BeginTx(ctx, nil)
		if err != nil {
			return count, fmt.Errorf("begin rollback: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationAdvisoryLock); err != nil {
			_ = transaction.Rollback()
			return count, fmt.Errorf("lock rollback: %w", err)
		}
		var appliedValue appliedMigration
		err = transaction.QueryRowContext(
			ctx, `SELECT version, name, checksum FROM schema_migrations ORDER BY version DESC LIMIT 1`,
		).Scan(&appliedValue.version, &appliedValue.name, &appliedValue.checksum)
		if errors.Is(err, sql.ErrNoRows) {
			if err := transaction.Commit(); err != nil {
				return count, fmt.Errorf("commit empty rollback: %w", err)
			}
			break
		}
		if err != nil {
			_ = transaction.Rollback()
			return count, fmt.Errorf("query rollback migration: %w", err)
		}
		if err := migrator.validateApplied([]appliedMigration{appliedValue}); err != nil {
			_ = transaction.Rollback()
			return count, err
		}
		value := migrator.byVersion[appliedValue.version]
		if _, err := transaction.ExecContext(ctx, value.downSQL); err != nil {
			_ = transaction.Rollback()
			return count, fmt.Errorf("rollback migration %d: %w", value.version, err)
		}
		if _, err := transaction.ExecContext(
			ctx, `DELETE FROM schema_migrations WHERE version = $1`, value.version,
		); err != nil {
			_ = transaction.Rollback()
			return count, fmt.Errorf("remove migration %d: %w", value.version, err)
		}
		if err := transaction.Commit(); err != nil {
			return count, fmt.Errorf("commit rollback %d: %w", value.version, err)
		}
		count++
	}
	return count, nil
}

type appliedMigration struct {
	version  int64
	name     string
	checksum string
}

func ensureMigrationTable(ctx context.Context, database *sql.DB) error {
	if database == nil {
		return errors.New("database is required")
	}
	if _, err := database.ExecContext(ctx, migrationTableSQL); err != nil {
		return fmt.Errorf("ensure schema migrations table: %w", err)
	}
	return nil
}

func loadAppliedMigrations(ctx context.Context, database *sql.DB) ([]appliedMigration, error) {
	rows, err := database.QueryContext(
		ctx, `SELECT version, name, checksum FROM schema_migrations ORDER BY version`,
	)
	if err != nil {
		return nil, fmt.Errorf("query applied migrations: %w", err)
	}
	defer rows.Close()
	result := make([]appliedMigration, 0)
	for rows.Next() {
		var value appliedMigration
		if err := rows.Scan(&value.version, &value.name, &value.checksum); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}
	return result, nil
}

func (migrator *Migrator) validateApplied(applied []appliedMigration) error {
	for _, appliedValue := range applied {
		value, exists := migrator.byVersion[appliedValue.version]
		if !exists {
			return fmt.Errorf("applied migration %d is missing from the binary", appliedValue.version)
		}
		if value.name != appliedValue.name || value.checksum != appliedValue.checksum {
			return fmt.Errorf("applied migration %d does not match the embedded migration", appliedValue.version)
		}
	}
	return nil
}

func parseMigrationName(fileName string) (int64, string, string, error) {
	direction := ""
	base := ""
	switch {
	case strings.HasSuffix(fileName, ".up.sql"):
		direction = "up"
		base = strings.TrimSuffix(fileName, ".up.sql")
	case strings.HasSuffix(fileName, ".down.sql"):
		direction = "down"
		base = strings.TrimSuffix(fileName, ".down.sql")
	default:
		return 0, "", "", fmt.Errorf("invalid migration file name %s", fileName)
	}
	versionText, name, found := strings.Cut(base, "_")
	if !found || strings.TrimSpace(name) == "" {
		return 0, "", "", fmt.Errorf("invalid migration file name %s", fileName)
	}
	version, err := strconv.ParseInt(versionText, 10, 64)
	if err != nil || version <= 0 {
		return 0, "", "", fmt.Errorf("invalid migration version in %s", fileName)
	}
	return version, name, direction, nil
}

func containsTransactionControl(sqlText string) bool {
	upper := strings.ToUpper(sqlText)
	for _, line := range strings.Split(upper, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "BEGIN;" || trimmed == "COMMIT;" || strings.HasPrefix(trimmed, "START TRANSACTION") {
			return true
		}
	}
	return false
}
