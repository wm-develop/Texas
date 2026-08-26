package room

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
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

func (repository *PostgresRepository) Create(ctx context.Context, value Room) error {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin create room: %w", err)
	}
	if err := insertRoom(ctx, transaction, value); err != nil {
		_ = transaction.Rollback()
		return err
	}
	if err := insertMembers(ctx, transaction, value.RoomID, value.Members); err != nil {
		_ = transaction.Rollback()
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit create room: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) CreateWithBuyIn(
	ctx context.Context,
	value Room,
	requestID string,
	amount int64,
	now time.Time,
) error {
	if len(value.Members) != 1 || value.Members[0].UserID != value.OwnerUserID ||
		amount <= 0 || amount != value.Members[0].Stack || amount > value.Rules.MaxBuyIn {
		return Error{Code: "invalid_buy_in"}
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin create room with buy in: %w", err)
	}
	wallet, revision, err := lockRoomWallet(ctx, transaction, value.OwnerUserID)
	if err != nil {
		_ = transaction.Rollback()
		return err
	}
	if wallet < amount {
		_ = transaction.Rollback()
		return Error{Code: "insufficient_wallet_chips"}
	}
	if err := insertRoom(ctx, transaction, value); err != nil {
		_ = transaction.Rollback()
		return err
	}
	if err := insertMembers(ctx, transaction, value.RoomID, value.Members); err != nil {
		_ = transaction.Rollback()
		return err
	}
	wallet -= amount
	revision++
	if err := updateRoomWallet(ctx, transaction, value.OwnerUserID, wallet, revision, now); err != nil {
		_ = transaction.Rollback()
		return err
	}
	if err := insertBuyInEntry(ctx, transaction, value.OwnerUserID, value.RoomID, requestID, amount, wallet, amount, revision, now); err != nil {
		_ = transaction.Rollback()
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit create room with buy in: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) JoinWithBuyIn(
	ctx context.Context,
	roomID string,
	member Member,
	requestID string,
	amount int64,
	now time.Time,
) (Room, error) {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return Room{}, fmt.Errorf("begin join room with buy in: %w", err)
	}
	wallet, walletRevision, err := lockRoomWallet(ctx, transaction, member.UserID)
	if err != nil {
		_ = transaction.Rollback()
		return Room{}, err
	}
	var repeatedRoomID string
	err = transaction.QueryRowContext(
		ctx,
		`SELECT COALESCE(room_id, '') FROM bankroll_entries
		 WHERE user_id = $1 AND request_id = $2`,
		member.UserID, requestID,
	).Scan(&repeatedRoomID)
	if err == nil {
		if repeatedRoomID != roomID {
			_ = transaction.Rollback()
			return Room{}, Error{Code: "invalid_request"}
		}
		if err := transaction.Commit(); err != nil {
			return Room{}, fmt.Errorf("commit repeated room join: %w", err)
		}
		return repository.ByID(ctx, roomID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		_ = transaction.Rollback()
		return Room{}, fmt.Errorf("load repeated room join: %w", err)
	}
	var maximum int64
	var maxPlayers int
	var status string
	err = transaction.QueryRowContext(
		ctx,
		`SELECT max_buy_in, max_players, status FROM rooms
		 WHERE room_id = $1 FOR UPDATE`,
		roomID,
	).Scan(&maximum, &maxPlayers, &status)
	if errors.Is(err, sql.ErrNoRows) || status == "closed" {
		_ = transaction.Rollback()
		return Room{}, Error{Code: "room_not_found"}
	}
	if err != nil {
		_ = transaction.Rollback()
		return Room{}, fmt.Errorf("lock room for join: %w", err)
	}
	if amount <= 0 || amount > maximum || member.Stack != amount {
		_ = transaction.Rollback()
		return Room{}, Error{Code: "invalid_buy_in"}
	}
	if wallet < amount {
		_ = transaction.Rollback()
		return Room{}, Error{Code: "insufficient_wallet_chips"}
	}
	var count int
	if err := transaction.QueryRowContext(
		ctx, `SELECT count(*) FROM room_members WHERE room_id = $1`, roomID,
	).Scan(&count); err != nil {
		_ = transaction.Rollback()
		return Room{}, fmt.Errorf("count room members: %w", err)
	}
	if count >= maxPlayers {
		_ = transaction.Rollback()
		return Room{}, Error{Code: "room_full"}
	}
	occupied := make(map[int]struct{}, count)
	rows, err := transaction.QueryContext(ctx, `SELECT seat_number FROM room_members WHERE room_id = $1`, roomID)
	if err != nil {
		_ = transaction.Rollback()
		return Room{}, fmt.Errorf("load occupied seats: %w", err)
	}
	for rows.Next() {
		var seat int
		if err := rows.Scan(&seat); err != nil {
			rows.Close()
			_ = transaction.Rollback()
			return Room{}, fmt.Errorf("scan occupied seat: %w", err)
		}
		occupied[seat] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		_ = transaction.Rollback()
		return Room{}, fmt.Errorf("close occupied seats: %w", err)
	}
	for seat := 1; seat <= maxPlayers; seat++ {
		if _, exists := occupied[seat]; !exists {
			member.Seat = seat
			break
		}
	}
	if member.Seat == 0 {
		_ = transaction.Rollback()
		return Room{}, Error{Code: "room_full"}
	}
	if err := insertMembers(ctx, transaction, roomID, []Member{member}); err != nil {
		_ = transaction.Rollback()
		return Room{}, err
	}
	if _, err := transaction.ExecContext(
		ctx, `UPDATE rooms SET revision = revision + 1 WHERE room_id = $1`, roomID,
	); err != nil {
		_ = transaction.Rollback()
		return Room{}, fmt.Errorf("increment room revision: %w", err)
	}
	wallet -= amount
	walletRevision++
	if err := updateRoomWallet(ctx, transaction, member.UserID, wallet, walletRevision, now); err != nil {
		_ = transaction.Rollback()
		return Room{}, err
	}
	if err := insertBuyInEntry(ctx, transaction, member.UserID, roomID, requestID, amount, wallet, amount, walletRevision, now); err != nil {
		_ = transaction.Rollback()
		return Room{}, err
	}
	if err := transaction.Commit(); err != nil {
		return Room{}, fmt.Errorf("commit join room with buy in: %w", err)
	}
	return repository.ByID(ctx, roomID)
}

func (repository *PostgresRepository) Save(ctx context.Context, value Room) error {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save room: %w", err)
	}
	result, err := transaction.ExecContext(
		ctx,
		`UPDATE rooms SET owner_user_id = $2, preset = $3, password_hash = $4,
		 max_players = $5, small_blind = $6, big_blind = $7, max_buy_in = $8,
		 action_seconds = $9, revision = $10 WHERE room_id = $1 AND status <> 'closed'`,
		value.RoomID, value.OwnerUserID, value.Preset, value.PasswordHash,
		value.MaxPlayers, value.Rules.SmallBlind, value.Rules.BigBlind,
		value.Rules.MaxBuyIn, value.Rules.ActionSeconds, value.Revision,
	)
	if err != nil {
		_ = transaction.Rollback()
		return postgresRoomError("update room", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		_ = transaction.Rollback()
		return fmt.Errorf("read saved room count: %w", err)
	}
	if affected == 0 {
		_ = transaction.Rollback()
		return ErrNotFound
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM room_members WHERE room_id = $1`, value.RoomID); err != nil {
		_ = transaction.Rollback()
		return fmt.Errorf("replace room members: %w", err)
	}
	if err := insertMembers(ctx, transaction, value.RoomID, value.Members); err != nil {
		_ = transaction.Rollback()
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit save room: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) ByID(ctx context.Context, roomID string) (Room, error) {
	return loadRoom(ctx, repository.database, `r.room_id = $1`, roomID)
}

func (repository *PostgresRepository) ByCode(ctx context.Context, code string) (Room, error) {
	return loadRoom(ctx, repository.database, `r.room_code = $1`, code)
}

func (repository *PostgresRepository) ByUser(ctx context.Context, userID string) (Room, error) {
	return loadRoom(ctx, repository.database, `EXISTS (
		SELECT 1 FROM room_members selected_member
		WHERE selected_member.room_id = r.room_id AND selected_member.user_id = $1
	)`, userID)
}

func (repository *PostgresRepository) Delete(ctx context.Context, roomID string) error {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin close room: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM room_members WHERE room_id = $1`, roomID); err != nil {
		_ = transaction.Rollback()
		return fmt.Errorf("remove closed room members: %w", err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`UPDATE rooms SET status = 'closed', closed_at = now(), revision = revision + 1
		 WHERE room_id = $1 AND status <> 'closed'`,
		roomID,
	); err != nil {
		_ = transaction.Rollback()
		return fmt.Errorf("close room: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit close room: %w", err)
	}
	return nil
}

type roomQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadRoom(ctx context.Context, queryer roomQueryer, predicate string, argument string) (Room, error) {
	var value Room
	var revision int64
	err := queryer.QueryRowContext(
		ctx,
		`SELECT r.room_id, trim(r.room_code), r.owner_user_id, r.preset,
		 r.password_hash, r.max_players, r.small_blind, r.big_blind,
		 r.max_buy_in, r.action_seconds, r.revision, r.created_at
		 FROM rooms r WHERE r.status <> 'closed' AND `+predicate,
		argument,
	).Scan(
		&value.RoomID, &value.Code, &value.OwnerUserID, &value.Preset,
		&value.PasswordHash, &value.MaxPlayers, &value.Rules.SmallBlind,
		&value.Rules.BigBlind, &value.Rules.MaxBuyIn, &value.Rules.ActionSeconds,
		&revision, &value.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Room{}, ErrNotFound
	}
	if err != nil {
		return Room{}, fmt.Errorf("load room: %w", err)
	}
	if revision < 0 {
		return Room{}, fmt.Errorf("load room: invalid revision %d", revision)
	}
	value.Revision = uint64(revision)
	value.Rules.StartingChips = value.Rules.MaxBuyIn
	rows, err := queryer.QueryContext(
		ctx,
		`SELECT m.user_id, u.display_name, m.seat_number, m.ready,
		 m.table_chips, m.joined_at
		 FROM room_members m JOIN users u ON u.user_id = m.user_id
		 WHERE m.room_id = $1 ORDER BY m.seat_number`,
		value.RoomID,
	)
	if err != nil {
		return Room{}, fmt.Errorf("load room members: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var member Member
		if err := rows.Scan(
			&member.UserID, &member.DisplayName, &member.Seat,
			&member.Ready, &member.Stack, &member.JoinedAt,
		); err != nil {
			return Room{}, fmt.Errorf("scan room member: %w", err)
		}
		value.Members = append(value.Members, member)
	}
	if err := rows.Err(); err != nil {
		return Room{}, fmt.Errorf("iterate room members: %w", err)
	}
	return value, nil
}

func insertRoom(ctx context.Context, transaction *sql.Tx, value Room) error {
	_, err := transaction.ExecContext(
		ctx,
		`INSERT INTO rooms (
		 room_id, room_code, owner_user_id, preset, password_hash, max_players,
		 small_blind, big_blind, max_buy_in, action_seconds, revision, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		value.RoomID, value.Code, value.OwnerUserID, value.Preset,
		value.PasswordHash, value.MaxPlayers, value.Rules.SmallBlind,
		value.Rules.BigBlind, value.Rules.MaxBuyIn, value.Rules.ActionSeconds,
		value.Revision, value.CreatedAt,
	)
	if err != nil {
		return postgresRoomError("insert room", err)
	}
	return nil
}

func insertMembers(ctx context.Context, transaction *sql.Tx, roomID string, members []Member) error {
	ordered := append([]Member(nil), members...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].Seat < ordered[right].Seat })
	for _, member := range ordered {
		_, err := transaction.ExecContext(
			ctx,
			`INSERT INTO room_members (
			 room_id, user_id, seat_number, table_chips, ready, joined_at
			) VALUES ($1, $2, $3, $4, $5, $6)`,
			roomID, member.UserID, member.Seat, member.Stack, member.Ready, member.JoinedAt,
		)
		if err != nil {
			return postgresRoomError("insert room member", err)
		}
	}
	return nil
}

func lockRoomWallet(ctx context.Context, transaction *sql.Tx, userID string) (int64, uint64, error) {
	var wallet, revision int64
	err := transaction.QueryRowContext(
		ctx,
		`SELECT wallet_chips, revision FROM account_wallets
		 WHERE user_id = $1 FOR UPDATE`,
		userID,
	).Scan(&wallet, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, Error{Code: "invalid_user"}
	}
	if err != nil {
		return 0, 0, fmt.Errorf("lock room wallet: %w", err)
	}
	if revision < 0 {
		return 0, 0, fmt.Errorf("lock room wallet: invalid revision %d", revision)
	}
	return wallet, uint64(revision), nil
}

func updateRoomWallet(
	ctx context.Context,
	transaction *sql.Tx,
	userID string,
	wallet int64,
	revision uint64,
	now time.Time,
) error {
	_, err := transaction.ExecContext(
		ctx,
		`UPDATE account_wallets SET wallet_chips = $2, revision = $3, updated_at = $4
		 WHERE user_id = $1`,
		userID, wallet, revision, now,
	)
	if err != nil {
		return fmt.Errorf("update room wallet: %w", err)
	}
	return nil
}

func insertBuyInEntry(
	ctx context.Context,
	transaction *sql.Tx,
	userID, roomID, requestID string,
	amount, wallet, table int64,
	revision uint64,
	now time.Time,
) error {
	_, err := transaction.ExecContext(
		ctx,
		`INSERT INTO bankroll_entries (
		 entry_id, request_id, user_id, room_id, reason, wallet_delta, table_delta,
		 wallet_balance_after, table_balance_after, revision_after, created_at
		) VALUES ($1, $2, $3, $4, 'buy_in', $5, $6, $7, $8, $9, $10)`,
		"bank_"+userID+"_"+requestID, requestID, userID, roomID,
		-amount, amount, wallet, table, revision, now,
	)
	if err != nil {
		return postgresRoomError("insert room buy in", err)
	}
	return nil
}

func postgresRoomError(operation string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return fmt.Errorf("%s: %w", operation, ErrConflict)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

var (
	_ Repository      = (*PostgresRepository)(nil)
	_ BuyInRepository = (*PostgresRepository)(nil)
)
