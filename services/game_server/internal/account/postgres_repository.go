package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresRepository struct {
	database *sql.DB
}

func NewPostgresRepository(database *sql.DB) (*PostgresRepository, error) {
	if database == nil {
		return nil, errors.New("database is required")
	}
	return &PostgresRepository{database: database}, nil
}

func (repository *PostgresRepository) CreateUser(ctx context.Context, user User) error {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin create user: %w", err)
	}
	if user.Role == RoleAdmin {
		if _, err := transaction.ExecContext(ctx, `SELECT pg_advisory_xact_lock(84729103)`); err != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("lock initial administrator: %w", err)
		}
		var exists bool
		if err := transaction.QueryRowContext(
			ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE role = 'admin' AND status <> 'deleted')`,
		).Scan(&exists); err != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("check initial administrator: %w", err)
		}
		if exists {
			_ = transaction.Rollback()
			return ErrAdminExists
		}
	}
	if user.Role == "" {
		user.Role = RolePlayer
	}
	if user.Status == "" {
		user.Status = StatusActive
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO users (
            user_id, username, display_name, password_hash, role, status, created_at, updated_at
         ) VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
		user.UserID, user.Username, user.DisplayName, user.PasswordHash,
		user.Role, user.Status, user.CreatedAt,
	); err != nil {
		_ = transaction.Rollback()
		return postgresAccountError("insert user", err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO account_wallets (user_id, wallet_chips, revision, updated_at)
         VALUES ($1, 0, 0, $2)`,
		user.UserID, user.CreatedAt,
	); err != nil {
		_ = transaction.Rollback()
		return postgresAccountError("initialize wallet", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit create user: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) UserByID(ctx context.Context, userID string) (User, error) {
	return scanUser(repository.database.QueryRowContext(
		ctx,
		`SELECT user_id, username, display_name, role, status, password_hash, created_at
         FROM users WHERE user_id = $1 AND status = 'active'`,
		userID,
	))
}

func (repository *PostgresRepository) UserByUsername(ctx context.Context, username string) (User, error) {
	return scanUser(repository.database.QueryRowContext(
		ctx,
		`SELECT user_id, username, display_name, role, status, password_hash, created_at
         FROM users WHERE lower(username) = lower($1) AND status = 'active'`,
		username,
	))
}

func (repository *PostgresRepository) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := repository.database.QueryContext(
		ctx,
		`SELECT user_id, username, display_name, role, status, password_hash, created_at
         FROM users ORDER BY created_at, user_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return users, nil
}

func (repository *PostgresRepository) UpdateUsername(
	ctx context.Context,
	userID, username string,
	now time.Time,
) error {
	result, err := repository.database.ExecContext(
		ctx,
		`UPDATE users SET username = $2, updated_at = $3
         WHERE user_id = $1 AND status <> 'deleted'`,
		userID, username, now,
	)
	if err != nil {
		return postgresAccountError("update username", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	return nil
}

func (repository *PostgresRepository) UpdateDisplayName(
	ctx context.Context,
	userID, displayName string,
	now time.Time,
) error {
	result, err := repository.database.ExecContext(
		ctx,
		`UPDATE users SET display_name = $2, updated_at = $3
         WHERE user_id = $1 AND status <> 'deleted'`,
		userID, displayName, now,
	)
	if err != nil {
		return fmt.Errorf("update display name: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	return nil
}

func (repository *PostgresRepository) UpdatePassword(ctx context.Context, userID, passwordHash string, now time.Time) error {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password reset: %w", err)
	}
	result, err := transaction.ExecContext(
		ctx,
		`UPDATE users SET password_hash = $2, updated_at = $3
         WHERE user_id = $1 AND status <> 'deleted'`,
		userID, passwordHash, now,
	)
	if err != nil {
		_ = transaction.Rollback()
		return fmt.Errorf("reset password: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		_ = transaction.Rollback()
		return ErrNotFound
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM refresh_sessions WHERE user_id = $1`, userID); err != nil {
		_ = transaction.Rollback()
		return fmt.Errorf("revoke password reset sessions: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit password reset: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) UpdateStatuses(ctx context.Context, actorUserID string, userIDs []string, status Status, now time.Time) error {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin account status update: %w", err)
	}
	for _, userID := range userIDs {
		var role Role
		if err := transaction.QueryRowContext(
			ctx, `SELECT role FROM users WHERE user_id = $1 FOR UPDATE`, userID,
		).Scan(&role); err != nil {
			_ = transaction.Rollback()
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock managed user: %w", err)
		}
		if userID == actorUserID || role == RoleAdmin {
			_ = transaction.Rollback()
			return ErrProtected
		}
	}
	for _, userID := range userIDs {
		if _, err := transaction.ExecContext(
			ctx, `UPDATE users SET status = $2, updated_at = $3 WHERE user_id = $1`,
			userID, status, now,
		); err != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("update account status: %w", err)
		}
		if status != StatusActive {
			if _, err := transaction.ExecContext(ctx, `DELETE FROM refresh_sessions WHERE user_id = $1`, userID); err != nil {
				_ = transaction.Rollback()
				return fmt.Errorf("revoke managed user sessions: %w", err)
			}
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit account status update: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) RegistrationEnabled(ctx context.Context) (bool, error) {
	var enabled bool
	if err := repository.database.QueryRowContext(
		ctx, `SELECT registration_enabled FROM server_settings WHERE singleton = true`,
	).Scan(&enabled); err != nil {
		return false, fmt.Errorf("load registration setting: %w", err)
	}
	return enabled, nil
}

func (repository *PostgresRepository) SetRegistrationEnabled(ctx context.Context, actorUserID string, enabled bool, now time.Time) error {
	_, err := repository.database.ExecContext(
		ctx,
		`UPDATE server_settings SET registration_enabled = $1, updated_by = $2, updated_at = $3
         WHERE singleton = true`,
		enabled, actorUserID, now,
	)
	if err != nil {
		return fmt.Errorf("update registration setting: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) RecordAudit(ctx context.Context, event AuditEvent) error {
	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return fmt.Errorf("encode audit metadata: %w", err)
	}
	_, err = repository.database.ExecContext(
		ctx,
		`INSERT INTO audit_events (
            event_id, actor_user_id, event_type, metadata, created_at
         ) VALUES ($1, $2, $3, $4, $5)`,
		event.EventID, event.ActorUserID, event.EventType, metadata, event.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("record account audit: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) SaveSession(ctx context.Context, session Session) error {
	_, err := repository.database.ExecContext(
		ctx,
		`INSERT INTO refresh_sessions (
            session_id, user_id, access_token_hash, refresh_token_hash,
            access_expires_at, refresh_expires_at, created_at, revoked_at
         ) VALUES ($1, $2, $3, $4, $5, $6, $7, NULL)
         ON CONFLICT (session_id) DO UPDATE SET
            user_id = EXCLUDED.user_id,
            access_token_hash = EXCLUDED.access_token_hash,
            refresh_token_hash = EXCLUDED.refresh_token_hash,
            access_expires_at = EXCLUDED.access_expires_at,
            refresh_expires_at = EXCLUDED.refresh_expires_at,
            created_at = EXCLUDED.created_at,
            revoked_at = NULL`,
		session.SessionID, session.UserID, session.AccessTokenHash, session.RefreshTokenHash,
		session.AccessExpiresAt, session.RefreshExpiresAt, session.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) SessionByAccessHash(ctx context.Context, tokenHash string) (Session, error) {
	return scanSession(repository.database.QueryRowContext(
		ctx,
		`SELECT session_id, user_id, access_token_hash, refresh_token_hash,
                access_expires_at, refresh_expires_at, created_at
         FROM refresh_sessions
         WHERE access_token_hash = $1 AND revoked_at IS NULL`,
		tokenHash,
	))
}

func (repository *PostgresRepository) SessionByRefreshHash(ctx context.Context, tokenHash string) (Session, error) {
	return scanSession(repository.database.QueryRowContext(
		ctx,
		`SELECT session_id, user_id, access_token_hash, refresh_token_hash,
                access_expires_at, refresh_expires_at, created_at
         FROM refresh_sessions
         WHERE refresh_token_hash = $1 AND revoked_at IS NULL`,
		tokenHash,
	))
}

func (repository *PostgresRepository) DeleteSession(ctx context.Context, sessionID string) error {
	if _, err := repository.database.ExecContext(
		ctx, `DELETE FROM refresh_sessions WHERE session_id = $1`, sessionID,
	); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (User, error) {
	var user User
	if err := row.Scan(
		&user.UserID, &user.Username, &user.DisplayName,
		&user.Role, &user.Status, &user.PasswordHash, &user.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("scan user: %w", err)
	}
	return user, nil
}

func scanSession(row rowScanner) (Session, error) {
	var session Session
	if err := row.Scan(
		&session.SessionID, &session.UserID,
		&session.AccessTokenHash, &session.RefreshTokenHash,
		&session.AccessExpiresAt, &session.RefreshExpiresAt, &session.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, fmt.Errorf("scan session: %w", err)
	}
	return session, nil
}

func postgresAccountError(operation string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return fmt.Errorf("%s: %w", operation, ErrConflict)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

var _ Repository = (*PostgresRepository)(nil)
