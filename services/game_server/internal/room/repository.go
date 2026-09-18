package room

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	ErrNotFound = errors.New("room not found")
	ErrConflict = errors.New("room conflict")
)

type Repository interface {
	Create(ctx context.Context, room Room) error
	Save(ctx context.Context, room Room) error
	ByID(ctx context.Context, roomID string) (Room, error)
	ByCode(ctx context.Context, code string) (Room, error)
	ByUser(ctx context.Context, userID string) (Room, error)
	Delete(ctx context.Context, roomID string) error
	// SaveRake 只改房间的抽水规则与版本号，不碰成员表。管理员会在牌局进行中调用它，
	// 而 Save 会按调用方手里的旧值整体重写成员行（含桌上筹码），与结算、补码、
	// 离桌返还这些直接改成员行的事务交错时会把筹码写回旧值。
	SaveRake(ctx context.Context, roomID string, settings RakeSettings) (Room, error)
	// ListOpen 返回所有未关闭的房间，新建的在前。供管理员设置抽水时选择房间。
	ListOpen(ctx context.Context) ([]Room, error)
}

// BuyInRepository is implemented by persistent repositories that must commit
// wallet and room membership changes in one database transaction.
type BuyInRepository interface {
	CreateWithBuyIn(ctx context.Context, value Room, requestID string, amount int64, now time.Time) error
	JoinWithBuyIn(ctx context.Context, roomID string, member Member, requestID string, amount int64, now time.Time) (Room, error)
}

type MemoryRepository struct {
	mu           sync.RWMutex
	byID         map[string]Room
	roomIDByCode map[string]string
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		byID:         make(map[string]Room),
		roomIDByCode: make(map[string]string),
	}
}

func (repository *MemoryRepository) Create(_ context.Context, value Room) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, exists := repository.byID[value.RoomID]; exists {
		return ErrConflict
	}
	if _, exists := repository.roomIDByCode[value.Code]; exists {
		return ErrConflict
	}
	repository.byID[value.RoomID] = cloneRoom(value)
	repository.roomIDByCode[value.Code] = value.RoomID
	return nil
}

func (repository *MemoryRepository) Save(_ context.Context, value Room) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, exists := repository.byID[value.RoomID]; !exists {
		return ErrNotFound
	}
	repository.byID[value.RoomID] = cloneRoom(value)
	return nil
}

func (repository *MemoryRepository) SaveRake(_ context.Context, roomID string, settings RakeSettings) (Room, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	value, exists := repository.byID[roomID]
	if !exists {
		return Room{}, ErrNotFound
	}
	value.Rake = settings
	value.Revision++
	repository.byID[roomID] = value
	return cloneRoom(value), nil
}

func (repository *MemoryRepository) ByID(_ context.Context, roomID string) (Room, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	value, exists := repository.byID[roomID]
	if !exists {
		return Room{}, ErrNotFound
	}
	return cloneRoom(value), nil
}

func (repository *MemoryRepository) ByCode(_ context.Context, code string) (Room, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	roomID, exists := repository.roomIDByCode[code]
	if !exists {
		return Room{}, ErrNotFound
	}
	return cloneRoom(repository.byID[roomID]), nil
}

func (repository *MemoryRepository) ByUser(_ context.Context, userID string) (Room, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	for _, value := range repository.byID {
		for _, member := range value.Members {
			if member.UserID == userID {
				return cloneRoom(value), nil
			}
		}
	}
	return Room{}, ErrNotFound
}

func (repository *MemoryRepository) Delete(_ context.Context, roomID string) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	value, exists := repository.byID[roomID]
	if !exists {
		return nil
	}
	delete(repository.byID, roomID)
	delete(repository.roomIDByCode, value.Code)
	return nil
}

func (repository *MemoryRepository) ListOpen(_ context.Context) ([]Room, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	result := make([]Room, 0, len(repository.byID))
	for _, value := range repository.byID {
		result = append(result, cloneRoom(value))
	}
	sort.Slice(result, func(left, right int) bool {
		if !result[left].CreatedAt.Equal(result[right].CreatedAt) {
			return result[left].CreatedAt.After(result[right].CreatedAt)
		}
		return result[left].RoomID < result[right].RoomID
	})
	return result, nil
}

func cloneRoom(value Room) Room {
	value.Members = append([]Member(nil), value.Members...)
	return value
}
