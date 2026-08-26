package room

import (
	"context"
	"errors"
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

func cloneRoom(value Room) Room {
	value.Members = append([]Member(nil), value.Members...)
	return value
}
