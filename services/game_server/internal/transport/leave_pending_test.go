package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/game/holdem"
)

// 弃牌后中途离开的人，成员记录要留到本手结算（桌上筹码记在上面）。这段时间对他
// 本人必须表现为「已经离开」：查当前房间得到 404，否则重启应用会被直接送回牌桌；
// 创建别的房间得到 leave_pending 而不是让人摸不着头脑的 already_in_room。
// 本手一结算，他就真正离开，可以照常创建房间。
func TestFoldedMidHandLeaverIsHiddenUntilTheHandSettles(t *testing.T) {
	ctx := context.Background()
	fixture := newOwnerFixture(t)
	third := addThirdMember(t, fixture)
	users := map[string]account.AuthResult{
		fixture.owner.User.UserID: fixture.owner,
		fixture.guest.User.UserID: fixture.guest,
		third.User.UserID:         third,
	}
	for userID := range users {
		if _, err := fixture.tables.Join(ctx, userID, fixture.created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	for userID := range users {
		if _, err := fixture.tables.SetReady(ctx, userID, true); err != nil {
			t.Fatal(err)
		}
	}
	started, err := fixture.tables.Snapshot(ctx, fixture.owner.User.UserID, fixture.created.RoomID)
	if err != nil || started.CurrentAction == nil {
		t.Fatalf("hand did not start: %#v err=%v", started.Phase, err)
	}
	leaver := users[started.CurrentAction.UserID]
	_, afterFold, err := fixture.tables.SubmitAction(ctx, leaver.User.UserID, fixture.created.RoomID, holdem.ActionRequest{
		ActionID: "fold-leaver", HandID: started.HandID,
		TableRevision: started.TableRevision, Action: holdem.ActionFold,
	})
	if err != nil || afterFold.Phase == holdem.PhaseWaitingNextHand {
		t.Fatalf("phase=%s err=%v", afterFold.Phase, err)
	}
	walletBefore, err := fixture.chips.Snapshot(ctx, leaver.User.UserID)
	if err != nil {
		t.Fatal(err)
	}

	response := fixture.post(t, "/v1/rooms/leave", leaver.AccessToken, map[string]any{})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("leave status=%d", response.StatusCode)
	}
	response.Body.Close()

	// 对本人：已经不在房间里
	current := doJSONRequest(t, http.MethodGet, fixture.server.URL+"/v1/rooms/current", leaver.AccessToken, nil)
	if current.StatusCode != http.StatusNotFound || responseErrorCode(t, current) != "room_not_found" {
		t.Fatalf("current room must be hidden while the leave is pending: status=%d", current.StatusCode)
	}
	// 对其他人：房间照常可见
	others := doJSONRequest(t, http.MethodGet, fixture.server.URL+"/v1/rooms/current", users[afterFold.CurrentAction.UserID].AccessToken, nil)
	if others.StatusCode != http.StatusOK {
		t.Fatalf("other members must still see the room: status=%d", others.StatusCode)
	}
	others.Body.Close()
	// 这时创建房间：明确告诉他是上一手还没结算
	createBody := map[string]any{
		"preset": "standard", "smallBlind": 10, "bigBlind": 20,
		"maxBuyIn": 5_000, "buyIn": 1_000, "requestId": "create-while-pending",
	}
	created := fixture.post(t, "/v1/rooms", leaver.AccessToken, createBody)
	if created.StatusCode != http.StatusConflict || responseErrorCode(t, created) != "leave_pending" {
		t.Fatalf("creating a room while the leave is pending: status=%d", created.StatusCode)
	}

	// 本手结算：退还完成、真正移出
	snapshot, err := fixture.tables.Snapshot(ctx, afterFold.CurrentAction.UserID, fixture.created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	_, ended, err := fixture.tables.SubmitAction(ctx, snapshot.CurrentAction.UserID, fixture.created.RoomID, holdem.ActionRequest{
		ActionID: "fold-to-end", HandID: snapshot.HandID,
		TableRevision: snapshot.TableRevision, Action: holdem.ActionFold,
	})
	if err != nil || ended.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("phase=%s err=%v", ended.Phase, err)
	}
	walletAfter, err := fixture.chips.Snapshot(ctx, leaver.User.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if walletAfter.TableChips != 0 || walletAfter.WalletChips != walletBefore.WalletChips+walletBefore.TableChips {
		t.Fatalf("the folded leaver bet nothing, the whole stack must be back: before=%#v after=%#v", walletBefore, walletAfter)
	}
	createBody["requestId"] = "create-after-settle"
	createdAfter := fixture.post(t, "/v1/rooms", leaver.AccessToken, createBody)
	if createdAfter.StatusCode != http.StatusCreated {
		t.Fatalf("creating a room after the hand settled: status=%d code=%s", createdAfter.StatusCode, responseErrorCode(t, createdAfter))
	}
	createdAfter.Body.Close()
}

func responseErrorCode(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return body.Error
}
