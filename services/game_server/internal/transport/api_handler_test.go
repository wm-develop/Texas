package transport

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/game/tablemanager"
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

func TestBankrollHTTPTopUpIsAuthenticatedAndIdempotent(t *testing.T) {
	accounts, rooms := testApplicationServices(t)
	chips, err := bankroll.NewService(bankroll.NewMemoryRepository(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(testLogger(), Options{Accounts: accounts, Rooms: rooms, Bankroll: chips}))
	defer server.Close()
	player := registerHTTPUser(t, server.URL, "wallet_user", "钱包牌手")

	unauthorized := doJSONRequest(t, http.MethodGet, server.URL+"/v1/bankroll", "", nil)
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.StatusCode)
	}

	for range 2 {
		response := doJSONRequest(t, http.MethodPost, server.URL+"/v1/bankroll/top-ups", player.AccessToken, map[string]any{"requestId": "topup-http-1", "amount": 8888})
		if response.StatusCode != http.StatusOK {
			t.Fatalf("top-up status=%d body=%s", response.StatusCode, readBody(response))
		}
		response.Body.Close()
	}
	response := doJSONRequest(t, http.MethodGet, server.URL+"/v1/bankroll", player.AccessToken, nil)
	defer response.Body.Close()
	var snapshot bankroll.Snapshot
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.WalletChips != 8888 {
		t.Fatalf("wallet chips=%d", snapshot.WalletChips)
	}
}

func TestConfiguredRoomBuyInPreviewAndCashOutHTTPFlow(t *testing.T) {
	accounts, _ := testApplicationServices(t)
	chips, err := bankroll.NewService(bankroll.NewMemoryRepository(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := security.NewPasswordHasher(1_000, cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rooms, err := room.NewService(room.NewMemoryRepository(), hasher, room.ServiceConfig{Bankroll: chips})
	if err != nil {
		t.Fatal(err)
	}
	tables, err := tablemanager.NewWithConfig(rooms, transportZeroRandom{}, tablemanager.ManagerConfig{Bankroll: chips})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(testLogger(), Options{
		Accounts: accounts, Rooms: rooms, Tables: tables, Bankroll: chips,
	}))
	defer server.Close()
	owner := registerHTTPUser(t, server.URL, "configured_owner", "房主")
	guest := registerHTTPUser(t, server.URL, "configured_guest", "好友")
	for _, player := range []account.AuthResult{owner, guest} {
		response := doJSONRequest(t, http.MethodPost, server.URL+"/v1/bankroll/top-ups", player.AccessToken, map[string]any{
			"requestId": "initial-" + player.User.UserID, "amount": 5_000,
		})
		if response.StatusCode != http.StatusOK {
			t.Fatalf("top-up status=%d body=%s", response.StatusCode, readBody(response))
		}
		response.Body.Close()
	}
	createBody := map[string]any{
		"preset": "standard", "maxPlayers": 6, "password": "friend-pass",
		"smallBlind": 25, "bigBlind": 50, "maxBuyIn": 3_000,
		"buyIn": 1_500, "requestId": "create-configured-room",
	}
	var created room.Room
	for attempt := 0; attempt < 2; attempt++ {
		response := doJSONRequest(t, http.MethodPost, server.URL+"/v1/rooms", owner.AccessToken, createBody)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("create status=%d body=%s", response.StatusCode, readBody(response))
		}
		var value room.Room
		if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if attempt == 0 {
			created = value
		} else if value.RoomID != created.RoomID {
			t.Fatalf("duplicate create allocated another room: %s != %s", value.RoomID, created.RoomID)
		}
	}
	previewResponse := doJSONRequest(t, http.MethodGet, server.URL+"/v1/rooms/preview?code="+created.Code, guest.AccessToken, nil)
	if previewResponse.StatusCode != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", previewResponse.StatusCode, readBody(previewResponse))
	}
	var preview room.Preview
	if err := json.NewDecoder(previewResponse.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	previewResponse.Body.Close()
	if preview.Rules.SmallBlind != 25 || preview.Rules.BigBlind != 50 || preview.Rules.MaxBuyIn != 3_000 || !preview.PasswordRequired {
		t.Fatalf("preview=%#v", preview)
	}
	joinResponse := doJSONRequest(t, http.MethodPost, server.URL+"/v1/rooms/join", guest.AccessToken, map[string]any{
		"code": created.Code, "password": "friend-pass", "buyIn": 750, "requestId": "join-configured-room",
	})
	if joinResponse.StatusCode != http.StatusOK {
		t.Fatalf("join status=%d body=%s", joinResponse.StatusCode, readBody(joinResponse))
	}
	joinResponse.Body.Close()
	leaveResponse := doJSONRequest(t, http.MethodPost, server.URL+"/v1/rooms/leave", guest.AccessToken, map[string]any{})
	if leaveResponse.StatusCode != http.StatusOK {
		t.Fatalf("leave status=%d body=%s", leaveResponse.StatusCode, readBody(leaveResponse))
	}
	leaveResponse.Body.Close()
	guestChips, err := chips.Snapshot(context.Background(), guest.User.UserID)
	if err != nil || guestChips.WalletChips != 5_000 || guestChips.TableChips != 0 {
		t.Fatalf("guest bankroll=%#v err=%v", guestChips, err)
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

func TestAdministratorHTTPFlowEnforcesRolesAndRegistrationSetting(t *testing.T) {
	accounts, _ := testApplicationServices(t)
	chips, err := bankroll.NewService(bankroll.NewMemoryRepository(), time.Now)
	if err != nil {
		t.Fatalf("bankroll NewService: %v", err)
	}
	server := httptest.NewServer(NewHandler(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		Options{Accounts: accounts, Bankroll: chips},
	))
	defer server.Close()

	administrator, err := accounts.RegisterWithOptions(
		context.Background(), "admin_http", "管理员", "password-123",
		account.RegistrationOptions{RequestInitialAdmin: true},
	)
	if err != nil {
		t.Fatalf("register administrator: %v", err)
	}
	player := registerHTTPUser(t, server.URL, "plain_http", "普通玩家")

	forbidden := doJSONRequest(
		t, http.MethodGet, server.URL+"/v1/admin/users", player.AccessToken, nil,
	)
	forbidden.Body.Close()
	if forbidden.StatusCode != http.StatusForbidden {
		t.Fatalf("ordinary admin list status=%d", forbidden.StatusCode)
	}

	listed := doJSONRequest(
		t, http.MethodGet, server.URL+"/v1/admin/users", administrator.AccessToken, nil,
	)
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("admin list status=%d body=%s", listed.StatusCode, readBody(listed))
	}
	var payload struct {
		Users []managedUserResponse `json:"users"`
	}
	if err := json.NewDecoder(listed.Body).Decode(&payload); err != nil || len(payload.Users) != 2 {
		t.Fatalf("admin users=%#v err=%v", payload.Users, err)
	}
	listed.Body.Close()
	var listedPlayer managedUserResponse
	for _, user := range payload.Users {
		if user.UserID == player.User.UserID {
			listedPlayer = user
		}
	}
	if !listedPlayer.Online {
		t.Fatalf("registered player should be online: %#v", listedPlayer)
	}

	renamed := doJSONRequest(
		t, http.MethodPost,
		server.URL+"/v1/admin/users/"+player.User.UserID+"/username",
		administrator.AccessToken, map[string]any{"username": "renamed_http"},
	)
	if renamed.StatusCode != http.StatusOK {
		t.Fatalf("rename status=%d body=%s", renamed.StatusCode, readBody(renamed))
	}
	renamed.Body.Close()
	if _, err := accounts.Login(context.Background(), "renamed_http", "password-123"); err != nil {
		t.Fatalf("login after administrator rename: %v", err)
	}

	wallet := doJSONRequest(
		t, http.MethodPost,
		server.URL+"/v1/admin/users/"+player.User.UserID+"/wallet",
		administrator.AccessToken,
		map[string]any{"requestId": "admin-wallet-http", "chips": 4321},
	)
	if wallet.StatusCode != http.StatusOK {
		t.Fatalf("wallet status=%d body=%s", wallet.StatusCode, readBody(wallet))
	}
	var walletSnapshot bankroll.Snapshot
	if err := json.NewDecoder(wallet.Body).Decode(&walletSnapshot); err != nil {
		t.Fatal(err)
	}
	wallet.Body.Close()
	if walletSnapshot.WalletChips != 4321 {
		t.Fatalf("wallet snapshot=%#v", walletSnapshot)
	}

	disabled := doJSONRequest(
		t, http.MethodPost, server.URL+"/v1/admin/settings/registration",
		administrator.AccessToken, map[string]any{"enabled": false},
	)
	disabled.Body.Close()
	if disabled.StatusCode != http.StatusOK {
		t.Fatalf("disable registration status=%d", disabled.StatusCode)
	}
	blocked := doJSONRequest(
		t, http.MethodPost, server.URL+"/v1/auth/register", "", map[string]any{
			"username": "blocked_http", "displayName": "禁止注册", "password": "password-123",
		},
	)
	body := readBody(blocked)
	blocked.Body.Close()
	if blocked.StatusCode != http.StatusForbidden || !strings.Contains(body, "registration_disabled") {
		t.Fatalf("blocked registration status=%d body=%s", blocked.StatusCode, body)
	}
}

func TestPersonalProfileHTTPFlowChangesUsernameAndPassword(t *testing.T) {
	accounts, _ := testApplicationServices(t)
	server := httptest.NewServer(NewHandler(testLogger(), Options{Accounts: accounts}))
	defer server.Close()
	player := registerHTTPUser(t, server.URL, "profile_old", "好友")

	rename := doJSONRequest(
		t, http.MethodPost, server.URL+"/v1/users/me/username", player.AccessToken,
		map[string]any{"username": "profile_new"},
	)
	if rename.StatusCode != http.StatusOK {
		t.Fatalf("rename status=%d body=%s", rename.StatusCode, readBody(rename))
	}
	rename.Body.Close()

	wrong := doJSONRequest(
		t, http.MethodPost, server.URL+"/v1/users/me/password", player.AccessToken,
		map[string]any{"currentPassword": "wrong-password", "newPassword": "new-password-456"},
	)
	wrongBody := readBody(wrong)
	wrong.Body.Close()
	if wrong.StatusCode != http.StatusUnauthorized || !strings.Contains(wrongBody, "invalid_current_password") {
		t.Fatalf("wrong password status=%d body=%s", wrong.StatusCode, wrongBody)
	}

	change := doJSONRequest(
		t, http.MethodPost, server.URL+"/v1/users/me/password", player.AccessToken,
		map[string]any{"currentPassword": "password-123", "newPassword": "new-password-456"},
	)
	if change.StatusCode != http.StatusOK {
		t.Fatalf("change password status=%d body=%s", change.StatusCode, readBody(change))
	}
	var changed account.AuthResult
	if err := json.NewDecoder(change.Body).Decode(&changed); err != nil {
		t.Fatal(err)
	}
	change.Body.Close()
	if changed.AccessToken == "" || changed.AccessToken == player.AccessToken {
		t.Fatalf("changed session=%#v", changed)
	}
	if _, err := accounts.Authenticate(context.Background(), player.AccessToken); err == nil {
		t.Fatal("old access token remained valid after password change")
	}
	if _, err := accounts.Login(context.Background(), "profile_new", "new-password-456"); err != nil {
		t.Fatalf("login with updated profile: %v", err)
	}
}

func TestAdministratorCanSeeRoomAndRemovePlayer(t *testing.T) {
	accounts, _ := testApplicationServices(t)
	chips, err := bankroll.NewService(bankroll.NewMemoryRepository(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := security.NewPasswordHasher(1_000, cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rooms, err := room.NewService(room.NewMemoryRepository(), hasher, room.ServiceConfig{Bankroll: chips})
	if err != nil {
		t.Fatal(err)
	}
	tables, err := tablemanager.NewWithConfig(rooms, transportZeroRandom{}, tablemanager.ManagerConfig{Bankroll: chips})
	if err != nil {
		t.Fatal(err)
	}
	administrator, err := accounts.RegisterWithOptions(
		context.Background(), "kick_admin", "管理员", "password-123",
		account.RegistrationOptions{RequestInitialAdmin: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(testLogger(), Options{
		Accounts: accounts, Bankroll: chips, Rooms: rooms, Tables: tables,
	}))
	defer server.Close()
	owner := registerHTTPUser(t, server.URL, "kick_owner", "房主")
	guest := registerHTTPUser(t, server.URL, "kick_guest", "好友")
	for _, player := range []account.AuthResult{owner, guest} {
		response := doJSONRequest(
			t, http.MethodPost, server.URL+"/v1/bankroll/top-ups", player.AccessToken,
			map[string]any{"requestId": "kick-topup-" + player.User.UserID, "amount": 5000},
		)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("top-up status=%d body=%s", response.StatusCode, readBody(response))
		}
		response.Body.Close()
	}
	create := doJSONRequest(
		t, http.MethodPost, server.URL+"/v1/rooms", owner.AccessToken,
		map[string]any{
			"preset": "standard", "maxPlayers": 6, "smallBlind": 10, "bigBlind": 20,
			"maxBuyIn": 3000, "buyIn": 2000, "requestId": "kick-create-room",
		},
	)
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.StatusCode, readBody(create))
	}
	var created room.Room
	if err := json.NewDecoder(create.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	create.Body.Close()
	join := doJSONRequest(
		t, http.MethodPost, server.URL+"/v1/rooms/join", guest.AccessToken,
		map[string]any{
			"code": created.Code, "buyIn": 1000, "requestId": "kick-join-room",
		},
	)
	if join.StatusCode != http.StatusOK {
		t.Fatalf("join status=%d body=%s", join.StatusCode, readBody(join))
	}
	join.Body.Close()

	listed := doJSONRequest(
		t, http.MethodGet, server.URL+"/v1/admin/users", administrator.AccessToken, nil,
	)
	var payload struct {
		Users []managedUserResponse `json:"users"`
	}
	if err := json.NewDecoder(listed.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	listed.Body.Close()
	var listedGuest managedUserResponse
	for _, user := range payload.Users {
		if user.UserID == guest.User.UserID {
			listedGuest = user
		}
	}
	if listedGuest.RoomCode != created.Code || listedGuest.TableChips != 1000 {
		t.Fatalf("listed guest=%#v", listedGuest)
	}

	kick := doJSONRequest(
		t, http.MethodPost,
		server.URL+"/v1/admin/users/"+guest.User.UserID+"/leave-room",
		administrator.AccessToken, map[string]any{},
	)
	if kick.StatusCode != http.StatusOK {
		t.Fatalf("kick status=%d body=%s", kick.StatusCode, readBody(kick))
	}
	kick.Body.Close()
	if _, err := rooms.Current(context.Background(), guest.User.UserID); err == nil {
		t.Fatal("guest still has a current room")
	} else {
		var roomError room.Error
		if !errors.As(err, &roomError) || roomError.Code != "room_not_found" {
			t.Fatalf("guest current room error=%v", err)
		}
	}
	snapshot, err := chips.Snapshot(context.Background(), guest.User.UserID)
	if err != nil || snapshot.WalletChips != 5000 || snapshot.TableChips != 0 {
		t.Fatalf("guest bankroll=%#v err=%v", snapshot, err)
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
