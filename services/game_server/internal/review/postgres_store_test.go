package review

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/postgres"
	"texas/services/game_server/migrations"
)

// 复盘仓储在真实库上的往返：设置单行表、开通名单与单独额度、任务的排队/领取/
// 完成/重试/延后重试、旧版结果、结果 JSON、用量统计。内存实现测不出列名、约束
// 与 SQL 本身的问题。
func TestPostgresReviewStore(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	database.SetMaxOpenConns(1)
	schema := fmt.Sprintf("review_store_test_%d", time.Now().UnixNano())
	if _, err := database.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
		_ = database.Close()
	})
	if _, err := database.ExecContext(ctx, `SET search_path TO `+schema); err != nil {
		t.Fatalf("set search path: %v", err)
	}
	migrator, err := postgres.NewMigrator(migrations.Files)
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	if _, err := migrator.Up(ctx, database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, userID := range []string{"usr_zhang", "usr_li", "usr_wang", "admin"} {
		if _, err := database.ExecContext(ctx,
			`INSERT INTO users (user_id, username, display_name, password_hash) VALUES ($1, $1, $1, 'hash')`, userID,
		); err != nil {
			t.Fatalf("create user %s: %v", userID, err)
		}
	}
	hands, err := history.NewPostgresStore(database)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	for index, handID := range []string{"hand_pg", "hand_pg_2"} {
		if err := hands.Append(sampleHand(handID, now.Add(time.Duration(index)*time.Minute))); err != nil {
			t.Fatalf("append hand: %v", err)
		}
	}
	store, err := NewPostgresStore(database)
	if err != nil {
		t.Fatal(err)
	}
	runStoreContract(ctx, t, store, now)
}

// 内存实现走同一套检查：两种实现的语义必须一致，本机没有数据库时也能跑。
func TestMemoryReviewStoreContract(t *testing.T) {
	runStoreContract(context.Background(), t, NewMemoryStore(), time.Now().UTC().Truncate(time.Microsecond))
}

func runStoreContract(ctx context.Context, t *testing.T, store Store, now time.Time) {
	t.Helper()
	// 设置：默认开着、不限额度
	settings, err := store.Settings(ctx)
	if err != nil || !settings.Enabled || settings.DailyLimitPerUser != 0 || settings.MaxInFlightPerUser != 0 ||
		settings.MonthlyTokenBudget != 0 {
		t.Fatalf("default settings=%+v err=%v", settings, err)
	}
	want := Settings{Enabled: false, DailyLimitPerUser: 3, MaxInFlightPerUser: 2, MonthlyTokenBudget: 1_000_000}
	if err := store.SaveSettings(ctx, want, "admin", now); err != nil {
		t.Fatal(err)
	}
	if settings, err = store.Settings(ctx); err != nil || settings != want {
		t.Fatalf("settings=%+v err=%v", settings, err)
	}

	// 开通名单：重复开通不报错，单独额度可设可清，收回后查不到
	for range 2 {
		if err := store.SetAccess(ctx, "usr_zhang", true, "admin", now); err != nil {
			t.Fatal(err)
		}
	}
	access, err := store.AccessFor(ctx, "usr_zhang")
	if err != nil || access.GrantedBy != "admin" || access.DailyLimit != nil || access.MaxInFlight != nil {
		t.Fatalf("access=%+v err=%v", access, err)
	}
	daily, inFlight := 5, 0
	if err := store.SetLimits(ctx, "usr_zhang", UserLimits{DailyLimit: &daily, MaxInFlight: &inFlight}); err != nil {
		t.Fatal(err)
	}
	if list, err := store.AccessList(ctx); err != nil || len(list) != 1 || *list[0].DailyLimit != 5 || *list[0].MaxInFlight != 0 {
		t.Fatalf("access list=%+v err=%v", list, err)
	}
	if err := store.SetLimits(ctx, "usr_zhang", UserLimits{DailyLimit: &daily}); err != nil {
		t.Fatal(err)
	}
	if access, _ = store.AccessFor(ctx, "usr_zhang"); access.MaxInFlight != nil {
		t.Fatalf("cleared limit=%+v", access)
	}
	if err := store.SetLimits(ctx, "usr_li", UserLimits{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("limits for someone not on the list: %v", err)
	}
	if err := store.SetAccess(ctx, "usr_zhang", false, "admin", now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AccessFor(ctx, "usr_zhang"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked access: %v", err)
	}

	// 任务：排队、同一手重复排队、领取、完成
	value := Review{
		ReviewID: "rev_pg", HandID: "hand_pg", UserID: "usr_zhang", PromptVersion: PromptVersion,
		Status: StatusQueued, CreatedAt: now, RequestedAt: now,
	}
	if err := store.Create(ctx, value); err != nil {
		t.Fatal(err)
	}
	value.ReviewID = "rev_pg_duplicate"
	if err := store.Create(ctx, value); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate create: %v", err)
	}
	if count, err := store.CountInFlight(ctx, "usr_zhang"); err != nil || count != 1 {
		t.Fatalf("in flight=%d err=%v", count, err)
	}
	claimed, found, err := store.ClaimNext(ctx, now)
	if err != nil || !found || claimed.ReviewID != "rev_pg" || claimed.Status != StatusRunning {
		t.Fatalf("claim=%+v found=%v err=%v", claimed, found, err)
	}
	if _, found, _ := store.ClaimNext(ctx, now); found {
		t.Fatal("nothing else is queued")
	}
	// 模型繁忙：延后重试，到点之前领不到
	if err := store.RetryLater(ctx, "rev_pg", now.Add(time.Minute), 7, 3); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := store.ClaimNext(ctx, now); found {
		t.Fatal("a delayed retry must wait")
	}
	if claimed, found, _ = store.ClaimNext(ctx, now.Add(2*time.Minute)); !found || claimed.Attempts != 1 ||
		claimed.NextAttemptAt == nil || claimed.InputTokens != 7 {
		t.Fatalf("retried claim=%+v found=%v", claimed, found)
	}
	// 进程重启：进行中的放回队列
	if err := store.RequeueRunning(ctx); err != nil {
		t.Fatal(err)
	}
	if claimed, found, _ = store.ClaimNext(ctx, now.Add(2*time.Minute)); !found || claimed.ReviewID != "rev_pg" {
		t.Fatal("the interrupted review must be claimable again")
	}
	if err := store.Finish(ctx, "rev_pg", StatusFailed, "model-x", nil, "model_error: boom", 93, 47, now); err != nil {
		t.Fatal(err)
	}
	// 失败的不占每人次数，也不算进行中
	if count, err := store.CountRequests(ctx, "usr_zhang", now); err != nil || count != 0 {
		t.Fatalf("failed review counted: count=%d err=%v", count, err)
	}
	if count, _ := store.CountInFlight(ctx, "usr_zhang"); count != 0 {
		t.Fatalf("failed review in flight=%d", count)
	}
	if err := store.Requeue(ctx, "rev_pg", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// 重新排队后 finished_at 清空，之前失败花掉的 token 仍计入预算；重试次数清零
	if usage, err := store.Usage(ctx, now.Add(2*time.Minute)); err != nil || usage.Tokens30d != 150 {
		t.Fatalf("usage while requeued=%+v err=%v", usage, err)
	}
	if claimed, found, _ = store.ClaimNext(ctx, now.Add(2*time.Minute)); !found || claimed.Attempts != 0 {
		t.Fatalf("the retried review must be claimable: %+v", claimed)
	}
	evTaken, evBest := -1.5, 2.25
	result := &Result{
		Summary: "总评", Decisions: []Decision{{
			Step: 2, Verdict: "合理", Reasoning: "分析", BestAction: "3bet", EVTaken: &evTaken, EVBest: &evBest,
			Facts: &DecisionFacts{Step: 2, PotBefore: 90, ToCall: 50, PotOdds: 35.7},
		}},
		KeyLessons: []string{"要点"}, OpponentNotes: []string{}, Hindsight: "回顾",
	}
	if err := store.Finish(ctx, "rev_pg", StatusDone, "model-x", result, "", 1000, 500, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	done, err := store.Find(ctx, "usr_zhang", "hand_pg", PromptVersion)
	if err != nil || done.Status != StatusDone || done.Result == nil || done.Result.Decisions[0].BestAction != "3bet" ||
		*done.Result.Decisions[0].EVBest != 2.25 || done.Result.Decisions[0].Facts.PotOdds != 35.7 ||
		done.InputTokens != 1100 || done.OutputTokens != 550 || done.FinishedAt == nil || done.Model != "model-x" {
		t.Fatalf("done=%+v err=%v", done, err)
	}
	if _, err := store.Find(ctx, "usr_li", "hand_pg", PromptVersion); !errors.Is(err, ErrNotFound) {
		t.Fatalf("someone else's review: %v", err)
	}
	// 旧版提示词的结果：新版没有时能找到最近完成的那条
	older := Review{
		ReviewID: "rev_pg_old", HandID: "hand_pg_2", UserID: "usr_zhang", PromptVersion: "v0",
		Status: StatusQueued, CreatedAt: now, RequestedAt: now,
	}
	if err := store.Create(ctx, older); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := store.ClaimNext(ctx, now.Add(3*time.Minute)); !found {
		t.Fatal("claim the old version")
	}
	if err := store.Finish(ctx, "rev_pg_old", StatusDone, "model-x", result, "", 1, 1, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if latest, err := store.FindLatestDone(ctx, "usr_zhang", "hand_pg_2"); err != nil || latest.PromptVersion != "v0" {
		t.Fatalf("latest done=%+v err=%v", latest, err)
	}
	if _, err := store.FindLatestDone(ctx, "usr_li", "hand_pg_2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("someone else's old review: %v", err)
	}

	// 余额不足：排着的全部记失败，进行中与已完成的不动
	queued := Review{
		ReviewID: "rev_pg_queued", HandID: "hand_pg_2", UserID: "usr_zhang", PromptVersion: PromptVersion,
		Status: StatusQueued, CreatedAt: now, RequestedAt: now.Add(4 * time.Minute),
	}
	if err := store.Create(ctx, queued); err != nil {
		t.Fatal(err)
	}
	if count, err := store.FailQueued(ctx, "model_insufficient_balance", now.Add(5*time.Minute)); err != nil || count != 1 {
		t.Fatalf("fail queued=%d err=%v", count, err)
	}
	if failed, _ := store.Find(ctx, "usr_zhang", "hand_pg_2", PromptVersion); failed.Status != StatusFailed ||
		failed.Error != "model_insufficient_balance" {
		t.Fatalf("failed=%+v", failed)
	}
	if kept, _ := store.Find(ctx, "usr_zhang", "hand_pg", PromptVersion); kept.Status != StatusDone {
		t.Fatalf("done review must be kept: %+v", kept)
	}

	// 牌局记录里的标识：任意版本完成过就算，当前版本失败不影响；只看本人的
	reviewed, err := store.DoneHands(ctx, "usr_zhang", []string{"hand_pg", "hand_pg_2", "hand_missing"})
	if err != nil || len(reviewed) != 2 || !reviewed["hand_pg"] || !reviewed["hand_pg_2"] {
		t.Fatalf("reviewed=%v err=%v", reviewed, err)
	}
	if reviewed, err := store.DoneHands(ctx, "usr_li", []string{"hand_pg", "hand_pg_2"}); err != nil || len(reviewed) != 0 {
		t.Fatalf("someone else's reviewed=%v err=%v", reviewed, err)
	}
	if reviewed, err := store.DoneHands(ctx, "usr_zhang", nil); err != nil || len(reviewed) != 0 {
		t.Fatalf("no hands reviewed=%v err=%v", reviewed, err)
	}

	// 用量：请求按 requested_at，token 按 finished_at
	if count, err := store.CountRequests(ctx, "usr_zhang", now); err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	usage, err := store.Usage(ctx, now.Add(6*time.Minute))
	if err != nil || usage.Requests24h != 3 || usage.Requests30d != 3 || usage.Tokens30d != 1652 {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
}
