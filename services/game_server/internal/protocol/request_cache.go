package protocol

import "sync"

// RequestCache retains recent responses by user and request ID so reconnects
// and retries cannot execute the same state-changing request twice.
type RequestCache struct {
	mu       sync.Mutex
	capacity int
	users    map[string]*userRequestCache
}

type userRequestCache struct {
	order     []string
	responses map[string]Envelope
}

func NewRequestCache(capacity int) *RequestCache {
	if capacity <= 0 {
		capacity = 1
	}
	return &RequestCache{
		capacity: capacity,
		users:    make(map[string]*userRequestCache),
	}
}

func (cache *RequestCache) Get(userID, requestID string) (Envelope, bool) {
	if userID == "" || requestID == "" {
		return Envelope{}, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	user := cache.users[userID]
	if user == nil {
		return Envelope{}, false
	}
	result, ok := user.responses[requestID]
	if !ok {
		return Envelope{}, false
	}
	return cloneEnvelope(result), true
}

func (cache *RequestCache) Put(userID, requestID string, response Envelope) {
	if userID == "" || requestID == "" {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	user := cache.users[userID]
	if user == nil {
		user = &userRequestCache{responses: make(map[string]Envelope)}
		cache.users[userID] = user
	}
	if _, exists := user.responses[requestID]; exists {
		return
	}
	user.order = append(user.order, requestID)
	user.responses[requestID] = cloneEnvelope(response)
	if len(user.order) <= cache.capacity {
		return
	}
	oldest := user.order[0]
	user.order = user.order[1:]
	delete(user.responses, oldest)
}
