// Package ratelimit 提供进程内的令牌桶限流与并发计数。
//
// 当前游戏服务是单实例，限流状态保存在内存中即可；接入 Redis 与多实例时，
// 这里的接口保持不变，只需替换存储实现。
package ratelimit

import (
	"sync"
	"time"
)

// Limiter 是按字符串键区分的令牌桶限流器。
//
// 每个键拥有容量为 burst 的桶，以 burst/window 的速率匀速补充。
// 这等价于「每 window 内最多 burst 次」的直观语义，同时允许短时突发。
type Limiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	burst     float64
	refillPer time.Duration // 补满一个令牌所需时间
	now       func() time.Time
	lastSweep time.Time
}

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

// 空闲桶超过该时长且已回满时被回收，防止键空间无限增长。
const sweepInterval = 5 * time.Minute

// New 创建「每 window 内最多 burst 次」的限流器。
func New(burst int, window time.Duration) *Limiter {
	if burst <= 0 {
		burst = 1
	}
	if window <= 0 {
		window = time.Second
	}
	return &Limiter{
		buckets:   make(map[string]*bucket),
		burst:     float64(burst),
		refillPer: window / time.Duration(burst),
		now:       time.Now,
	}
}

// Allow 在配额允许时消耗一次并返回 true，否则返回 false 且不消耗。
func (limiter *Limiter) Allow(key string) bool {
	return limiter.take(key, true)
}

// Peek 报告当前是否还有配额，但不消耗。
// 用于「先判断是否被锁定，失败后再计数」的登录场景。
func (limiter *Limiter) Peek(key string) bool {
	return limiter.take(key, false)
}

// Reset 清除某个键的计数，例如登录成功后解除该用户名的失败累计。
func (limiter *Limiter) Reset(key string) {
	if limiter == nil {
		return
	}
	limiter.mu.Lock()
	delete(limiter.buckets, key)
	limiter.mu.Unlock()
}

// RetryAfter 估算某个键距离下一次可用还需等待多久。
// 只在拒绝后用于设置 Retry-After 响应头，向上取整到秒。
func (limiter *Limiter) RetryAfter(key string) time.Duration {
	if limiter == nil {
		return 0
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	entry, exists := limiter.buckets[key]
	if !exists {
		return 0
	}
	limiter.refill(entry)
	if entry.tokens >= 1 {
		return 0
	}
	missing := 1 - entry.tokens
	wait := time.Duration(missing * float64(limiter.refillPer))
	return wait.Round(time.Second) + time.Second
}

func (limiter *Limiter) take(key string, consume bool) bool {
	if limiter == nil {
		return true
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	current := limiter.now()
	limiter.sweepLocked(current)

	entry, exists := limiter.buckets[key]
	if !exists {
		entry = &bucket{tokens: limiter.burst, lastSeen: current}
		limiter.buckets[key] = entry
	}
	limiter.refill(entry)
	if entry.tokens < 1 {
		return false
	}
	if consume {
		entry.tokens--
	}
	return true
}

func (limiter *Limiter) refill(entry *bucket) {
	current := limiter.now()
	elapsed := current.Sub(entry.lastSeen)
	if elapsed > 0 {
		entry.tokens += float64(elapsed) / float64(limiter.refillPer)
		if entry.tokens > limiter.burst {
			entry.tokens = limiter.burst
		}
	}
	entry.lastSeen = current
}

func (limiter *Limiter) sweepLocked(current time.Time) {
	if current.Sub(limiter.lastSweep) < sweepInterval {
		return
	}
	limiter.lastSweep = current
	for key, entry := range limiter.buckets {
		idle := current.Sub(entry.lastSeen)
		// 空闲时间足以回满整桶时，桶与新建无异，可以安全删除
		if idle >= time.Duration(limiter.burst*float64(limiter.refillPer)) && idle >= sweepInterval {
			delete(limiter.buckets, key)
		}
	}
}

// Concurrent 限制每个键同时持有的资源数量，例如单 IP 的 WebSocket 连接数。
type Concurrent struct {
	mu      sync.Mutex
	counts  map[string]int
	maximum int
}

// NewConcurrent 创建每键最多 maximum 个并发的计数器。
func NewConcurrent(maximum int) *Concurrent {
	if maximum <= 0 {
		maximum = 1
	}
	return &Concurrent{counts: make(map[string]int), maximum: maximum}
}

// Acquire 尝试为键占用一个名额；成功返回 true，调用方必须在结束时 Release。
func (concurrent *Concurrent) Acquire(key string) bool {
	if concurrent == nil {
		return true
	}
	concurrent.mu.Lock()
	defer concurrent.mu.Unlock()
	if concurrent.counts[key] >= concurrent.maximum {
		return false
	}
	concurrent.counts[key]++
	return true
}

// Release 归还一个名额。
func (concurrent *Concurrent) Release(key string) {
	if concurrent == nil {
		return
	}
	concurrent.mu.Lock()
	defer concurrent.mu.Unlock()
	if concurrent.counts[key] <= 1 {
		delete(concurrent.counts, key)
		return
	}
	concurrent.counts[key]--
}

// Count 返回键当前占用的名额数，供测试与指标使用。
func (concurrent *Concurrent) Count(key string) int {
	if concurrent == nil {
		return 0
	}
	concurrent.mu.Lock()
	defer concurrent.mu.Unlock()
	return concurrent.counts[key]
}
