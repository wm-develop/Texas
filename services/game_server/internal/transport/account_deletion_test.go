package transport

import (
	"context"
	cryptorand "crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/config"
	"texas/services/game_server/internal/game/tablemanager"
	"texas/services/game_server/internal/room"
	"texas/services/game_server/internal/security"
)

func mustHasher(t *testing.T) *security.PasswordHasher {
	t.Helper()
	hasher, err := security.NewPasswordHasher(1_000, cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return hasher
}

type deletionFixture struct {
	server   *httptest.Server
	accounts *account.Service
	chips    *bankroll.Service
	rooms    *room.Service
	admin    account.AuthResult
	player   account.AuthResult
}

func newDeletionFixture(t *testing.T, limits config.RateLimits) deletionFixture {
	t.Helper()
	ctx := context.Background()
	chips, err := bankroll.NewService(bankroll.NewMemoryRepository(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	accounts, _ := testApplicationServices(t)
	rooms, err := room.NewService(room.NewMemoryRepository(), mustHasher(t), room.ServiceConfig{Bankroll: chips})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := accounts.RegisterWithOptions(ctx, "first_admin", "管理员", "password-123",
		account.RegistrationOptions{RequestInitialAdmin: true})
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	player, err := accounts.Register(ctx, "leaving_player", "要走的人", "player-secret-1")
	if err != nil {
		t.Fatalf("register player: %v", err)
	}
	if _, err := chips.TopUp(ctx, player.User.UserID, "topup-player", 5_000); err != nil {
		t.Fatal(err)
	}
	if _, err := chips.TopUp(ctx, admin.User.UserID, "topup-admin", 100); err != nil {
		t.Fatal(err)
	}
	tables, err := tablemanager.NewWithConfig(rooms, transportZeroRandom{}, tablemanager.ManagerConfig{Bankroll: chips})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(testLogger(), Options{
		Accounts: accounts, Rooms: rooms, Tables: tables, Bankroll: chips, RateLimits: limits,
	}))
	t.Cleanup(server.Close)
	return deletionFixture{server: server, accounts: accounts, chips: chips, rooms: rooms, admin: admin, player: player}
}

func (fixture deletionFixture) deleteAccount(t *testing.T, token, password string) *http.Response {
	t.Helper()
	return postJSON(t, fixture.server, "/v1/users/me/delete",
		map[string]string{"password": password},
		map[string]string{"Authorization": "Bearer " + token})
}

func TestSelfDeleteMovesChipsToAdminAnonymisesAndFreesUsername(t *testing.T) {
	ctx := context.Background()
	fixture := newDeletionFixture(t, config.RateLimits{})

	response := fixture.deleteAccount(t, fixture.player.AccessToken, "player-secret-1")
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: status %d", response.StatusCode)
	}

	// 筹码整体转入管理员钱包，流水注明来源
	adminWallet, _ := fixture.chips.Snapshot(ctx, fixture.admin.User.UserID)
	if adminWallet.WalletChips != 5_100 {
		t.Fatalf("admin wallet=%d, expected 100+5000", adminWallet.WalletChips)
	}
	entries, _ := fixture.chips.Entries(ctx, fixture.admin.User.UserID, 10)
	var credited bool
	for _, entry := range entries {
		if entry.Reason == bankroll.ReasonAccountDeletion &&
			entry.WalletDelta == 5_000 && entry.ReferenceID == fixture.player.User.UserID {
			credited = true
		}
	}
	if !credited {
		t.Fatalf("admin ledger lacks an account_deletion credit referencing the deleted user: %#v", entries)
	}
	playerWallet, _ := fixture.chips.Snapshot(ctx, fixture.player.User.UserID)
	if playerWallet.WalletChips != 0 {
		t.Fatalf("deleted player still holds %d chips", playerWallet.WalletChips)
	}

	// 会话已撤销
	if _, err := fixture.accounts.Authenticate(ctx, fixture.player.AccessToken); err == nil {
		t.Fatal("old access token must be revoked")
	}
	if _, err := fixture.accounts.Refresh(ctx, fixture.player.RefreshToken); err == nil {
		t.Fatal("old refresh token must be revoked")
	}
	// 原用户名不能再登录，但可以重新注册
	if _, err := fixture.accounts.Login(ctx, "leaving_player", "player-secret-1"); err == nil {
		t.Fatal("deleted account must not be able to log in")
	}
	reborn, err := fixture.accounts.Register(ctx, "leaving_player", "新来的", "another-secret-1")
	if err != nil {
		t.Fatalf("username should be free after deletion: %v", err)
	}
	if reborn.User.UserID == fixture.player.User.UserID {
		t.Fatal("re-registration must create a new account, not resurrect the old one")
	}
	// 旧账号已脱敏
	users, _ := fixture.accounts.ListUsers(ctx, fixture.admin.User)
	for _, user := range users {
		if user.UserID == fixture.player.User.UserID {
			if user.Status != account.StatusDeleted || user.DisplayName != account.DeletedDisplayName ||
				user.Username != account.DeletedUsername(user.UserID) {
				t.Fatalf("deleted user not anonymised: %#v", user)
			}
		}
	}
	// 重复提交（例如客户端重试）不会再次转账
	again := fixture.deleteAccount(t, fixture.player.AccessToken, "player-secret-1")
	if again.StatusCode != http.StatusUnauthorized {
		t.Fatalf("second delete with revoked token should be 401, got %d", again.StatusCode)
	}
	adminAfter, _ := fixture.chips.Snapshot(ctx, fixture.admin.User.UserID)
	if adminAfter.WalletChips != 5_100 {
		t.Fatal("retry must not double-credit the admin")
	}
}

func TestSelfDeleteRejectedWhileInRoomAndForAdmins(t *testing.T) {
	ctx := context.Background()
	fixture := newDeletionFixture(t, config.RateLimits{})

	if _, err := fixture.rooms.Create(ctx, participant(fixture.player), room.PresetStandard, 2, ""); err != nil {
		t.Fatal(err)
	}
	if response := fixture.deleteAccount(t, fixture.player.AccessToken, "player-secret-1"); response.StatusCode != http.StatusConflict {
		t.Fatalf("in-room deletion should be 409, got %d", response.StatusCode)
	}
	wallet, _ := fixture.chips.Snapshot(ctx, fixture.player.User.UserID)
	if wallet.WalletChips+wallet.TableChips != 5_000 {
		t.Fatal("rejected deletion must not move any chips")
	}

	if response := fixture.deleteAccount(t, fixture.admin.AccessToken, "password-123"); response.StatusCode != http.StatusForbidden {
		t.Fatalf("admin self-deletion should be 403, got %d", response.StatusCode)
	}
}

func TestSelfDeleteWrongPasswordCountsTowardsLockout(t *testing.T) {
	fixture := newDeletionFixture(t, config.RateLimits{
		LoginFailuresPerUser: config.RateLimit{Burst: 2, Window: time.Hour},
	})
	for index := 0; index < 2; index++ {
		if response := fixture.deleteAccount(t, fixture.player.AccessToken, "wrong"); response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d", index+1, response.StatusCode)
		}
	}
	// 第三次即使密码正确也已锁定，且登录接口同样被锁
	if response := fixture.deleteAccount(t, fixture.player.AccessToken, "player-secret-1"); response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("locked user should get 429, got %d", response.StatusCode)
	}
	login := postJSON(t, fixture.server, "/v1/auth/login",
		map[string]string{"username": "leaving_player", "password": "player-secret-1"}, nil)
	if login.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("lockout must be shared with the login endpoint, got %d", login.StatusCode)
	}
	wallet, _ := fixture.chips.Snapshot(context.Background(), fixture.player.User.UserID)
	if wallet.WalletChips != 5_000 {
		t.Fatal("failed attempts must not move chips")
	}
}
