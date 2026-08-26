package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

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
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO users (
            user_id, username, display_name, password_hash, created_at, updated_at
         ) VALUES ($1, $2, $3, $4, $5, $5)`,
		user.UserID, user.Username, user.DisplayName, user.PasswordHash, user.CreatedAt,
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
		`SELECT user_id, username, display_name, password_hash, created_at
         FROM users WHERE user_id = $1 AND status = 'active'`,
		userID,
	))
}

func (repository *PostgresRepository) UserByUsername(ctx context.Context, username string) (User, error) {
	return scanUser(repository.database.QueryRowContext(
		ctx,
		`SELECT user_id, username, display_name, password_hash, created_at
         FROM users WHERE lower(username) = lower($1) AND status = 'active'`,
		username,
	))
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
		&user.UserID, &user.Username, &user.DisplayName, &user.PasswordHash, &user.CreatedAt,
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
