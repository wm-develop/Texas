package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/chat"
	"texas/services/game_server/internal/game/tablemanager"
	"texas/services/game_server/internal/protocol"
	"texas/services/game_server/internal/room"
)

type transportZeroRandom struct{}

func (transportZeroRandom) Intn(int) (int, error) { return 0, nil }

func TestWebSocketFriendTableFlowAndHoleCardPrivacy(t *testing.T) {
	accounts, rooms := testApplicationServices(t)
	owner, err := accounts.Register(context.Background(), "ws_owner", "房主", "password-123")
	if err != nil {
		t.Fatalf("register owner: %v", err)
	}
	guest, err := accounts.Register(context.Background(), "ws_guest", "好友", "password-123")
	if err != nil {
		t.Fatalf("register guest: %v", err)
	}
	created, err := rooms.Create(context.Background(), participant(owner), room.PresetStandard, 2, "")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, err := rooms.Join(context.Background(), participant(guest), created.Code, ""); err != nil {
		t.Fatalf("join room: %v", err)
	}
	tables, err := tablemanager.New(rooms, transportZeroRandom{})
	if err != nil {
		t.Fatalf("table manager: %v", err)
	}
	nextMessageID := 0
	chatService, err := chat.NewService(chat.Policy{
		MaximumRunes: 200, MaximumPerWindow: 5, RateWindow: time.Second, HistoryLimit: 50,
	}, time.Now, func() string {
		nextMessageID++
		return fmt.Sprintf("message_%d", nextMessageID)
	})
	if err != nil {
		t.Fatalf("chat service: %v", err)
	}
	server := httptest.NewServer(NewHandler(testLogger(), Options{
		Accounts: accounts, Rooms: rooms, Tables: tables, Chat: chatService,
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ownerConnection := dialTestSocket(t, ctx, server.URL)
	defer ownerConnection.CloseNow()
	guestConnection := dialTestSocket(t, ctx, server.URL)
	defer guestConnection.CloseNow()
	authenticateTestSocket(t, ctx, ownerConnection, owner.AccessToken, "owner-device")
	authenticateTestSocket(t, ctx, guestConnection, guest.AccessToken, "guest-device")
	joinTestTable(t, ctx, ownerConnection, created.RoomID)
	joinTestTable(t, ctx, guestConnection, created.RoomID)

	writeTestEnvelope(t, ctx, ownerConnection, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableVoiceStateSet), RequestID: "owner-voice",
		TableID: created.RoomID, Payload: json.RawMessage(`{"joined":true,"microphoneEnabled":false}`),
	})
	readUntilType(t, ctx, ownerConnection, protocol.TypeTableVoiceStateSet)
	ownerVoice := readUntilType(t, ctx, ownerConnection, protocol.TypeTableVoiceState)
	guestVoice := readUntilType(t, ctx, guestConnection, protocol.TypeTableVoiceState)
	for _, message := range []protocol.Envelope{ownerVoice, guestVoice} {
		var state protocol.VoiceStatePayload
		if err := json.Unmarshal(message.Payload, &state); err != nil || len(state.Members) != 1 ||
			state.Members[0].UserID != owner.User.UserID || state.Members[0].MicrophoneEnabled {
			t.Fatalf("voice state=%#v error=%v", state, err)
		}
	}

	writeTestEnvelope(t, ctx, ownerConnection, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableReadySet), RequestID: "owner-ready",
		Payload: json.RawMessage(`{"ready":true}`),
	})
	readUntilType(t, ctx, ownerConnection, protocol.TypeTableReadySet)
	writeTestEnvelope(t, ctx, guestConnection, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableReadySet), RequestID: "guest-ready",
		Payload: json.RawMessage(`{"ready":true}`),
	})
	readUntilType(t, ctx, guestConnection, protocol.TypeTableReadySet)

	ownerSnapshot := readSnapshotInPhase(t, ctx, ownerConnection, "PREFLOP")
	guestSnapshot := readSnapshotInPhase(t, ctx, guestConnection, "PREFLOP")
	if len(ownerSnapshot.HoleCards) != 2 || len(guestSnapshot.HoleCards) != 2 {
		t.Fatalf("owner holes=%v guest holes=%v", ownerSnapshot.HoleCards, guestSnapshot.HoleCards)
	}
	ownerJSON, _ := json.Marshal(ownerSnapshot)
	for _, guestCard := range guestSnapshot.HoleCards {
		if strings.Contains(string(ownerJSON), `"`+guestCard+`"`) {
			t.Fatalf("owner snapshot leaked guest card %s: %s", guestCard, ownerJSON)
		}
	}

	actorConnection := ownerConnection
	actorSnapshot := ownerSnapshot
	if ownerSnapshot.CurrentAction == nil {
		t.Fatal("missing current action")
	}
	if ownerSnapshot.CurrentAction.UserID == guest.User.UserID {
		actorConnection = guestConnection
		actorSnapshot = guestSnapshot
	}
	originalDeadline := actorSnapshot.CurrentAction.Deadline
	writeTestEnvelope(t, ctx, actorConnection, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableTimeExtensionUse), RequestID: "extend-action",
		TableID: created.RoomID, Payload: json.RawMessage(`{}`),
	})
	readUntilType(t, ctx, actorConnection, protocol.TypeTableTimeExtensionAccepted)
	ownerSnapshot = readSnapshotInPhase(t, ctx, ownerConnection, "PREFLOP")
	guestSnapshot = readSnapshotInPhase(t, ctx, guestConnection, "PREFLOP")
	actorSnapshot = ownerSnapshot
	if ownerSnapshot.CurrentAction != nil && ownerSnapshot.CurrentAction.UserID == guest.User.UserID {
		actorSnapshot = guestSnapshot
	}
	if actorSnapshot.CurrentAction == nil {
		t.Fatal("time extension removed current action")
	}
	actorExtensions := -1
	for _, seat := range actorSnapshot.Seats {
		if seat.UserID == actorSnapshot.CurrentAction.UserID {
			actorExtensions = seat.TimeExtensions
		}
	}
	if actorSnapshot.CurrentAction.Deadline <= originalDeadline || actorExtensions != 1 {
		t.Fatalf("time extension snapshot=%#v actorExtensions=%d", actorSnapshot.CurrentAction, actorExtensions)
	}
	// Retrying the same request returns its first response without consuming a
	// second extension card.
	writeTestEnvelope(t, ctx, actorConnection, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableTimeExtensionUse), RequestID: "extend-action",
		TableID: created.RoomID, Payload: json.RawMessage(`{}`),
	})
	readUntilType(t, ctx, actorConnection, protocol.TypeTableTimeExtensionAccepted)
	writeTestEnvelope(t, ctx, actorConnection, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableSnapshotRequest), RequestID: "verify-extension",
		TableID: created.RoomID, Payload: json.RawMessage(`{"reason":"test"}`),
	})
	extendedSnapshot := readSnapshotInPhase(t, ctx, actorConnection, "PREFLOP")
	for _, seat := range extendedSnapshot.Seats {
		if seat.UserID == actorSnapshot.CurrentAction.UserID && seat.TimeExtensions != 1 {
			t.Fatalf("duplicate request consumed another extension: %#v", seat)
		}
	}
	writeTestEnvelope(t, ctx, actorConnection, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableActionSubmit), RequestID: "fold-action",
		TableID: created.RoomID, HandID: actorSnapshot.HandID, TableRevision: actorSnapshot.TableRevision,
		Payload: json.RawMessage(`{"actionId":"fold-1","action":"fold"}`),
	})
	ownerSettled := readSnapshotInPhase(t, ctx, ownerConnection, "WAITING_NEXT_HAND")
	guestSettled := readSnapshotInPhase(t, ctx, guestConnection, "WAITING_NEXT_HAND")
	if ownerSettled.Settlement == nil || guestSettled.Settlement == nil {
		t.Fatal("settlement missing from personalized snapshots")
	}

	writeTestEnvelope(t, ctx, ownerConnection, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableChatSend), RequestID: "chat-1",
		Payload: json.RawMessage(`{"clientMessageId":"local-1","kind":"text","content":"好牌！"}`),
	})
	readUntilType(t, ctx, ownerConnection, protocol.TypeTableChatAccepted)
	ownerChat := readUntilType(t, ctx, ownerConnection, protocol.TypeTableChatMessage)
	guestChat := readUntilType(t, ctx, guestConnection, protocol.TypeTableChatMessage)
	var ownerPayload protocol.ChatMessagePayload
	var guestPayload protocol.ChatMessagePayload
	if json.Unmarshal(ownerChat.Payload, &ownerPayload) != nil || json.Unmarshal(guestChat.Payload, &guestPayload) != nil {
		t.Fatal("decode broadcast chat")
	}
	if ownerPayload.Content != "好牌！" || guestPayload.MessageID != ownerPayload.MessageID {
		t.Fatalf("owner chat=%#v guest chat=%#v", ownerPayload, guestPayload)
	}

	writeTestEnvelope(t, ctx, ownerConnection, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableChatSend), RequestID: "chat-2",
		Payload: json.RawMessage(`{"clientMessageId":"local-2","kind":"text","content":"再来一手"}`),
	})
	readUntilType(t, ctx, ownerConnection, protocol.TypeTableChatAccepted)
	readUntilType(t, ctx, ownerConnection, protocol.TypeTableChatMessage)
	secondGuestChat := readUntilType(t, ctx, guestConnection, protocol.TypeTableChatMessage)
	if secondGuestChat.Sequence != guestChat.Sequence+1 {
		t.Fatalf("chat sequence=%d want=%d", secondGuestChat.Sequence, guestChat.Sequence+1)
	}

	replayPayload, _ := json.Marshal(protocol.SnapshotRequestPayload{
		LastSequence: guestChat.Sequence,
		Reason:       "integration_test_gap",
	})
	writeTestEnvelope(t, ctx, guestConnection, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableSnapshotRequest), RequestID: "replay-chat",
		TableID: created.RoomID, Payload: replayPayload,
	})
	replayedChat := readUntilType(t, ctx, guestConnection, protocol.TypeTableChatMessage)
	if replayedChat.Sequence != secondGuestChat.Sequence {
		t.Fatalf("replayed sequence=%d want=%d", replayedChat.Sequence, secondGuestChat.Sequence)
	}
	replayCompleted := readUntilType(t, ctx, guestConnection, protocol.TypeTableReplayCompleted)
	var replayResult protocol.ReplayCompletedPayload
	if err := json.Unmarshal(replayCompleted.Payload, &replayResult); err != nil {
		t.Fatalf("decode replay result: %v", err)
	}
	if replayResult.Replayed != 1 || replayResult.LastSequence != secondGuestChat.Sequence {
		t.Fatalf("replay result=%#v", replayResult)
	}
}

func TestWebSocketReconnectRestoresCurrentHandAndPrivateCards(t *testing.T) {
	accounts, rooms := testApplicationServices(t)
	owner, err := accounts.Register(context.Background(), "reconnect_owner", "房主", "password-123")
	if err != nil {
		t.Fatalf("register owner: %v", err)
	}
	guest, err := accounts.Register(context.Background(), "reconnect_guest", "好友", "password-123")
	if err != nil {
		t.Fatalf("register guest: %v", err)
	}
	created, err := rooms.Create(context.Background(), participant(owner), room.PresetStandard, 2, "")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, err := rooms.Join(context.Background(), participant(guest), created.Code, ""); err != nil {
		t.Fatalf("join room: %v", err)
	}
	tables, err := tablemanager.New(rooms, transportZeroRandom{})
	if err != nil {
		t.Fatalf("table manager: %v", err)
	}
	server := httptest.NewServer(NewHandler(testLogger(), Options{
		Accounts: accounts, Rooms: rooms, Tables: tables,
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ownerConnection := dialTestSocket(t, ctx, server.URL)
	defer ownerConnection.CloseNow()
	guestConnection := dialTestSocket(t, ctx, server.URL)
	authenticateTestSocket(t, ctx, ownerConnection, owner.AccessToken, "owner-device")
	authenticateTestSocket(t, ctx, guestConnection, guest.AccessToken, "guest-device")
	joinTestTable(t, ctx, ownerConnection, created.RoomID)
	joinTestTable(t, ctx, guestConnection, created.RoomID)

	writeTestEnvelope(t, ctx, ownerConnection, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableReadySet), RequestID: "owner-ready",
		Payload: json.RawMessage(`{"ready":true}`),
	})
	readUntilType(t, ctx, ownerConnection, protocol.TypeTableReadySet)
	writeTestEnvelope(t, ctx, guestConnection, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableReadySet), RequestID: "guest-ready",
		Payload: json.RawMessage(`{"ready":true}`),
	})
	readUntilType(t, ctx, guestConnection, protocol.TypeTableReadySet)

	readSnapshotInPhase(t, ctx, ownerConnection, "PREFLOP")
	guestBeforeMessage := readUntilSnapshotInPhase(t, ctx, guestConnection, "PREFLOP")
	var guestBefore tablemanager.Snapshot
	if err := json.Unmarshal(guestBeforeMessage.Payload, &guestBefore); err != nil {
		t.Fatalf("decode guest before: %v", err)
	}
	if len(guestBefore.HoleCards) != 2 {
		t.Fatalf("guest before cards=%v", guestBefore.HoleCards)
	}
	guestConnection.CloseNow()
	ownerDisconnected := readSnapshotInPhase(t, ctx, ownerConnection, "PREFLOP")
	for _, seat := range ownerDisconnected.Seats {
		if seat.UserID == guest.User.UserID && seat.Connected {
			t.Fatalf("guest remained connected: %#v", seat)
		}
	}

	reconnected := dialTestSocket(t, ctx, server.URL)
	defer reconnected.CloseNow()
	authenticateTestSocket(t, ctx, reconnected, guest.AccessToken, "guest-device-2")
	joinPayload, _ := json.Marshal(protocol.TableJoinPayload{LastSequence: guestBeforeMessage.Sequence})
	writeTestEnvelope(t, ctx, reconnected, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableJoin), RequestID: "rejoin",
		TableID: created.RoomID, Payload: joinPayload,
	})
	readUntilType(t, ctx, reconnected, protocol.TypeTableJoined)
	guestAfter := readSnapshotInPhase(t, ctx, reconnected, "PREFLOP")
	if guestAfter.HandID != guestBefore.HandID ||
		!reflect.DeepEqual(guestAfter.HoleCards, guestBefore.HoleCards) {
		t.Fatalf("before=%#v after=%#v", guestBefore, guestAfter)
	}
	for _, seat := range guestAfter.Seats {
		if seat.UserID == guest.User.UserID && !seat.Connected {
			t.Fatalf("guest was not restored as connected: %#v", seat)
		}
	}
}

func participant(result account.AuthResult) room.Participant {
	return room.Participant{UserID: result.User.UserID, DisplayName: result.User.DisplayName}
}

func dialTestSocket(t *testing.T, ctx context.Context, serverURL string) *websocket.Conn {
	t.Helper()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(serverURL, "http")+"/ws", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	return connection
}

func authenticateTestSocket(
	t *testing.T,
	ctx context.Context,
	connection *websocket.Conn,
	token string,
	deviceID string,
) {
	t.Helper()
	payload, _ := json.Marshal(protocol.SessionAuthenticatePayload{AccessToken: token, DeviceID: deviceID})
	writeTestEnvelope(t, ctx, connection, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeSessionAuthenticate), RequestID: "authenticate", Payload: payload,
	})
	readUntilType(t, ctx, connection, protocol.TypeSessionAuthenticated)
}

func joinTestTable(t *testing.T, ctx context.Context, connection *websocket.Conn, roomID string) {
	t.Helper()
	writeTestEnvelope(t, ctx, connection, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableJoin), RequestID: "join", TableID: roomID,
		Payload: json.RawMessage(`{}`),
	})
	readUntilType(t, ctx, connection, protocol.TypeTableJoined)
}

func writeTestEnvelope(t *testing.T, ctx context.Context, connection *websocket.Conn, message protocol.Envelope) {
	t.Helper()
	if err := wsjson.Write(ctx, connection, message); err != nil {
		t.Fatalf("write %s: %v", message.Type, err)
	}
}

func readUntilType(
	t *testing.T,
	ctx context.Context,
	connection *websocket.Conn,
	wanted protocol.MessageType,
) protocol.Envelope {
	t.Helper()
	for {
		var message protocol.Envelope
		if err := wsjson.Read(ctx, connection, &message); err != nil {
			t.Fatalf("read %s: %v", wanted, err)
		}
		if message.Type == string(wanted) {
			return message
		}
		if message.Type == string(protocol.TypeSystemError) || message.Type == string(protocol.TypeTableActionRejected) {
			t.Fatalf("unexpected error while waiting for %s: %s", wanted, message.Payload)
		}
	}
}

func readSnapshotInPhase(
	t *testing.T,
	ctx context.Context,
	connection *websocket.Conn,
	phase string,
) tablemanager.Snapshot {
	t.Helper()
	for {
		message := readUntilType(t, ctx, connection, protocol.TypeTableSnapshot)
		var snapshot tablemanager.Snapshot
		if err := json.Unmarshal(message.Payload, &snapshot); err != nil {
			t.Fatalf("decode snapshot: %v", err)
		}
		if string(snapshot.Phase) == phase {
			return snapshot
		}
	}
}

func readUntilSnapshotInPhase(
	t *testing.T,
	ctx context.Context,
	connection *websocket.Conn,
	phase string,
) protocol.Envelope {
	t.Helper()
	for {
		message := readUntilType(t, ctx, connection, protocol.TypeTableSnapshot)
		var snapshot tablemanager.Snapshot
		if err := json.Unmarshal(message.Payload, &snapshot); err != nil {
			t.Fatalf("decode snapshot: %v", err)
		}
		if string(snapshot.Phase) == phase {
			return message
		}
	}
}
