package transport

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"texas/services/game_server/internal/protocol"
	"texas/services/game_server/internal/trtc"
)

func TestHealth(t *testing.T) {
	server := httptest.NewServer(NewHandler(testLogger(), Options{}))
	defer server.Close()

	response, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}

func TestWebSocketPing(t *testing.T) {
	server := httptest.NewServer(NewHandler(testLogger(), Options{}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	websocketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	connection, _, err := websocket.Dial(ctx, websocketURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer connection.CloseNow()

	request := protocol.Envelope{
		Version:   1,
		Type:      "system.ping",
		RequestID: "test-request",
	}
	if err := wsjson.Write(ctx, connection, request); err != nil {
		t.Fatalf("write websocket message: %v", err)
	}

	var response protocol.Envelope
	if err := wsjson.Read(ctx, connection, &response); err != nil {
		t.Fatalf("read websocket message: %v", err)
	}
	if response.Type != "system.pong" {
		t.Fatalf("type = %q, want system.pong", response.Type)
	}
	if response.RequestID != request.RequestID {
		t.Fatalf("requestId = %q, want %q", response.RequestID, request.RequestID)
	}
}

func TestTRTCCredentials(t *testing.T) {
	server := httptest.NewServer(NewHandler(testLogger(), Options{
		TRTCIssuer: fakeTRTCIssuer{},
	}))
	defer server.Close()

	response, err := http.Post(
		server.URL+"/v1/trtc/credentials",
		"application/json",
		strings.NewReader(`{"userId":"user_1","roomId":"table_1"}`),
	)
	if err != nil {
		t.Fatalf("POST credentials: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}

func TestTRTCCredentialsRequiresAuthorizedUserAndTableForRemoteRequest(t *testing.T) {
	authorizer := &fakeTRTCAuthorizer{}
	handler := NewHandler(testLogger(), Options{
		TRTCIssuer:     fakeTRTCIssuer{},
		TRTCAuthorizer: authorizer,
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"http://game.example/v1/trtc/credentials",
		strings.NewReader(`{"userId":"user_1","roomId":"table_1"}`),
	)
	request.RemoteAddr = "203.0.113.10:4567"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access-token")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if authorizer.token != "access-token" || authorizer.userID != "user_1" || authorizer.tableID != "table_1" {
		t.Fatalf("authorization input: %#v", authorizer)
	}
}

func TestTRTCCredentialsRejectsUnauthorizedTable(t *testing.T) {
	handler := NewHandler(testLogger(), Options{
		TRTCIssuer:     fakeTRTCIssuer{},
		TRTCAuthorizer: &fakeTRTCAuthorizer{err: trtc.AccessError{Code: "permission_denied"}},
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"http://game.example/v1/trtc/credentials",
		strings.NewReader(`{"userId":"user_1","roomId":"table_2"}`),
	)
	request.RemoteAddr = "203.0.113.10:4567"
	request.Header.Set("Authorization", "Bearer access-token")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestTRTCCredentialsStillRequiresSessionThroughLoopbackForwarding(t *testing.T) {
	handler := NewHandler(testLogger(), Options{
		TRTCIssuer:     fakeTRTCIssuer{},
		TRTCAuthorizer: &fakeTRTCAuthorizer{err: trtc.AccessError{Code: "authentication_required"}},
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"http://127.0.0.1/v1/trtc/credentials",
		strings.NewReader(`{"userId":"user_1","roomId":"table_1"}`),
	)
	request.RemoteAddr = "127.0.0.1:4567"
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestLocalDevelopmentCORSPreflight(t *testing.T) {
	server := httptest.NewServer(NewHandler(testLogger(), Options{}))
	defer server.Close()

	request, err := http.NewRequest(http.MethodOptions, server.URL+"/v1/trtc/credentials", nil)
	if err != nil {
		t.Fatalf("create preflight request: %v", err)
	}
	request.Header.Set("Origin", "http://localhost:54321")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("OPTIONS credentials: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:54321" {
		t.Fatalf("allow origin = %q, want local development origin", got)
	}
}

func TestLocalDevelopmentCORSRejectsNonLocalOrigin(t *testing.T) {
	server := httptest.NewServer(NewHandler(testLogger(), Options{}))
	defer server.Close()

	request, err := http.NewRequest(http.MethodOptions, server.URL+"/v1/trtc/credentials", nil)
	if err != nil {
		t.Fatalf("create preflight request: %v", err)
	}
	request.Header.Set("Origin", "https://example.com")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("OPTIONS credentials: %v", err)
	}
	defer response.Body.Close()

	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("allow origin = %q, want empty", got)
	}
}

type fakeTRTCIssuer struct{}

func (fakeTRTCIssuer) Issue(userID string, roomID string) (trtc.Credentials, error) {
	return trtc.Credentials{
		SDKAppID: 1400000000,
		UserID:   userID,
		RoomID:   roomID,
		UserSig:  "test-user-sig",
		ExpireIn: 3600,
	}, nil
}

type fakeTRTCAuthorizer struct {
	token   string
	userID  string
	tableID string
	err     error
}

func (authorizer *fakeTRTCAuthorizer) AuthorizeVoice(
	_ context.Context,
	token string,
	userID string,
	tableID string,
) error {
	authorizer.token = token
	authorizer.userID = userID
	authorizer.tableID = tableID
	return authorizer.err
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
