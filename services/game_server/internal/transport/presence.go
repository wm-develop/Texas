package transport

import (
	"sync"
	"time"
)

const onlinePresenceTTL = 90 * time.Second

type presenceTracker struct {
	mu       sync.Mutex
	lastSeen map[string]time.Time
	now      func() time.Time
}

func newPresenceTracker() *presenceTracker {
	return &presenceTracker{
		lastSeen: make(map[string]time.Time),
		now:      time.Now,
	}
}

func (tracker *presenceTracker) touch(userID string) {
	if tracker == nil || userID == "" {
		return
	}
	tracker.mu.Lock()
	tracker.lastSeen[userID] = tracker.now()
	tracker.mu.Unlock()
}

func (tracker *presenceTracker) online(userID string) bool {
	if tracker == nil || userID == "" {
		return false
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	lastSeen, exists := tracker.lastSeen[userID]
	if !exists {
		return false
	}
	if tracker.now().Sub(lastSeen) > onlinePresenceTTL {
		delete(tracker.lastSeen, userID)
		return false
	}
	return true
}
