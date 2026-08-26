package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const postgresOperationTimeout = 5 * time.Second

type PostgresStore struct {
	database *sql.DB
}

func NewPostgresStore(database *sql.DB) (*PostgresStore, error) {
	if database == nil {
		return nil, errors.New("database is required")
	}
	return &PostgresStore{database: database}, nil
}

func (store *PostgresStore) Append(hand Hand) error {
	if err := validate(hand); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), postgresOperationTimeout)
	defer cancel()
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin append hand history: %w", err)
	}
	potAwards, err := json.Marshal(hand.PotAwards)
	if err != nil {
		_ = transaction.Rollback()
		return fmt.Errorf("encode pot awards: %w", err)
	}
	revealedHands, err := json.Marshal(hand.RevealedHands)
	if err != nil {
		_ = transaction.Rollback()
		return fmt.Errorf("encode revealed hands: %w", err)
	}
	_, err = transaction.ExecContext(
		ctx,
		`INSERT INTO hands (
		 hand_id, room_id, room_code, dealer_seat, board_cards, pot_awards,
		 revealed_hands, showdown, started_at, ended_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		hand.HandID, hand.RoomID, hand.RoomCode, persistentDealerSeat(hand), hand.Board,
		string(potAwards), string(revealedHands), hand.Showdown, hand.StartedAt, hand.EndedAt,
	)
	if err != nil {
		_ = transaction.Rollback()
		return fmt.Errorf("insert hand history: %w", err)
	}
	for _, player := range hand.Players {
		_, err := transaction.ExecContext(
			ctx,
			`INSERT INTO hand_players (
			 hand_id, user_id, display_name, seat_number, starting_stack,
			 ending_stack, chip_delta, hole_cards
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			hand.HandID, player.UserID, player.DisplayName, player.Seat,
			player.StartingStack, player.EndingStack, player.Delta, player.HoleCards,
		)
		if err != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("insert hand player: %w", err)
		}
	}
	for _, action := range hand.Actions {
		_, err := transaction.ExecContext(
			ctx,
			`INSERT INTO hand_actions (
			 action_id, hand_id, user_id, sequence_number, street,
			 action_type, committed, raise_to, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			action.ActionID, hand.HandID, action.UserID, action.Sequence,
			action.Street, action.Type, action.Committed, action.RaiseTo, action.CreatedAt,
		)
		if err != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("insert hand action: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit hand history: %w", err)
	}
	return nil
}

func (store *PostgresStore) Hand(handID string) (Hand, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), postgresOperationTimeout)
	defer cancel()
	value, err := store.loadHand(ctx, handID)
	return value, err == nil
}

func (store *PostgresStore) RecentForPlayer(userID string, limit int) []Hand {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	ctx, cancel := context.WithTimeout(context.Background(), postgresOperationTimeout)
	defer cancel()
	rows, err := store.database.QueryContext(
		ctx,
		`SELECT h.hand_id FROM hands h
		 JOIN hand_players p ON p.hand_id = h.hand_id
		 WHERE p.user_id = $1 ORDER BY h.ended_at DESC LIMIT $2`,
		userID, limit,
	)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var handID string
		if err := rows.Scan(&handID); err != nil {
			rows.Close()
			return nil
		}
		ids = append(ids, handID)
	}
	if err := rows.Close(); err != nil {
		return nil
	}
	result := make([]Hand, 0, len(ids))
	for _, handID := range ids {
		hand, err := store.loadHand(ctx, handID)
		if err != nil {
			return nil
		}
		result = append(result, forRecipient(hand, userID))
	}
	return result
}

func (store *PostgresStore) loadHand(ctx context.Context, handID string) (Hand, error) {
	var value Hand
	var potAwards, revealedHands []byte
	err := store.database.QueryRowContext(
		ctx,
		`SELECT hand_id, room_id, trim(room_code), dealer_seat, board_cards, pot_awards,
		 revealed_hands, showdown, started_at, ended_at
		 FROM hands WHERE hand_id = $1`,
		handID,
	).Scan(
		&value.HandID, &value.RoomID, &value.RoomCode, &value.DealerSeat, &value.Board,
		&potAwards, &revealedHands, &value.Showdown, &value.StartedAt, &value.EndedAt,
	)
	if err != nil {
		return Hand{}, err
	}
	if err := json.Unmarshal(potAwards, &value.PotAwards); err != nil {
		return Hand{}, fmt.Errorf("decode pot awards: %w", err)
	}
	if err := json.Unmarshal(revealedHands, &value.RevealedHands); err != nil {
		return Hand{}, fmt.Errorf("decode revealed hands: %w", err)
	}
	rows, err := store.database.QueryContext(
		ctx,
		`SELECT user_id, display_name, seat_number, starting_stack,
		 ending_stack, chip_delta, hole_cards
		 FROM hand_players WHERE hand_id = $1 ORDER BY seat_number`,
		handID,
	)
	if err != nil {
		return Hand{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var player PlayerResult
		if err := rows.Scan(
			&player.UserID, &player.DisplayName, &player.Seat,
			&player.StartingStack, &player.EndingStack, &player.Delta, &player.HoleCards,
		); err != nil {
			return Hand{}, err
		}
		value.Players = append(value.Players, player)
	}
	if err := rows.Err(); err != nil {
		return Hand{}, err
	}
	actionRows, err := store.database.QueryContext(
		ctx,
		`SELECT action_id, user_id, sequence_number, street, action_type,
		 committed, raise_to, created_at FROM hand_actions
		 WHERE hand_id = $1 ORDER BY sequence_number`,
		handID,
	)
	if err != nil {
		return Hand{}, err
	}
	defer actionRows.Close()
	for actionRows.Next() {
		var action Action
		if err := actionRows.Scan(
			&action.ActionID, &action.UserID, &action.Sequence, &action.Street,
			&action.Type, &action.Committed, &action.RaiseTo, &action.CreatedAt,
		); err != nil {
			return Hand{}, err
		}
		value.Actions = append(value.Actions, action)
	}
	return value, actionRows.Err()
}

var _ Store = (*PostgresStore)(nil)

func persistentDealerSeat(hand Hand) int {
	if hand.DealerSeat > 0 {
		return hand.DealerSeat
	}
	return hand.Players[0].Seat
}
