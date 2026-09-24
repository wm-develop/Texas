package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type PostgresStore struct {
	database *sql.DB
}

func NewPostgresStore(database *sql.DB) (*PostgresStore, error) {
	if database == nil {
		return nil, errors.New("database is required")
	}
	return &PostgresStore{database: database}, nil
}

func (store *PostgresStore) Settings(ctx context.Context) (Settings, error) {
	var settings Settings
	err := store.database.QueryRowContext(ctx,
		`SELECT enabled, daily_limit_per_user, max_in_flight_per_user, monthly_token_budget
		 FROM review_settings WHERE singleton = true`,
	).Scan(&settings.Enabled, &settings.DailyLimitPerUser, &settings.MaxInFlightPerUser, &settings.MonthlyTokenBudget)
	if err != nil {
		return Settings{}, fmt.Errorf("read review settings: %w", err)
	}
	return settings, nil
}

func (store *PostgresStore) SaveSettings(ctx context.Context, settings Settings, actorUserID string, now time.Time) error {
	result, err := store.database.ExecContext(ctx,
		`UPDATE review_settings SET enabled = $1, daily_limit_per_user = $2, max_in_flight_per_user = $3,
		 monthly_token_budget = $4, updated_by = $5, updated_at = $6 WHERE singleton = true`,
		settings.Enabled, settings.DailyLimitPerUser, settings.MaxInFlightPerUser, settings.MonthlyTokenBudget,
		actorUserID, now,
	)
	if err != nil {
		return fmt.Errorf("save review settings: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return fmt.Errorf("save review settings: %d rows, %v", affected, err)
	}
	return nil
}

const accessColumns = `user_id, granted_by, granted_at, daily_limit, max_in_flight`

func scanAccess(row interface{ Scan(...any) error }) (Access, error) {
	var value Access
	var daily, inFlight sql.NullInt64
	if err := row.Scan(&value.UserID, &value.GrantedBy, &value.GrantedAt, &daily, &inFlight); err != nil {
		return Access{}, err
	}
	if daily.Valid {
		limit := int(daily.Int64)
		value.DailyLimit = &limit
	}
	if inFlight.Valid {
		limit := int(inFlight.Int64)
		value.MaxInFlight = &limit
	}
	return value, nil
}

func (store *PostgresStore) AccessFor(ctx context.Context, userID string) (Access, error) {
	value, err := scanAccess(store.database.QueryRowContext(ctx,
		`SELECT `+accessColumns+` FROM review_access WHERE user_id = $1`, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return Access{}, ErrNotFound
	}
	if err != nil {
		return Access{}, fmt.Errorf("read review access: %w", err)
	}
	return value, nil
}

func (store *PostgresStore) SetAccess(ctx context.Context, userID string, granted bool, actorUserID string, now time.Time) error {
	var err error
	if granted {
		_, err = store.database.ExecContext(ctx,
			`INSERT INTO review_access (user_id, granted_by, granted_at) VALUES ($1, $2, $3)
			 ON CONFLICT (user_id) DO NOTHING`,
			userID, actorUserID, now,
		)
	} else {
		_, err = store.database.ExecContext(ctx, `DELETE FROM review_access WHERE user_id = $1`, userID)
	}
	if err != nil {
		return fmt.Errorf("save review access: %w", err)
	}
	return nil
}

func (store *PostgresStore) SetLimits(ctx context.Context, userID string, limits UserLimits) error {
	nullable := func(value *int) any {
		if value == nil {
			return nil
		}
		return *value
	}
	result, err := store.database.ExecContext(ctx,
		`UPDATE review_access SET daily_limit = $2, max_in_flight = $3 WHERE user_id = $1`,
		userID, nullable(limits.DailyLimit), nullable(limits.MaxInFlight),
	)
	if err != nil {
		return fmt.Errorf("save review limits: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (store *PostgresStore) AccessList(ctx context.Context) ([]Access, error) {
	rows, err := store.database.QueryContext(ctx,
		`SELECT `+accessColumns+` FROM review_access ORDER BY granted_at, user_id`)
	if err != nil {
		return nil, fmt.Errorf("list review access: %w", err)
	}
	defer rows.Close()
	result := []Access{}
	for rows.Next() {
		value, err := scanAccess(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

const reviewColumns = `review_id, hand_id, user_id, prompt_version, status, model, result, error,
	input_tokens, output_tokens, created_at, requested_at, finished_at, attempts, next_attempt_at`

func scanReview(row interface{ Scan(...any) error }) (Review, error) {
	var value Review
	var result []byte
	var finished, next sql.NullTime
	if err := row.Scan(
		&value.ReviewID, &value.HandID, &value.UserID, &value.PromptVersion, &value.Status, &value.Model,
		&result, &value.Error, &value.InputTokens, &value.OutputTokens, &value.CreatedAt,
		&value.RequestedAt, &finished, &value.Attempts, &next,
	); err != nil {
		return Review{}, err
	}
	if len(result) > 0 && string(result) != "null" {
		var decoded Result
		if err := json.Unmarshal(result, &decoded); err != nil {
			return Review{}, fmt.Errorf("decode review result: %w", err)
		}
		value.Result = &decoded
	}
	if finished.Valid {
		value.FinishedAt = &finished.Time
	}
	if next.Valid {
		value.NextAttemptAt = &next.Time
	}
	return value, nil
}

func (store *PostgresStore) Find(ctx context.Context, userID, handID, promptVersion string) (Review, error) {
	value, err := scanReview(store.database.QueryRowContext(ctx,
		`SELECT `+reviewColumns+` FROM hand_reviews WHERE user_id = $1 AND hand_id = $2 AND prompt_version = $3`,
		userID, handID, promptVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Review{}, ErrNotFound
	}
	return value, err
}

func (store *PostgresStore) FindLatestDone(ctx context.Context, userID, handID string) (Review, error) {
	value, err := scanReview(store.database.QueryRowContext(ctx,
		`SELECT `+reviewColumns+` FROM hand_reviews
		 WHERE user_id = $1 AND hand_id = $2 AND status = 'done' AND finished_at IS NOT NULL
		 ORDER BY finished_at DESC, review_id DESC LIMIT 1`,
		userID, handID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Review{}, ErrNotFound
	}
	return value, err
}

func (store *PostgresStore) DoneHands(ctx context.Context, userID string, handIDs []string) (map[string]bool, error) {
	result := map[string]bool{}
	if len(handIDs) == 0 {
		return result, nil
	}
	rows, err := store.database.QueryContext(ctx,
		`SELECT DISTINCT hand_id FROM hand_reviews
		 WHERE user_id = $1 AND hand_id = ANY($2) AND status = 'done'`,
		userID, handIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("list reviewed hands: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var handID string
		if err := rows.Scan(&handID); err != nil {
			return nil, fmt.Errorf("list reviewed hands: %w", err)
		}
		result[handID] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list reviewed hands: %w", err)
	}
	return result, nil
}

func (store *PostgresStore) Create(ctx context.Context, value Review) error {
	result, err := store.database.ExecContext(ctx,
		`INSERT INTO hand_reviews (review_id, hand_id, user_id, prompt_version, status, created_at, requested_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (hand_id, user_id, prompt_version) DO NOTHING`,
		value.ReviewID, value.HandID, value.UserID, value.PromptVersion, value.Status, value.CreatedAt, value.RequestedAt,
	)
	if err != nil {
		return fmt.Errorf("create review: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("create review: %w", err)
	}
	if affected == 0 {
		return ErrExists
	}
	return nil
}

func (store *PostgresStore) Requeue(ctx context.Context, reviewID string, now time.Time) error {
	result, err := store.database.ExecContext(ctx,
		`UPDATE hand_reviews SET status = 'queued', requested_at = $2, error = '', finished_at = NULL,
		 attempts = 0, next_attempt_at = NULL
		 WHERE review_id = $1 AND status = 'failed'`,
		reviewID, now,
	)
	if err != nil {
		return fmt.Errorf("requeue review: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimNext 用 FOR UPDATE SKIP LOCKED 取任务：几个工作协程同时领取时不会抢到
// 同一条。
func (store *PostgresStore) ClaimNext(ctx context.Context, now time.Time) (Review, bool, error) {
	value, err := scanReview(store.database.QueryRowContext(ctx,
		`UPDATE hand_reviews SET status = 'running'
		 WHERE review_id = (
		   SELECT review_id FROM hand_reviews
		   WHERE status = 'queued' AND (next_attempt_at IS NULL OR next_attempt_at <= $1)
		   ORDER BY requested_at, review_id LIMIT 1 FOR UPDATE SKIP LOCKED
		 )
		 RETURNING `+reviewColumns,
		now,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Review{}, false, nil
	}
	if err != nil {
		return Review{}, false, fmt.Errorf("claim review: %w", err)
	}
	return value, true, nil
}

func (store *PostgresStore) Finish(ctx context.Context, reviewID, status, model string, result *Result, failure string,
	inputTokens, outputTokens int64, now time.Time,
) error {
	var encoded any
	if result != nil {
		data, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("encode review result: %w", err)
		}
		encoded = string(data)
	}
	outcome, err := store.database.ExecContext(ctx,
		`UPDATE hand_reviews SET status = $2, model = $3, result = $4::jsonb, error = $5,
		 input_tokens = input_tokens + $6, output_tokens = output_tokens + $7, finished_at = $8,
		 next_attempt_at = NULL
		 WHERE review_id = $1`,
		reviewID, status, model, encoded, failure, inputTokens, outputTokens, now,
	)
	if err != nil {
		return fmt.Errorf("finish review: %w", err)
	}
	if affected, err := outcome.RowsAffected(); err != nil || affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (store *PostgresStore) RetryLater(ctx context.Context, reviewID string, next time.Time, inputTokens, outputTokens int64) error {
	outcome, err := store.database.ExecContext(ctx,
		`UPDATE hand_reviews SET status = 'queued', attempts = attempts + 1, next_attempt_at = $2,
		 input_tokens = input_tokens + $3, output_tokens = output_tokens + $4
		 WHERE review_id = $1 AND status = 'running'`,
		reviewID, next, inputTokens, outputTokens,
	)
	if err != nil {
		return fmt.Errorf("retry review later: %w", err)
	}
	if affected, err := outcome.RowsAffected(); err != nil || affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (store *PostgresStore) RequeueRunning(ctx context.Context) error {
	if _, err := store.database.ExecContext(ctx,
		`UPDATE hand_reviews SET status = 'queued' WHERE status = 'running'`); err != nil {
		return fmt.Errorf("requeue running reviews: %w", err)
	}
	return nil
}

func (store *PostgresStore) FailIfRunning(ctx context.Context, reviewID, failure string, now time.Time) error {
	result, err := store.database.ExecContext(ctx,
		`UPDATE hand_reviews SET status = 'failed', error = $2, result = NULL, finished_at = $3, next_attempt_at = NULL
		 WHERE review_id = $1 AND status = 'running'`,
		reviewID, failure, now,
	)
	if err != nil {
		return fmt.Errorf("fail running review: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (store *PostgresStore) FailQueued(ctx context.Context, failure string, now time.Time) (int, error) {
	result, err := store.database.ExecContext(ctx,
		`UPDATE hand_reviews SET status = 'failed', error = $1, finished_at = $2, next_attempt_at = NULL
		 WHERE status = 'queued'`,
		failure, now,
	)
	if err != nil {
		return 0, fmt.Errorf("fail queued reviews: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("fail queued reviews: %w", err)
	}
	return int(affected), nil
}

func (store *PostgresStore) CountRequests(ctx context.Context, userID string, since time.Time) (int, error) {
	var count int
	err := store.database.QueryRowContext(ctx,
		`SELECT count(*) FROM hand_reviews WHERE user_id = $1 AND status <> 'failed' AND requested_at >= $2`, userID, since,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count review requests: %w", err)
	}
	return count, nil
}

func (store *PostgresStore) CountInFlight(ctx context.Context, userID string) (int, error) {
	var count int
	err := store.database.QueryRowContext(ctx,
		`SELECT count(*) FROM hand_reviews WHERE user_id = $1 AND status IN ('queued', 'running')`, userID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count reviews in flight: %w", err)
	}
	return count, nil
}

func (store *PostgresStore) Usage(ctx context.Context, now time.Time) (Usage, error) {
	var usage Usage
	err := store.database.QueryRowContext(ctx,
		`SELECT
		   count(*) FILTER (WHERE requested_at >= $1),
		   count(*) FILTER (WHERE requested_at >= $2),
		   COALESCE(sum(input_tokens + output_tokens) FILTER (WHERE COALESCE(finished_at, requested_at) >= $2), 0)
		 FROM hand_reviews`,
		now.Add(-24*time.Hour), now.Add(-30*24*time.Hour),
	).Scan(&usage.Requests24h, &usage.Requests30d, &usage.Tokens30d)
	if err != nil {
		return Usage{}, fmt.Errorf("read review usage: %w", err)
	}
	return usage, nil
}

var _ Store = (*PostgresStore)(nil)
