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

// 复盘仓储在真实库上的往返：设置单行表、开通名单、任务的排队/领取/完成/重试、
// 结果 JSON、用量统计。内存实现测不出列名、约束与 SQL 本身的问题。
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
	if err := hands.Append(sampleHand("hand_pg", now)); err != nil {
		t.Fatalf("append hand: %v", err)
	}
	store, err := NewPostgresStore(database)
	if err != nil {
		t.Fatal(err)
	}

	// 设置：默认开着、不限额度
	settings, err := store.Settings(ctx)
	if err != nil || !settings.Enabled || settings.DailyLimitPerUser != 0 || settings.MonthlyTokenBudget != 0 {
		t.Fatalf("default settings=%+v err=%v", settings, err)
	}
	want := Settings{Enabled: false, DailyLimitPerUser: 3, MonthlyTokenBudget: 1_000_000}
	if err := store.SaveSettings(ctx, want, "admin", now); err != nil {
		t.Fatal(err)
	}
	if settings, err = store.Settings(ctx); err != nil || settings != want {
		t.Fatalf("settings=%+v err=%v", settings, err)
	}

	// 开通名单：重复开通不报错，收回后查不到
	for range 2 {
		if err := store.SetAccess(ctx, "usr_zhang", true, "admin", now); err != nil {
			t.Fatal(err)
		}
	}
	if granted, err := store.HasAccess(ctx, "usr_zhang"); err != nil || !granted {
		t.Fatalf("granted=%v err=%v", granted, err)
	}
	if list, err := store.AccessList(ctx); err != nil || len(list) != 1 || list[0].GrantedBy != "admin" {
		t.Fatalf("access list=%+v err=%v", list, err)
	}
	if err := store.SetAccess(ctx, "usr_zhang", false, "admin", now); err != nil {
		t.Fatal(err)
	}
	if granted, _ := store.HasAccess(ctx, "usr_zhang"); granted {
		t.Fatal("access must be revoked")
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
	claimed, found, err := store.ClaimNext(ctx)
	if err != nil || !found || claimed.ReviewID != "rev_pg" || claimed.Status != StatusRunning {
		t.Fatalf("claim=%+v found=%v err=%v", claimed, found, err)
	}
	if _, found, _ := store.ClaimNext(ctx); found {
		t.Fatal("nothing else is queued")
	}
	// 进程重启：进行中的放回队列
	if err := store.RequeueRunning(ctx); err != nil {
		t.Fatal(err)
	}
	if claimed, found, _ = store.ClaimNext(ctx); !found || claimed.ReviewID != "rev_pg" {
		t.Fatal("the interrupted review must be claimable again")
	}
	if err := store.Finish(ctx, "rev_pg", StatusFailed, "model-x", nil, "model_error: boom", 100, 50, now); err != nil {
		t.Fatal(err)
	}
	// 失败的不占每人次数
	if count, err := store.CountRequests(ctx, "usr_zhang", now); err != nil || count != 0 {
		t.Fatalf("failed review counted: count=%d err=%v", count, err)
	}
	if err := store.Requeue(ctx, "rev_pg", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// 重新排队后 finished_at 清空，之前失败花掉的 token 仍计入预算
	if usage, err := store.Usage(ctx, now.Add(2*time.Minute)); err != nil || usage.Tokens30d != 150 {
		t.Fatalf("usage while requeued=%+v err=%v", usage, err)
	}
	if _, found, _ = store.ClaimNext(ctx); !found {
		t.Fatal("the retried review must be claimable")
	}
	evTaken, evBest := -1.5, 2.25
	result := &Result{
		Summary: "总评", Decisions: []Decision{{
			Step: 2, Verdict: "合理", Reasoning: "分析", BestAction: "3bet", EVTaken: &evTaken, EVBest: &evBest,
			Facts: &DecisionFacts{Step: 2, PotBefore: 90, ToCall: 50, PotOdds: 35.7},
		}},
		Situation: &Situation{HeroPosition: "SB", Players: 3, SmallBlind: 10, BigBlind: 20,
			Seats: []SeatFacts{{Position: "SB", StartingStack: 1000, IsHero: true}}},
		KeyLessons: []string{"要点"}, OpponentNotes: []string{}, Hindsight: "回顾",
	}
	if err := store.Finish(ctx, "rev_pg", StatusDone, "model-x", result, "", 1000, 500, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	done, err := store.Find(ctx, "usr_zhang", "hand_pg", PromptVersion)
	if err != nil || done.Status != StatusDone || done.Result == nil || done.Result.Decisions[0].BestAction != "3bet" ||
		*done.Result.Decisions[0].EVBest != 2.25 || done.Result.Decisions[0].Facts.PotOdds != 35.7 ||
		done.Result.Situation.Seats[0].StartingStack != 1000 ||
		done.InputTokens != 1100 || done.OutputTokens != 550 || done.FinishedAt == nil || done.Model != "model-x" {
		t.Fatalf("done=%+v err=%v", done, err)
	}
	if _, err := store.Find(ctx, "usr_li", "hand_pg", PromptVersion); !errors.Is(err, ErrNotFound) {
		t.Fatalf("someone else's review: %v", err)
	}

	// 用量：请求按 requested_at，token 按 finished_at
	if count, err := store.CountRequests(ctx, "usr_zhang", now); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	usage, err := store.Usage(ctx, now.Add(3*time.Minute))
	if err != nil || usage.Requests24h != 1 || usage.Requests30d != 1 || usage.Tokens30d != 1650 {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
}
