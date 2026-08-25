package ledger

import (
	"errors"
	"sync"
)

type Entry struct {
	EntryID      string `json:"entryId"`
	HandID       string `json:"handId"`
	PlayerID     string `json:"playerId"`
	Delta        int64  `json:"delta"`
	BalanceAfter int64  `json:"balanceAfter"`
}

type Store interface {
	Append(entries []Entry) error
	EntriesForHand(handID string) []Entry
}

type InMemoryStore struct {
	mu      sync.RWMutex
	entries []Entry
	byID    map[string]struct{}
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{byID: make(map[string]struct{})}
}

func (store *InMemoryStore) Append(entries []Entry) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	batchIDs := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.EntryID == "" || entry.HandID == "" || entry.PlayerID == "" || entry.BalanceAfter < 0 {
			return errors.New("invalid ledger entry")
		}
		if _, exists := store.byID[entry.EntryID]; exists {
			return errors.New("ledger entry already exists")
		}
		if _, exists := batchIDs[entry.EntryID]; exists {
			return errors.New("duplicate ledger entry in batch")
		}
		batchIDs[entry.EntryID] = struct{}{}
	}
	store.entries = append(store.entries, entries...)
	for entryID := range batchIDs {
		store.byID[entryID] = struct{}{}
	}
	return nil
}

func (store *InMemoryStore) EntriesForHand(handID string) []Entry {
	store.mu.RLock()
	defer store.mu.RUnlock()
	result := make([]Entry, 0)
	for _, entry := range store.entries {
		if entry.HandID == handID {
			result = append(result, entry)
		}
	}
	return result
}
