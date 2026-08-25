package transport

import (
	"bytes"
	cryptorand "crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/room"
	"texas/services/game_server/internal/security"
)

func TestAccountAndFriendRoomHTTPFlow(t *testing.T) {
	accounts, rooms := testApplicationServices(t)
	server := httptest.NewServer(NewHandler(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		Options{Accounts: accounts, Rooms: rooms},
	))
	defer server.Close()

	owner := registerHTTPUser(t, server.URL, "owner_1", "房主")
	guest := registerHTTPUser(t, server.URL, "guest_1", "好友")

	unauthorized, err := http.Get(server.URL + "/v1/users/me")
	if err != nil {
		t.Fatalf("GET me: %v", err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.StatusCode)
	}

	createdResponse := doJSONRequest(t, http.MethodPost, server.URL+"/v1/rooms", owner.AccessToken, map[string]any{
		"preset": "standard", "maxPlayers": 2, "password": "room-pass",
	})
	defer createdResponse.Body.Close()
	if createdResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create room status=%d body=%s", createdResponse.StatusCode, readBody(createdResponse))
	}
	var created room.Room
	if err := json.NewDecoder(createdResponse.Body).Decode(&created); err != nil {
		t.Fatalf("decode room: %v", err)
	}
	if created.Code == "" || created.PasswordHash != "" || len(created.Members) != 1 {
		t.Fatalf("created room=%#v", created)
	}

	wrongPassword := doJSONRequest(t, http.MethodPost, server.URL+"/v1/rooms/join", guest.AccessToken, map[string]any{
		"code": created.Code, "password": "wrong-pass",
	})
	wrongPassword.Body.Close()
	if wrongPassword.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong password status=%d", wrongPassword.StatusCode)
	}

	joinedResponse := doJSONRequest(t, http.MethodPost, server.URL+"/v1/rooms/join", guest.AccessToken, map[string]any{
		"code": created.Code, "password": "room-pass",
	})
	defer joinedResponse.Body.Close()
	if joinedResponse.StatusCode != http.StatusOK {
		t.Fatalf("join room status=%d body=%s", joinedResponse.StatusCode, readBody(joinedResponse))
	}
	var joined room.Room
	if err := json.NewDecoder(joinedResponse.Body).Decode(&joined); err != nil {
		t.Fatalf("decode joined room: %v", err)
	}
	if len(joined.Members) != 2 {
		t.Fatalf("joined members=%#v", joined.Members)
	}

	readyResponse := doJSONRequest(t, http.MethodPost, server.URL+"/v1/rooms/ready", guest.AccessToken, map[string]any{
		"ready": true,
	})
	defer readyResponse.Body.Close()
	if readyResponse.StatusCode != http.StatusOK {
		t.Fatalf("ready status=%d body=%s", readyResponse.StatusCode, readBody(readyResponse))
	}
}

func TestRecentHandsRequiresAuthenticationAndReturnsPersonalHistory(t *testing.T) {
	accounts, rooms := testApplicationServices(t)
	hands := history.NewInMemoryStore()
	server := httptest.NewServer(NewHandler(testLogger(), Options{
		Accounts: accounts, Rooms: rooms, History: hands,
	}))
	defer server.Close()

	player := registerHTTPUser(t, server.URL, "history_user", "牌手")
	if err := hands.Append(history.Hand{
		HandID: "hand_recent", RoomID: "room_recent", RoomCode: "654321", EndedAt: time.Now(),
		Players: []history.PlayerResult{
			{UserID: player.User.UserID, DisplayName: "牌手", Seat: 1, StartingStack: 2000, EndingStack: 2040, Delta: 40, HoleCards: []string{"As", "Ah"}},
			{UserID: "friend", DisplayName: "好友", Seat: 2, StartingStack: 2000, EndingStack: 1960, Delta: -40, HoleCards: []string{"2c", "3d"}},
		},
	}); err != nil {
		t.Fatalf("append history: %v", err)
	}

	unauthorized := doJSONRequest(t, http.MethodGet, server.URL+"/v1/hands/recent", "", nil)
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.StatusCode)
	}
	response := doJSONRequest(t, http.MethodGet, server.URL+"/v1/hands/recent?limit=10", player.AccessToken, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("recent status=%d body=%s", response.StatusCode, readBody(response))
	}
	var payload struct {
		Hands []history.Hand `json:"hands"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode recent hands: %v", err)
	}
	if len(payload.Hands) != 1 || len(payload.Hands[0].Players[0].HoleCards) != 2 ||
		len(payload.Hands[0].Players[1].HoleCards) != 0 {
		t.Fatalf("recent payload=%#v", payload.Hands)
	}
}

func registerHTTPUser(t *testing.T, baseURL string, username string, displayName string) account.AuthResult {
	t.Helper()
	response := doJSONRequest(t, http.MethodPost, baseURL+"/v1/auth/register", "", map[string]any{
		"username": username, "displayName": displayName, "password": "password-123",
	})
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", response.StatusCode, readBody(response))
	}
	var result account.AuthResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	if result.AccessToken == "" || result.RefreshToken == "" || result.User.PasswordHash != "" {
		t.Fatalf("invalid registration result=%#v", result)
	}
	return result
}

func doJSONRequest(t *testing.T, method string, url string, token string, body any) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	request, err := http.NewRequest(method, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, url, err)
	}
	return response
}

func readBody(response *http.Response) string {
	data, _ := io.ReadAll(response.Body)
	return strings.TrimSpace(string(data))
}

func testApplicationServices(t *testing.T) (*account.Service, *room.Service) {
	t.Helper()
	hasher, err := security.NewPasswordHasher(1_000, cryptorand.Reader)
	if err != nil {
		t.Fatalf("NewPasswordHasher: %v", err)
	}
	accounts, err := account.NewService(account.NewMemoryRepository(), hasher, account.ServiceConfig{
		AccessTTL: 24 * time.Hour, RefreshTTL: 30 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("account NewService: %v", err)
	}
	rooms, err := room.NewService(room.NewMemoryRepository(), hasher, room.ServiceConfig{})
	if err != nil {
		t.Fatalf("room NewService: %v", err)
	}
	return accounts, rooms
}
