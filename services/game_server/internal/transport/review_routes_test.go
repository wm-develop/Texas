package transport

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/review"
)

type scriptedModel struct{ content string }

func (model scriptedModel) Name() string { return "scripted" }

func (model scriptedModel) Complete(context.Context, string, string) (review.Completion, error) {
	return review.Completion{Content: model.content, Model: "scripted", InputTokens: 10, OutputTokens: 5}, nil
}

// AI 复盘端到端：管理员开通 → 玩家打一手 → 发起复盘 → 后台分析 → 取回结果。
// 没开通的人看不到入口、发起被拒；普通玩家管不了名单与设置。
func TestHandReviewFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newOwnerFixture(t)
	reviews, err := review.NewService(review.NewMemoryStore(), fixture.hands, scriptedModel{
		// 步号取一个范围：只有本人决策的那几步会被保留
		content: `{"summary":"打得不错","decisions":[` +
			reviewDecisions(1, 30) + `],"keyLessons":["保持"],"opponentNotes":[],"hindsight":"摊牌赢"}`,
	}, time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(testLogger(), Options{
		Accounts: fixture.accounts, Rooms: fixture.rooms, Tables: fixture.tables, Bankroll: fixture.chips,
		History: fixture.hands, Review: reviews,
	}))
	defer server.Close()
	administrator, err := fixture.accounts.RegisterWithOptions(ctx, "review_admin", "管理员", "password-123",
		account.RegistrationOptions{RequestInitialAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	owner, guest := fixture.owner, fixture.guest

	// 打一手到摊牌
	roomID := fixture.created.RoomID
	for _, userID := range []string{owner.User.UserID, guest.User.UserID} {
		if _, err := fixture.tables.Join(ctx, userID, roomID); err != nil {
			t.Fatal(err)
		}
	}
	for _, userID := range []string{owner.User.UserID, guest.User.UserID} {
		if _, err := fixture.tables.SetReady(ctx, userID, true); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := fixture.tables.Snapshot(ctx, owner.User.UserID, roomID)
	if err != nil {
		t.Fatal(err)
	}
	handID := snapshot.HandID
	for steps := 0; snapshot.Phase != holdem.PhaseWaitingNextHand; steps++ {
		action := holdem.ActionCall
		if snapshot.CurrentAction.Options.CanCheck {
			action = holdem.ActionCheck
		}
		if _, snapshot, err = fixture.tables.SubmitAction(ctx, snapshot.CurrentAction.UserID, roomID, holdem.ActionRequest{
			ActionID: "review-step-" + string(rune('a'+steps)), HandID: snapshot.HandID,
			TableRevision: snapshot.TableRevision, Action: action,
		}); err != nil {
			t.Fatal(err)
		}
	}

	access := func(token string) bool {
		var payload struct {
			Available bool `json:"available"`
		}
		decodeBody(t, doJSONRequest(t, http.MethodGet, server.URL+"/v1/review/access", token, nil), &payload)
		return payload.Available
	}
	reviewPath := server.URL + "/v1/hands/" + handID + "/review"
	if access(owner.AccessToken) {
		t.Fatal("nobody is on the list yet")
	}
	refused := doJSONRequest(t, http.MethodPost, reviewPath, owner.AccessToken, map[string]any{})
	if refused.StatusCode != http.StatusForbidden || responseErrorCode(t, refused) != "review_not_allowed" {
		t.Fatalf("not on the list: status=%d", refused.StatusCode)
	}

	// 普通玩家管不了名单与设置
	forbidden := doJSONRequest(t, http.MethodPost, server.URL+"/v1/admin/review/access", owner.AccessToken,
		map[string]any{"userId": owner.User.UserID, "granted": true})
	if forbidden.StatusCode != http.StatusForbidden {
		t.Fatalf("player granting access: status=%d", forbidden.StatusCode)
	}
	forbidden.Body.Close()
	granted := doJSONRequest(t, http.MethodPost, server.URL+"/v1/admin/review/access", administrator.AccessToken,
		map[string]any{"userId": owner.User.UserID, "granted": true})
	if granted.StatusCode != http.StatusOK {
		t.Fatalf("grant: status=%d body=%s", granted.StatusCode, readBody(granted))
	}
	granted.Body.Close()
	if !access(owner.AccessToken) || access(guest.AccessToken) {
		t.Fatal("only the granted player may use the review")
	}
	// 设置：漏传字段被拒
	partial := doJSONRequest(t, http.MethodPost, server.URL+"/v1/admin/review/settings", administrator.AccessToken,
		map[string]any{"enabled": true})
	if partial.StatusCode != http.StatusBadRequest || responseErrorCode(t, partial) != "invalid_review_settings" {
		t.Fatalf("partial settings: status=%d", partial.StatusCode)
	}
	// 0.9.0 的管理页不传「每人同时进行」：保持原值，不被改成不限
	full := func(fields map[string]any) review.Settings {
		var saved review.Settings
		decodeBody(t, doJSONRequest(t, http.MethodPost, server.URL+"/v1/admin/review/settings",
			administrator.AccessToken, fields), &saved)
		return saved
	}
	if saved := full(map[string]any{"enabled": true, "dailyLimitPerUser": 0, "maxInFlightPerUser": 3,
		"monthlyTokenBudget": 0}); saved.MaxInFlightPerUser != 3 {
		t.Fatalf("saved=%+v", saved)
	}
	if saved := full(map[string]any{"enabled": true, "dailyLimitPerUser": 0, "monthlyTokenBudget": 0}); saved.MaxInFlightPerUser != 3 {
		t.Fatalf("an old admin client must not reset the in-flight limit: %+v", saved)
	}
	full(map[string]any{"enabled": true, "dailyLimitPerUser": 0, "maxInFlightPerUser": 0, "monthlyTokenBudget": 0})

	// 发起 → 排队 → 后台处理 → 完成
	var queued review.Review
	decodeBody(t, doJSONRequest(t, http.MethodPost, reviewPath, owner.AccessToken, map[string]any{}), &queued)
	if queued.Status != review.StatusQueued {
		t.Fatalf("queued=%+v", queued)
	}
	if !reviews.ProcessNext(ctx) {
		t.Fatal("the review must be processed")
	}
	var done review.Review
	decodeBody(t, doJSONRequest(t, http.MethodGet, reviewPath, owner.AccessToken, nil), &done)
	if done.Status != review.StatusDone || done.Result == nil || done.Result.Summary != "打得不错" {
		t.Fatalf("done=%+v", done)
	}
	// 对手没开通：取不到，也不能拿别人的手去试
	other := doJSONRequest(t, http.MethodGet, reviewPath, guest.AccessToken, nil)
	if other.StatusCode != http.StatusForbidden {
		t.Fatalf("guest reading the review: status=%d", other.StatusCode)
	}
	other.Body.Close()

	// 管理员看到名单与用量
	var overview review.Overview
	decodeBody(t, doJSONRequest(t, http.MethodGet, server.URL+"/v1/admin/review", administrator.AccessToken, nil), &overview)
	if !overview.ModelConfigured || len(overview.Access) != 1 || overview.Usage.Tokens30d != 15 || overview.Usage.Requests24h != 1 {
		t.Fatalf("overview=%+v", overview)
	}
	// 单独额度：跟随全局用 null，设了就在名单里看得到；普通玩家改不了
	limits := doJSONRequest(t, http.MethodPost, server.URL+"/v1/admin/review/limits", administrator.AccessToken,
		map[string]any{"userId": owner.User.UserID, "dailyLimit": 3, "maxInFlight": nil})
	if limits.StatusCode != http.StatusOK {
		t.Fatalf("limits: status=%d body=%s", limits.StatusCode, readBody(limits))
	}
	limits.Body.Close()
	forbidden = doJSONRequest(t, http.MethodPost, server.URL+"/v1/admin/review/limits", owner.AccessToken,
		map[string]any{"userId": owner.User.UserID, "dailyLimit": 100})
	if forbidden.StatusCode != http.StatusForbidden {
		t.Fatalf("player setting limits: status=%d", forbidden.StatusCode)
	}
	forbidden.Body.Close()
	notGranted := doJSONRequest(t, http.MethodPost, server.URL+"/v1/admin/review/limits", administrator.AccessToken,
		map[string]any{"userId": guest.User.UserID, "dailyLimit": 3})
	if notGranted.StatusCode != http.StatusForbidden || responseErrorCode(t, notGranted) != "review_not_allowed" {
		t.Fatalf("limits for someone not on the list: status=%d", notGranted.StatusCode)
	}
	decodeBody(t, doJSONRequest(t, http.MethodGet, server.URL+"/v1/admin/review", administrator.AccessToken, nil), &overview)
	if overview.Access[0].DailyLimit == nil || *overview.Access[0].DailyLimit != 3 || overview.Access[0].MaxInFlight != nil {
		t.Fatalf("access with limits=%+v", overview.Access[0])
	}
	body := readBody(doJSONRequest(t, http.MethodGet, server.URL+"/v1/admin/audit?limit=50", administrator.AccessToken, nil))
	if !strings.Contains(body, "admin.review_access_changed") || !strings.Contains(body, "admin.review_limits_changed") {
		t.Fatal("granting access and setting limits must be audited")
	}
}

func reviewDecisions(from, to int) string {
	parts := make([]string, 0, to-from+1)
	for step := from; step <= to; step++ {
		parts = append(parts, fmt.Sprintf(`{"step":%d,"verdict":"合理","reasoning":"标准打法","bestAction":"跟注"}`, step))
	}
	return strings.Join(parts, ",")
}
