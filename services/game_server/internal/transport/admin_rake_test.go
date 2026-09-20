package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/protocol"
)

func decodeBody(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode body: %v", err)
	}
}

// 管理员给房间开抽水：房间聊天里出现一条系统公告，下一手的结算写明抽了多少，
// 抽走的筹码进了管理员钱包，后台能按房间看到累计；房主和普通玩家改不了规则，
// 管理员自己不能创建或加入房间。
func TestAdminSetsRakeAndPlayersAreToldInChat(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newOwnerFixture(t)
	third := addThirdMember(t, fixture)
	administrator, err := fixture.accounts.RegisterWithOptions(ctx, "rake_admin", "管理员", "password-123",
		account.RegistrationOptions{RequestInitialAdmin: true})
	if err != nil {
		t.Fatalf("register administrator: %v", err)
	}
	roomID := fixture.created.RoomID
	users := map[string]account.AuthResult{
		fixture.owner.User.UserID: fixture.owner, fixture.guest.User.UserID: fixture.guest, third.User.UserID: third,
	}

	guestSocket := dialTestSocket(t, ctx, fixture.server.URL)
	defer guestSocket.Close(websocket.StatusNormalClosure, "done")
	authenticateTestSocket(t, ctx, guestSocket, fixture.guest.AccessToken, "guest-device")
	joinTestTable(t, ctx, guestSocket, roomID)

	settings := map[string]any{
		"enabled": true, "basisPoints": 250, "cap": 100, "postflopEnabled": true, "postflopAmount": 20,
	}
	ratePath := "/v1/admin/rooms/" + roomID + "/rake"
	// 房主不是管理员，改不了
	forbidden := doJSONRequest(t, http.MethodPost, fixture.server.URL+ratePath, fixture.owner.AccessToken, settings)
	if forbidden.StatusCode != http.StatusForbidden {
		t.Fatalf("room owner must not change the rake: status=%d", forbidden.StatusCode)
	}
	forbidden.Body.Close()
	// 漏传字段不能被当成「关掉」
	partial := doJSONRequest(t, http.MethodPost, fixture.server.URL+ratePath, administrator.AccessToken, map[string]any{"enabled": true})
	if partial.StatusCode != http.StatusBadRequest || responseErrorCode(t, partial) != "invalid_rake_settings" {
		t.Fatalf("partial settings: status=%d", partial.StatusCode)
	}
	// 翻后加抽超过一个大盲（本房间 20）被拒
	tooMuch := map[string]any{"enabled": true, "basisPoints": 250, "cap": 100, "postflopEnabled": true, "postflopAmount": 21}
	rejected := doJSONRequest(t, http.MethodPost, fixture.server.URL+ratePath, administrator.AccessToken, tooMuch)
	if rejected.StatusCode != http.StatusBadRequest || responseErrorCode(t, rejected) != "invalid_rake_settings" {
		t.Fatalf("postflop above one big blind: status=%d", rejected.StatusCode)
	}

	updated := doJSONRequest(t, http.MethodPost, fixture.server.URL+ratePath, administrator.AccessToken, settings)
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("set rake: status=%d code=%s", updated.StatusCode, responseErrorCode(t, updated))
	}
	updated.Body.Close()

	// 房间里的人在聊天里收到系统公告，写明具体规则
	chatMessage := readUntilType(t, ctx, guestSocket, protocol.TypeTableChatMessage)
	var announcement protocol.ChatMessagePayload
	if err := json.Unmarshal(chatMessage.Payload, &announcement); err != nil {
		t.Fatal(err)
	}
	if announcement.Kind != "system" || announcement.DisplayName != "系统公告" {
		t.Fatalf("announcement=%#v", announcement)
	}
	for _, expected := range []string{"2.5%", "最多 100", "加抽 20", "下一手"} {
		if !strings.Contains(announcement.Content, expected) {
			t.Fatalf("announcement %q is missing %q", announcement.Content, expected)
		}
	}
	// 公告之后紧跟一份快照，带着新规则：牌桌信息栏不必等下一个牌局事件才更新
	pushed := readUntilType(t, ctx, guestSocket, protocol.TypeTableSnapshot)
	var pushedSnapshot struct {
		Rake struct {
			Enabled     bool `json:"enabled"`
			BasisPoints int  `json:"basisPoints"`
		} `json:"rake"`
	}
	if err := json.Unmarshal(pushed.Payload, &pushedSnapshot); err != nil {
		t.Fatal(err)
	}
	if !pushedSnapshot.Rake.Enabled || pushedSnapshot.Rake.BasisPoints != 250 {
		t.Fatalf("snapshot after the rake change=%s", pushed.Payload)
	}
	// 规则没变时不重复公告
	again := doJSONRequest(t, http.MethodPost, fixture.server.URL+ratePath, administrator.AccessToken, settings)
	if again.StatusCode != http.StatusOK {
		t.Fatalf("repeat set rake: status=%d", again.StatusCode)
	}
	again.Body.Close()

	// 打一手到摊牌
	for userID := range users {
		if _, err := fixture.tables.Join(ctx, userID, roomID); err != nil {
			t.Fatal(err)
		}
	}
	for userID := range users {
		if _, err := fixture.tables.SetReady(ctx, userID, true); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := fixture.tables.Snapshot(ctx, fixture.owner.User.UserID, roomID)
	if err != nil || snapshot.CurrentAction == nil {
		t.Fatalf("hand did not start: err=%v", err)
	}
	for steps := 0; snapshot.Phase != holdem.PhaseWaitingNextHand; steps++ {
		if steps > 30 || snapshot.CurrentAction == nil {
			t.Fatalf("hand did not finish: phase=%s", snapshot.Phase)
		}
		action := holdem.ActionCall
		if snapshot.CurrentAction.Options.CanCheck {
			action = holdem.ActionCheck
		}
		_, snapshot, err = fixture.tables.SubmitAction(ctx, snapshot.CurrentAction.UserID, roomID, holdem.ActionRequest{
			ActionID: "step-" + string(rune('a'+steps)), HandID: snapshot.HandID,
			TableRevision: snapshot.TableRevision, Action: action,
		})
		if err != nil {
			t.Fatalf("step %d: %v", steps, err)
		}
	}
	// 三人各投 20，底池 60：2.5% 向下取整是 1，翻后再加 20
	if snapshot.Settlement == nil || snapshot.Settlement.Rake != 21 {
		t.Fatalf("settlement=%#v", snapshot.Settlement)
	}
	adminWallet, err := fixture.chips.Snapshot(ctx, administrator.User.UserID)
	if err != nil || adminWallet.WalletChips != 21 {
		t.Fatalf("admin wallet=%#v err=%v", adminWallet, err)
	}

	// 后台：房间列表带着规则与累计，汇总页有合计
	var roomList struct {
		Rooms []adminRoomResponse `json:"rooms"`
	}
	decodeBody(t, doJSONRequest(t, http.MethodGet, fixture.server.URL+"/v1/admin/rooms", administrator.AccessToken, nil), &roomList)
	if len(roomList.Rooms) != 1 || roomList.Rooms[0].RoomCode != fixture.created.Code ||
		!roomList.Rooms[0].Rake.Enabled || roomList.Rooms[0].Rake.BasisPoints != 250 ||
		roomList.Rooms[0].RakeTotal != 21 || roomList.Rooms[0].RakeHands != 1 || roomList.Rooms[0].SeatedCount != 3 {
		t.Fatalf("admin rooms=%#v", roomList.Rooms)
	}
	var summary struct {
		Rooms []struct {
			RoomID   string `json:"roomId"`
			RoomCode string `json:"roomCode"`
			Closed   bool   `json:"closed"`
			Hands    int    `json:"hands"`
			Total    int64  `json:"total"`
		} `json:"rooms"`
		Total int64 `json:"total"`
		Hands int   `json:"hands"`
	}
	decodeBody(t, doJSONRequest(t, http.MethodGet, fixture.server.URL+"/v1/admin/rake", administrator.AccessToken, nil), &summary)
	if summary.Total != 21 || summary.Hands != 1 || len(summary.Rooms) != 1 ||
		summary.Rooms[0].RoomCode != fixture.created.Code || summary.Rooms[0].Closed {
		t.Fatalf("rake summary=%#v", summary)
	}
	playerView := doJSONRequest(t, http.MethodGet, fixture.server.URL+"/v1/admin/rake", fixture.owner.AccessToken, nil)
	if playerView.StatusCode != http.StatusForbidden {
		t.Fatalf("players must not see the rake summary: status=%d", playerView.StatusCode)
	}
	playerView.Body.Close()

	// 管理员不能上桌：创建与加入都被拒
	create := fixture.post(t, "/v1/rooms", administrator.AccessToken, map[string]any{
		"preset": "standard", "smallBlind": 10, "bigBlind": 20, "maxBuyIn": 5_000, "buyIn": 1_000, "requestId": "admin-create",
	})
	if create.StatusCode != http.StatusForbidden || responseErrorCode(t, create) != "admin_cannot_play" {
		t.Fatalf("admin create: status=%d", create.StatusCode)
	}
	join := fixture.post(t, "/v1/rooms/join", administrator.AccessToken, map[string]any{
		"code": fixture.created.Code, "buyIn": 1_000, "requestId": "admin-join",
	})
	if join.StatusCode != http.StatusForbidden || responseErrorCode(t, join) != "admin_cannot_play" {
		t.Fatalf("admin join: status=%d", join.StatusCode)
	}
}

func TestRakeAnnouncementWording(t *testing.T) {
	for _, testCase := range []struct {
		basisPoints int
		want        string
	}{{500, "5%"}, {250, "2.5%"}, {25, "0.25%"}, {1000, "10%"}, {5, "0.05%"}} {
		if got := formatBasisPoints(testCase.basisPoints); got != testCase.want {
			t.Errorf("formatBasisPoints(%d)=%q want %q", testCase.basisPoints, got, testCase.want)
		}
	}
}
