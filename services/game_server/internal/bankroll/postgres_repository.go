package bankroll

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

type PostgresRepository struct {
	database *sql.DB
}

func NewPostgresRepository(database *sql.DB) (*PostgresRepository, error) {
	if database == nil {
		return nil, errInvalidRepository
	}
	return &PostgresRepository{database: database}, nil
}

func (repository *PostgresRepository) Snapshot(ctx context.Context, userID string) (Snapshot, error) {
	var result Snapshot
	result.UserID = userID
	err := repository.database.QueryRowContext(
		ctx,
		`SELECT w.wallet_chips, w.revision,
                COALESCE(m.room_id, ''), COALESCE(m.table_chips, 0)
         FROM account_wallets w
         LEFT JOIN room_members m ON m.user_id = w.user_id
             AND EXISTS (
                 SELECT 1 FROM rooms r
                 WHERE r.room_id = m.room_id AND r.status <> 'closed'
             )
         WHERE w.user_id = $1`,
		userID,
	).Scan(&result.WalletChips, &result.Revision, &result.TableID, &result.TableChips)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, Error{Code: "invalid_user"}
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("load bankroll snapshot: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) TopUp(
	ctx context.Context,
	userID, requestID string,
	amount int64,
	now time.Time,
) (Snapshot, error) {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("begin top up: %w", err)
	}
	wallet, revision, err := lockWallet(ctx, transaction, userID)
	if err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, err
	}
	if previous, found, err := entrySnapshot(ctx, transaction, userID, requestID); err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, err
	} else if found {
		if err := transaction.Commit(); err != nil {
			return Snapshot{}, fmt.Errorf("commit repeated top up: %w", err)
		}
		return previous, nil
	}
	if amount <= 0 || wallet > maximumChipAmount-amount {
		_ = transaction.Rollback()
		return Snapshot{}, Error{Code: "invalid_chip_amount"}
	}
	wallet += amount
	revision++
	if _, err := transaction.ExecContext(
		ctx,
		`UPDATE account_wallets
         SET wallet_chips = $2, revision = $3, updated_at = $4
         WHERE user_id = $1`,
		userID, wallet, revision, now,
	); err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, fmt.Errorf("update wallet top up: %w", err)
	}
	result := Snapshot{UserID: userID, WalletChips: wallet, Revision: revision}
	if err := insertEntry(ctx, transaction, Entry{
		EntryID: entryID(userID, requestID), RequestID: requestID, UserID: userID,
		Reason: ReasonVirtualTopUp, WalletDelta: amount,
		WalletBalanceAfter: wallet, RevisionAfter: revision, CreatedAt: now,
	}); err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, err
	}
	if err := transaction.Commit(); err != nil {
		return Snapshot{}, fmt.Errorf("commit top up: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) SetWallet(
	ctx context.Context,
	userID, requestID string,
	amount int64,
	now time.Time,
) (Snapshot, error) {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("begin wallet adjustment: %w", err)
	}
	wallet, revision, err := lockWallet(ctx, transaction, userID)
	if err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, err
	}
	if previous, found, err := entrySnapshot(ctx, transaction, userID, requestID); err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, err
	} else if found {
		if err := transaction.Commit(); err != nil {
			return Snapshot{}, fmt.Errorf("commit repeated wallet adjustment: %w", err)
		}
		return previous, nil
	}
	var inRoom bool
	if err := transaction.QueryRowContext(
		ctx,
		`SELECT EXISTS (
            SELECT 1 FROM room_members m
            JOIN rooms r ON r.room_id = m.room_id
            WHERE m.user_id = $1 AND r.status <> 'closed'
         )`,
		userID,
	).Scan(&inRoom); err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, fmt.Errorf("check wallet room membership: %w", err)
	}
	if inRoom {
		_ = transaction.Rollback()
		return Snapshot{}, Error{Code: "user_in_room"}
	}
	if amount < 0 || amount > maximumChipAmount {
		_ = transaction.Rollback()
		return Snapshot{}, Error{Code: "invalid_chip_amount"}
	}
	delta := amount - wallet
	revision++
	if _, err := transaction.ExecContext(
		ctx,
		`UPDATE account_wallets
         SET wallet_chips = $2, revision = $3, updated_at = $4
         WHERE user_id = $1`,
		userID, amount, revision, now,
	); err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, fmt.Errorf("update adjusted wallet: %w", err)
	}
	result := Snapshot{UserID: userID, WalletChips: amount, Revision: revision}
	if err := insertEntry(ctx, transaction, Entry{
		EntryID: entryID(userID, requestID), RequestID: requestID, UserID: userID,
		Reason: ReasonAdminAdjust, WalletDelta: delta,
		WalletBalanceAfter: amount, RevisionAfter: revision, CreatedAt: now,
	}); err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, err
	}
	if err := transaction.Commit(); err != nil {
		return Snapshot{}, fmt.Errorf("commit wallet adjustment: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) TransferToTable(
	ctx context.Context,
	userID, tableID, requestID string,
	amount, maximum int64,
	reason Reason,
	now time.Time,
) (Snapshot, error) {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("begin table transfer: %w", err)
	}
	wallet, revision, err := lockWallet(ctx, transaction, userID)
	if err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, err
	}
	if previous, found, err := entrySnapshot(ctx, transaction, userID, requestID); err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, err
	} else if found {
		if err := transaction.Commit(); err != nil {
			return Snapshot{}, fmt.Errorf("commit repeated table transfer: %w", err)
		}
		return previous, nil
	}
	var tableChips, configuredMaximum int64
	err = transaction.QueryRowContext(
		ctx,
		`SELECT m.table_chips, r.max_buy_in
         FROM room_members m
         JOIN rooms r ON r.room_id = m.room_id
         WHERE m.user_id = $1 AND m.room_id = $2 AND r.status <> 'closed'
         FOR UPDATE OF m`,
		userID, tableID,
	).Scan(&tableChips, &configuredMaximum)
	if errors.Is(err, sql.ErrNoRows) {
		_ = transaction.Rollback()
		return Snapshot{}, Error{Code: "invalid_table_balance"}
	}
	if err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, fmt.Errorf("lock table balance: %w", err)
	}
	if maximum != configuredMaximum || amount <= 0 || tableChips > configuredMaximum-amount {
		_ = transaction.Rollback()
		return Snapshot{}, Error{Code: "maximum_buy_in_exceeded"}
	}
	if wallet < amount {
		_ = transaction.Rollback()
		return Snapshot{}, Error{Code: "insufficient_wallet_chips"}
	}
	wallet -= amount
	tableChips += amount
	revision++
	if _, err := transaction.ExecContext(
		ctx,
		`UPDATE account_wallets
         SET wallet_chips = $2, revision = $3, updated_at = $4
         WHERE user_id = $1`,
		userID, wallet, revision, now,
	); err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, fmt.Errorf("debit wallet: %w", err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`UPDATE room_members SET table_chips = $3 WHERE user_id = $1 AND room_id = $2`,
		userID, tableID, tableChips,
	); err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, fmt.Errorf("credit table balance: %w", err)
	}
	result := Snapshot{
		UserID: userID, WalletChips: wallet, TableID: tableID,
		TableChips: tableChips, Revision: revision,
	}
	if err := insertEntry(ctx, transaction, Entry{
		EntryID: entryID(userID, requestID), RequestID: requestID,
		UserID: userID, TableID: tableID, Reason: reason,
		WalletDelta: -amount, TableDelta: amount,
		WalletBalanceAfter: wallet, TableBalanceAfter: tableChips,
		RevisionAfter: revision, CreatedAt: now,
	}); err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, err
	}
	if err := transaction.Commit(); err != nil {
		return Snapshot{}, fmt.Errorf("commit table transfer: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) ApplySettlement(
	ctx context.Context,
	tableID, handID string,
	balances map[string]int64,
	_ int64,
	now time.Time,
) error {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin settlement: %w", err)
	}
	result, err := transaction.ExecContext(
		ctx,
		`INSERT INTO bankroll_settlements (room_id, hand_id, created_at)
         VALUES ($1, $2, $3)
         ON CONFLICT (room_id, hand_id) DO NOTHING`,
		tableID, handID, now,
	)
	if err != nil {
		_ = transaction.Rollback()
		return fmt.Errorf("record settlement marker: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		_ = transaction.Rollback()
		return fmt.Errorf("read settlement marker result: %w", err)
	}
	if inserted == 0 {
		if err := transaction.Commit(); err != nil {
			return fmt.Errorf("commit repeated settlement: %w", err)
		}
		return nil
	}
	userIDs := make([]string, 0, len(balances))
	for userID, balance := range balances {
		if userID == "" || balance < 0 {
			_ = transaction.Rollback()
			return Error{Code: "invalid_table_balance"}
		}
		userIDs = append(userIDs, userID)
	}
	sort.Strings(userIDs)
	wallets := make(map[string]int64, len(userIDs))
	revisions := make(map[string]uint64, len(userIDs))
	for _, userID := range userIDs {
		wallet, revision, err := lockWallet(ctx, transaction, userID)
		if err != nil {
			_ = transaction.Rollback()
			return err
		}
		wallets[userID] = wallet
		revisions[userID] = revision
	}
	previous := make(map[string]int64, len(userIDs))
	var beforeTotal, afterTotal int64
	for _, userID := range userIDs {
		var balance int64
		err := transaction.QueryRowContext(
			ctx,
			`SELECT table_chips FROM room_members
             WHERE room_id = $1 AND user_id = $2 FOR UPDATE`,
			tableID, userID,
		).Scan(&balance)
		if errors.Is(err, sql.ErrNoRows) {
			_ = transaction.Rollback()
			return Error{Code: "invalid_table_balance"}
		}
		if err != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("lock settlement table balance: %w", err)
		}
		previous[userID] = balance
		beforeTotal += balance
		afterTotal += balances[userID]
	}
	if beforeTotal != afterTotal {
		_ = transaction.Rollback()
		return Error{Code: "table_chips_not_conserved"}
	}
	for _, userID := range userIDs {
		balance := balances[userID]
		if previous[userID] == balance {
			continue
		}
		revision := revisions[userID] + 1
		if _, err := transaction.ExecContext(
			ctx,
			`UPDATE room_members SET table_chips = $3 WHERE room_id = $1 AND user_id = $2`,
			tableID, userID, balance,
		); err != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("update settlement table balance: %w", err)
		}
		if _, err := transaction.ExecContext(
			ctx,
			`UPDATE account_wallets SET revision = $2, updated_at = $3 WHERE user_id = $1`,
			userID, revision, now,
		); err != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("update settlement revision: %w", err)
		}
		requestID := "settlement:" + handID
		if err := insertEntry(ctx, transaction, Entry{
			EntryID:   entryID(userID, "settlement:"+tableID+":"+handID),
			RequestID: requestID, UserID: userID, TableID: tableID,
			ReferenceID: handID, Reason: ReasonSettlement,
			TableDelta: balance - previous[userID], WalletBalanceAfter: wallets[userID],
			TableBalanceAfter: balance, RevisionAfter: revision, CreatedAt: now,
		}); err != nil {
			_ = transaction.Rollback()
			return err
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit settlement: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) CashOut(
	ctx context.Context,
	userID, tableID, requestID string,
	now time.Time,
) (Snapshot, error) {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("begin cash out: %w", err)
	}
	wallet, revision, err := lockWallet(ctx, transaction, userID)
	if err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, err
	}
	if previous, found, err := entrySnapshot(ctx, transaction, userID, requestID); err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, err
	} else if found {
		if err := transaction.Commit(); err != nil {
			return Snapshot{}, fmt.Errorf("commit repeated cash out: %w", err)
		}
		return previous, nil
	}
	var tableChips int64
	err = transaction.QueryRowContext(
		ctx,
		`SELECT table_chips FROM room_members
         WHERE user_id = $1 AND room_id = $2 FOR UPDATE`,
		userID, tableID,
	).Scan(&tableChips)
	if errors.Is(err, sql.ErrNoRows) {
		_ = transaction.Rollback()
		return Snapshot{}, Error{Code: "invalid_table_balance"}
	}
	if err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, fmt.Errorf("lock cash out balance: %w", err)
	}
	if wallet > maximumChipAmount-tableChips {
		_ = transaction.Rollback()
		return Snapshot{}, Error{Code: "invalid_chip_amount"}
	}
	wallet += tableChips
	revision++
	if _, err := transaction.ExecContext(
		ctx,
		`UPDATE account_wallets
         SET wallet_chips = $2, revision = $3, updated_at = $4
         WHERE user_id = $1`,
		userID, wallet, revision, now,
	); err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, fmt.Errorf("credit cash out wallet: %w", err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`UPDATE room_members SET table_chips = 0, ready = false
         WHERE user_id = $1 AND room_id = $2`,
		userID, tableID,
	); err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, fmt.Errorf("clear cash out table balance: %w", err)
	}
	result := Snapshot{UserID: userID, WalletChips: wallet, Revision: revision}
	if err := insertEntry(ctx, transaction, Entry{
		EntryID: entryID(userID, requestID), RequestID: requestID,
		UserID: userID, TableID: tableID, Reason: ReasonCashOut,
		WalletDelta: tableChips, TableDelta: -tableChips,
		WalletBalanceAfter: wallet, RevisionAfter: revision, CreatedAt: now,
	}); err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, err
	}
	if err := transaction.Commit(); err != nil {
		return Snapshot{}, fmt.Errorf("commit cash out: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) Entries(ctx context.Context, userID string, limit int) ([]Entry, error) {
	rows, err := repository.database.QueryContext(
		ctx,
		`SELECT entry_id, request_id, user_id, COALESCE(room_id, ''), reference_id,
                reason, wallet_delta, table_delta, wallet_balance_after,
                table_balance_after, revision_after, created_at
         FROM bankroll_entries
         WHERE user_id = $1
         ORDER BY created_at DESC, entry_id DESC
         LIMIT $2`,
		userID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("load bankroll entries: %w", err)
	}
	defer rows.Close()
	result := make([]Entry, 0, limit)
	for rows.Next() {
		var value Entry
		if err := rows.Scan(
			&value.EntryID, &value.RequestID, &value.UserID, &value.TableID,
			&value.ReferenceID, &value.Reason, &value.WalletDelta, &value.TableDelta,
			&value.WalletBalanceAfter, &value.TableBalanceAfter,
			&value.RevisionAfter, &value.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan bankroll entry: %w", err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bankroll entries: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) TransferWallet(
	ctx context.Context,
	fromUserID, toUserID, requestID string,
	reason Reason,
	referenceID string,
	now time.Time,
) (Snapshot, error) {
	if fromUserID == "" || toUserID == "" || fromUserID == toUserID {
		return Snapshot{}, Error{Code: "invalid_user"}
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("begin wallet transfer: %w", err)
	}
	// 固定按 user_id 顺序加锁，避免两笔互相转账的事务死锁
	first, second := fromUserID, toUserID
	if second < first {
		first, second = second, first
	}
	balances := map[string]int64{}
	revisions := map[string]uint64{}
	for _, userID := range []string{first, second} {
		wallet, revision, err := lockWallet(ctx, transaction, userID)
		if err != nil {
			_ = transaction.Rollback()
			return Snapshot{}, err
		}
		balances[userID], revisions[userID] = wallet, revision
	}
	if previous, found, err := entrySnapshot(ctx, transaction, fromUserID, requestID); err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, err
	} else if found {
		if err := transaction.Commit(); err != nil {
			return Snapshot{}, fmt.Errorf("commit repeated wallet transfer: %w", err)
		}
		return previous, nil
	}
	var tableChips int64
	if err := transaction.QueryRowContext(
		ctx,
		`SELECT COALESCE(SUM(table_chips), 0) FROM room_members WHERE user_id = $1`,
		fromUserID,
	).Scan(&tableChips); err != nil {
		_ = transaction.Rollback()
		return Snapshot{}, fmt.Errorf("check table balance before transfer: %w", err)
	}
	if tableChips != 0 {
		_ = transaction.Rollback()
		return Snapshot{}, Error{Code: "user_in_room"}
	}
	amount := balances[fromUserID]
	if balances[toUserID] > maximumChipAmount-amount {
		_ = transaction.Rollback()
		return Snapshot{}, Error{Code: "invalid_chip_amount"}
	}
	fromRevision := revisions[fromUserID] + 1
	toRevision := revisions[toUserID] + 1
	toWallet := balances[toUserID] + amount
	for _, update := range []struct {
		userID   string
		wallet   int64
		revision uint64
	}{{fromUserID, 0, fromRevision}, {toUserID, toWallet, toRevision}} {
		if _, err := transaction.ExecContext(
			ctx,
			`UPDATE account_wallets
             SET wallet_chips = $2, revision = $3, updated_at = $4
             WHERE user_id = $1`,
			update.userID, update.wallet, update.revision, now,
		); err != nil {
			_ = transaction.Rollback()
			return Snapshot{}, fmt.Errorf("update wallet transfer: %w", err)
		}
	}
	entries := []Entry{
		{
			EntryID: entryID(fromUserID, requestID), RequestID: requestID, UserID: fromUserID,
			ReferenceID: referenceID, Reason: reason, WalletDelta: -amount,
			WalletBalanceAfter: 0, RevisionAfter: fromRevision, CreatedAt: now,
		},
		{
			EntryID: entryID(toUserID, requestID), RequestID: requestID, UserID: toUserID,
			ReferenceID: referenceID, Reason: reason, WalletDelta: amount,
			WalletBalanceAfter: toWallet, RevisionAfter: toRevision, CreatedAt: now,
		},
	}
	for _, entry := range entries {
		if err := insertEntry(ctx, transaction, entry); err != nil {
			_ = transaction.Rollback()
			return Snapshot{}, err
		}
	}
	if err := transaction.Commit(); err != nil {
		return Snapshot{}, fmt.Errorf("commit wallet transfer: %w", err)
	}
	return Snapshot{UserID: fromUserID, WalletChips: 0, Revision: fromRevision}, nil
}

func lockWallet(ctx context.Context, transaction *sql.Tx, userID string) (int64, uint64, error) {
	var wallet int64
	var revision uint64
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
		return 0, 0, fmt.Errorf("lock wallet: %w", err)
	}
	return wallet, revision, nil
}

func entrySnapshot(
	ctx context.Context,
	transaction *sql.Tx,
	userID, requestID string,
) (Snapshot, bool, error) {
	var result Snapshot
	result.UserID = userID
	err := transaction.QueryRowContext(
		ctx,
		`SELECT COALESCE(room_id, ''), wallet_balance_after,
                table_balance_after, revision_after
         FROM bankroll_entries WHERE user_id = $1 AND request_id = $2`,
		userID, requestID,
	).Scan(&result.TableID, &result.WalletChips, &result.TableChips, &result.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("load idempotent bankroll result: %w", err)
	}
	return result, true, nil
}

func insertEntry(ctx context.Context, transaction *sql.Tx, value Entry) error {
	_, err := transaction.ExecContext(
		ctx,
		`INSERT INTO bankroll_entries (
            entry_id, request_id, user_id, room_id, reference_id, reason,
            wallet_delta, table_delta, wallet_balance_after,
            table_balance_after, revision_after, created_at
         ) VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, $9, $10, $11, $12)`,
		value.EntryID, value.RequestID, value.UserID, value.TableID,
		value.ReferenceID, value.Reason, value.WalletDelta, value.TableDelta,
		value.WalletBalanceAfter, value.TableBalanceAfter,
		value.RevisionAfter, value.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert bankroll entry: %w", err)
	}
	return nil
}

var _ Repository = (*PostgresRepository)(nil)
