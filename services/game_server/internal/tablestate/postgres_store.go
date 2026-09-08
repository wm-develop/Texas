package tablestate

import (
	"context"
	"database/sql"
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

// Save 覆盖同一房间的旧快照。一手牌里每个动作都会调用一次，只保留最新的。
func (store *PostgresStore) Save(ctx context.Context, record Record) error {
	if err := validate(record); err != nil {
		return err
	}
	updatedAt := record.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	_, err := store.database.ExecContext(
		ctx,
		`INSERT INTO table_states (room_id, hand_id, revision, state, updated_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (room_id) DO UPDATE SET
		     hand_id = EXCLUDED.hand_id,
		     revision = EXCLUDED.revision,
		     state = EXCLUDED.state,
		     updated_at = EXCLUDED.updated_at`,
		record.RoomID, record.HandID, int64(record.Revision), record.State, updatedAt,
	)
	if err != nil {
		return fmt.Errorf("save table state: %w", err)
	}
	return nil
}

func (store *PostgresStore) Load(ctx context.Context, roomID string) (Record, bool, error) {
	record := Record{RoomID: roomID}
	var revision int64
	err := store.database.QueryRowContext(
		ctx,
		`SELECT hand_id, revision, state, updated_at FROM table_states WHERE room_id = $1`,
		roomID,
	).Scan(&record.HandID, &revision, &record.State, &record.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("load table state: %w", err)
	}
	record.Revision = uint64(revision)
	return record, true, nil
}

func (store *PostgresStore) Delete(ctx context.Context, roomID string) error {
	if _, err := store.database.ExecContext(
		ctx, `DELETE FROM table_states WHERE room_id = $1`, roomID,
	); err != nil {
		return fmt.Errorf("delete table state: %w", err)
	}
	return nil
}

func (store *PostgresStore) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int, error) {
	result, err := store.database.ExecContext(
		ctx, `DELETE FROM table_states WHERE updated_at < $1`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("delete stale table states: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return int(removed), nil
}
