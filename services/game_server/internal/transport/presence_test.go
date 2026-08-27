package transport

import (
	"testing"
	"time"
)

func TestPresenceTrackerExpiresInactiveUsers(t *testing.T) {
	now := time.Unix(1_000, 0)
	tracker := newPresenceTracker()
	tracker.now = func() time.Time { return now }

	tracker.touch("user-1")
	if !tracker.online("user-1") {
		t.Fatal("recently active user should be online")
	}
	now = now.Add(onlinePresenceTTL + time.Second)
	if tracker.online("user-1") {
		t.Fatal("inactive user should be offline")
	}
}
