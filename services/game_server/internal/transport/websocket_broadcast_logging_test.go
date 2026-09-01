package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"texas/services/game_server/internal/game/tablemanager"
	"texas/services/game_server/internal/protocol"
	"texas/services/game_server/internal/room"
)

// safeBuffer collects log output written from the hub while other goroutines
// may still be active.
type safeBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (writer *safeBuffer) Write(payload []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.buffer.Write(payload)
}

func (writer *safeBuffer) String() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.buffer.String()
}

func decodeLogRecords(t *testing.T, raw string) []map[string]any {
	t.Helper()
	records := make([]map[string]any, 0)
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line is not JSON: %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

// closedServerConnection returns a real server-side WebSocket connection that
// has already been closed, so every write on it fails deterministically.
func closedServerConnection(t *testing.T) *websocket.Conn {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			return
		}
		accepted <- connection
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clientConnection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	var serverConnection *websocket.Conn
	select {
	case serverConnection = <-accepted:
	case <-ctx.Done():
		t.Fatal("server did not accept the connection")
	}
	clientConnection.CloseNow()
	serverConnection.CloseNow()
	return serverConnection
}

// newLoggingHub returns a hub writing JSON logs into output, plus a real table
// manager and a room ID. The registered client is deliberately NOT a member of
// that room, so snapshot generation fails the same way a real fault would.
func newLoggingHub(t *testing.T, output *safeBuffer) (*tableHub, *tablemanager.Manager, string) {
	t.Helper()
	accounts, rooms := testApplicationServices(t)
	owner, err := accounts.Register(context.Background(), "log_owner", "房主", "password-123")
	if err != nil {
		t.Fatalf("register owner: %v", err)
	}
	created, err := rooms.Create(context.Background(), participant(owner), room.PresetStandard, 2, "")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	tables, err := tablemanager.New(rooms, transportZeroRandom{})
	if err != nil {
		t.Fatalf("table manager: %v", err)
	}
	hub := &tableHub{
		logger:  slog.New(slog.NewJSONHandler(output, nil)),
		clients: make(map[string]map[*webSocketClient]struct{}),
		buffers: make(map[string]*protocol.EventBuffer),
		voice:   make(map[string]map[string]protocol.VoiceMemberState),
	}
	client := &webSocketClient{roomID: created.RoomID, connection: closedServerConnection(t)}
	client.user.UserID = "not_a_member"
	hub.register(client)
	return hub, tables, created.RoomID
}

// A snapshot broadcast that reaches nobody freezes the entire table with no
// other visible symptom. It must always produce an operator-facing log entry
// carrying the room and sequence, never fail silently.
func TestSnapshotBroadcastFailureIsLoggedWithTableContext(t *testing.T) {
	output := &safeBuffer{}
	hub, tables, roomID := newLoggingHub(t, output)

	if err := hub.broadcastSnapshots(t.Context(), tables, roomID, nil); err != nil {
		t.Fatalf("broadcastSnapshots returned error: %v", err)
	}

	records := decodeLogRecords(t, output.String())
	if len(records) == 0 {
		t.Fatal("a broadcast that reached no client produced no log output")
	}

	var sawTableWide bool
	for _, record := range records {
		if record["level"] != "ERROR" {
			continue
		}
		if record["msg"] == "table snapshot broadcast reached no client" {
			sawTableWide = true
			if record["room_id"] != roomID {
				t.Errorf("table-wide failure log missing room_id: %#v", record)
			}
			if _, ok := record["sequence"]; !ok {
				t.Errorf("table-wide failure log missing sequence: %#v", record)
			}
			if _, ok := record["clients"]; !ok {
				t.Errorf("table-wide failure log missing client count: %#v", record)
			}
		}
	}
	if !sawTableWide {
		t.Errorf("no table-wide failure log recorded; got %#v", records)
	}
}

// Per-client delivery failures on shared events must also be attributable to a
// room, user and message type.
func TestSharedEventDeliveryFailureIsLogged(t *testing.T) {
	output := &safeBuffer{}
	hub, _, roomID := newLoggingHub(t, output)

	if err := hub.broadcast(roomID, protocol.TypeTableChatMessage, map[string]string{"a": "b"}); err != nil {
		t.Fatalf("broadcast returned error: %v", err)
	}

	var found bool
	for _, record := range decodeLogRecords(t, output.String()) {
		if record["msg"] == "table event delivery failed" {
			found = true
			if record["room_id"] != roomID || record["user_id"] != "not_a_member" {
				t.Errorf("delivery failure log missing identifiers: %#v", record)
			}
			if record["type"] != string(protocol.TypeTableChatMessage) {
				t.Errorf("delivery failure log missing message type: %#v", record)
			}
		}
	}
	if !found {
		t.Errorf("no delivery failure log recorded; got %q", output.String())
	}
}

// Logs must never carry hole cards, tokens or other hidden state.
func TestBroadcastFailureLogsCarryNoSecrets(t *testing.T) {
	output := &safeBuffer{}
	hub, tables, roomID := newLoggingHub(t, output)
	_ = hub.broadcastSnapshots(t.Context(), tables, roomID, nil)

	logged := output.String()
	for _, forbidden := range []string{"holeCards", "accessToken", "password", "userSig"} {
		if strings.Contains(logged, forbidden) {
			t.Errorf("log output leaked %q: %s", forbidden, logged)
		}
	}
}
