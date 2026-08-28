package chat

import (
	"errors"
	"sync"
	"time"
)

type Store interface {
	ByClientMessage(tableID, userID, clientMessageID string) (Message, bool, error)
	Save(message Message) (Message, error)
	History(tableID string, limit int) ([]Message, error)
	SetMuted(change ModerationChange) error
	IsMuted(userID string) (bool, error)
}

type ModerationChange struct {
	AuditEventID string
	ActorUserID  string
	TargetUserID string
	Muted        bool
	ChangedAt    time.Time
}

type MemoryStore struct {
	mu       sync.RWMutex
	accepted map[string]Message
	history  map[string][]Message
	muted    map[string]bool
	changes  []ModerationChange
	limit    int
}

func NewMemoryStore(limit int) *MemoryStore {
	return &MemoryStore{
		accepted: make(map[string]Message),
		history:  make(map[string][]Message),
		muted:    make(map[string]bool),
		limit:    limit,
	}
}

func (store *MemoryStore) ByClientMessage(tableID, userID, clientMessageID string) (Message, bool, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	message, exists := store.accepted[messageKey(tableID, userID, clientMessageID)]
	return message, exists, nil
}

func (store *MemoryStore) Save(message Message) (Message, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := messageKey(message.TableID, message.UserID, message.ClientMessageID)
	if previous, exists := store.accepted[key]; exists {
		return previous, nil
	}
	if message.MessageID == "" {
		return Message{}, errors.New("message id is required")
	}
	store.accepted[key] = message
	history := append(store.history[message.TableID], message)
	if store.limit > 0 && len(history) > store.limit {
		history = append([]Message(nil), history[len(history)-store.limit:]...)
	}
	store.history[message.TableID] = history
	return message, nil
}

func (store *MemoryStore) History(tableID string, limit int) ([]Message, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	history := store.history[tableID]
	if len(history) > limit {
		history = history[len(history)-limit:]
	}
	return append([]Message(nil), history...), nil
}

func (store *MemoryStore) SetMuted(change ModerationChange) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if change.Muted {
		store.muted[change.TargetUserID] = true
	} else {
		delete(store.muted, change.TargetUserID)
	}
	store.changes = append(store.changes, change)
	return nil
}

func (store *MemoryStore) IsMuted(userID string) (bool, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.muted[userID], nil
}

func messageKey(tableID, userID, clientMessageID string) string {
	return tableID + "\x00" + userID + "\x00" + clientMessageID
}
