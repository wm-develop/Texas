package chat

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

func (store *PostgresStore) ByClientMessage(tableID, userID, clientMessageID string) (Message, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	message, err := scanMessage(store.database.QueryRowContext(
		ctx,
		`SELECT message_id, client_message_id, user_id, display_name,
		 room_id, kind, content, sent_at FROM chat_messages
		 WHERE room_id = $1 AND user_id = $2 AND client_message_id = $3`,
		tableID, userID, clientMessageID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, false, nil
	}
	if err != nil {
		return Message{}, false, fmt.Errorf("load chat idempotency record: %w", err)
	}
	return message, true, nil
}

func (store *PostgresStore) Save(message Message) (Message, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := store.database.ExecContext(
		ctx,
		`INSERT INTO chat_messages (
		 message_id, client_message_id, room_id, user_id, display_name,
		 kind, content, sent_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (room_id, user_id, client_message_id) DO NOTHING`,
		message.MessageID, message.ClientMessageID, message.TableID, message.UserID,
		message.DisplayName, message.Kind, message.Content, message.SentAt,
	)
	if err != nil {
		return Message{}, fmt.Errorf("save chat message: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Message{}, fmt.Errorf("read saved chat result: %w", err)
	}
	if affected == 1 {
		return message, nil
	}
	previous, found, err := store.ByClientMessage(message.TableID, message.UserID, message.ClientMessageID)
	if err != nil {
		return Message{}, err
	}
	if !found {
		return Message{}, errors.New("chat message conflict could not be resolved")
	}
	return previous, nil
}

func (store *PostgresStore) History(tableID string, limit int) ([]Message, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := store.database.QueryContext(
		ctx,
		`SELECT message_id, client_message_id, user_id, display_name,
		 room_id, kind, content, sent_at FROM (
			SELECT message_id, client_message_id, user_id, display_name,
			 room_id, kind, content, sent_at
			FROM chat_messages WHERE room_id = $1
			ORDER BY sent_at DESC, message_id DESC LIMIT $2
		 ) recent ORDER BY sent_at, message_id`,
		tableID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("load chat history: %w", err)
	}
	defer rows.Close()
	result := make([]Message, 0, limit)
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan chat history: %w", err)
		}
		result = append(result, message)
	}
	return result, rows.Err()
}

func (store *PostgresStore) SetMuted(change ModerationChange) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin chat moderation: %w", err)
	}
	defer transaction.Rollback()
	if change.Muted {
		_, err = transaction.ExecContext(
			ctx,
			`INSERT INTO chat_mutes (
			 user_id, muted_by_user_id, muted_at, updated_at
			) VALUES ($1, $2, $3, $3)
			 ON CONFLICT (user_id) DO UPDATE SET
			 muted_by_user_id = EXCLUDED.muted_by_user_id,
			 muted_at = EXCLUDED.muted_at,
			 updated_at = EXCLUDED.updated_at`,
			change.TargetUserID, change.ActorUserID, change.ChangedAt,
		)
	} else {
		_, err = transaction.ExecContext(
			ctx, `DELETE FROM chat_mutes WHERE user_id = $1`, change.TargetUserID,
		)
	}
	if err != nil {
		return fmt.Errorf("persist chat mute: %w", err)
	}
	metadata, err := json.Marshal(map[string]any{
		"targetUserId": change.TargetUserID,
		"muted":        change.Muted,
	})
	if err != nil {
		return fmt.Errorf("encode chat moderation audit: %w", err)
	}
	eventType := "admin.chat_unmuted"
	if change.Muted {
		eventType = "admin.chat_muted"
	}
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO audit_events (
		 event_id, actor_user_id, event_type, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5)`,
		change.AuditEventID, change.ActorUserID, eventType, metadata, change.ChangedAt,
	); err != nil {
		return fmt.Errorf("record chat moderation audit: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit chat moderation: %w", err)
	}
	return nil
}

func (store *PostgresStore) IsMuted(userID string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var muted bool
	if err := store.database.QueryRowContext(
		ctx, `SELECT EXISTS (SELECT 1 FROM chat_mutes WHERE user_id = $1)`, userID,
	).Scan(&muted); err != nil {
		return false, fmt.Errorf("load chat mute: %w", err)
	}
	return muted, nil
}

type messageScanner interface {
	Scan(...any) error
}

func scanMessage(scanner messageScanner) (Message, error) {
	var message Message
	err := scanner.Scan(
		&message.MessageID, &message.ClientMessageID, &message.UserID,
		&message.DisplayName, &message.TableID, &message.Kind,
		&message.Content, &message.SentAt,
	)
	return message, err
}

var _ Store = (*PostgresStore)(nil)
