package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/replay"
)

// 中转服务常返回很长的中文报错。按字节截断会切出半个汉字，数据库拒收，这一条
// 就永远停在「进行中」。
func TestModelErrorDetailIsCutOnCharacters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = writer.Write([]byte("x" + strings.Repeat("当前分组上游负载已饱和", 40)))
	}))
	defer server.Close()
	client, err := NewOpenAIClient(OpenAIConfig{BaseURL: server.URL, Model: "m", APIKey: "k", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Complete(context.Background(), "system", "user")
	var modelError ModelError
	if !errors.As(err, &modelError) || !utf8.ValidString(modelError.Detail) || !utf8.ValidString(err.Error()) {
		t.Fatalf("err=%v", err)
	}
}

func TestClipRemovesWhatTheDatabaseRejects(t *testing.T) {
	value := clip("好\x00坏"+string([]byte{0xe4, 0xb8}), 100)
	if value != "好坏" {
		t.Fatalf("clip=%q", value)
	}
}

// 结果存不进去时退一步记失败，不能停在「进行中」让玩家永远等、也没法重试。
// rejectResults 模拟数据库拒收结果里的某段文字；down 模拟数据库整个不可用。
type failingFinishStore struct {
	*MemoryStore
	rejectResults bool
	down          bool
	attempts      int
}

func (store *failingFinishStore) Finish(
	ctx context.Context, reviewID, status, model string, result *Result, failure string,
	inputTokens, outputTokens int64, finishedAt time.Time,
) error {
	store.attempts++
	if store.down || (store.rejectResults && result != nil) {
		return errors.New("database rejected the row")
	}
	return store.MemoryStore.Finish(ctx, reviewID, status, model, result, failure, inputTokens, outputTokens, finishedAt)
}

func withoutFinishRetryDelays(t *testing.T) {
	saved := finishRetryDelays
	finishRetryDelays = []time.Duration{0, 0}
	t.Cleanup(func() { finishRetryDelays = saved })
}

func TestUnsavableResultBecomesARetryableFailure(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	hands := history.NewInMemoryStore()
	if err := hands.Append(sampleHand("hand_1", now)); err != nil {
		t.Fatal(err)
	}
	withoutFinishRetryDelays(t)
	store := &failingFinishStore{MemoryStore: NewMemoryStore(), rejectResults: true}
	service, err := NewService(store, hands, &fakeModel{responses: []string{goodOutput, goodOutput}},
		func() time.Time { return now }, nil)
	if err != nil {
		t.Fatal(err)
	}
	service.recovering = false
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", now)
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil {
		t.Fatal(err)
	}
	service.ProcessNext(ctx)
	failed, _ := service.Get(ctx, "usr_zhang", "hand_1")
	if failed.Status != StatusFailed || failed.Error != "internal_error" || failed.InputTokens != 1000 {
		t.Fatalf("review=%+v", failed)
	}
	if retried, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil || retried.Status != StatusQueued {
		t.Fatalf("retry=%+v err=%v", retried, err)
	}
}

// 排队期间关掉总开关、收回权限或额度用完：不再调用模型。
func TestQueuedReviewsRespectLaterAdminChanges(t *testing.T) {
	ctx := context.Background()
	for name, change := range map[string]func(*Service, *MemoryStore){
		"review_unavailable": func(service *Service, _ *MemoryStore) {
			_ = service.UpdateSettings(ctx, Settings{Enabled: false}, "admin")
		},
		"review_not_allowed": func(_ *Service, store *MemoryStore) {
			_ = store.SetAccess(ctx, "usr_zhang", false, "admin", time.Now())
		},
	} {
		t.Run(name, func(t *testing.T) {
			model := &fakeModel{responses: []string{goodOutput}}
			service, store, _, _ := newTestService(t, model)
			_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
			if _, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil {
				t.Fatal(err)
			}
			change(service, store)
			service.ProcessNext(ctx)
			if len(model.calls) != 0 {
				t.Fatal("the model must not be called")
			}
			value, _ := store.Find(ctx, "usr_zhang", "hand_1", PromptVersion)
			if value.Status != StatusFailed || value.Error != name {
				t.Fatalf("review=%+v", value)
			}
		})
	}

	// 预算：第一条用掉 1500 token，排在后面的那条不再调用
	model := &fakeModel{responses: []string{goodOutput, goodOutput}}
	service, store, hands, now := newTestService(t, model)
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	if err := hands.Append(sampleHand("hand_2", now.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	_ = service.UpdateSettings(ctx, Settings{Enabled: true, MonthlyTokenBudget: 1000}, "admin")
	for _, handID := range []string{"hand_1", "hand_2"} {
		if _, err := service.Request(ctx, "usr_zhang", handID); err != nil {
			t.Fatal(err)
		}
	}
	service.ProcessNext(ctx)
	service.ProcessNext(ctx)
	if len(model.calls) != 1 {
		t.Fatalf("calls=%d", len(model.calls))
	}
	// 两条同一时刻发起，谁先被领取不固定：一条完成，另一条因额度用完失败
	first, _ := store.Find(ctx, "usr_zhang", "hand_1", PromptVersion)
	second, _ := store.Find(ctx, "usr_zhang", "hand_2", PromptVersion)
	if first.Status == StatusDone {
		first, second = second, first
	}
	if second.Status != StatusDone || first.Status != StatusFailed || first.Error != "review_budget_exhausted" {
		t.Fatalf("reviews=%+v / %+v", first, second)
	}
}

// 失败的那次不占每人次数：上限 1 时赶上模型出错，还能重试。
func TestFailedAttemptsDoNotUseTheDailyLimit(t *testing.T) {
	ctx := context.Background()
	model := &fakeModel{errors: []error{errors.New("timeout")}, responses: []string{"", goodOutput}}
	service, store, hands, now := newTestService(t, model)
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	if err := hands.Append(sampleHand("hand_2", now.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	_ = service.UpdateSettings(ctx, Settings{Enabled: true, DailyLimitPerUser: 1}, "admin")
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil {
		t.Fatal(err)
	}
	service.ProcessNext(ctx)
	if retried, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil || retried.Status != StatusQueued {
		t.Fatalf("retry=%+v err=%v", retried, err)
	}
	service.ProcessNext(ctx)
	if _, err := service.Request(ctx, "usr_zhang", "hand_2"); codeOf(err) != "review_daily_limit" {
		t.Fatalf("the successful review uses the limit: %v", err)
	}
}

// 重新排队期间，之前失败花掉的 token 仍计入预算。
func TestRequeuedTokensStayInTheBudget(t *testing.T) {
	ctx := context.Background()
	model := &fakeModel{responses: []string{"坏", "坏"}}
	service, store, _, now := newTestService(t, model)
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil {
		t.Fatal(err)
	}
	service.ProcessNext(ctx)
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil {
		t.Fatal(err)
	}
	usage, _ := store.Usage(ctx, *now)
	if usage.Tokens30d != 3000 {
		t.Fatalf("tokens=%d", usage.Tokens30d)
	}
}

// 模型已经答完才停机的，结果照样存下，不再花第二次钱。
type cancellingModel struct {
	fakeModel
	cancel context.CancelFunc
}

func (model *cancellingModel) Complete(ctx context.Context, system, user string) (Completion, error) {
	model.cancel()
	return model.fakeModel.Complete(ctx, system, user)
}

func TestResultArrivingDuringShutdownIsKept(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	model := &cancellingModel{fakeModel: fakeModel{responses: []string{goodOutput}}, cancel: cancel}
	service, store, _, _ := newTestService(t, model)
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil {
		t.Fatal(err)
	}
	service.ProcessNext(ctx)
	value, _ := store.Find(context.Background(), "usr_zhang", "hand_1", PromptVersion)
	if value.Status != StatusDone {
		t.Fatalf("review=%+v", value)
	}
}

// 大盲时所有人弃牌：本人一个决定都没做，不发给模型。
func TestHandsWithoutDecisionsAreRefused(t *testing.T) {
	ctx := context.Background()
	service, store, hands, now := newTestService(t, &fakeModel{})
	walk := history.Hand{
		HandID: "hand_walk", RoomID: "room_secret", RoomCode: "654321", DealerSeat: 3,
		StartedAt: now.Add(time.Minute), EndedAt: now.Add(2 * time.Minute), SmallBlind: 10, BigBlind: 20,
		SmallBlindSeat: 1, BigBlindSeat: 2,
		Players: []history.PlayerResult{
			{UserID: "usr_zhang", DisplayName: "张三", Seat: 1, StartingStack: 1000, EndingStack: 990, Delta: -10},
			{UserID: "usr_li", DisplayName: "李四", Seat: 2, StartingStack: 1000, EndingStack: 1010, Delta: 10},
			{UserID: "usr_wang", DisplayName: "王五", Seat: 3, StartingStack: 1000, EndingStack: 1000},
		},
		Actions: []history.Action{
			{UserID: "usr_wang", Sequence: 1, Street: "preflop", Type: "fold"},
			{UserID: "usr_zhang", Sequence: 2, Street: "preflop", Type: "fold"},
		},
		PotAwards: []holdem.PotAward{{
			Amount: 20, WinnerPlayerIDs: []string{"usr_li"},
			Payouts: []holdem.Payout{{PlayerID: "usr_li", Amount: 20}},
		}},
	}
	if err := hands.Append(walk); err != nil {
		t.Fatal(err)
	}
	_ = store.SetAccess(ctx, "usr_li", true, "admin", time.Now())
	if _, err := service.Request(ctx, "usr_li", "hand_walk"); codeOf(err) != "review_no_decisions" {
		t.Fatalf("walk: %v", err)
	}
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	if _, err := service.Request(ctx, "usr_zhang", "hand_walk"); err != nil {
		t.Fatalf("a fold is a decision: %v", err)
	}
}

// 对手倾向只用这一手之前的牌：本手自己的摊牌与之后才打的牌都是决策当时不知道的。
func TestTendenciesUseOnlyEarlierHands(t *testing.T) {
	service, _, hands, now := newTestService(t, &fakeModel{})
	for _, value := range []struct {
		id  string
		end time.Time
	}{{"hand_0", now.Add(-time.Hour)}, {"hand_2", now.Add(time.Hour)}} {
		if err := hands.Append(sampleHand(value.id, value.end)); err != nil {
			t.Fatal(err)
		}
	}
	recent, err := service.recentHands("usr_zhang", "hand_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].HandID != "hand_0" {
		t.Fatalf("recent=%d first=%v", len(recent), recent)
	}
}

func TestSettingsHaveAnUpperBound(t *testing.T) {
	service, _, _, _ := newTestService(t, &fakeModel{})
	for _, settings := range []Settings{
		{DailyLimitPerUser: maximumDailyLimit + 1},
		{MonthlyTokenBudget: maximumTokenBudget + 1},
	} {
		if err := service.UpdateSettings(context.Background(), settings, "admin"); codeOf(err) != "invalid_review_settings" {
			t.Fatalf("settings=%+v err=%v", settings, err)
		}
	}
}

// 实测 DeepSeek 有时把输入字段抄回来、漏写 verdict：不能默认成「有争议」，要算不合格重问。
// 常见同义说法归一到四档。
func TestVerdictsAreNormalisedAndRequired(t *testing.T) {
	steps := map[int]bool{3: true, 9: true}
	missing := `{"summary":"总评","decisions":[{"step":3,"action":"raise","reasoning":"分析"}]}`
	if _, err := parseResult(missing, steps); err == nil {
		t.Fatal("a decision without a verdict must be rejected")
	}
	if _, err := parseResult(`{"summary":"总评","decisions":[]}`, steps); err == nil {
		t.Fatal("a review without any decision must be rejected when the player made decisions")
	}
	unknown := `{"summary":"总评","decisions":[{"step":3,"verdict":"一般般","reasoning":"a"},{"step":9,"verdict":"好","reasoning":"b"}]}`
	if _, err := parseResult(unknown, steps); err == nil {
		t.Fatal("an unrecognised verdict must be rejected, not shown as 有争议")
	}
	for raw, want := range map[string]string{"【失误】": "失误", "“不合理”": "失误", "标准": "合理", "BAD": "失误"} {
		result, err := parseResult(`{"summary":"总评","decisions":[{"step":3,"verdict":"`+raw+`","reasoning":"a","bestAction":"弃牌"}]}`, steps)
		if err != nil || result.Decisions[0].Verdict != want {
			t.Fatalf("%s: result=%+v err=%v", raw, result, err)
		}
	}
	if result, err := parseResult(`{"summary":"总评"}`, map[int]bool{}); err != nil || len(result.Decisions) != 0 {
		t.Fatalf("no decisions to review: %+v %v", result, err)
	}
	result, err := parseResult(`{"summary":"总评","decisions":[`+
		`{"step":3,"verdict":"「错误」","reasoning":"a","bestAction":"弃牌"},{"step":9,"verdict":"Good","reasoning":"b"}]}`, steps)
	if err != nil || result.Decisions[0].Verdict != "失误" || result.Decisions[1].Verdict != "好" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

// 本人筹码比对手的全下少：赢得到的底池只到本人能跟的那部分，赔率要按它算。
// 按整个底池算会把 45% 算成 15%，模型就会把亏钱的跟注判成「好」。
func TestShortStackCallUsesTheWinnablePot(t *testing.T) {
	before := replay.Step{
		Index: 5, Kind: replay.KindAction, Street: "flop", Pot: 1040,
		Seats: []replay.SeatState{
			{UserID: "usr_zhang", Stack: 180, TotalBet: 20},
			{UserID: "usr_wang", Stack: 0, StreetBet: 1000, TotalBet: 1020, AllIn: true},
		},
	}
	call := replay.Step{
		Index: 6, Kind: replay.KindAction, Street: "flop", ActorID: "usr_zhang", Action: "all_in", Amount: 180, Pot: 1220,
		Seats: []replay.SeatState{
			{UserID: "usr_zhang", Stack: 0, StreetBet: 180, TotalBet: 200, AllIn: true},
			before.Seats[1],
		},
	}
	timeline := replay.Timeline{HandID: "hand_short_call", BigBlind: 20, Steps: []replay.Step{before, call}}
	decision := decisionAt(timeline, 1, "usr_zhang", []string{"As", "Kd"},
		map[string]string{"usr_wang": "BTN"}, func(value int64) float64 { return float64(value) / 20 })
	if decision.ToCall != 180 || decision.WinnablePot != 220 || decision.PotOdds != 45 || decision.PotBefore != 1040 {
		t.Fatalf("decision=%+v", decision)
	}
}

// 数据库短暂断开：先重试存结果；一直存不进去时这一条留在「进行中」，但玩家看到的
// 是失败、可以重新发起，而不是一直转圈到服务重启。
func TestRunningReviewNobodyProcessesIsRetryable(t *testing.T) {
	ctx := context.Background()
	withoutFinishRetryDelays(t)
	now := time.Unix(1_800_000_000, 0).UTC()
	hands := history.NewInMemoryStore()
	if err := hands.Append(sampleHand("hand_1", now)); err != nil {
		t.Fatal(err)
	}
	store := &failingFinishStore{MemoryStore: NewMemoryStore()}
	service, err := NewService(store, hands, &fakeModel{responses: []string{goodOutput, goodOutput}},
		func() time.Time { return now }, nil)
	if err != nil {
		t.Fatal(err)
	}
	service.recovering = false
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", now)
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil {
		t.Fatal(err)
	}
	store.down = true
	service.ProcessNext(ctx)
	if store.attempts != 4 {
		t.Fatalf("save attempts=%d", store.attempts)
	}
	raw, _ := store.Find(ctx, "usr_zhang", "hand_1", PromptVersion)
	shown, _ := service.Get(ctx, "usr_zhang", "hand_1")
	if raw.Status != StatusRunning || shown.Status != StatusFailed || shown.Error != "internal_error" {
		t.Fatalf("raw=%s shown=%+v", raw.Status, shown)
	}
	store.down = false
	_ = service.UpdateSettings(ctx, Settings{Enabled: true, DailyLimitPerUser: 1}, "admin")
	retried, err := service.Request(ctx, "usr_zhang", "hand_1")
	if err != nil || retried.Status != StatusQueued {
		t.Fatalf("retry=%+v err=%v", retried, err)
	}
	service.ProcessNext(ctx)
	if done, _ := service.Get(ctx, "usr_zhang", "hand_1"); done.Status != StatusDone {
		t.Fatalf("done=%+v", done)
	}
}

// 正在分析的那一条不能被当成没人处理。
func TestTheReviewBeingProcessedIsNotOrphaned(t *testing.T) {
	ctx := context.Background()
	var service *Service
	var seen Review
	model := &callbackModel{inner: &fakeModel{responses: []string{goodOutput}}, during: func() {
		seen, _ = service.Get(ctx, "usr_zhang", "hand_1")
	}}
	service, store, _, _ := newTestService(t, model)
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil {
		t.Fatal(err)
	}
	service.ProcessNext(ctx)
	if seen.Status != StatusRunning {
		t.Fatalf("while processing=%+v", seen)
	}
}

type callbackModel struct {
	inner  Model
	during func()
}

func (model *callbackModel) Name() string { return model.inner.Name() }

func (model *callbackModel) Complete(ctx context.Context, system, user string) (Completion, error) {
	model.during()
	return model.inner.Complete(ctx, system, user)
}

// 输出到了长度上限：单独的原因码，不再重问（再问也一样被截断）。
func TestTruncatedOutputHasItsOwnFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"model":"m","choices":[{"message":{"content":""},"finish_reason":"length"}],` +
			`"usage":{"prompt_tokens":10,"completion_tokens":20}}`))
	}))
	defer server.Close()
	client, err := NewOpenAIClient(OpenAIConfig{BaseURL: server.URL, Model: "m", APIKey: "k", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	service, store, _, _ := newTestService(t, client)
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil {
		t.Fatal(err)
	}
	service.ProcessNext(ctx)
	failed, _ := service.Get(ctx, "usr_zhang", "hand_1")
	if failed.Status != StatusFailed || failed.Error != "output_truncated" || failed.OutputTokens != 20 {
		t.Fatalf("review=%+v", failed)
	}
}

// 进程刚启动、RequeueRunning 还没做完：库里的「进行中」是上次留下的，马上会被
// 放回队列，不能当成没人处理显示失败。
func TestRunningReviewsAreNotOrphanedBeforeRecovery(t *testing.T) {
	ctx := context.Background()
	service, store, _, _ := newTestService(t, &fakeModel{responses: []string{goodOutput}})
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := store.ClaimNext(ctx, time.Now()); !found {
		t.Fatal("claim")
	}
	service.recovering = true
	if shown, _ := service.Get(ctx, "usr_zhang", "hand_1"); shown.Status != StatusRunning {
		t.Fatalf("before recovery=%+v", shown)
	}
	if again, _ := service.Request(ctx, "usr_zhang", "hand_1"); again.Status != StatusRunning {
		t.Fatalf("request before recovery=%+v", again)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		service.Run(runCtx)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if value, _ := store.Find(ctx, "usr_zhang", "hand_1", PromptVersion); value.Status == StatusDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the recovered review must be processed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
}

// 走 orphan 分支时，只改仍是「进行中」的那一条：后台协程恰好做完了就不能覆盖。
func TestFailIfRunningDoesNotOverwriteAFinishedReview(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Unix(1_800_000_000, 0).UTC()
	_ = store.Create(ctx, Review{ReviewID: "rev_1", HandID: "h", UserID: "u", PromptVersion: PromptVersion,
		Status: StatusQueued, CreatedAt: now, RequestedAt: now})
	if _, found, _ := store.ClaimNext(ctx, time.Now()); !found {
		t.Fatal("claim")
	}
	_ = store.Finish(ctx, "rev_1", StatusDone, "m", &Result{Summary: "好"}, "", 1, 1, now)
	if err := store.FailIfRunning(ctx, "rev_1", "internal_error", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("finished review: %v", err)
	}
	if value, _ := store.Find(ctx, "u", "h", PromptVersion); value.Status != StatusDone || value.Result == nil {
		t.Fatalf("value=%+v", value)
	}
}

// 最好的五张按牌力的读法排：点数从大到小，A2345 顺子的 A 放最后。
func TestBestFiveIsSortedForReading(t *testing.T) {
	best, ok := bestHand(parseCards([]string{"Ad", "2c"}), parseCards([]string{"5d", "Kc", "3c", "4s", "9h"}))
	if !ok || strings.Join(best.five, " ") != "5d 4s 3c 2c Ad" {
		t.Fatalf("wheel=%+v", best)
	}
	best, ok = bestHand(parseCards([]string{"Ad", "2c"}), parseCards([]string{"5d", "Kc", "Kd", "3c", "5c"}))
	if !ok || strings.Join(best.five, " ") != "Ad Kc Kd 5d 5c" || strings.Join(best.holeUsed, " ") != "Ad" {
		t.Fatalf("two pair=%+v", best)
	}
}

// 每一步都要有最佳行动；估算数字宽松解析，自相矛盾或超出范围的不显示。
func TestBestActionAndEstimates(t *testing.T) {
	steps := map[int]bool{3: true, 9: true}
	missing := `{"summary":"总评","decisions":[{"step":3,"verdict":"失误","reasoning":"a"}]}`
	if _, err := parseResult(missing, steps); err == nil {
		t.Fatal("a mistake without the best action must be rejected")
	}
	result, err := parseResult(`{"summary":"总评","decisions":[`+
		`{"step":3,"verdict":"好","reasoning":"a","equityVsRangePercent":"62%","evTakenBB":"1.5BB","evBestBB":1.5},`+
		`{"step":9,"verdict":"失误","reasoning":"b","bestAction":"弃牌","equityVsRangePercent":180,"evTakenBB":2,"evBestBB":-1}]}`, steps)
	// 「本次已是最佳」却估出负 EV：弃牌是 0，最佳不可能是负的，不显示
	negative, _ := parseResult(`{"summary":"总评","decisions":[`+
		`{"step":3,"verdict":"合理","reasoning":"a","bestAction":"本次行动已是最佳","evTakenBB":-0.3,"evBestBB":-0.3}]}`, steps)
	if negative == nil || negative.Decisions[0].EVTaken != nil || negative.Decisions[0].EVBest != nil {
		t.Fatalf("negative best=%+v", negative)
	}
	if err != nil {
		t.Fatal(err)
	}
	first, second := result.Decisions[0], result.Decisions[1]
	if first.BestAction != "本次行动已是最佳选择" || *first.EquityVsRange != 62 || *first.EVTaken != 1.5 || *first.EVBest != 1.5 {
		t.Fatalf("first=%+v", first)
	}
	if second.EquityVsRange != nil || second.EVTaken != nil || second.EVBest != nil {
		t.Fatalf("impossible estimates must be dropped: %+v", second)
	}
}

// 服务端把每一步的精确数字挂到结果上，不经过模型。
func TestResultCarriesExactNumbers(t *testing.T) {
	ctx := context.Background()
	service, store, _, _ := newTestService(t, &fakeModel{responses: []string{goodOutput}})
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil {
		t.Fatal(err)
	}
	service.ProcessNext(ctx)
	done, _ := service.Get(ctx, "usr_zhang", "hand_1")
	first := done.Result.Decisions[0]
	if first.Facts == nil || first.Facts.PotBefore != 90 || first.Facts.ToCall != 50 || first.BestAction != "3bet 到 180" {
		t.Fatalf("first=%+v", first)
	}
}

// DeepSeek 的错误码归类：限流与服务繁忙自动重试，余额不足与密钥失效是全局故障，
// 请求格式与参数错误直接失败。
func TestModelFailuresAreClassified(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{ModelError{Status: 429}, failureModelBusy},
		{ModelError{Status: 500}, failureModelBusy},
		{ModelError{Status: 503}, failureModelBusy},
		{ModelError{Status: 402}, failureModelBalance},
		{ModelError{Status: 401}, failureModelUnauthorized},
		{ModelError{Status: 400}, failureModelError},
		{ModelError{Status: 422}, failureModelError},
		{transportError{errors.New("connection reset")}, failureModelBusy},
		{fmt.Errorf("wrapped: %w", context.DeadlineExceeded), failureModelTimeout},
		{ModelError{Status: 403}, failureModelError},
		{errors.New("model returned no content"), failureModelError},
	}
	for _, value := range cases {
		if got := classifyModelFailure(value.err); got != value.want {
			t.Fatalf("%v: got %s want %s", value.err, got, value.want)
		}
	}
}

// 限流、服务繁忙：隔一段时间自动重试，玩家那边仍是分析中；三次都不行才失败。
func TestBusyModelIsRetriedLater(t *testing.T) {
	ctx := context.Background()
	busy := ModelError{Status: 429, Detail: "rate limited"}
	model := &fakeModel{errors: []error{busy, busy, busy}}
	service, store, _, now := newTestService(t, model)
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil {
		t.Fatal(err)
	}
	service.ProcessNext(ctx)
	waiting, _ := service.Get(ctx, "usr_zhang", "hand_1")
	if waiting.Status != StatusQueued || waiting.Attempts != 1 || waiting.NextAttemptAt == nil ||
		!waiting.NextAttemptAt.Equal(now.Add(30*time.Second)) {
		t.Fatalf("after the first failure=%+v", waiting)
	}
	if service.ProcessNext(ctx) {
		t.Fatal("the retry must wait until it is due")
	}
	*now = now.Add(30 * time.Second)
	service.ProcessNext(ctx)
	if second, _ := service.Get(ctx, "usr_zhang", "hand_1"); second.Attempts != 2 ||
		second.NextAttemptAt == nil || !second.NextAttemptAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("after the second failure=%+v", second)
	}
	*now = now.Add(2 * time.Minute)
	service.ProcessNext(ctx)
	failed, _ := service.Get(ctx, "usr_zhang", "hand_1")
	if failed.Status != StatusFailed || failed.Error != failureModelBusy || len(model.calls) != 3 {
		t.Fatalf("after three failures=%+v calls=%d", failed, len(model.calls))
	}
}

// 余额不足：这一条与排着的都记失败，冷却期内拒绝新的请求，管理员页显示原因；
// 冷却期过后放行，成功一次就清掉。
func TestInsufficientBalanceStopsTheQueue(t *testing.T) {
	ctx := context.Background()
	model := &fakeModel{errors: []error{ModelError{Status: 402, Detail: "Insufficient Balance"}},
		responses: []string{"", goodOutput}}
	service, store, hands, now := newTestService(t, model)
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	for index, handID := range []string{"hand_2", "hand_3"} {
		if err := hands.Append(sampleHand(handID, now.Add(time.Duration(index+1)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	for _, handID := range []string{"hand_1", "hand_2"} {
		if _, err := service.Request(ctx, "usr_zhang", handID); err != nil {
			t.Fatal(err)
		}
	}
	service.ProcessNext(ctx)
	for _, handID := range []string{"hand_1", "hand_2"} {
		if value, _ := service.Get(ctx, "usr_zhang", handID); value.Status != StatusFailed ||
			value.Error != failureModelBalance {
			t.Fatalf("%s=%+v", handID, value)
		}
	}
	if len(model.calls) != 1 {
		t.Fatalf("the queued review must not call the model: calls=%d", len(model.calls))
	}
	if _, err := service.Request(ctx, "usr_zhang", "hand_3"); codeOf(err) != failureModelBalance {
		t.Fatalf("during the cooldown: %v", err)
	}
	overview, _ := service.Overview(ctx)
	if overview.ModelHealth == nil || overview.ModelHealth.Failure != failureModelBalance {
		t.Fatalf("overview=%+v", overview)
	}
	*now = now.Add(modelOutageCooldown)
	if _, err := service.Request(ctx, "usr_zhang", "hand_3"); err != nil {
		t.Fatalf("after the cooldown: %v", err)
	}
	service.ProcessNext(ctx)
	if overview, _ = service.Overview(ctx); overview.ModelHealth != nil {
		t.Fatalf("a success must clear the outage: %+v", overview.ModelHealth)
	}
}

// 每人同时进行的上限与单独额度：单独设的优先，没设的跟随全局。
func TestInFlightAndPerUserLimits(t *testing.T) {
	ctx := context.Background()
	service, store, hands, now := newTestService(t, &fakeModel{})
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	for index, handID := range []string{"hand_2", "hand_3"} {
		if err := hands.Append(sampleHand(handID, now.Add(time.Duration(index+1)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	_ = service.UpdateSettings(ctx, Settings{Enabled: true, MaxInFlightPerUser: 1}, "admin")
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Request(ctx, "usr_zhang", "hand_2"); codeOf(err) != "review_in_flight_limit" {
		t.Fatalf("global in-flight limit: %v", err)
	}
	two, one := 2, 1
	if err := service.SetLimits(ctx, "usr_zhang", UserLimits{MaxInFlight: &two}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Request(ctx, "usr_zhang", "hand_2"); err != nil {
		t.Fatalf("own limit wins: %v", err)
	}
	// 单独的每日额度：已经发起两条，上限 1
	if err := service.SetLimits(ctx, "usr_zhang", UserLimits{DailyLimit: &one}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Request(ctx, "usr_zhang", "hand_3"); codeOf(err) != "review_in_flight_limit" {
		// 同时进行的上限跟随全局（1），先撞上它
		t.Fatalf("in flight follows the global value again: %v", err)
	}
	zero := 0
	if err := service.SetLimits(ctx, "usr_zhang", UserLimits{DailyLimit: &one, MaxInFlight: &zero}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Request(ctx, "usr_zhang", "hand_3"); codeOf(err) != "review_daily_limit" {
		t.Fatalf("own daily limit: %v", err)
	}
	if err := service.SetLimits(ctx, "usr_li", UserLimits{}); codeOf(err) != "review_not_allowed" {
		t.Fatalf("limits for someone not on the list: %v", err)
	}
	negative := -1
	if err := service.SetLimits(ctx, "usr_zhang", UserLimits{DailyLimit: &negative}); codeOf(err) != "invalid_review_settings" {
		t.Fatalf("negative limit: %v", err)
	}
}

// 新版提示词还没分析过时，先给旧版的结果并标明过时；重新发起按新版分析。
func TestOutdatedResultIsShownUntilReanalysed(t *testing.T) {
	ctx := context.Background()
	service, store, _, now := newTestService(t, &fakeModel{responses: []string{goodOutput}})
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	older := Review{ReviewID: "rev_old", HandID: "hand_1", UserID: "usr_zhang", PromptVersion: "v0",
		Status: StatusQueued, CreatedAt: *now, RequestedAt: *now}
	_ = store.Create(ctx, older)
	_, _, _ = store.ClaimNext(ctx, *now)
	_ = store.Finish(ctx, "rev_old", StatusDone, "m", &Result{Summary: "旧版"}, "", 1, 1, *now)
	shown, err := service.Get(ctx, "usr_zhang", "hand_1")
	if err != nil || !shown.Outdated || shown.Result.Summary != "旧版" {
		t.Fatalf("shown=%+v err=%v", shown, err)
	}
	if queued, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil || queued.Status != StatusQueued ||
		queued.PromptVersion != PromptVersion {
		t.Fatalf("reanalyse=%+v err=%v", queued, err)
	}
	service.ProcessNext(ctx)
	if fresh, _ := service.Get(ctx, "usr_zhang", "hand_1"); fresh.Outdated || fresh.Status != StatusDone {
		t.Fatalf("fresh=%+v", fresh)
	}
}

// 几个工作协程同时分析：正在处理的几条都不会被当成没人处理；停机时做完再退出。
func TestSeveralWorkersKeepTrackOfWhatTheyProcess(t *testing.T) {
	ctx := context.Background()
	release := make(chan struct{})
	started := make(chan string, 2)
	model := &blockingModel{release: release, started: started}
	service, store, hands, now := newTestService(t, model)
	service.SetWorkers(2)
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	if err := hands.Append(sampleHand("hand_2", now.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	for _, handID := range []string{"hand_1", "hand_2"} {
		if _, err := service.Request(ctx, "usr_zhang", handID); err != nil {
			t.Fatal(err)
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		service.Run(runCtx)
	}()
	<-started
	<-started
	for _, handID := range []string{"hand_1", "hand_2"} {
		if value, _ := service.Get(ctx, "usr_zhang", handID); value.Status != StatusRunning {
			t.Fatalf("%s=%+v", handID, value)
		}
	}
	cancel()
	close(release)
	<-done
	for _, handID := range []string{"hand_1", "hand_2"} {
		if value, _ := service.Get(ctx, "usr_zhang", handID); value.Status != StatusDone {
			t.Fatalf("%s must finish during shutdown: %+v", handID, value)
		}
	}
}

type blockingModel struct {
	release chan struct{}
	started chan string
}

func (model *blockingModel) Name() string { return "blocking" }

func (model *blockingModel) Complete(context.Context, string, string) (Completion, error) {
	model.started <- "started"
	<-model.release
	return Completion{Content: goodOutput, Model: "blocking"}, nil
}

// 发给 DeepSeek 的请求：打开思考模式、带思考强度与匿名 user_id；匿名编号稳定、
// 反推不出账号。关掉之后三个字段都不发（给不认识它们的兼容服务用）。
func TestRequestBodyCarriesThinkingEffortAndAnonymousUser(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		bodies = append(bodies, body)
		_, _ = writer.Write([]byte("\n\n" + `{"model":"deepseek-flash","choices":[{"message":{"content":"{}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	client, err := NewOpenAIClient(OpenAIConfig{
		BaseURL: server.URL, Model: "deepseek-flash", APIKey: "k", Thinking: "enabled", ReasoningEffort: "xhigh",
		SendUserID: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(WithEndUser(context.Background(), "usr_zhang"), "s", "u"); err != nil {
		t.Fatal(err)
	}
	body := bodies[0]
	thinking, _ := body["thinking"].(map[string]any)
	userID, _ := body["user_id"].(string)
	if body["model"] != "deepseek-flash" || thinking["type"] != "enabled" || body["reasoning_effort"] != "xhigh" ||
		!strings.HasPrefix(userID, "u_") || strings.Contains(userID, "zhang") || userID != anonymousUserID("usr_zhang") {
		t.Fatalf("body=%v", body)
	}
	plain, _ := NewOpenAIClient(OpenAIConfig{BaseURL: server.URL, Model: "m", APIKey: "k"})
	if _, err := plain.Complete(WithEndUser(context.Background(), "usr_zhang"), "s", "u"); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"thinking", "reasoning_effort", "user_id"} {
		if _, sent := bodies[1][key]; sent {
			t.Fatalf("%s must not be sent when disabled: %v", key, bodies[1])
		}
	}
	if anonymousUserID("usr_zhang") == anonymousUserID("usr_li") {
		t.Fatal("different players need different ids")
	}
}

// DeepSeek 以 200 返回、但因资源不足中途停止，或排队太久只回一段空白就断开：
// 都按「繁忙」稍后重试，不当成模型输出不合格。
func TestAbortedResponsesAreTreatedAsBusy(t *testing.T) {
	for name, body := range map[string]string{
		"insufficient_system_resource": `{"choices":[{"message":{"content":"{\"summary\""},"finish_reason":"insufficient_system_resource"}]}`,
		"aborted":                      `{"choices":[{"message":{"content":""},"finish_reason":"aborted"}]}`,
		"keep-alive only":              "\n\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = writer.Write([]byte(body))
			}))
			defer server.Close()
			client, _ := NewOpenAIClient(OpenAIConfig{BaseURL: server.URL, Model: "m", APIKey: "k"})
			_, err := client.Complete(context.Background(), "s", "u")
			if got := classifyModelFailure(err); got != failureModelBusy {
				t.Fatalf("err=%v classified=%s", err, got)
			}
		})
	}
}

// 超时只重试一次：思考模式下一次生成就可能超过时限，多试几次只会让玩家白等。
func TestTimeoutIsRetriedOnlyOnce(t *testing.T) {
	ctx := context.Background()
	model := &fakeModel{errors: []error{
		transportError{fmt.Errorf("call model service: %w", context.DeadlineExceeded)},
		transportError{fmt.Errorf("call model service: %w", context.DeadlineExceeded)},
	}}
	service, store, _, now := newTestService(t, model)
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil {
		t.Fatal(err)
	}
	service.ProcessNext(ctx)
	if waiting, _ := service.Get(ctx, "usr_zhang", "hand_1"); waiting.Status != StatusQueued || waiting.Attempts != 1 {
		t.Fatalf("after the first timeout=%+v", waiting)
	}
	*now = now.Add(30 * time.Second)
	service.ProcessNext(ctx)
	failed, _ := service.Get(ctx, "usr_zhang", "hand_1")
	if failed.Status != StatusFailed || failed.Error != failureModelTimeout || len(model.calls) != 2 {
		t.Fatalf("after the second timeout=%+v calls=%d", failed, len(model.calls))
	}
}

// 用新版重新分析时，排队、分析中、失败的这段时间里旧版结果照样带着。
func TestPreviousResultStaysVisibleWhileReanalysing(t *testing.T) {
	ctx := context.Background()
	model := &fakeModel{errors: []error{ModelError{Status: 422, Detail: "bad"}}}
	service, store, _, now := newTestService(t, model)
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	_ = store.Create(ctx, Review{ReviewID: "rev_old", HandID: "hand_1", UserID: "usr_zhang", PromptVersion: "v0",
		Status: StatusQueued, CreatedAt: *now, RequestedAt: *now})
	_, _, _ = store.ClaimNext(ctx, *now)
	_ = store.Finish(ctx, "rev_old", StatusDone, "m", &Result{Summary: "旧版"}, "", 1, 1, *now)
	queued, err := service.Request(ctx, "usr_zhang", "hand_1")
	if err != nil || queued.Status != StatusQueued || queued.Previous == nil || queued.Previous.Summary != "旧版" {
		t.Fatalf("queued=%+v err=%v", queued, err)
	}
	service.ProcessNext(ctx)
	failed, _ := service.Get(ctx, "usr_zhang", "hand_1")
	if failed.Status != StatusFailed || failed.Previous == nil || failed.Outdated {
		t.Fatalf("failed=%+v", failed)
	}
}
