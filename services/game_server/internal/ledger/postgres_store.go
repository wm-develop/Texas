package ledger

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

func (store *PostgresStore) Append(entries []Entry) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin append ledger: %w", err)
	}
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.EntryID == "" || entry.HandID == "" || entry.PlayerID == "" || entry.BalanceAfter < 0 {
			_ = transaction.Rollback()
			return errors.New("invalid ledger entry")
		}
		if _, exists := seen[entry.EntryID]; exists {
			_ = transaction.Rollback()
			return errors.New("duplicate ledger entry in batch")
		}
		seen[entry.EntryID] = struct{}{}
		_, err := transaction.ExecContext(
			ctx,
			`INSERT INTO hand_ledger_entries (
			 entry_id, hand_id, user_id, chip_delta, balance_after
			) VALUES ($1, $2, $3, $4, $5)`,
			entry.EntryID, entry.HandID, entry.PlayerID, entry.Delta, entry.BalanceAfter,
		)
		if err != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("insert ledger entry: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit ledger: %w", err)
	}
	return nil
}

func (store *PostgresStore) EntriesForHand(handID string) []Entry {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := store.database.QueryContext(
		ctx,
		`SELECT entry_id, hand_id, user_id, chip_delta, balance_after
		 FROM hand_ledger_entries WHERE hand_id = $1 ORDER BY user_id`,
		handID,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var result []Entry
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(
			&entry.EntryID, &entry.HandID, &entry.PlayerID,
			&entry.Delta, &entry.BalanceAfter,
		); err != nil {
			return nil
		}
		result = append(result, entry)
	}
	return result
}

var _ Store = (*PostgresStore)(nil)
