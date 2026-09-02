package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/game/tablemanager"
	"texas/services/game_server/internal/protocol"
	"texas/services/game_server/internal/room"
)

type auditResponse struct {
	Events []account.AuditEvent         `json:"events"`
	Users  map[string]auditUserResponse `json:"users"`
}

func fetchAudit(t *testing.T, serverURL, token, query string) (auditResponse, int) {
	t.Helper()
	response := doJSONRequest(t, http.MethodGet, serverURL+"/v1/admin/audit"+query, token, nil)
	defer response.Body.Close()
	var payload auditResponse
	if response.StatusCode == http.StatusOK {
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatalf("decode audit: %v", err)
		}
	}
	return payload, response.StatusCode
}

func hasEvent(events []account.AuditEvent, eventType string, predicate func(account.AuditEvent) bool) bool {
	for _, event := range events {
		if event.EventType == eventType && (predicate == nil || predicate(event)) {
			return true
		}
	}
	return false
}

func TestAdminAuditListsEventsWithUserNamesAndFilters(t *testing.T) {
	ctx := context.Background()
	accounts, rooms := testApplicationServices(t)
	server := httptest.NewServer(NewHandler(testLogger(), Options{Accounts: accounts, Rooms: rooms}))
	defer server.Close()

	administrator, err := accounts.RegisterWithOptions(ctx, "audit_admin", "管理员", "password-123",
		account.RegistrationOptions{RequestInitialAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	player := registerHTTPUser(t, server.URL, "audited_player", "被审计的人")
	other := registerHTTPUser(t, server.URL, "other_player", "路人")
	if _, err := accounts.UpdateOwnDisplayName(ctx, player.User, "改名后的人"); err != nil {
		t.Fatal(err)
	}
	if err := accounts.UpdateManagedStatuses(ctx, administrator.User, []string{other.User.UserID}, account.StatusSuspended); err != nil {
		t.Fatal(err)
	}

	if _, status := fetchAudit(t, server.URL, player.AccessToken, ""); status != http.StatusForbidden {
		t.Fatalf("player audit status=%d", status)
	}

	all, status := fetchAudit(t, server.URL, administrator.AccessToken, "")
	if status != http.StatusOK {
		t.Fatalf("admin audit status=%d", status)
	}
	if !hasEvent(all.Events, "admin.bootstrap", nil) ||
		!hasEvent(all.Events, "user.display_name_changed", func(event account.AuditEvent) bool {
			return event.ActorUserID == player.User.UserID
		}) ||
		!hasEvent(all.Events, "admin.user_status_changed", nil) {
		t.Fatalf("audit missing expected events: %#v", all.Events)
	}
	if all.Events[0].CreatedAt.Before(all.Events[len(all.Events)-1].CreatedAt) {
		t.Fatal("audit events must be newest first")
	}
	if user, ok := all.Users[player.User.UserID]; !ok || user.Username != "audited_player" {
		t.Fatalf("users map should resolve the actor: %#v", all.Users)
	}
	if user, ok := all.Users[other.User.UserID]; !ok || user.Username != "other_player" {
		t.Fatalf("users map should resolve targetUserIds: %#v", all.Users)
	}

	// 按用户筛选：作为对象（targetUserIds）出现的事件也要命中
	filtered, _ := fetchAudit(t, server.URL, administrator.AccessToken, "?userId="+other.User.UserID)
	if len(filtered.Events) == 0 || !hasEvent(filtered.Events, "admin.user_status_changed", nil) {
		t.Fatalf("filtered audit=%#v", filtered.Events)
	}
	for _, event := range filtered.Events {
		if event.EventType == "user.display_name_changed" {
			t.Fatal("filter leaked events unrelated to the user")
		}
	}
	limited, _ := fetchAudit(t, server.URL, administrator.AccessToken, "?limit=1")
	if len(limited.Events) != 1 {
		t.Fatalf("limit=1 returned %d events", len(limited.Events))
	}
}

func TestVoiceJoinAndLeaveArePersistedAsRoomEvents(t *testing.T) {
	accounts, rooms := testApplicationServices(t)
	administrator, err := accounts.RegisterWithOptions(context.Background(), "voice_admin", "管理员", "password-123",
		account.RegistrationOptions{RequestInitialAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := accounts.Register(context.Background(), "voice_owner", "房主", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	created, err := rooms.Create(context.Background(), participant(owner), room.PresetStandard, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	tables, err := tablemanager.New(rooms, transportZeroRandom{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(testLogger(), Options{Accounts: accounts, Rooms: rooms, Tables: tables}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connection := dialTestSocket(t, ctx, server.URL)
	defer connection.CloseNow()
	authenticateTestSocket(t, ctx, connection, owner.AccessToken, "owner-device")
	joinTestTable(t, ctx, connection, created.RoomID)

	send := func(requestID, payload string) {
		writeTestEnvelope(t, ctx, connection, protocol.Envelope{
			Version: 1, Type: string(protocol.TypeTableVoiceStateSet), RequestID: requestID,
			TableID: created.RoomID, Payload: json.RawMessage(payload),
		})
		readUntilType(t, ctx, connection, protocol.TypeTableVoiceStateSet)
	}
	send("voice-1", `{"joined":true,"microphoneEnabled":true}`)
	send("voice-2", `{"joined":true,"microphoneEnabled":false}`) // 仅切麦：不产生事件
	send("voice-3", `{"joined":false,"microphoneEnabled":false}`)
	send("voice-4", `{"joined":true,"microphoneEnabled":false}`)
	// 断开连接时服务端应记录一次「退出语音（连接断开）」
	connection.CloseNow()

	var events []account.AuditEvent
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		events, err = accounts.ListAudit(context.Background(), administrator.User, account.AuditQuery{UserID: owner.User.UserID})
		if err != nil {
			t.Fatal(err)
		}
		if hasEvent(events, "voice.left", func(event account.AuditEvent) bool { return event.Metadata["reason"] == "disconnected" }) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var joined, left int
	for _, event := range events {
		if event.RoomID != created.RoomID && (event.EventType == "voice.joined" || event.EventType == "voice.left") {
			t.Fatalf("voice event without room id: %#v", event)
		}
		switch event.EventType {
		case "voice.joined":
			joined++
		case "voice.left":
			left++
		}
	}
	if joined != 2 || left != 2 {
		t.Fatalf("joined=%d left=%d events=%#v", joined, left, events)
	}
	if !hasEvent(events, "voice.joined", func(event account.AuditEvent) bool { return event.Metadata["microphoneEnabled"] == true }) ||
		!hasEvent(events, "voice.left", func(event account.AuditEvent) bool { return event.Metadata["reason"] == "self" }) {
		t.Fatalf("voice metadata incomplete: %#v", events)
	}
}
