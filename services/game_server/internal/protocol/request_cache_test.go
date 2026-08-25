package protocol

import (
	"encoding/json"
	"testing"
)

func TestRequestCacheReturnsFirstResponseAndEvictsOldest(t *testing.T) {
	cache := NewRequestCache(2)
	cache.Put("user", "one", Envelope{Type: "accepted", Payload: json.RawMessage(`{"value":1}`)})
	cache.Put("user", "one", Envelope{Type: "rejected"})
	cache.Put("user", "two", Envelope{Type: "accepted"})

	first, ok := cache.Get("user", "one")
	if !ok || first.Type != "accepted" {
		t.Fatalf("first=%#v ok=%v", first, ok)
	}
	first.Payload[0] = 'x'
	unchanged, _ := cache.Get("user", "one")
	if unchanged.Payload[0] != '{' {
		t.Fatal("cached payload was mutated by caller")
	}

	cache.Put("user", "three", Envelope{Type: "accepted"})
	if _, ok := cache.Get("user", "one"); ok {
		t.Fatal("oldest response was not evicted")
	}
	if _, ok := cache.Get("other", "two"); ok {
		t.Fatal("response leaked across users")
	}
}
