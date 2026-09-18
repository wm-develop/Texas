package bankroll

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

type Reason string

const (
	ReasonVirtualTopUp Reason = "virtual_top_up"
	ReasonBuyIn        Reason = "buy_in"
	ReasonRebuy        Reason = "rebuy"
	ReasonSettlement   Reason = "hand_settlement"
	ReasonCashOut      Reason = "cash_out"
	ReasonAdminAdjust  Reason = "admin_adjustment"
	// ReasonRake 是抽水入账：user_id 为管理员，TableID 为房间，ReferenceID 为手号。
	ReasonRake Reason = "rake"
	// ReasonAccountDeletion 记录注销账号时钱包整体转入管理员钱包的两笔流水。
	ReasonAccountDeletion Reason = "account_deletion"
)

type Snapshot struct {
	UserID      string `json:"userId"`
	WalletChips int64  `json:"walletChips"`
	TableID     string `json:"tableId,omitempty"`
	TableChips  int64  `json:"tableChips"`
	Revision    uint64 `json:"revision"`
}

type Entry struct {
	EntryID            string    `json:"entryId"`
	RequestID          string    `json:"requestId"`
	UserID             string    `json:"userId"`
	TableID            string    `json:"tableId,omitempty"`
	ReferenceID        string    `json:"referenceId,omitempty"`
	Reason             Reason    `json:"reason"`
	WalletDelta        int64     `json:"walletDelta"`
	TableDelta         int64     `json:"tableDelta"`
	WalletBalanceAfter int64     `json:"walletBalanceAfter"`
	TableBalanceAfter  int64     `json:"tableBalanceAfter"`
	RevisionAfter      uint64    `json:"revisionAfter"`
	CreatedAt          time.Time `json:"createdAt"`
}

type Repository interface {
	Snapshot(ctx context.Context, userID string) (Snapshot, error)
	TopUp(ctx context.Context, userID, requestID string, amount int64, now time.Time) (Snapshot, error)
	SetWallet(ctx context.Context, userID, requestID string, amount int64, now time.Time) (Snapshot, error)
	TransferToTable(ctx context.Context, userID, tableID, requestID string, amount, maximum int64, reason Reason, now time.Time) (Snapshot, error)
	// ApplySettlement 把一手牌结算后的筹码写进各人的桌上余额。rake 不为零时，这笔
	// 筹码在同一事务里从牌桌转进收款人的钱包，守恒校验为「结算前各人桌上余额之和
	// = 结算后之和 + 抽水」。按 (tableID, handID) 幂等。
	ApplySettlement(ctx context.Context, tableID, handID string, balances map[string]int64, maximum int64, rake Rake, now time.Time) error
	CashOut(ctx context.Context, userID, tableID, requestID string, now time.Time) (Snapshot, error)
	// TransferWallet 把 fromUserID 的全部钱包筹码转给 toUserID，双方各记一条
	// reason 流水且 reference_id 为 referenceID。fromUserID 必须没有牌桌余额。
	// 按 (fromUserID, requestID) 幂等，返回 fromUserID 转出后的快照。
	TransferWallet(ctx context.Context, fromUserID, toUserID, requestID string, reason Reason, referenceID string, now time.Time) (Snapshot, error)
	Entries(ctx context.Context, userID string, limit int) ([]Entry, error)
	// RoomLedger 汇总某人在某个房间内的钱包收支：带入与补码为负，离桌返还
	// 为正。净胜负还要加上此刻仍在牌桌上的筹码，见 Service.RoomResult。
	RoomLedger(ctx context.Context, userID, roomID string) (RoomLedger, error)
	// RakeByRoom 按房间汇总抽水，最近有抽水的房间在前，已关闭的房间也在内。
	RakeByRoom(ctx context.Context) ([]RoomRake, error)
}

// Rake 是一手牌的抽水及其收款人。Amount 为 0 表示这一手不抽水。
type Rake struct {
	RecipientUserID string
	Amount          int64
}

func (rake Rake) valid() bool {
	return rake.Amount >= 0 && (rake.Amount == 0 || rake.RecipientUserID != "")
}

// RoomRake 是一个房间的累计抽水。
type RoomRake struct {
	RoomID string `json:"roomId"`
	// RoomCode 与 Closed 由持久化仓储从房间表补上；内存仓储不知道房间，留空。
	RoomCode string    `json:"roomCode"`
	Closed   bool      `json:"closed"`
	Hands    int       `json:"hands"`
	Total    int64     `json:"total"`
	LastAt   time.Time `json:"lastAt"`
}

type Error struct{ Code string }

func (err Error) Error() string { return err.Code }

type accountState struct {
	wallet   int64
	tables   map[string]int64
	revision uint64
}

type MemoryRepository struct {
	mu       sync.Mutex
	accounts map[string]*accountState
	entries  []Entry
	results  map[string]Snapshot
	settled  map[string]struct{}
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		accounts: make(map[string]*accountState),
		results:  make(map[string]Snapshot),
		settled:  make(map[string]struct{}),
	}
}

func (repository *MemoryRepository) Snapshot(_ context.Context, userID string) (Snapshot, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.snapshotLocked(userID, ""), nil
}

func (repository *MemoryRepository) TopUp(_ context.Context, userID, requestID string, amount int64, now time.Time) (Snapshot, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if previous, ok := repository.results[idempotencyKey(userID, requestID)]; ok {
		return previous, nil
	}
	state := repository.accountLocked(userID)
	if amount <= 0 || state.wallet > maximumChipAmount-amount {
		return Snapshot{}, Error{Code: "invalid_chip_amount"}
	}
	state.wallet += amount
	state.revision++
	result := repository.snapshotLocked(userID, "")
	repository.appendLocked(Entry{
		EntryID: entryID(userID, requestID), RequestID: requestID, UserID: userID,
		Reason: ReasonVirtualTopUp, WalletDelta: amount,
		WalletBalanceAfter: result.WalletChips, RevisionAfter: result.Revision, CreatedAt: now,
	})
	repository.results[idempotencyKey(userID, requestID)] = result
	return result, nil
}

func (repository *MemoryRepository) SetWallet(
	_ context.Context,
	userID, requestID string,
	amount int64,
	now time.Time,
) (Snapshot, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if previous, ok := repository.results[idempotencyKey(userID, requestID)]; ok {
		return previous, nil
	}
	state := repository.accountLocked(userID)
	if len(state.tables) != 0 {
		return Snapshot{}, Error{Code: "user_in_room"}
	}
	if amount < 0 || amount > maximumChipAmount {
		return Snapshot{}, Error{Code: "invalid_chip_amount"}
	}
	delta := amount - state.wallet
	state.wallet = amount
	state.revision++
	result := repository.snapshotLocked(userID, "")
	repository.appendLocked(Entry{
		EntryID: entryID(userID, requestID), RequestID: requestID, UserID: userID,
		Reason: ReasonAdminAdjust, WalletDelta: delta,
		WalletBalanceAfter: result.WalletChips, RevisionAfter: result.Revision, CreatedAt: now,
	})
	repository.results[idempotencyKey(userID, requestID)] = result
	return result, nil
}

func (repository *MemoryRepository) TransferToTable(
	_ context.Context,
	userID, tableID, requestID string,
	amount, maximum int64,
	reason Reason,
	now time.Time,
) (Snapshot, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if previous, ok := repository.results[idempotencyKey(userID, requestID)]; ok {
		return previous, nil
	}
	state := repository.accountLocked(userID)
	current := state.tables[tableID]
	if tableID == "" || amount <= 0 || maximum <= 0 || current > maximum-amount {
		return Snapshot{}, Error{Code: "maximum_buy_in_exceeded"}
	}
	if state.wallet < amount {
		return Snapshot{}, Error{Code: "insufficient_wallet_chips"}
	}
	state.wallet -= amount
	state.tables[tableID] = current + amount
	state.revision++
	result := repository.snapshotLocked(userID, tableID)
	repository.appendLocked(Entry{
		EntryID: entryID(userID, requestID), RequestID: requestID, UserID: userID, TableID: tableID,
		Reason: reason, WalletDelta: -amount, TableDelta: amount,
		WalletBalanceAfter: result.WalletChips, TableBalanceAfter: result.TableChips,
		RevisionAfter: result.Revision, CreatedAt: now,
	})
	repository.results[idempotencyKey(userID, requestID)] = result
	return result, nil
}

func (repository *MemoryRepository) ApplySettlement(
	_ context.Context,
	tableID, handID string,
	balances map[string]int64,
	_ int64,
	rake Rake,
	now time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	settlementKey := tableID + "\x00" + handID
	if _, ok := repository.settled[settlementKey]; ok {
		return nil
	}
	if !rake.valid() {
		return Error{Code: "invalid_table_balance"}
	}
	var before int64
	after := rake.Amount
	for userID, balance := range balances {
		if userID == "" || balance < 0 {
			return Error{Code: "invalid_table_balance"}
		}
		before += repository.accountLocked(userID).tables[tableID]
		after += balance
	}
	if before != after {
		return Error{Code: "table_chips_not_conserved"}
	}
	// 收款人钱包放不下要在动任何余额之前发现：这里没有事务可回滚，改了一半再报错
	// 会让之后每次重试都对不上账。
	if rake.Amount > 0 && repository.accountLocked(rake.RecipientUserID).wallet > maximumChipAmount-rake.Amount {
		return Error{Code: "invalid_chip_amount"}
	}
	userIDs := make([]string, 0, len(balances))
	for userID := range balances {
		userIDs = append(userIDs, userID)
	}
	sort.Strings(userIDs)
	for _, userID := range userIDs {
		state := repository.accountLocked(userID)
		previous := state.tables[tableID]
		balance := balances[userID]
		if previous == balance {
			continue
		}
		state.tables[tableID] = balance
		state.revision++
		repository.appendLocked(Entry{
			EntryID:   entryID(userID, "settlement:"+tableID+":"+handID),
			RequestID: "settlement:" + handID, UserID: userID, TableID: tableID,
			ReferenceID: handID, Reason: ReasonSettlement, TableDelta: balance - previous,
			WalletBalanceAfter: state.wallet, TableBalanceAfter: balance,
			RevisionAfter: state.revision, CreatedAt: now,
		})
	}
	if rake.Amount > 0 {
		recipient := repository.accountLocked(rake.RecipientUserID)
		recipient.wallet += rake.Amount
		recipient.revision++
		repository.appendLocked(Entry{
			EntryID:   entryID(rake.RecipientUserID, "rake:"+tableID+":"+handID),
			RequestID: "rake:" + handID, UserID: rake.RecipientUserID, TableID: tableID,
			ReferenceID: handID, Reason: ReasonRake, WalletDelta: rake.Amount,
			WalletBalanceAfter: recipient.wallet, RevisionAfter: recipient.revision, CreatedAt: now,
		})
	}
	repository.settled[settlementKey] = struct{}{}
	return nil
}

func (repository *MemoryRepository) RakeByRoom(_ context.Context) ([]RoomRake, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	byRoom := make(map[string]*RoomRake)
	for _, entry := range repository.entries {
		if entry.Reason != ReasonRake {
			continue
		}
		summary := byRoom[entry.TableID]
		if summary == nil {
			summary = &RoomRake{RoomID: entry.TableID}
			byRoom[entry.TableID] = summary
		}
		summary.Hands++
		summary.Total += entry.WalletDelta
		if entry.CreatedAt.After(summary.LastAt) {
			summary.LastAt = entry.CreatedAt
		}
	}
	result := make([]RoomRake, 0, len(byRoom))
	for _, summary := range byRoom {
		result = append(result, *summary)
	}
	sort.Slice(result, func(left, right int) bool {
		if !result[left].LastAt.Equal(result[right].LastAt) {
			return result[left].LastAt.After(result[right].LastAt)
		}
		return result[left].RoomID < result[right].RoomID
	})
	return result, nil
}

func (repository *MemoryRepository) CashOut(_ context.Context, userID, tableID, requestID string, now time.Time) (Snapshot, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if previous, ok := repository.results[idempotencyKey(userID, requestID)]; ok {
		return previous, nil
	}
	state := repository.accountLocked(userID)
	amount := state.tables[tableID]
	if state.wallet > maximumChipAmount-amount {
		return Snapshot{}, Error{Code: "invalid_chip_amount"}
	}
	state.wallet += amount
	delete(state.tables, tableID)
	state.revision++
	result := repository.snapshotLocked(userID, "")
	repository.appendLocked(Entry{
		EntryID: entryID(userID, requestID), RequestID: requestID, UserID: userID, TableID: tableID,
		Reason: ReasonCashOut, WalletDelta: amount, TableDelta: -amount,
		WalletBalanceAfter: result.WalletChips, RevisionAfter: result.Revision, CreatedAt: now,
	})
	repository.results[idempotencyKey(userID, requestID)] = result
	return result, nil
}

func (repository *MemoryRepository) Entries(_ context.Context, userID string, limit int) ([]Entry, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	result := make([]Entry, 0, limit)
	for index := len(repository.entries) - 1; index >= 0 && len(result) < limit; index-- {
		if repository.entries[index].UserID == userID {
			result = append(result, repository.entries[index])
		}
	}
	return result, nil
}

func (repository *MemoryRepository) accountLocked(userID string) *accountState {
	state := repository.accounts[userID]
	if state == nil {
		state = &accountState{tables: make(map[string]int64)}
		repository.accounts[userID] = state
	}
	return state
}

func (repository *MemoryRepository) snapshotLocked(userID, tableID string) Snapshot {
	state := repository.accountLocked(userID)
	if tableID == "" {
		for activeTableID := range state.tables {
			tableID = activeTableID
			break
		}
	}
	return Snapshot{UserID: userID, WalletChips: state.wallet, TableID: tableID, TableChips: state.tables[tableID], Revision: state.revision}
}

func (repository *MemoryRepository) appendLocked(entry Entry) {
	repository.entries = append(repository.entries, entry)
}

func (repository *MemoryRepository) TransferWallet(
	_ context.Context,
	fromUserID, toUserID, requestID string,
	reason Reason,
	referenceID string,
	now time.Time,
) (Snapshot, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if previous, ok := repository.results[idempotencyKey(fromUserID, requestID)]; ok {
		return previous, nil
	}
	if fromUserID == "" || toUserID == "" || fromUserID == toUserID {
		return Snapshot{}, Error{Code: "invalid_user"}
	}
	from := repository.accountLocked(fromUserID)
	to := repository.accountLocked(toUserID)
	for _, balance := range from.tables {
		if balance != 0 {
			return Snapshot{}, Error{Code: "user_in_room"}
		}
	}
	amount := from.wallet
	if to.wallet > maximumChipAmount-amount {
		return Snapshot{}, Error{Code: "invalid_chip_amount"}
	}
	from.wallet = 0
	from.revision++
	to.wallet += amount
	to.revision++
	result := repository.snapshotLocked(fromUserID, "")
	recipient := repository.snapshotLocked(toUserID, "")
	repository.appendLocked(Entry{
		EntryID: entryID(fromUserID, requestID), RequestID: requestID, UserID: fromUserID,
		ReferenceID: referenceID, Reason: reason, WalletDelta: -amount,
		WalletBalanceAfter: result.WalletChips, RevisionAfter: result.Revision, CreatedAt: now,
	})
	repository.appendLocked(Entry{
		EntryID: entryID(toUserID, requestID), RequestID: requestID, UserID: toUserID,
		ReferenceID: referenceID, Reason: reason, WalletDelta: amount,
		WalletBalanceAfter: recipient.WalletChips, RevisionAfter: recipient.Revision, CreatedAt: now,
	})
	repository.results[idempotencyKey(fromUserID, requestID)] = result
	return result, nil
}

func idempotencyKey(userID, requestID string) string { return userID + "\x00" + requestID }
func entryID(userID, requestID string) string        { return fmt.Sprintf("bank_%s_%s", userID, requestID) }

var errInvalidRepository = errors.New("invalid bankroll repository")

// RoomLedger 是某人在某个房间内的钱包收支汇总。
type RoomLedger struct {
	// BoughtIn 为累计从钱包投入牌桌的筹码（带入与补码）。
	BoughtIn int64 `json:"boughtIn"`
	// ReturnedToWallet 为累计从牌桌返还钱包的筹码（离桌返还）。
	ReturnedToWallet int64 `json:"returnedToWallet"`
}

// RoomResult 是某人在某个房间内的净胜负。
type RoomResult struct {
	RoomID string `json:"roomId"`
	// TableChips 为此刻仍在牌桌上的筹码。
	TableChips int64 `json:"tableChips"`
	RoomLedger
	// Net 为净胜负：返还 + 桌上筹码 - 投入。为正表示赢。
	Net int64 `json:"net"`
}

func (repository *MemoryRepository) RoomLedger(
	_ context.Context,
	userID, roomID string,
) (RoomLedger, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	var result RoomLedger
	for _, entry := range repository.entries {
		if entry.UserID != userID || entry.TableID != roomID {
			continue
		}
		if entry.WalletDelta < 0 {
			result.BoughtIn -= entry.WalletDelta
		} else {
			result.ReturnedToWallet += entry.WalletDelta
		}
	}
	return result, nil
}
