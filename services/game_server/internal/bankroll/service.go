package bankroll

import (
	"context"
	"errors"
	"regexp"
	"time"
)

const maximumChipAmount int64 = 9_000_000_000_000_000

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9:_-]{1,96}$`)

func ValidRequestID(value string) bool { return validRequestID.MatchString(value) }

type Service struct {
	repository Repository
	now        func() time.Time
}

func NewService(repository Repository, now func() time.Time) (*Service, error) {
	if repository == nil {
		return nil, errInvalidRepository
	}
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, now: now}, nil
}

func (service *Service) Snapshot(ctx context.Context, userID string) (Snapshot, error) {
	if userID == "" {
		return Snapshot{}, Error{Code: "invalid_user"}
	}
	return service.repository.Snapshot(ctx, userID)
}

func (service *Service) TopUp(ctx context.Context, userID, requestID string, amount int64) (Snapshot, error) {
	if userID == "" || !ValidRequestID(requestID) || amount <= 0 || amount > maximumChipAmount {
		return Snapshot{}, Error{Code: "invalid_chip_amount"}
	}
	return service.repository.TopUp(ctx, userID, requestID, amount, service.now())
}

func (service *Service) SetWallet(
	ctx context.Context,
	userID, requestID string,
	amount int64,
) (Snapshot, error) {
	if userID == "" || !ValidRequestID(requestID) || amount < 0 || amount > maximumChipAmount {
		return Snapshot{}, Error{Code: "invalid_chip_amount"}
	}
	return service.repository.SetWallet(ctx, userID, requestID, amount, service.now())
}

func (service *Service) BuyIn(ctx context.Context, userID, tableID, requestID string, amount, maximum int64) (Snapshot, error) {
	return service.transfer(ctx, userID, tableID, requestID, amount, maximum, ReasonBuyIn)
}

func (service *Service) Rebuy(ctx context.Context, userID, tableID, requestID string, amount, maximum int64) (Snapshot, error) {
	return service.transfer(ctx, userID, tableID, requestID, amount, maximum, ReasonRebuy)
}

func (service *Service) transfer(ctx context.Context, userID, tableID, requestID string, amount, maximum int64, reason Reason) (Snapshot, error) {
	if userID == "" || tableID == "" || !ValidRequestID(requestID) || amount <= 0 || maximum <= 0 || maximum > maximumChipAmount {
		return Snapshot{}, Error{Code: "invalid_chip_amount"}
	}
	return service.repository.TransferToTable(ctx, userID, tableID, requestID, amount, maximum, reason, service.now())
}

func (service *Service) ApplySettlement(ctx context.Context, tableID, handID string, balances map[string]int64, maximum int64) error {
	if tableID == "" || handID == "" || len(balances) < 2 || maximum <= 0 {
		return Error{Code: "invalid_table_balance"}
	}
	return service.repository.ApplySettlement(ctx, tableID, handID, balances, maximum, service.now())
}

// TransferWallet 把一名用户的全部钱包筹码转给另一名用户，用于账号注销。
// referenceID 写入双方流水的 reference_id，便于事后追溯来源。
func (service *Service) TransferWallet(ctx context.Context, fromUserID, toUserID, requestID, referenceID string) (Snapshot, error) {
	if fromUserID == "" || toUserID == "" || fromUserID == toUserID || !ValidRequestID(requestID) {
		return Snapshot{}, Error{Code: "invalid_request"}
	}
	return service.repository.TransferWallet(
		ctx, fromUserID, toUserID, requestID, ReasonAccountDeletion, referenceID, service.now(),
	)
}

func (service *Service) CashOut(ctx context.Context, userID, tableID, requestID string) (Snapshot, error) {
	if userID == "" || tableID == "" || !ValidRequestID(requestID) {
		return Snapshot{}, Error{Code: "invalid_request"}
	}
	return service.repository.CashOut(ctx, userID, tableID, requestID, service.now())
}

func (service *Service) Entries(ctx context.Context, userID string, limit int) ([]Entry, error) {
	if userID == "" || limit <= 0 || limit > 100 {
		return nil, Error{Code: "invalid_request"}
	}
	return service.repository.Entries(ctx, userID, limit)
}

func IsErrorCode(err error, code string) bool {
	var bankrollError Error
	return errors.As(err, &bankrollError) && bankrollError.Code == code
}

// RoomResult 汇总某人在某个房间内的净胜负。
//
// 净胜负 = 离桌返还 + 此刻桌上筹码 - 累计带入。把桌上筹码计入的原因是
// 玩家通常在牌局中途查看，此时这一局的盈亏还没有通过离桌返还落回钱包。
func (service *Service) RoomResult(ctx context.Context, userID, roomID string) (RoomResult, error) {
	if userID == "" || roomID == "" {
		return RoomResult{}, Error{Code: "invalid_request"}
	}
	ledger, err := service.repository.RoomLedger(ctx, userID, roomID)
	if err != nil {
		return RoomResult{}, err
	}
	snapshot, err := service.repository.Snapshot(ctx, userID)
	if err != nil {
		return RoomResult{}, err
	}
	tableChips := int64(0)
	if snapshot.TableID == roomID {
		tableChips = snapshot.TableChips
	}
	return RoomResult{
		RoomID:     roomID,
		TableChips: tableChips,
		RoomLedger: ledger,
		Net:        ledger.ReturnedToWallet + tableChips - ledger.BoughtIn,
	}, nil
}
