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

// ErrNotFound 表示没有这条复盘（或这个人不在开通名单里）。
var ErrNotFound = errors.New("review not found")

// Settings 是管理员可随时调整的全局设置。额度为 0 表示不限。
type Settings struct {
	Enabled bool `json:"enabled"`
	// DailyLimitPerUser 是每人最近 24 小时内最多发起几次复盘（失败的不算）。
	DailyLimitPerUser int `json:"dailyLimitPerUser"`
	// MaxInFlightPerUser 是每人同时最多有几条在排队或分析中。
	MaxInFlightPerUser int `json:"maxInFlightPerUser"`
	// MonthlyTokenBudget 是全服最近 30 天最多消耗多少 token（输入加输出）。
	MonthlyTokenBudget int64 `json:"monthlyTokenBudget"`
}

// UserLimits 是管理员给某个人单独设的额度；为空的一项跟随全局设置，0 表示不限。
type UserLimits struct {
	DailyLimit  *int `json:"dailyLimit"`
	MaxInFlight *int `json:"maxInFlight"`
}

// Access 是一条开通记录。
type Access struct {
	UserID    string    `json:"userId"`
	GrantedBy string    `json:"grantedBy"`
	GrantedAt time.Time `json:"grantedAt"`
	UserLimits
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
	// Attempts 是因模型服务繁忙等临时原因已经失败、自动重试了几次；NextAttemptAt
	// 是下一次重试的时间，到点之前不会被领取。
	Attempts      int        `json:"attempts,omitempty"`
	NextAttemptAt *time.Time `json:"nextAttemptAt,omitempty"`
	// Outdated 表示这是旧版提示词的结果：新版还没分析过时先给玩家看旧的。不入库。
	Outdated bool `json:"outdated,omitempty"`
	// Previous 是当前版本还没有结果时，旧版提示词最近一次完成的结果。不入库。
	Previous *Result `json:"previous,omitempty"`
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
	// AccessFor 返回某人的开通记录；不在名单里时返回 ErrNotFound。
	AccessFor(ctx context.Context, userID string) (Access, error)
	SetAccess(ctx context.Context, userID string, granted bool, actorUserID string, now time.Time) error
	// SetLimits 设置某人的单独额度；不在名单里时返回 ErrNotFound。
	SetLimits(ctx context.Context, userID string, limits UserLimits) error
	AccessList(ctx context.Context) ([]Access, error)

	// Find 返回某人某手某版提示词的复盘。
	Find(ctx context.Context, userID, handID, promptVersion string) (Review, error)
	// FindLatestDone 返回某人某手任意版本里最近完成的一条。
	FindLatestDone(ctx context.Context, userID, handID string) (Review, error)
	// Create 新建一条排队中的复盘；已存在时返回 ErrExists。
	Create(ctx context.Context, value Review) error
	// Requeue 把失败的复盘重新排队，requested_at 更新为 now，重试次数清零。
	Requeue(ctx context.Context, reviewID string, now time.Time) error
	// ClaimNext 取出最早排队、且已到重试时间的一条并标为进行中；没有时第二个
	// 返回值为 false。
	ClaimNext(ctx context.Context, now time.Time) (Review, bool, error)
	// Finish 写入结果或失败原因，并把本次消耗的 token 累加上去。
	Finish(ctx context.Context, reviewID, status, model string, result *Result, failure string,
		inputTokens, outputTokens int64, now time.Time) error
	// RetryLater 把进行中的一条放回队列，next 之前不再领取，重试次数加一，本次
	// 消耗的 token 照记。requested_at 不变：自动重试不是新的请求。
	RetryLater(ctx context.Context, reviewID string, next time.Time, inputTokens, outputTokens int64) error
	// RequeueRunning 把进行中的全部放回队列：服务重启时它们的处理已经中断。
	RequeueRunning(ctx context.Context) error
	// FailIfRunning 把仍是「进行中」的那一条记成失败；已经不是进行中时返回
	// ErrNotFound（后台协程可能刚好把它做完了，不能覆盖）。
	FailIfRunning(ctx context.Context, reviewID, failure string, now time.Time) error
	// FailQueued 把排队中的全部记成失败，返回条数：大模型账户余额不足或密钥
	// 失效时，排着的再调用也只会失败。
	FailQueued(ctx context.Context, failure string, now time.Time) (int, error)
	// CountRequests 统计某人 since 之后发起、没有失败的复盘数（排队、进行中、已完成）。
	// 失败的不占次数：否则上限设成 1 时，唯一那次赶上模型出错，玩家什么都没拿到
	// 却再也不能重试。失败消耗的 token 仍计入全服预算。
	CountRequests(ctx context.Context, userID string, since time.Time) (int, error)
	// CountInFlight 统计某人排队中与分析中的条数。
	CountInFlight(ctx context.Context, userID string) (int, error)
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

func (store *MemoryStore) AccessFor(_ context.Context, userID string) (Access, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, granted := store.access[userID]
	if !granted {
		return Access{}, ErrNotFound
	}
	return cloneAccess(value), nil
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

func (store *MemoryStore) SetLimits(_ context.Context, userID string, limits UserLimits) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, granted := store.access[userID]
	if !granted {
		return ErrNotFound
	}
	value.UserLimits = cloneLimits(limits)
	store.access[userID] = value
	return nil
}

func (store *MemoryStore) AccessList(context.Context) ([]Access, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make([]Access, 0, len(store.access))
	for _, value := range store.access {
		result = append(result, cloneAccess(value))
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

func (store *MemoryStore) FindLatestDone(_ context.Context, userID, handID string) (Review, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var latest *Review
	for _, value := range store.reviews {
		if value.UserID != userID || value.HandID != handID || value.Status != StatusDone || value.FinishedAt == nil {
			continue
		}
		if latest == nil || value.FinishedAt.After(*latest.FinishedAt) {
			copied := value
			latest = &copied
		}
	}
	if latest == nil {
		return Review{}, ErrNotFound
	}
	return cloneReview(*latest), nil
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
	value.Attempts, value.NextAttemptAt = 0, nil
	store.reviews[reviewID] = value
	return nil
}

func (store *MemoryStore) ClaimNext(_ context.Context, now time.Time) (Review, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var next *Review
	for id := range store.reviews {
		value := store.reviews[id]
		if value.Status != StatusQueued || (value.NextAttemptAt != nil && value.NextAttemptAt.After(now)) {
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
	value.NextAttemptAt = nil
	store.reviews[reviewID] = value
	return nil
}

func (store *MemoryStore) RetryLater(_ context.Context, reviewID string, next time.Time, inputTokens, outputTokens int64) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, exists := store.reviews[reviewID]
	if !exists || value.Status != StatusRunning {
		return ErrNotFound
	}
	at := next
	value.Status, value.Attempts, value.NextAttemptAt = StatusQueued, value.Attempts+1, &at
	value.InputTokens += inputTokens
	value.OutputTokens += outputTokens
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
	value.NextAttemptAt = nil
	store.reviews[reviewID] = value
	return nil
}

func (store *MemoryStore) FailQueued(_ context.Context, failure string, now time.Time) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	count := 0
	for id, value := range store.reviews {
		if value.Status != StatusQueued {
			continue
		}
		finished := now
		value.Status, value.Error, value.FinishedAt, value.NextAttemptAt = StatusFailed, failure, &finished, nil
		store.reviews[id] = value
		count++
	}
	return count, nil
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

func (store *MemoryStore) CountInFlight(_ context.Context, userID string) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	count := 0
	for _, value := range store.reviews {
		if value.UserID == userID && (value.Status == StatusQueued || value.Status == StatusRunning) {
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

func cloneAccess(value Access) Access {
	value.UserLimits = cloneLimits(value.UserLimits)
	return value
}

func cloneLimits(limits UserLimits) UserLimits {
	copyInt := func(value *int) *int {
		if value == nil {
			return nil
		}
		copied := *value
		return &copied
	}
	return UserLimits{DailyLimit: copyInt(limits.DailyLimit), MaxInFlight: copyInt(limits.MaxInFlight)}
}

func cloneReview(value Review) Review {
	value.Result = cloneResult(value.Result)
	if value.FinishedAt != nil {
		finished := *value.FinishedAt
		value.FinishedAt = &finished
	}
	if value.NextAttemptAt != nil {
		next := *value.NextAttemptAt
		value.NextAttemptAt = &next
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
