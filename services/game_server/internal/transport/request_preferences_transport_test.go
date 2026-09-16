package transport

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/game/tablemanager"
	"texas/services/game_server/internal/protocol"
)

// 换座被拒时只有申请者收到 table.request.declined，而且它不占序号；
// 「不再接受此人」之后再申请，服务端立刻以 seat_swap_requester_blocked 拒绝；
// 在设置里关掉换座申请后则是 seat_swap_requests_disabled。
func TestDeclinedSeatSwapNotifiesOnlyTheRequester(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture := newOwnerFixture(t)

	ownerSocket := dialTestSocket(t, ctx, fixture.server.URL)
	defer ownerSocket.Close(websocket.StatusNormalClosure, "done")
	authenticateTestSocket(t, ctx, ownerSocket, fixture.owner.AccessToken, "owner-device")
	joinTestTable(t, ctx, ownerSocket, fixture.created.RoomID)
	guestSocket := dialTestSocket(t, ctx, fixture.server.URL)
	defer guestSocket.Close(websocket.StatusNormalClosure, "done")
	authenticateTestSocket(t, ctx, guestSocket, fixture.guest.AccessToken, "guest-device")
	joinTestTable(t, ctx, guestSocket, fixture.created.RoomID)

	ownerSnapshot, err := fixture.tables.Snapshot(ctx, fixture.owner.User.UserID, fixture.created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	guestSeat := 0
	for _, seat := range ownerSnapshot.Seats {
		if seat.UserID == fixture.guest.User.UserID {
			guestSeat = seat.Seat
		}
	}
	if guestSeat == 0 {
		t.Fatalf("guest has no seat: %#v", ownerSnapshot.Seats)
	}

	requestSwap := func(requestID string) {
		writeTestEnvelope(t, ctx, ownerSocket, protocol.Envelope{
			Version: 1, Type: string(protocol.TypeTableSeatChangeRequest), RequestID: requestID,
			TableID: fixture.created.RoomID,
			Payload: json.RawMessage(`{"targetSeat":` + itoa(guestSeat) + `}`),
		})
	}
	requestSwap("swap-1")
	readUntilType(t, ctx, ownerSocket, protocol.TypeTableSeatChangeRequest)

	// 好友的快照里出现待处理申请
	var pendingID string
	for pendingID == "" {
		message := readUntilType(t, ctx, guestSocket, protocol.TypeTableSnapshot)
		var snapshot tablemanager.Snapshot
		if err := json.Unmarshal(message.Payload, &snapshot); err != nil {
			t.Fatal(err)
		}
		if len(snapshot.SeatSwapRequests) == 1 {
			pendingID = snapshot.SeatSwapRequests[0].RequestID
		}
	}

	// 好友拒绝并屏蔽这名申请者
	writeTestEnvelope(t, ctx, guestSocket, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableSeatSwapRespond), RequestID: "respond-1",
		TableID: fixture.created.RoomID,
		Payload: json.RawMessage(`{"pendingRequestId":"` + pendingID + `","accept":false,"scope":"requester"}`),
	})
	readUntilType(t, ctx, guestSocket, protocol.TypeTableSeatSwapRespond)

	declinedMessage := readUntilType(t, ctx, ownerSocket, protocol.TypeTableRequestDeclined)
	if declinedMessage.Sequence != 0 {
		t.Fatalf("a direct notice must not consume a room sequence: %d", declinedMessage.Sequence)
	}
	var declined protocol.RequestDeclinedPayload
	if err := json.Unmarshal(declinedMessage.Payload, &declined); err != nil {
		t.Fatal(err)
	}
	if declined.Kind != "seat_swap" || declined.RequestID != pendingID ||
		declined.TargetUserID != fixture.guest.User.UserID || declined.TargetDisplayName != "好友" ||
		declined.Scope != tablemanager.DeclineRequester {
		t.Fatalf("declined payload=%#v", declined)
	}

	// 被申请者自己的连接上不该出现这条通知：读到下一条快照为止都没有它。
	// 快照由同一个处理协程在回执之后立刻广播，读不到就是真出了问题；
	// 读超时会顺带关掉连接，所以这里直接失败而不是继续往下跑。
	for {
		var message protocol.Envelope
		readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
		err := wsjson.Read(readCtx, guestSocket, &message)
		readCancel()
		if err != nil {
			t.Fatalf("waiting for the responder's snapshot: %v", err)
		}
		if message.Type == string(protocol.TypeTableRequestDeclined) {
			t.Fatal("the decline notice leaked to the responder")
		}
		if message.Type == string(protocol.TypeTableSnapshot) {
			break
		}
	}

	// 再申请：直接被拒，好友那边不会再弹窗
	requestSwap("swap-2")
	if code := errorCodeOf(t, readUntilType(t, ctx, ownerSocket, protocol.TypeSystemError)); code != "seat_swap_requester_blocked" {
		t.Fatalf("error code=%s", code)
	}

	// 关掉偏好后换一个错误码
	writeTestEnvelope(t, ctx, guestSocket, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableRequestPreferencesSet), RequestID: "prefs-1",
		TableID: fixture.created.RoomID,
		Payload: json.RawMessage(`{"allowSeatSwapRequests":false,"allowHoleCardViewRequests":true}`),
	})
	reply := readUntilType(t, ctx, guestSocket, protocol.TypeTableRequestPreferencesSet)
	var echoed protocol.RequestPreferencesPayload
	if err := json.Unmarshal(reply.Payload, &echoed); err != nil || echoed.AllowSeatSwapRequests == nil ||
		echoed.AllowHoleCardViewRequests == nil || *echoed.AllowSeatSwapRequests || !*echoed.AllowHoleCardViewRequests {
		t.Fatalf("reply=%s err=%v", reply.Payload, err)
	}
	var updated tablemanager.Snapshot
	if err := json.Unmarshal(readUntilType(t, ctx, guestSocket, protocol.TypeTableSnapshot).Payload, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.RequestPreferences.AllowSeatSwapRequests {
		t.Fatalf("snapshot preferences=%#v", updated.RequestPreferences)
	}
	// 屏蔽只对好友自己有效，房主的快照里看到的是自己的默认偏好。
	var ownerView tablemanager.Snapshot
	if err := json.Unmarshal(readUntilType(t, ctx, ownerSocket, protocol.TypeTableSnapshot).Payload, &ownerView); err != nil {
		t.Fatal(err)
	}
	if ownerView.RequestPreferences != tablemanager.DefaultRequestPreferences() {
		t.Fatalf("owner must only see their own preferences: %#v", ownerView.RequestPreferences)
	}
	third := addThirdMember(t, fixture)
	thirdSocket := dialTestSocket(t, ctx, fixture.server.URL)
	defer thirdSocket.Close(websocket.StatusNormalClosure, "done")
	authenticateTestSocket(t, ctx, thirdSocket, third.AccessToken, "third-device")
	joinTestTable(t, ctx, thirdSocket, fixture.created.RoomID)
	writeTestEnvelope(t, ctx, thirdSocket, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableSeatChangeRequest), RequestID: "swap-3",
		TableID: fixture.created.RoomID,
		Payload: json.RawMessage(`{"targetSeat":` + itoa(guestSeat) + `}`),
	})
	if code := errorCodeOf(t, readUntilType(t, ctx, thirdSocket, protocol.TypeSystemError)); code != "seat_swap_requests_disabled" {
		t.Fatalf("error code=%s", code)
	}
}

func TestSeatSwapRespondRejectsUnknownScope(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fixture := newOwnerFixture(t)
	guestSocket := dialTestSocket(t, ctx, fixture.server.URL)
	defer guestSocket.Close(websocket.StatusNormalClosure, "done")
	authenticateTestSocket(t, ctx, guestSocket, fixture.guest.AccessToken, "guest-device")
	joinTestTable(t, ctx, guestSocket, fixture.created.RoomID)
	writeTestEnvelope(t, ctx, guestSocket, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableSeatSwapRespond), RequestID: "respond-bad",
		TableID: fixture.created.RoomID,
		Payload: json.RawMessage(`{"pendingRequestId":"nothing","accept":false,"scope":"everyone"}`),
	})
	if code := errorCodeOf(t, readUntilType(t, ctx, guestSocket, protocol.TypeSystemError)); code != "invalid_decline_scope" {
		t.Fatalf("error code=%s", code)
	}
	// 偏好两个字段必填：漏传一个不能被当成「关掉」
	writeTestEnvelope(t, ctx, guestSocket, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableRequestPreferencesSet), RequestID: "prefs-partial",
		TableID: fixture.created.RoomID,
		Payload: json.RawMessage(`{"allowSeatSwapRequests":false}`),
	})
	if code := errorCodeOf(t, readUntilType(t, ctx, guestSocket, protocol.TypeSystemError)); code != "invalid_request" {
		t.Fatalf("partial preferences error code=%s", code)
	}
	snapshot, err := fixture.tables.Snapshot(ctx, fixture.guest.User.UserID, fixture.created.RoomID)
	if err != nil || snapshot.RequestPreferences != tablemanager.DefaultRequestPreferences() {
		t.Fatalf("partial payload must not change preferences: %#v err=%v", snapshot.RequestPreferences, err)
	}
}

func itoa(value int) string {
	return json.Number(jsonInt(value)).String()
}

func jsonInt(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// 看牌申请走完整的 WebSocket 链路：被拒（本手不再接受任何人）时只有申请者收到
// kind 为 hole_card_view 的 table.request.declined，再申请被 hole_card_view_blocked_this_hand
// 当场拒绝；另一位同意后再申请则是 hole_card_view_already_granted。
func TestDeclinedHoleCardViewOverWebSocket(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newOwnerFixture(t)
	third := addThirdMember(t, fixture)

	names := map[string]string{
		fixture.owner.User.UserID: "房主", fixture.guest.User.UserID: "好友", third.User.UserID: "第三人",
	}
	sockets := map[string]*websocket.Conn{}
	for _, user := range []account.AuthResult{fixture.owner, fixture.guest, third} {
		socket := dialTestSocket(t, ctx, fixture.server.URL)
		defer socket.Close(websocket.StatusNormalClosure, "done")
		authenticateTestSocket(t, ctx, socket, user.AccessToken, user.User.UserID+"-device")
		joinTestTable(t, ctx, socket, fixture.created.RoomID)
		sockets[user.User.UserID] = socket
	}
	for userID, socket := range sockets {
		writeTestEnvelope(t, ctx, socket, protocol.Envelope{
			Version: 1, Type: string(protocol.TypeTableReadySet), RequestID: "ready-" + userID,
			TableID: fixture.created.RoomID, Payload: json.RawMessage(`{"ready":true}`),
		})
		readUntilType(t, ctx, socket, protocol.TypeTableReadySet)
	}
	started := readSnapshotInPhase(t, ctx, sockets[fixture.owner.User.UserID], "PREFLOP")
	if started.CurrentAction == nil {
		t.Fatal("no current action after the hand started")
	}
	folder := started.CurrentAction.UserID
	if _, _, err := fixture.tables.SubmitAction(ctx, folder, fixture.created.RoomID, holdem.ActionRequest{
		ActionID: "fold-requester", HandID: started.HandID,
		TableRevision: started.TableRevision, Action: holdem.ActionFold,
	}); err != nil {
		t.Fatal(err)
	}
	var targets []string
	for userID := range sockets {
		if userID != folder {
			targets = append(targets, userID)
		}
	}
	sort.Strings(targets)
	requester := sockets[folder]

	requestView := func(requestID, target string) {
		writeTestEnvelope(t, ctx, requester, protocol.Envelope{
			Version: 1, Type: string(protocol.TypeTableHoleCardsViewRequest), RequestID: requestID,
			TableID: fixture.created.RoomID,
			Payload: json.RawMessage(`{"targetUserId":"` + target + `"}`),
		})
	}
	pendingRequestFor := func(target string) string {
		for {
			var snapshot tablemanager.Snapshot
			if err := json.Unmarshal(readUntilType(t, ctx, sockets[target], protocol.TypeTableSnapshot).Payload, &snapshot); err != nil {
				t.Fatal(err)
			}
			if len(snapshot.HoleCardRequests) == 1 {
				return snapshot.HoleCardRequests[0].RequestID
			}
		}
	}
	respond := func(target, pendingID, body string) {
		writeTestEnvelope(t, ctx, sockets[target], protocol.Envelope{
			Version: 1, Type: string(protocol.TypeTableHoleCardsViewRespond), RequestID: "respond-" + pendingID,
			TableID: fixture.created.RoomID,
			Payload: json.RawMessage(`{"pendingRequestId":"` + pendingID + `",` + body + `}`),
		})
		readUntilType(t, ctx, sockets[target], protocol.TypeTableHoleCardsViewRespond)
	}

	// 第一位目标：本手不再接受任何人
	requestView("view-1", targets[0])
	readUntilType(t, ctx, requester, protocol.TypeTableHoleCardsViewRequest)
	pendingID := pendingRequestFor(targets[0])
	respond(targets[0], pendingID, `"accept":false,"scope":"everyone"`)
	declinedMessage := readUntilType(t, ctx, requester, protocol.TypeTableRequestDeclined)
	var declined protocol.RequestDeclinedPayload
	if err := json.Unmarshal(declinedMessage.Payload, &declined); err != nil {
		t.Fatal(err)
	}
	if declined.Kind != "hole_card_view" || declined.RequestID != pendingID || declined.TargetUserID != targets[0] ||
		declined.TargetDisplayName != names[targets[0]] || declined.Scope != tablemanager.DeclineEveryone ||
		declinedMessage.Sequence != 0 {
		t.Fatalf("declined=%#v sequence=%d", declined, declinedMessage.Sequence)
	}
	requestView("view-2", targets[0])
	if code := errorCodeOf(t, readUntilType(t, ctx, requester, protocol.TypeSystemError)); code != "hole_card_view_blocked_this_hand" {
		t.Fatalf("error code=%s", code)
	}

	// 第二位目标：同意后本手不能再申请
	requestView("view-3", targets[1])
	readUntilType(t, ctx, requester, protocol.TypeTableHoleCardsViewRequest)
	pendingID = pendingRequestFor(targets[1])
	respond(targets[1], pendingID, `"accept":true`)
	var revealed tablemanager.Snapshot
	for len(revealed.PrivateReveals) == 0 {
		if err := json.Unmarshal(readUntilType(t, ctx, requester, protocol.TypeTableSnapshot).Payload, &revealed); err != nil {
			t.Fatal(err)
		}
	}
	if revealed.PrivateReveals[0].PlayerID != targets[1] || len(revealed.PrivateReveals[0].HoleCards) != 2 {
		t.Fatalf("private reveals=%#v", revealed.PrivateReveals)
	}
	// 私下看牌只给申请者：第一位目标的快照里不能出现第二位目标的牌
	otherSnapshot, err := fixture.tables.Snapshot(ctx, targets[0], fixture.created.RoomID)
	if err != nil || len(otherSnapshot.PrivateReveals) != 0 {
		t.Fatalf("private view leaked to a bystander: %#v err=%v", otherSnapshot.PrivateReveals, err)
	}
	requestView("view-4", targets[1])
	if code := errorCodeOf(t, readUntilType(t, ctx, requester, protocol.TypeSystemError)); code != "hole_card_view_already_granted" {
		t.Fatalf("error code=%s", code)
	}
}
