package review

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/replay"
)

// Error 是给客户端的业务错误码。
type Error struct{ Code string }

func (err Error) Error() string { return err.Code }

// 统计对手倾向最多往回看多少手。
const tendencyHandLimit = 300

// 管理员设置的上限，只为不让数据库列溢出。
const (
	maximumDailyLimit  = 1_000_000
	maximumInFlight    = 1_000
	maximumTokenBudget = 1_000_000_000_000
)

// 模型服务繁忙（429、5xx、网络、资源不足中断）时自动重试：一共最多调用三次，两次
// 重试分别隔 30 秒和 2 分钟。间隔是给对方恢复的时间，太密只会继续撞限流。超时时
// 一共最多调用两次（与繁忙的重试共用次数）：思考模式下一次生成就可能超过时限，
// 多试几次只会让玩家白等。
const (
	maxModelAttempts   = 3
	maxTimeoutAttempts = 2
)

var modelRetryDelays = []time.Duration{30 * time.Second, 2 * time.Minute}

// 余额不足或密钥失效之后这么久之内不再接收新的复盘：调用也只会失败，还会把
// 玩家的等待白白拉长。时间到了再放行，下一次调用会告诉我们问题解决了没有。
const modelOutageCooldown = 5 * time.Minute

// ModelHealth 是最近一次「全局性」的模型故障（余额不足、密钥失效），给管理员看。
// CoolingDown 为真表示还在冷却期、拒绝新请求；过了冷却期但还没有成功调用过时为假，
// 下一次调用会确认问题解决了没有。
type ModelHealth struct {
	Failure     string    `json:"failure"`
	At          time.Time `json:"at"`
	CoolingDown bool      `json:"coolingDown"`
}

// 一次模型调用的兜底上限。真正的超时是 REVIEW_TIMEOUT_SECONDS（最多 15 分钟，
// 由 HTTP 客户端执行）；这里只防某个模型实现自己不设超时把队列卡死，必须比
// 那个上限长，否则管理员调大超时也不起作用。
const modelCallTimeout = 20 * time.Minute

type Service struct {
	store  Store
	hands  history.Store
	model  Model
	now    func() time.Time
	logger *slog.Logger
	wake   chan struct{}

	// workers 是同时分析的条数。每条要调用模型几十秒到一两分钟，一个协程串行
	// 处理时，几个人同时发起就要排很久。
	workers int

	// processing 是后台协程此刻正在分析的那些条。库里是「进行中」、却不在这里的，
	// 说明上次结果没存进去（数据库当时不可用）或是进程重启前留下的：不能让玩家
	// 对着它一直等，按失败处理、允许重新发起。单实例部署，进程内记一下就够。
	mu         sync.Mutex
	processing map[string]bool
	// recovering 在进程启动、RequeueRunning 做完之前为真：这时库里的「进行中」
	// 都是上次留下的、马上会被放回队列，不能当成没人处理。
	recovering bool
	// health 是最近一次余额不足或密钥失效；冷却期内拒绝新的复盘。
	health *ModelHealth
}

// 后台协程每次读写仓储的时限：连接半开时不能让协程永远挂住、整个队列停摆。
const storeCallTimeout = 10 * time.Second

// 结果存库失败时的重试间隔：数据库短暂断开时不丢掉已经付过钱的结果。
var finishRetryDelays = []time.Duration{time.Second, 3 * time.Second, 10 * time.Second}

// NewService 创建复盘服务。model 为空表示没有配置大模型：功能整体不可用，
// 其余部分（管理员开通名单、设置）照常可用。
func NewService(store Store, hands history.Store, model Model, now func() time.Time, logger *slog.Logger) (*Service, error) {
	if store == nil || hands == nil {
		return nil, errors.New("review service requires a store and a hand history")
	}
	if now == nil {
		now = time.Now
	}
	return &Service{
		store: store, hands: hands, model: model, now: now, logger: logger, wake: make(chan struct{}, 1),
		workers: 1, processing: make(map[string]bool), recovering: model != nil,
	}, nil
}

// SetWorkers 设置同时分析的条数，在 Run 之前调用。
func (service *Service) SetWorkers(workers int) {
	if workers < 1 {
		workers = 1
	}
	service.workers = workers
}

// ModelConfigured 报告是否配置了大模型。
func (service *Service) ModelConfigured() bool { return service.model != nil }

// ModelName 是配置的模型名，没配置时为空。
func (service *Service) ModelName() string {
	if service.model == nil {
		return ""
	}
	return service.model.Name()
}

// Available 报告这个人现在能不能用复盘：配置了模型、全局开关打开、他在开通名单里。
func (service *Service) Available(ctx context.Context, userID string) (bool, error) {
	if service.model == nil {
		return false, nil
	}
	settings, err := service.store.Settings(ctx)
	if err != nil || !settings.Enabled {
		return false, err
	}
	if _, err := service.store.AccessFor(ctx, userID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// DoneHands 返回 handIDs 里本人已有复盘结果（任意版本）的手号，给牌局记录加标识。
// 现在用不了复盘（没开通、总开关关了、没配置模型）时返回空：结果打不开，不给标识。
func (service *Service) DoneHands(ctx context.Context, userID string, handIDs []string) (map[string]bool, error) {
	if len(handIDs) == 0 {
		return map[string]bool{}, nil
	}
	available, err := service.Available(ctx, userID)
	if err != nil || !available {
		return map[string]bool{}, err
	}
	return service.store.DoneHands(ctx, userID, handIDs)
}

// Request 为本人某一手发起复盘。已经有结果或正在分析的直接返回那一条，不重复花钱；
// 失败过的重新排队。
func (service *Service) Request(ctx context.Context, userID, handID string) (Review, error) {
	access, err := service.checkAccess(ctx, userID)
	if err != nil {
		return Review{}, err
	}
	hand, err := service.hands.HandForPlayer(userID, handID)
	if err != nil {
		if errors.Is(err, history.ErrHandNotFound) {
			return Review{}, Error{Code: "hand_not_found"}
		}
		return Review{}, err
	}
	// 本人这一手一个决定都没做（例如大盲时所有人弃牌）就没有可点评的，不花这个钱
	timeline, err := replay.Build(hand, userID)
	if err != nil {
		return Review{}, Error{Code: "replay_unavailable"}
	}
	if !hasDecision(timeline, userID) {
		return Review{}, Error{Code: "review_no_decisions"}
	}
	existing, err := service.store.Find(ctx, userID, handID, PromptVersion)
	orphaned := false
	if err == nil && existing.Status == StatusRunning {
		if orphaned, err = service.failOrphan(ctx, existing); err != nil {
			return Review{}, err
		}
		if !orphaned {
			// 还有人在处理，或者刚好被处理完了：按库里现在的样子返回
			current, err := service.store.Find(ctx, userID, handID, PromptVersion)
			if err != nil {
				return Review{}, err
			}
			return service.withPrevious(ctx, publicReview(current)), nil
		}
	}
	switch {
	case orphaned:
		// 已经记成失败，下面按失败重新排队；这一条本来就占着次数，不再查额度
	case err == nil && existing.Status != StatusFailed:
		return service.withPrevious(ctx, publicReview(existing)), nil
	case err != nil && !errors.Is(err, ErrNotFound):
		return Review{}, err
	}
	// 冷却期内连没人处理的那条也不重新排队：调用也只会失败
	if outage := service.modelOutage(); outage != "" {
		return Review{}, Error{Code: outage}
	}
	if !orphaned {
		if err := service.checkQuota(ctx, access); err != nil {
			return Review{}, err
		}
	}
	now := service.now()
	if err == nil {
		if err := service.store.Requeue(ctx, existing.ReviewID, now); err != nil && !errors.Is(err, ErrNotFound) {
			return Review{}, err
		}
	} else {
		created := Review{
			ReviewID: newReviewID(), HandID: handID, UserID: userID, PromptVersion: PromptVersion,
			Status: StatusQueued, CreatedAt: now, RequestedAt: now,
		}
		if err := service.store.Create(ctx, created); err != nil && !errors.Is(err, ErrExists) {
			return Review{}, err
		}
	}
	service.signal()
	current, err := service.store.Find(ctx, userID, handID, PromptVersion)
	if err != nil {
		return Review{}, err
	}
	return service.withPrevious(ctx, publicReview(current)), nil
}

// withPrevious 在当前版本还没有结果（排队、分析中、失败）时附上旧版提示词最近
// 一次完成的结果：玩家点了「用新版重新分析」之后，原来的复盘不能看不到。
func (service *Service) withPrevious(ctx context.Context, value Review) Review {
	if value.Status == StatusDone {
		return value
	}
	older, err := service.store.FindLatestDone(ctx, value.UserID, value.HandID)
	if err != nil || older.PromptVersion == value.PromptVersion || older.Result == nil {
		return value
	}
	value.Previous = older.Result
	return value
}

// Get 返回本人某一手的复盘。当前版本的提示词还没分析过、但旧版分析过时，
// 先给旧版的结果（Outdated 为真），玩家可以再用新版重新分析；都没有时返回
// ErrNotFound。
func (service *Service) Get(ctx context.Context, userID, handID string) (Review, error) {
	if _, err := service.checkAccess(ctx, userID); err != nil {
		return Review{}, err
	}
	value, err := service.store.Find(ctx, userID, handID, PromptVersion)
	if errors.Is(err, ErrNotFound) {
		older, olderErr := service.store.FindLatestDone(ctx, userID, handID)
		if olderErr != nil {
			return Review{}, olderErr
		}
		older.Outdated = true
		return publicReview(older), nil
	}
	if err != nil {
		return Review{}, err
	}
	if service.orphaned(value) {
		// 只改给客户端看的状态，让它停止轮询、显示可以重试；重新发起时才动库
		value.Status, value.Error = StatusFailed, "internal_error"
	}
	return service.withPrevious(ctx, publicReview(value)), nil
}

// orphaned 报告一条「进行中」的复盘是不是没有人在处理了。
func (service *Service) orphaned(value Review) bool {
	if value.Status != StatusRunning {
		return false
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	return !service.recovering && !service.processing[value.ReviewID]
}

// failOrphan 在锁里核对并把没人处理的那一条记成失败：领取也在这把锁里，核对与
// 写库之间不会被领取插进来；只改仍是「进行中」的，刚被做完的不会被覆盖。
func (service *Service) failOrphan(ctx context.Context, value Review) (bool, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.recovering || service.processing[value.ReviewID] {
		return false, nil
	}
	err := service.store.FailIfRunning(ctx, value.ReviewID, "internal_error", service.now())
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (service *Service) checkAccess(ctx context.Context, userID string) (Access, error) {
	if service.model == nil {
		return Access{}, Error{Code: "review_unavailable"}
	}
	settings, err := service.store.Settings(ctx)
	if err != nil {
		return Access{}, err
	}
	if !settings.Enabled {
		return Access{}, Error{Code: "review_unavailable"}
	}
	access, err := service.store.AccessFor(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return Access{}, Error{Code: "review_not_allowed"}
	}
	if err != nil {
		return Access{}, err
	}
	return access, nil
}

// effectiveLimit 取某人的单独额度，没单独设时用全局的。
func effectiveLimit(own *int, global int) int {
	if own != nil {
		return *own
	}
	return global
}

func (service *Service) checkQuota(ctx context.Context, access Access) error {
	settings, err := service.store.Settings(ctx)
	if err != nil {
		return err
	}
	now := service.now()
	if limit := effectiveLimit(access.MaxInFlight, settings.MaxInFlightPerUser); limit > 0 {
		count, err := service.store.CountInFlight(ctx, access.UserID)
		if err != nil {
			return err
		}
		if count >= limit {
			return Error{Code: "review_in_flight_limit"}
		}
	}
	if limit := effectiveLimit(access.DailyLimit, settings.DailyLimitPerUser); limit > 0 {
		count, err := service.store.CountRequests(ctx, access.UserID, now.Add(-24*time.Hour))
		if err != nil {
			return err
		}
		if count >= limit {
			return Error{Code: "review_daily_limit"}
		}
	}
	if settings.MonthlyTokenBudget > 0 {
		usage, err := service.store.Usage(ctx, now)
		if err != nil {
			return err
		}
		if usage.Tokens30d >= settings.MonthlyTokenBudget {
			return Error{Code: "review_budget_exhausted"}
		}
	}
	return nil
}

// Overview 是管理员看到的全貌。
type Overview struct {
	Settings        Settings `json:"settings"`
	Access          []Access `json:"access"`
	Usage           Usage    `json:"usage"`
	ModelConfigured bool     `json:"modelConfigured"`
	Model           string   `json:"model"`
	Workers         int      `json:"workers"`
	// ModelHealth 是最近一次余额不足或密钥失效，恢复正常后清空。
	ModelHealth *ModelHealth `json:"modelHealth,omitempty"`
}

func (service *Service) Overview(ctx context.Context) (Overview, error) {
	settings, err := service.store.Settings(ctx)
	if err != nil {
		return Overview{}, err
	}
	access, err := service.store.AccessList(ctx)
	if err != nil {
		return Overview{}, err
	}
	usage, err := service.store.Usage(ctx, service.now())
	if err != nil {
		return Overview{}, err
	}
	service.mu.Lock()
	var health *ModelHealth
	if service.health != nil {
		copied := *service.health
		copied.CoolingDown = service.now().Sub(copied.At) < modelOutageCooldown
		health = &copied
	}
	service.mu.Unlock()
	return Overview{
		Settings: settings, Access: access, Usage: usage,
		ModelConfigured: service.model != nil, Model: service.ModelName(), Workers: service.workers,
		ModelHealth: health,
	}, nil
}

// modelOutage 在余额不足或密钥失效的冷却期内返回对应的原因码。
func (service *Service) modelOutage() string {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.health == nil || service.now().Sub(service.health.At) >= modelOutageCooldown {
		return ""
	}
	return service.health.Failure
}

// UpdateSettings 保存全局设置；额度不能为负。
func (service *Service) UpdateSettings(ctx context.Context, settings Settings, actorUserID string) error {
	// 上限防的是数据库列溢出，远超任何实际用量
	if settings.DailyLimitPerUser < 0 || settings.DailyLimitPerUser > maximumDailyLimit ||
		settings.MaxInFlightPerUser < 0 || settings.MaxInFlightPerUser > maximumInFlight ||
		settings.MonthlyTokenBudget < 0 || settings.MonthlyTokenBudget > maximumTokenBudget {
		return Error{Code: "invalid_review_settings"}
	}
	return service.store.SaveSettings(ctx, settings, actorUserID, service.now())
}

// SetLimits 给已开通的人单独设额度；为空的一项跟随全局设置。
func (service *Service) SetLimits(ctx context.Context, userID string, limits UserLimits) error {
	if value := limits.DailyLimit; value != nil && (*value < 0 || *value > maximumDailyLimit) {
		return Error{Code: "invalid_review_settings"}
	}
	if value := limits.MaxInFlight; value != nil && (*value < 0 || *value > maximumInFlight) {
		return Error{Code: "invalid_review_settings"}
	}
	err := service.store.SetLimits(ctx, userID, limits)
	if errors.Is(err, ErrNotFound) {
		return Error{Code: "review_not_allowed"}
	}
	return err
}

// SetAccess 开通或收回某人的复盘权限。调用方负责确认该用户存在。
func (service *Service) SetAccess(ctx context.Context, userID string, granted bool, actorUserID string) error {
	return service.store.SetAccess(ctx, userID, granted, actorUserID, service.now())
}

func (service *Service) signal() {
	select {
	case service.wake <- struct{}{}:
	default:
	}
}

// Run 启动后台工作协程，直到 ctx 结束且手上的都做完才返回。服务重启时先把上次
// 没处理完的放回队列。
func (service *Service) Run(ctx context.Context) {
	if service.model == nil {
		return
	}
	recoverCtx, cancel := context.WithTimeout(ctx, storeCallTimeout)
	if err := service.store.RequeueRunning(recoverCtx); err != nil {
		// 放不回去的留在「进行中」，由 orphaned 让玩家看到失败、可以重新发起
		service.logError("review queue could not be recovered", err)
	}
	cancel()
	service.mu.Lock()
	service.recovering = false
	service.mu.Unlock()
	var group sync.WaitGroup
	for range service.workers {
		group.Add(1)
		go func() {
			defer group.Done()
			service.work(ctx)
		}()
	}
	group.Wait()
}

func (service *Service) work(ctx context.Context) {
	// 自动重试的那条到点时没有人发信号，靠定时器去看
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		for service.ProcessNext(ctx) {
			if ctx.Err() != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-service.wake:
		case <-ticker.C:
		}
	}
}

// ProcessNext 处理队列里最早的一条，队列空时返回 false。测试直接调它。
func (service *Service) ProcessNext(ctx context.Context) bool {
	if service.model == nil {
		return false
	}
	// 领取与登记「正在处理」在同一把锁里：否则玩家恰好在两者之间查询，会把刚领取
	// 的这条误判成没人处理
	service.mu.Lock()
	claimCtx, cancel := context.WithTimeout(ctx, storeCallTimeout)
	value, found, err := service.store.ClaimNext(claimCtx, service.now())
	cancel()
	if err == nil && found {
		service.processing[value.ReviewID] = true
	}
	service.mu.Unlock()
	if err != nil {
		service.logError("review could not be claimed", err)
		return false
	}
	if !found {
		return false
	}
	defer func() {
		service.mu.Lock()
		delete(service.processing, value.ReviewID)
		service.mu.Unlock()
	}()
	// 已经领取的这一条做完再停：模型调用与存库都不随停机取消，否则已经在生成的
	// 内容白付钱、重启后还要再调一次。停机时 main 在排空牌桌的时限内等它
	work := context.WithoutCancel(ctx)
	// 排队期间管理员可能关了总开关、收回了权限或额度已经用完：这时不再调用
	// 模型，直接记失败，玩家之后可以重新发起
	allowedCtx, cancel := context.WithTimeout(work, storeCallTimeout)
	code := service.stillAllowed(allowedCtx, value.UserID)
	cancel()
	if code != "" {
		service.finish(work, value.ReviewID, StatusFailed, service.model.Name(), nil, code, Completion{})
		return true
	}
	result, completion, failure := service.analyse(work, value)
	model := completion.Model
	if model == "" {
		model = service.model.Name()
	}
	completion.Model = model
	status := StatusDone
	failure = clip(failure, 1000)
	if failure != "" {
		status, result = StatusFailed, nil
		code := FailureCode(failure)
		switch {
		case (code == failureModelBusy && value.Attempts+1 < maxModelAttempts) ||
			(code == failureModelTimeout && value.Attempts+1 < maxTimeoutAttempts):
			// 限流、服务繁忙、超时：稍后自动重试，玩家那边仍显示分析中
			next := service.now().Add(modelRetryDelays[min(value.Attempts, len(modelRetryDelays)-1)])
			if service.logger != nil {
				service.logger.Warn("hand review will be retried", "reviewId", value.ReviewID,
					"attempt", value.Attempts+1, "next", next, "failure", failure)
			}
			retryCtx, cancel := context.WithTimeout(work, storeCallTimeout)
			err := service.store.RetryLater(retryCtx, value.ReviewID, next, completion.InputTokens, completion.OutputTokens)
			cancel()
			if err == nil {
				return true
			}
			service.logError("hand review could not be scheduled for retry", err)
		case code == failureModelBalance || code == failureModelUnauthorized:
			service.modelOutageDetected(work, code)
		}
		if service.logger != nil {
			service.logger.Error("hand review failed", "reviewId", value.ReviewID, "handId", value.HandID, "failure", failure)
		}
	} else {
		service.clearModelOutage()
	}
	service.finish(work, value.ReviewID, status, model, result, failure, completion)
	return true
}

// modelOutageDetected 处理余额不足、密钥失效：排着的全部记失败（再调用也只会
// 失败），冷却期内拒绝新的请求，管理员页显示原因。
func (service *Service) modelOutageDetected(ctx context.Context, code string) {
	service.mu.Lock()
	service.health = &ModelHealth{Failure: code, At: service.now()}
	service.mu.Unlock()
	failCtx, cancel := context.WithTimeout(ctx, storeCallTimeout)
	count, err := service.store.FailQueued(failCtx, code, service.now())
	cancel()
	if err != nil {
		service.logError("queued reviews could not be failed after a model outage", err)
	}
	if service.logger != nil {
		service.logger.Error("model service refused all reviews", "failure", code, "queuedFailed", count)
	}
}

func (service *Service) clearModelOutage() {
	service.mu.Lock()
	service.health = nil
	service.mu.Unlock()
}

// finish 存下结果。数据库短暂不可用时隔几秒重试；一直存不进去（例如数据库拒收
// 某段文字）就退一步只记原因码，token 照记。再不行这一条会留在「进行中」，
// orphaned 会让玩家看到失败、可以重新发起，不会一直转圈。
func (service *Service) finish(
	ctx context.Context, reviewID, status, model string, result *Result, failure string, completion Completion,
) {
	save := func(status string, result *Result, failure string) error {
		saveCtx, cancel := context.WithTimeout(ctx, storeCallTimeout)
		defer cancel()
		return service.store.Finish(saveCtx, reviewID, status, model, result, failure,
			completion.InputTokens, completion.OutputTokens, service.now())
	}
	err := save(status, result, failure)
	for _, delay := range finishRetryDelays {
		if err == nil {
			return
		}
		service.logError("review result could not be saved, retrying", err)
		time.Sleep(delay)
		err = save(status, result, failure)
	}
	if err == nil {
		return
	}
	service.logError("review result could not be saved", err)
	if status == StatusDone || failure != "internal_error" {
		if err := save(StatusFailed, nil, "internal_error"); err != nil {
			// 这次调用花掉的 token 没能记进用量，至少留在日志里对账
			if service.logger != nil {
				service.logger.Error("review failure could not be saved either", "error", err, "reviewId", reviewID,
					"inputTokens", completion.InputTokens, "outputTokens", completion.OutputTokens)
			}
		}
	}
}

// stillAllowed 在真正调用模型前再核对一次开关、名单与额度，返回失败原因码；
// 可以继续时返回空串。读不到设置时放行，由发起时的检查兜底。
func (service *Service) stillAllowed(ctx context.Context, userID string) string {
	if _, err := service.checkAccess(ctx, userID); err != nil {
		var reviewError Error
		if errors.As(err, &reviewError) {
			return reviewError.Code
		}
		return ""
	}
	settings, err := service.store.Settings(ctx)
	if err != nil || settings.MonthlyTokenBudget <= 0 {
		return ""
	}
	usage, err := service.store.Usage(ctx, service.now())
	if err == nil && usage.Tokens30d >= settings.MonthlyTokenBudget {
		return "review_budget_exhausted"
	}
	return ""
}

// hasDecision 报告本人在这一手里有没有行动过（超时代为行动的也算）。
func hasDecision(timeline replay.Timeline, userID string) bool {
	for index, step := range timeline.Steps {
		if index > 0 && step.Kind == replay.KindAction && step.ActorID == userID {
			return true
		}
	}
	return false
}

// analyse 整理数据、调用模型、解析结果。第三个返回值非空表示失败，前缀是给
// 客户端看的原因码。
func (service *Service) analyse(ctx context.Context, value Review) (*Result, Completion, string) {
	hand, err := service.hands.HandForPlayer(value.UserID, value.HandID)
	if err != nil {
		return nil, Completion{}, "hand_not_found: " + err.Error()
	}
	timeline, err := replay.Build(hand, value.UserID)
	if err != nil {
		return nil, Completion{}, "replay_unavailable: " + err.Error()
	}
	recent, err := service.recentHands(value.UserID, value.HandID)
	if err != nil {
		return nil, Completion{}, "history_unavailable: " + err.Error()
	}
	facts := BuildFacts(hand, timeline, value.UserID, recent)
	prompt, err := userPrompt(facts)
	if err != nil {
		return nil, Completion{}, "invalid_input: " + err.Error()
	}
	steps := make(map[int]bool, len(facts.Decisions))
	for _, decision := range facts.Decisions {
		steps[decision.Step] = true
	}
	var total Completion
	// 模型偶尔输出不合格式的内容，再问一次；两次都不行就算失败
	for attempt := 0; attempt < 2; attempt++ {
		callCtx, cancel := context.WithTimeout(WithEndUser(ctx, value.UserID), modelCallTimeout)
		completion, err := service.model.Complete(callCtx, systemPrompt, prompt)
		cancel()
		total.Model = completion.Model
		total.InputTokens += completion.InputTokens
		total.OutputTokens += completion.OutputTokens
		if err != nil {
			return nil, total, classifyModelFailure(err) + ": " + err.Error()
		}
		// 截断了再问一次也一样会截断，直接记失败，让管理员去调输出上限
		if completion.Truncated {
			return nil, total, "output_truncated: the model hit its output limit"
		}
		result, err := parseResult(completion.Content, steps)
		if err == nil {
			attachFacts(result, facts)
			return result, total, ""
		}
	}
	return nil, total, "invalid_output: the model did not return a valid review"
}

// recentHands 取这一手之前的牌局：本手自己的动作和摊牌、以及之后才打的牌，都是
// 决策当时不知道的，不能混进对手倾向。
func (service *Service) recentHands(userID, handID string) ([]history.Hand, error) {
	var result []history.Hand
	cursor := handID
	for len(result) < tendencyHandLimit {
		page, err := service.hands.PageForPlayer(userID, cursor, 100)
		if err != nil {
			return nil, err
		}
		result = append(result, page...)
		if len(page) < 100 {
			break
		}
		cursor = page[len(page)-1].HandID
	}
	return result, nil
}

func (service *Service) logError(message string, err error) {
	if service.logger != nil {
		service.logger.Error(message, "error", err)
	}
}

// publicReview 去掉失败详情，只留原因码：详情可能含模型服务的报错原文。
func publicReview(value Review) Review {
	value.Error = FailureCode(value.Error)
	return value
}

// FailureCode 取失败原因的前缀码，例如 model_error。
func FailureCode(failure string) string {
	if index := strings.Index(failure, ":"); index > 0 {
		return failure[:index]
	}
	return failure
}

func newReviewID() string {
	var random [12]byte
	_, _ = cryptorand.Read(random[:])
	return "rev_" + hex.EncodeToString(random[:])
}
