package ratelimit

import (
	"testing"
	"time"
)

func newTestLimiter(burst int, window time.Duration) (*Limiter, *time.Time) {
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	limiter := New(burst, window)
	limiter.now = func() time.Time { return clock }
	return limiter, &clock
}

func TestLimiterAllowsBurstThenRejects(t *testing.T) {
	limiter, _ := newTestLimiter(3, time.Minute)
	for index := 0; index < 3; index++ {
		if !limiter.Allow("ip") {
			t.Fatalf("attempt %d should be allowed within burst", index+1)
		}
	}
	if limiter.Allow("ip") {
		t.Fatal("fourth attempt should be rejected")
	}
	// 其他键互不影响
	if !limiter.Allow("other") {
		t.Fatal("a different key must have its own bucket")
	}
}

func TestLimiterRefillsOverTime(t *testing.T) {
	limiter, clock := newTestLimiter(2, 20*time.Second) // 每 10 秒补一个
	limiter.Allow("k")
	limiter.Allow("k")
	if limiter.Allow("k") {
		t.Fatal("bucket should be empty")
	}
	*clock = clock.Add(10 * time.Second)
	if !limiter.Allow("k") {
		t.Fatal("one token should have refilled after 10s")
	}
	if limiter.Allow("k") {
		t.Fatal("only one token should have refilled")
	}
	*clock = clock.Add(time.Hour)
	// 长时间空闲后回满，但不超过容量
	if !limiter.Allow("k") || !limiter.Allow("k") || limiter.Allow("k") {
		t.Fatal("bucket should refill to exactly burst capacity")
	}
}

func TestPeekDoesNotConsumeAndResetClears(t *testing.T) {
	limiter, _ := newTestLimiter(1, time.Minute)
	if !limiter.Peek("user") {
		t.Fatal("peek should report availability")
	}
	if !limiter.Peek("user") {
		t.Fatal("peek must not consume")
	}
	if !limiter.Allow("user") {
		t.Fatal("allow should consume the single token")
	}
	if limiter.Peek("user") {
		t.Fatal("peek should now report exhaustion")
	}
	limiter.Reset("user")
	if !limiter.Allow("user") {
		t.Fatal("reset should restore the bucket")
	}
}

func TestRetryAfterIsPositiveOnlyWhenExhausted(t *testing.T) {
	limiter, _ := newTestLimiter(1, 30*time.Second)
	if limiter.RetryAfter("k") != 0 {
		t.Fatal("unknown key needs no wait")
	}
	limiter.Allow("k")
	wait := limiter.RetryAfter("k")
	if wait <= 0 || wait > 31*time.Second {
		t.Fatalf("unexpected retry-after %v", wait)
	}
}

func TestSweepRemovesIdleBuckets(t *testing.T) {
	limiter, clock := newTestLimiter(1, time.Second)
	limiter.Allow("stale")
	*clock = clock.Add(2 * sweepInterval)
	limiter.Allow("fresh") // 触发清扫
	limiter.mu.Lock()
	_, staleExists := limiter.buckets["stale"]
	limiter.mu.Unlock()
	if staleExists {
		t.Fatal("idle, refilled bucket should have been swept")
	}
}

func TestNilLimiterIsPermissive(t *testing.T) {
	var limiter *Limiter
	if !limiter.Allow("x") || !limiter.Peek("x") || limiter.RetryAfter("x") != 0 {
		t.Fatal("nil limiter must allow everything so limits can be disabled")
	}
}

func TestConcurrentAcquireRelease(t *testing.T) {
	concurrent := NewConcurrent(2)
	if !concurrent.Acquire("ip") || !concurrent.Acquire("ip") {
		t.Fatal("two acquisitions should succeed")
	}
	if concurrent.Acquire("ip") {
		t.Fatal("third acquisition must be rejected")
	}
	if concurrent.Count("ip") != 2 {
		t.Fatalf("count=%d", concurrent.Count("ip"))
	}
	concurrent.Release("ip")
	if !concurrent.Acquire("ip") {
		t.Fatal("release should free a slot")
	}
	concurrent.Release("ip")
	concurrent.Release("ip")
	if concurrent.Count("ip") != 0 {
		t.Fatal("fully released key should be removed")
	}
	// 多余的 Release 不得让计数变负
	concurrent.Release("ip")
	if concurrent.Count("ip") != 0 {
		t.Fatal("over-release must not underflow")
	}
	var nilConcurrent *Concurrent
	if !nilConcurrent.Acquire("x") {
		t.Fatal("nil concurrent limiter must be permissive")
	}
}
