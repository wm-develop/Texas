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
	maximumTokenBudget = 1_000_000_000_000
)

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

	// processing 是后台协程此刻正在分析的那一条。库里是「进行中」、却不是它的，
	// 说明上次结果没存进去（数据库当时不可用）或是进程重启前留下的：不能让玩家
	// 对着它一直等，按失败处理、允许重新发起。单实例部署，进程内记一下就够。
	mu         sync.Mutex
	processing string
	// recovering 在进程启动、RequeueRunning 做完之前为真：这时库里的「进行中」
	// 都是上次留下的、马上会被放回队列，不能当成没人处理。
	recovering bool
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
		recovering: model != nil,
	}, nil
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
	return service.store.HasAccess(ctx, userID)
}

// Request 为本人某一手发起复盘。已经有结果或正在分析的直接返回那一条，不重复花钱；
// 失败过的重新排队。
func (service *Service) Request(ctx context.Context, userID, handID string) (Review, error) {
	if err := service.checkAccess(ctx, userID); err != nil {
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
			return publicReview(current), nil
		}
	}
	switch {
	case orphaned:
		// 已经记成失败，下面按失败重新排队；这一条本来就占着次数，不再查额度
	case err == nil && existing.Status != StatusFailed:
		return publicReview(existing), nil
	case err != nil && !errors.Is(err, ErrNotFound):
		return Review{}, err
	}
	if !orphaned {
		if err := service.checkQuota(ctx, userID); err != nil {
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
	return publicReview(current), nil
}

// Get 返回本人某一手的复盘；没有发起过时返回 ErrNotFound。
func (service *Service) Get(ctx context.Context, userID, handID string) (Review, error) {
	if err := service.checkAccess(ctx, userID); err != nil {
		return Review{}, err
	}
	value, err := service.store.Find(ctx, userID, handID, PromptVersion)
	if err != nil {
		return Review{}, err
	}
	if service.orphaned(value) {
		// 只改给客户端看的状态，让它停止轮询、显示可以重试；重新发起时才动库
		value.Status, value.Error = StatusFailed, "internal_error"
	}
	return publicReview(value), nil
}

// orphaned 报告一条「进行中」的复盘是不是没有人在处理了。
func (service *Service) orphaned(value Review) bool {
	if value.Status != StatusRunning {
		return false
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	return !service.recovering && service.processing != value.ReviewID
}

// failOrphan 在锁里核对并把没人处理的那一条记成失败：领取也在这把锁里，核对与
// 写库之间不会被领取插进来；只改仍是「进行中」的，刚被做完的不会被覆盖。
func (service *Service) failOrphan(ctx context.Context, value Review) (bool, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.recovering || service.processing == value.ReviewID {
		return false, nil
	}
	err := service.store.FailIfRunning(ctx, value.ReviewID, "internal_error", service.now())
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (service *Service) checkAccess(ctx context.Context, userID string) error {
	if service.model == nil {
		return Error{Code: "review_unavailable"}
	}
	settings, err := service.store.Settings(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return Error{Code: "review_unavailable"}
	}
	granted, err := service.store.HasAccess(ctx, userID)
	if err != nil {
		return err
	}
	if !granted {
		return Error{Code: "review_not_allowed"}
	}
	return nil
}

func (service *Service) checkQuota(ctx context.Context, userID string) error {
	settings, err := service.store.Settings(ctx)
	if err != nil {
		return err
	}
	now := service.now()
	if settings.DailyLimitPerUser > 0 {
		count, err := service.store.CountRequests(ctx, userID, now.Add(-24*time.Hour))
		if err != nil {
			return err
		}
		if count >= settings.DailyLimitPerUser {
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
	return Overview{
		Settings: settings, Access: access, Usage: usage,
		ModelConfigured: service.model != nil, Model: service.ModelName(),
	}, nil
}

// UpdateSettings 保存全局设置；额度不能为负。
func (service *Service) UpdateSettings(ctx context.Context, settings Settings, actorUserID string) error {
	// 上限防的是数据库列溢出，远超任何实际用量
	if settings.DailyLimitPerUser < 0 || settings.DailyLimitPerUser > maximumDailyLimit ||
		settings.MonthlyTokenBudget < 0 || settings.MonthlyTokenBudget > maximumTokenBudget {
		return Error{Code: "invalid_review_settings"}
	}
	return service.store.SaveSettings(ctx, settings, actorUserID, service.now())
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

// Run 是后台工作协程：一次处理一条，直到 ctx 结束。服务重启时把上次没处理完的
// 放回队列。
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
	ticker := time.NewTicker(10 * time.Second)
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
	value, found, err := service.store.ClaimNext(claimCtx)
	cancel()
	if err == nil && found {
		service.processing = value.ReviewID
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
		service.processing = ""
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
	status := StatusDone
	failure = clip(failure, 1000)
	if failure != "" {
		status, result = StatusFailed, nil
		if service.logger != nil {
			service.logger.Error("hand review failed", "reviewId", value.ReviewID, "handId", value.HandID, "failure", failure)
		}
	}
	model := completion.Model
	if model == "" {
		model = service.model.Name()
	}
	completion.Model = model
	service.finish(work, value.ReviewID, status, model, result, failure, completion)
	return true
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
	if err := service.checkAccess(ctx, userID); err != nil {
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
		callCtx, cancel := context.WithTimeout(ctx, modelCallTimeout)
		completion, err := service.model.Complete(callCtx, systemPrompt, prompt)
		cancel()
		total.Model = completion.Model
		total.InputTokens += completion.InputTokens
		total.OutputTokens += completion.OutputTokens
		if err != nil {
			return nil, total, "model_error: " + err.Error()
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
