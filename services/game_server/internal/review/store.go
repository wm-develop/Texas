// Package review 是 AI 复盘：把本人某一手的决策连同服务端算好的局面数据交给
// 兼容 OpenAI 接口的大模型，结果按手缓存。只对管理员开通的账号开放。
package review

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// 复盘任务的状态。
const (
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// ErrExists 表示同一手、同一人、同一版提示词的复盘已经存在。
var ErrExists = errors.New("review already exists")

// ErrNotFound 表示没有这条复盘。
var ErrNotFound = errors.New("review not found")

// Settings 是管理员可随时调整的全局设置。两个额度为 0 表示不限。
type Settings struct {
	Enabled bool `json:"enabled"`
	// DailyLimitPerUser 是每人最近 24 小时内最多发起几次复盘。
	DailyLimitPerUser int `json:"dailyLimitPerUser"`
	// MonthlyTokenBudget 是全服最近 30 天最多消耗多少 token（输入加输出）。
	MonthlyTokenBudget int64 `json:"monthlyTokenBudget"`
}

// Access 是一条开通记录。
type Access struct {
	UserID    string    `json:"userId"`
	GrantedBy string    `json:"grantedBy"`
	GrantedAt time.Time `json:"grantedAt"`
}

// Review 是一手牌的一次复盘。
type Review struct {
	ReviewID      string     `json:"reviewId"`
	HandID        string     `json:"handId"`
	UserID        string     `json:"userId"`
	PromptVersion string     `json:"promptVersion"`
	Status        string     `json:"status"`
	Model         string     `json:"model,omitempty"`
	Result        *Result    `json:"result,omitempty"`
	Error         string     `json:"failure,omitempty"`
	InputTokens   int64      `json:"inputTokens"`
	OutputTokens  int64      `json:"outputTokens"`
	CreatedAt     time.Time  `json:"createdAt"`
	RequestedAt   time.Time  `json:"requestedAt"`
	FinishedAt    *time.Time `json:"finishedAt,omitempty"`
}

// Usage 是用量统计。
type Usage struct {
	Requests24h int   `json:"requests24h"`
	Requests30d int   `json:"requests30d"`
	Tokens30d   int64 `json:"tokens30d"`
}

type Store interface {
	Settings(ctx context.Context) (Settings, error)
	SaveSettings(ctx context.Context, settings Settings, actorUserID string, now time.Time) error
	HasAccess(ctx context.Context, userID string) (bool, error)
	SetAccess(ctx context.Context, userID string, granted bool, actorUserID string, now time.Time) error
	AccessList(ctx context.Context) ([]Access, error)

	// Find 返回某人某手某版提示词的复盘。
	Find(ctx context.Context, userID, handID, promptVersion string) (Review, error)
	// Create 新建一条排队中的复盘；已存在时返回 ErrExists。
	Create(ctx context.Context, value Review) error
	// Requeue 把失败的复盘重新排队，requested_at 更新为 now。
	Requeue(ctx context.Context, reviewID string, now time.Time) error
	// ClaimNext 取出最早排队的一条并标为进行中；没有时第二个返回值为 false。
	ClaimNext(ctx context.Context) (Review, bool, error)
	// Finish 写入结果或失败原因，并把本次消耗的 token 累加上去。
	Finish(ctx context.Context, reviewID, status, model string, result *Result, failure string,
		inputTokens, outputTokens int64, now time.Time) error
	// RequeueRunning 把进行中的全部放回队列：服务重启时它们的处理已经中断。
	RequeueRunning(ctx context.Context) error
	// FailIfRunning 把仍是「进行中」的那一条记成失败；已经不是进行中时返回
	// ErrNotFound（后台协程可能刚好把它做完了，不能覆盖）。
	FailIfRunning(ctx context.Context, reviewID, failure string, now time.Time) error
	// CountRequests 统计某人 since 之后发起、没有失败的复盘数（排队、进行中、已完成）。
	// 失败的不占次数：否则上限设成 1 时，唯一那次赶上模型出错，玩家什么都没拿到
	// 却再也不能重试。失败消耗的 token 仍计入全服预算。
	CountRequests(ctx context.Context, userID string, since time.Time) (int, error)
	// Usage 返回全服用量统计。
	Usage(ctx context.Context, now time.Time) (Usage, error)
}

// MemoryStore 是内存实现，供测试与不接数据库的开发环境使用。
type MemoryStore struct {
	mu       sync.Mutex
	settings Settings
	access   map[string]Access
	reviews  map[string]Review
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		settings: Settings{Enabled: true},
		access:   make(map[string]Access),
		reviews:  make(map[string]Review),
	}
}

func (store *MemoryStore) Settings(context.Context) (Settings, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.settings, nil
}

func (store *MemoryStore) SaveSettings(_ context.Context, settings Settings, _ string, _ time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.settings = settings
	return nil
}

func (store *MemoryStore) HasAccess(_ context.Context, userID string) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	_, granted := store.access[userID]
	return granted, nil
}

func (store *MemoryStore) SetAccess(_ context.Context, userID string, granted bool, actorUserID string, now time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if !granted {
		delete(store.access, userID)
		return nil
	}
	if _, exists := store.access[userID]; !exists {
		store.access[userID] = Access{UserID: userID, GrantedBy: actorUserID, GrantedAt: now}
	}
	return nil
}

func (store *MemoryStore) AccessList(context.Context) ([]Access, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make([]Access, 0, len(store.access))
	for _, value := range store.access {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].GrantedAt.Before(result[right].GrantedAt) })
	return result, nil
}

func (store *MemoryStore) Find(_ context.Context, userID, handID, promptVersion string) (Review, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, value := range store.reviews {
		if value.UserID == userID && value.HandID == handID && value.PromptVersion == promptVersion {
			return cloneReview(value), nil
		}
	}
	return Review{}, ErrNotFound
}

func (store *MemoryStore) Create(_ context.Context, value Review) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, existing := range store.reviews {
		if existing.UserID == value.UserID && existing.HandID == value.HandID && existing.PromptVersion == value.PromptVersion {
			return ErrExists
		}
	}
	store.reviews[value.ReviewID] = cloneReview(value)
	return nil
}

func (store *MemoryStore) Requeue(_ context.Context, reviewID string, now time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, exists := store.reviews[reviewID]
	if !exists || value.Status != StatusFailed {
		return ErrNotFound
	}
	value.Status, value.RequestedAt, value.Error, value.FinishedAt = StatusQueued, now, "", nil
	store.reviews[reviewID] = value
	return nil
}

func (store *MemoryStore) ClaimNext(context.Context) (Review, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var next *Review
	for id := range store.reviews {
		value := store.reviews[id]
		if value.Status != StatusQueued {
			continue
		}
		if next == nil || value.RequestedAt.Before(next.RequestedAt) ||
			(value.RequestedAt.Equal(next.RequestedAt) && value.ReviewID < next.ReviewID) {
			copied := value
			next = &copied
		}
	}
	if next == nil {
		return Review{}, false, nil
	}
	next.Status = StatusRunning
	store.reviews[next.ReviewID] = *next
	return cloneReview(*next), true, nil
}

func (store *MemoryStore) Finish(_ context.Context, reviewID, status, model string, result *Result, failure string,
	inputTokens, outputTokens int64, now time.Time,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, exists := store.reviews[reviewID]
	if !exists {
		return ErrNotFound
	}
	value.Status, value.Model, value.Error = status, model, failure
	value.Result = cloneResult(result)
	value.InputTokens += inputTokens
	value.OutputTokens += outputTokens
	finished := now
	value.FinishedAt = &finished
	store.reviews[reviewID] = value
	return nil
}

func (store *MemoryStore) FailIfRunning(_ context.Context, reviewID, failure string, now time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, exists := store.reviews[reviewID]
	if !exists || value.Status != StatusRunning {
		return ErrNotFound
	}
	finished := now
	value.Status, value.Error, value.Result, value.FinishedAt = StatusFailed, failure, nil, &finished
	store.reviews[reviewID] = value
	return nil
}

func (store *MemoryStore) RequeueRunning(context.Context) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for id, value := range store.reviews {
		if value.Status == StatusRunning {
			value.Status = StatusQueued
			store.reviews[id] = value
		}
	}
	return nil
}

func (store *MemoryStore) CountRequests(_ context.Context, userID string, since time.Time) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	count := 0
	for _, value := range store.reviews {
		if value.UserID == userID && value.Status != StatusFailed && !value.RequestedAt.Before(since) {
			count++
		}
	}
	return count, nil
}

func (store *MemoryStore) Usage(_ context.Context, now time.Time) (Usage, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var usage Usage
	for _, value := range store.reviews {
		if !value.RequestedAt.Before(now.Add(-24 * time.Hour)) {
			usage.Requests24h++
		}
		if !value.RequestedAt.Before(now.Add(-30 * 24 * time.Hour)) {
			usage.Requests30d++
		}
		// 重新排队时 finished_at 清空，这期间按发起时间算，之前失败花掉的 token
		// 不能从预算里消失
		counted := value.RequestedAt
		if value.FinishedAt != nil {
			counted = *value.FinishedAt
		}
		if !counted.Before(now.Add(-30 * 24 * time.Hour)) {
			usage.Tokens30d += value.InputTokens + value.OutputTokens
		}
	}
	return usage, nil
}

func cloneReview(value Review) Review {
	value.Result = cloneResult(value.Result)
	if value.FinishedAt != nil {
		finished := *value.FinishedAt
		value.FinishedAt = &finished
	}
	return value
}

func cloneResult(result *Result) *Result {
	if result == nil {
		return nil
	}
	copied := *result
	copied.Decisions = append([]Decision(nil), result.Decisions...)
	copied.KeyLessons = append([]string(nil), result.KeyLessons...)
	copied.OpponentNotes = append([]string(nil), result.OpponentNotes...)
	return &copied
}

var _ Store = (*MemoryStore)(nil)
