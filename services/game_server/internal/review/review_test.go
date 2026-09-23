package review

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/replay"
)

// 三人桌：庄位 3（王五）、小盲 1（本人张三）、大盲 2（李四）。
// 翻前王五加注到 60，本人跟注，李四弃牌；翻牌本人过牌、王五下注 60、本人跟注；
// 转牌两人过牌；河牌本人下注 100，王五弃牌。没摊牌，王五和李四的牌谁都没看到。
func sampleHand(handID string, endedAt time.Time) history.Hand {
	return history.Hand{
		HandID: handID, RoomID: "room_secret", RoomCode: "654321", DealerSeat: 3,
		StartedAt: endedAt.Add(-time.Minute), EndedAt: endedAt, SmallBlind: 10, BigBlind: 20,
		SmallBlindSeat: 1, BigBlindSeat: 2,
		Board: []string{"Ah", "7c", "2d", "9s", "3h"},
		Players: []history.PlayerResult{
			{UserID: "usr_zhang", DisplayName: "张三", Seat: 1, StartingStack: 1000, EndingStack: 1140, Delta: 140, HoleCards: []string{"As", "Kd"}},
			{UserID: "usr_li", DisplayName: "李四", Seat: 2, StartingStack: 1000, EndingStack: 980, Delta: -20, HoleCards: []string{"Qc", "Qd"}},
			{UserID: "usr_wang", DisplayName: "王五", Seat: 3, StartingStack: 1000, EndingStack: 880, Delta: -120, HoleCards: []string{"8h", "8s"}},
		},
		Actions: []history.Action{
			{UserID: "usr_wang", Sequence: 1, Street: "preflop", Type: "raise", Committed: 60, RaiseTo: 60},
			{UserID: "usr_zhang", Sequence: 2, Street: "preflop", Type: "call", Committed: 50},
			{UserID: "usr_li", Sequence: 3, Street: "preflop", Type: "fold"},
			{UserID: "usr_zhang", Sequence: 4, Street: "flop", Type: "check"},
			{UserID: "usr_wang", Sequence: 5, Street: "flop", Type: "bet", Committed: 60, RaiseTo: 60},
			{UserID: "usr_zhang", Sequence: 6, Street: "flop", Type: "call", Committed: 60},
			{UserID: "usr_zhang", Sequence: 7, Street: "turn", Type: "check"},
			{UserID: "usr_wang", Sequence: 8, Street: "turn", Type: "check"},
			{UserID: "usr_zhang", Sequence: 9, Street: "river", Type: "bet", Committed: 100, RaiseTo: 100},
			{UserID: "usr_wang", Sequence: 10, Street: "river", Type: "fold"},
		},
		PotAwards: []holdem.PotAward{{
			Amount: 260, WinnerPlayerIDs: []string{"usr_zhang"},
			Payouts: []holdem.Payout{{PlayerID: "usr_zhang", Amount: 260}},
		}},
	}
}

type fakeModel struct {
	mu        sync.Mutex
	responses []string
	errors    []error
	calls     []string
}

func (model *fakeModel) Name() string { return "fake-model" }

func (model *fakeModel) Complete(_ context.Context, system, user string) (Completion, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	model.calls = append(model.calls, user)
	index := len(model.calls) - 1
	if index < len(model.errors) && model.errors[index] != nil {
		return Completion{Model: "fake-model"}, model.errors[index]
	}
	content := `{"summary":"ok"}`
	if index < len(model.responses) {
		content = model.responses[index]
	}
	return Completion{Content: content, Model: "fake-model", InputTokens: 1000, OutputTokens: 500}, nil
}

const goodOutput = "```json\n" + `{"summary":"翻前在 SB 平跟 BTN 的开局偏被动。","decisions":[` +
	`{"step":2,"verdict":"合理","reasoning":"AKo 面对 BTN 开局，3bet 更好。","betterOption":"3bet 到 180"},` +
	`{"step":99,"verdict":"好","reasoning":"不存在的步"},` +
	`{"step":12,"verdict":"「错误」","reasoning":"河牌价值下注。","bestAction":"下注到 150"}],` +
	`"keyLessons":["位置不利时用 3bet 夺回主动权"],"opponentNotes":[],"hindsight":"对手弃牌，没有摊牌。"}` + "\n```"

func newTestService(t *testing.T, model Model) (*Service, *MemoryStore, *history.InMemoryStore, *time.Time) {
	t.Helper()
	now := time.Unix(1_800_000_000, 0).UTC()
	hands := history.NewInMemoryStore()
	if err := hands.Append(sampleHand("hand_1", now)); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	service, err := NewService(store, hands, model, func() time.Time { return now }, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 测试直接调 ProcessNext、不跑 Run：当作启动时的回收已经做完
	service.recovering = false
	return service, store, hands, &now
}

func codeOf(err error) string {
	var reviewError Error
	if errors.As(err, &reviewError) {
		return reviewError.Code
	}
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

func TestAccessRules(t *testing.T) {
	ctx := context.Background()
	unconfigured, store, _, _ := newTestService(t, nil)
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	if _, err := unconfigured.Request(ctx, "usr_zhang", "hand_1"); codeOf(err) != "review_unavailable" {
		t.Fatalf("without a model: %v", err)
	}

	service, store, _, _ := newTestService(t, &fakeModel{})
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); codeOf(err) != "review_not_allowed" {
		t.Fatalf("not on the list: %v", err)
	}
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	if _, err := service.Request(ctx, "usr_zhang", "no_such_hand"); codeOf(err) != "hand_not_found" {
		t.Fatalf("unknown hand: %v", err)
	}
	// 名单上的人也只能复盘自己打过的牌
	_ = store.SetAccess(ctx, "outsider", true, "admin", time.Now())
	if _, err := service.Request(ctx, "outsider", "hand_1"); codeOf(err) != "hand_not_found" {
		t.Fatalf("someone else's hand: %v", err)
	}
	_ = store.SaveSettings(ctx, Settings{Enabled: false}, "admin", time.Now())
	if available, _ := service.Available(ctx, "usr_zhang"); available {
		t.Fatal("the global switch is off")
	}
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); codeOf(err) != "review_unavailable" {
		t.Fatalf("globally disabled: %v", err)
	}
}

func TestReviewIsAnalysedOnceAndCached(t *testing.T) {
	ctx := context.Background()
	model := &fakeModel{responses: []string{goodOutput}}
	service, store, _, _ := newTestService(t, model)
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())

	queued, err := service.Request(ctx, "usr_zhang", "hand_1")
	if err != nil || queued.Status != StatusQueued {
		t.Fatalf("request=%+v err=%v", queued, err)
	}
	if !service.ProcessNext(ctx) || service.ProcessNext(ctx) {
		t.Fatal("exactly one review should be processed")
	}
	done, err := service.Get(ctx, "usr_zhang", "hand_1")
	if err != nil || done.Status != StatusDone || done.Result == nil {
		t.Fatalf("review=%+v err=%v", done, err)
	}
	result := done.Result
	// 不存在的步被丢掉，结论的同义说法归一
	if len(result.Decisions) != 2 || result.Decisions[0].Step != 2 || result.Decisions[1].Verdict != "失误" {
		t.Fatalf("decisions=%+v", result.Decisions)
	}
	if done.InputTokens != 1000 || done.OutputTokens != 500 || done.Model != "fake-model" {
		t.Fatalf("usage=%+v", done)
	}
	// 再点一次直接返回缓存，不再花钱
	again, err := service.Request(ctx, "usr_zhang", "hand_1")
	if err != nil || again.Status != StatusDone || len(model.calls) != 1 {
		t.Fatalf("second request=%+v err=%v calls=%d", again, err, len(model.calls))
	}
}

// 交给模型的内容不含昵称、用户 ID、房间码，也不含没亮过的底牌。
func TestPromptCarriesNoIdentityOrHiddenCards(t *testing.T) {
	ctx := context.Background()
	model := &fakeModel{responses: []string{goodOutput}}
	service, store, _, _ := newTestService(t, model)
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil {
		t.Fatal(err)
	}
	service.ProcessNext(ctx)
	prompt := model.calls[0]
	for _, forbidden := range []string{"张三", "李四", "王五", "usr_zhang", "usr_li", "usr_wang", "room_secret", "654321",
		"Qc", "Qd", "8h", "8s"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("the prompt leaks %q", forbidden)
		}
	}
	for _, required := range []string{"\"As\"", "\"Kd\"", "\"SB\"", "\"BTN\"", "potOddsPercent"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("the prompt is missing %s", required)
		}
	}
}

func TestInvalidOutputFailsAndCanBeRetried(t *testing.T) {
	ctx := context.Background()
	model := &fakeModel{responses: []string{"不是 JSON", "还不是", goodOutput}}
	service, store, _, _ := newTestService(t, model)
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil {
		t.Fatal(err)
	}
	service.ProcessNext(ctx)
	failed, _ := service.Get(ctx, "usr_zhang", "hand_1")
	if failed.Status != StatusFailed || failed.Error != "invalid_output" || failed.Result != nil {
		t.Fatalf("failed review=%+v", failed)
	}
	// 两次尝试的 token 都记上
	if failed.InputTokens != 2000 {
		t.Fatalf("tokens=%d", failed.InputTokens)
	}
	retried, err := service.Request(ctx, "usr_zhang", "hand_1")
	if err != nil || retried.Status != StatusQueued {
		t.Fatalf("retry=%+v err=%v", retried, err)
	}
	service.ProcessNext(ctx)
	done, _ := service.Get(ctx, "usr_zhang", "hand_1")
	if done.Status != StatusDone || done.InputTokens != 3000 {
		t.Fatalf("after retry=%+v", done)
	}
}

func TestModelErrorsDoNotLeakDetails(t *testing.T) {
	ctx := context.Background()
	model := &fakeModel{errors: []error{ModelError{Status: 401, Detail: "invalid api key sk-secret"}}}
	service, store, _, _ := newTestService(t, model)
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil {
		t.Fatal(err)
	}
	service.ProcessNext(ctx)
	failed, _ := service.Get(ctx, "usr_zhang", "hand_1")
	if failed.Status != StatusFailed || failed.Error != "model_error" {
		t.Fatalf("failed review=%+v", failed)
	}
}

func TestQuotas(t *testing.T) {
	ctx := context.Background()
	service, store, hands, now := newTestService(t, &fakeModel{responses: []string{goodOutput, goodOutput}})
	_ = store.SetAccess(ctx, "usr_zhang", true, "admin", time.Now())
	if err := hands.Append(sampleHand("hand_2", now.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	_ = service.UpdateSettings(ctx, Settings{Enabled: true, DailyLimitPerUser: 1}, "admin")
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Request(ctx, "usr_zhang", "hand_2"); codeOf(err) != "review_daily_limit" {
		t.Fatalf("daily limit: %v", err)
	}
	// 已经发起过的那一手随时可以查看，不占额度
	if _, err := service.Request(ctx, "usr_zhang", "hand_1"); err != nil {
		t.Fatalf("repeat request: %v", err)
	}
	service.ProcessNext(ctx)
	_ = service.UpdateSettings(ctx, Settings{Enabled: true, MonthlyTokenBudget: 1500}, "admin")
	if _, err := service.Request(ctx, "usr_zhang", "hand_2"); codeOf(err) != "review_budget_exhausted" {
		t.Fatalf("budget: %v", err)
	}
	if err := service.UpdateSettings(ctx, Settings{DailyLimitPerUser: -1}, "admin"); codeOf(err) != "invalid_review_settings" {
		t.Fatalf("negative limit: %v", err)
	}
}

// 局面数据：数字由服务端算好。翻前本人在 SB 面对 BTN 加注到 60。
func TestFactsForTheFirstDecision(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	hand := redacted(sampleHand("hand_1", now))
	timeline, err := replay.Build(hand, "usr_zhang")
	if err != nil {
		t.Fatal(err)
	}
	facts := BuildFacts(hand, timeline, "usr_zhang", nil)
	if facts.Hero.Position != "SB" || len(facts.Decisions) != 5 || len(facts.Actions) != 10 {
		t.Fatalf("hero=%+v decisions=%d actions=%d", facts.Hero, len(facts.Decisions), len(facts.Actions))
	}
	first := facts.Decisions[0]
	if first.PotBefore != 90 || first.ToCall != 50 || first.PotOdds != 35.7 || first.EffectiveStack != 1000 ||
		first.SPR != 10.9 || first.Action != "call" || len(first.OpponentsInHand) != 2 {
		t.Fatalf("first decision=%+v", first)
	}
	// AK 对两手随机牌的翻前胜率大约在 50% 附近
	if first.EquityVsRandom < 40 || first.EquityVsRandom > 60 {
		t.Fatalf("equity=%v", first.EquityVsRandom)
	}
	// 同一手每次算出来都一样
	again := BuildFacts(hand, timeline, "usr_zhang", nil)
	if again.Decisions[0].EquityVsRandom != first.EquityVsRandom {
		t.Fatal("equity must be deterministic")
	}
	flop := facts.Decisions[1]
	if flop.Street != "flop" || flop.HandCategory != "one_pair" || len(flop.Board) != 3 || flop.ToCall != 0 {
		t.Fatalf("flop decision=%+v", flop)
	}
	if len(facts.Hindsight.Revealed) != 0 || facts.Hindsight.Showdown {
		t.Fatalf("hindsight=%+v", facts.Hindsight)
	}
}

// 对手倾向只数两人同时在场的牌局，只看公开动作。
func TestTendenciesCountOnlySharedHands(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	hand := redacted(sampleHand("hand_1", now))
	timeline, _ := replay.Build(hand, "usr_zhang")
	shared := redacted(sampleHand("hand_0", now.Add(-time.Hour)))
	elsewhere := sampleHand("hand_x", now.Add(-2*time.Hour))
	elsewhere.Players[0].UserID = "someone_else"
	for index := range elsewhere.Actions {
		if elsewhere.Actions[index].UserID == "usr_zhang" {
			elsewhere.Actions[index].UserID = "someone_else"
		}
	}
	facts := BuildFacts(hand, timeline, "usr_zhang", []history.Hand{hand, shared, elsewhere})
	var button OpponentFacts
	for _, opponent := range facts.Opponents {
		if opponent.Position == "BTN" {
			button = opponent
		}
	}
	// 两手同桌：每手都加注入池、翻后一次下注一次过牌、没到摊牌
	if button.HandsSeen != 2 || button.VPIP != 100 || button.PFR != 100 || button.WTSD != 0 {
		t.Fatalf("button tendencies=%+v", button)
	}
}

func TestParseResultRejectsGarbage(t *testing.T) {
	for _, content := range []string{"", "没有", "{}", `{"summary":""}`, `{"summary": 3}`} {
		if _, err := parseResult(content, map[int]bool{1: true}); err == nil {
			t.Errorf("accepted %q", content)
		}
	}
}

// redacted 模拟牌谱按请求者裁剪：只留本人的底牌（这手没有摊牌）。
func redacted(hand history.Hand) history.Hand {
	for index := range hand.Players {
		if hand.Players[index].UserID != "usr_zhang" {
			hand.Players[index].HoleCards = nil
		}
	}
	return hand
}

// 大盲筹码不足、全下只投了 5：引擎仍要求别人跟满一个大盲，需跟注额不能按 5 算。
// SPR 只用还没投进底池的筹码。
func TestFactsAgainstAShortBigBlind(t *testing.T) {
	seats := func(zhang, wang, li replay.SeatState) []replay.SeatState {
		return []replay.SeatState{zhang, wang, li}
	}
	blinds := replay.Step{
		Index: 0, Kind: "blinds", Street: "preflop", Pot: 15,
		Seats: seats(
			replay.SeatState{UserID: "usr_zhang", Stack: 1000},
			replay.SeatState{UserID: "usr_wang", Stack: 490, StreetBet: 10, TotalBet: 10},
			replay.SeatState{UserID: "usr_li", Stack: 0, StreetBet: 5, TotalBet: 5, AllIn: true},
		),
	}
	call := replay.Step{
		Index: 1, Kind: replay.KindAction, Street: "preflop", ActorID: "usr_zhang", Action: "call", Amount: 20, Pot: 35,
		Seats: seats(
			replay.SeatState{UserID: "usr_zhang", Stack: 980, StreetBet: 20, TotalBet: 20},
			blinds.Seats[1], blinds.Seats[2],
		),
	}
	timeline := replay.Timeline{HandID: "hand_short", SmallBlind: 10, BigBlind: 20, Steps: []replay.Step{blinds, call}}
	decision := decisionAt(timeline, 1, "usr_zhang", []string{"As", "Kd"},
		map[string]string{"usr_wang": "SB", "usr_li": "BB"}, func(value int64) float64 { return float64(value) / 20 })
	if decision.ToCall != 20 || decision.PotOdds != 57.1 || decision.SPR != 32.7 {
		t.Fatalf("decision=%+v", decision)
	}
}

// 没超过当前最高投入的全下只是跟不足额，不能算成加注。
func TestAllInsAreClassifiedByWhatTheyDid(t *testing.T) {
	hand := history.Hand{
		SmallBlind: 10, BigBlind: 20, SmallBlindSeat: 1, BigBlindSeat: 2,
		Players: []history.PlayerResult{{UserID: "sb", Seat: 1}, {UserID: "bb", Seat: 2}, {UserID: "btn", Seat: 3}},
		Actions: []history.Action{
			{UserID: "btn", Street: "preflop", Type: "all_in", Committed: 15},
			{UserID: "sb", Street: "preflop", Type: "all_in", Committed: 40},
			{UserID: "bb", Street: "preflop", Type: "all_in", Committed: 20},
			{UserID: "bb", Street: "flop", Type: "all_in", Committed: 30},
		},
	}
	var got []string
	for _, action := range classifyAllIns(hand) {
		got = append(got, action.Type)
	}
	// 按钮 15 < 大盲 20 → 跟；小盲 10+40=50 > 20 → 加；大盲 20+20=40 < 50 → 跟；翻牌首个下注 → 加
	if strings.Join(got, ",") != "call,raise,call,raise" {
		t.Fatalf("classified=%v", got)
	}
	// 0.8.0 之前的牌谱没记盲注座位：按庄位推出（庄位 3 → 小盲 1、大盲 2）。
	// 按钮加到 60，小盲再全下 60（合计 70）是加注
	old := history.Hand{
		SmallBlind: 10, BigBlind: 20, DealerSeat: 3,
		Players: []history.PlayerResult{{UserID: "sb", Seat: 1}, {UserID: "bb", Seat: 2}, {UserID: "btn", Seat: 3}},
		Actions: []history.Action{
			{UserID: "btn", Street: "preflop", Type: "raise", Committed: 60, RaiseTo: 60},
			{UserID: "sb", Street: "preflop", Type: "all_in", Committed: 60},
		},
	}
	if classified := classifyAllIns(old); classified[1].Type != "raise" {
		t.Fatalf("old hand classified=%+v", classified)
	}
}
