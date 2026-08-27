package account

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"testing"
	"time"

	"texas/services/game_server/internal/security"
)

func TestRegisterLoginAuthenticateAndRefresh(t *testing.T) {
	now := time.Unix(1_000, 0)
	service := mustAccountService(t, &now)
	ctx := context.Background()

	registered, err := service.Register(ctx, "player_1", "玩家一", "password-123")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if registered.User.PasswordHash == "" || registered.User.PasswordHash == "password-123" {
		t.Fatal("password was not securely hashed")
	}
	user, err := service.Authenticate(ctx, registered.AccessToken)
	if err != nil || user.UserID != registered.User.UserID {
		t.Fatalf("Authenticate user=%#v error=%v", user, err)
	}

	loggedIn, err := service.Login(ctx, "PLAYER_1", "password-123")
	if err != nil || loggedIn.User.UserID != registered.User.UserID {
		t.Fatalf("Login result=%#v error=%v", loggedIn, err)
	}
	if _, err := service.Login(ctx, "player_1", "wrong-password"); err == nil {
		t.Fatal("wrong password logged in")
	}

	refreshed, err := service.Refresh(ctx, registered.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refreshed.AccessToken == registered.AccessToken || refreshed.RefreshToken == registered.RefreshToken {
		t.Fatal("refresh did not rotate tokens")
	}
	if _, err := service.Authenticate(ctx, registered.AccessToken); err == nil {
		t.Fatal("rotated access token remained valid")
	}
	if err := service.Logout(ctx, refreshed.AccessToken); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := service.Authenticate(ctx, refreshed.AccessToken); accountErrorCode(err) != "authentication_required" {
		t.Fatalf("logged out token authenticate error=%v", err)
	}
}

func TestRegistrationValidationAndExpiry(t *testing.T) {
	now := time.Unix(1_000, 0)
	service := mustAccountService(t, &now)
	ctx := context.Background()
	result, err := service.Register(ctx, "player_1", "玩家一", "password-123")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := service.Register(ctx, "PLAYER_1", "另一个玩家", "password-456"); accountErrorCode(err) != "username_taken" {
		t.Fatalf("duplicate error=%v", err)
	}
	if _, err := service.Register(ctx, "x", "玩家", "password-123"); accountErrorCode(err) != "invalid_profile" {
		t.Fatalf("profile error=%v", err)
	}
	now = now.Add(25 * time.Hour)
	if _, err := service.Authenticate(ctx, result.AccessToken); accountErrorCode(err) != "authentication_required" {
		t.Fatalf("expired authentication error=%v", err)
	}
}

func TestRegistrationDoesNotHideRepositoryFailureAsUsernameConflict(t *testing.T) {
	now := time.Unix(1_000, 0)
	hasher, err := security.NewPasswordHasher(1_000, cryptorand.Reader)
	if err != nil {
		t.Fatalf("NewPasswordHasher: %v", err)
	}
	repositoryFailure := errors.New("database unavailable")
	repository := &failingCreateRepository{
		MemoryRepository: NewMemoryRepository(),
		err:              repositoryFailure,
	}
	service, err := NewService(repository, hasher, ServiceConfig{
		AccessTTL: 24 * time.Hour, RefreshTTL: 30 * 24 * time.Hour,
		Now: func() time.Time { return now }, Random: cryptorand.Reader,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := service.Register(context.Background(), "player_1", "玩家一", "password-123"); !errors.Is(err, repositoryFailure) || accountErrorCode(err) != "" {
		t.Fatalf("Register error=%v", err)
	}
}

func TestInitialAdministratorControlsRegistrationAndAccounts(t *testing.T) {
	now := time.Unix(2_000, 0)
	service := mustAccountService(t, &now)
	ctx := context.Background()

	administrator, err := service.RegisterWithOptions(
		ctx, "server_admin", "管理员", "password-123",
		RegistrationOptions{RequestInitialAdmin: true},
	)
	if err != nil || administrator.User.Role != RoleAdmin {
		t.Fatalf("initial administrator=%#v err=%v", administrator.User, err)
	}
	if _, err := service.RegisterWithOptions(
		ctx, "second_admin", "第二管理员", "password-123",
		RegistrationOptions{RequestInitialAdmin: true},
	); accountErrorCode(err) != "admin_already_initialized" {
		t.Fatalf("second administrator error=%v", err)
	}

	settings, err := service.SetRegistrationEnabled(ctx, administrator.User, false)
	if err != nil || settings.Enabled {
		t.Fatalf("disable registration=%#v err=%v", settings, err)
	}
	if _, err := service.Register(ctx, "blocked_user", "被阻止", "password-123"); accountErrorCode(err) != "registration_disabled" {
		t.Fatalf("disabled registration error=%v", err)
	}

	managed, err := service.CreateManagedUser(
		ctx, administrator.User, "managed_user", "受管玩家", "password-123",
	)
	if err != nil || managed.Role != RolePlayer {
		t.Fatalf("managed user=%#v err=%v", managed, err)
	}
	if err := service.ResetManagedPassword(
		ctx, administrator.User, managed.UserID, "new-password-456",
	); err != nil {
		t.Fatalf("ResetManagedPassword: %v", err)
	}
	if _, err := service.Login(ctx, managed.Username, "new-password-456"); err != nil {
		t.Fatalf("login with reset password: %v", err)
	}
	if err := service.UpdateManagedStatuses(
		ctx, administrator.User, []string{managed.UserID}, StatusSuspended,
	); err != nil {
		t.Fatalf("suspend user: %v", err)
	}
	if _, err := service.Login(ctx, managed.Username, "new-password-456"); accountErrorCode(err) != "invalid_credentials" {
		t.Fatalf("suspended login error=%v", err)
	}
	if err := service.UpdateManagedStatuses(
		ctx, administrator.User, []string{managed.UserID}, StatusActive,
	); err != nil {
		t.Fatalf("restore user: %v", err)
	}
	if err := service.UpdateManagedStatuses(
		ctx, administrator.User, []string{administrator.User.UserID}, StatusDeleted,
	); accountErrorCode(err) != "protected_account" {
		t.Fatalf("administrator self-delete error=%v", err)
	}
}

func TestUserCanChangeOwnUsernameAndPassword(t *testing.T) {
	now := time.Unix(3_000, 0)
	service := mustAccountService(t, &now)
	ctx := context.Background()

	registered, err := service.Register(ctx, "old_login", "好友", "password-123")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	updated, err := service.UpdateOwnUsername(ctx, registered.User, "new_login")
	if err != nil || updated.Username != "new_login" {
		t.Fatalf("UpdateOwnUsername user=%#v err=%v", updated, err)
	}
	if _, err := service.Login(ctx, "old_login", "password-123"); accountErrorCode(err) != "invalid_credentials" {
		t.Fatalf("old username login error=%v", err)
	}
	if _, err := service.Login(ctx, "new_login", "password-123"); err != nil {
		t.Fatalf("new username login: %v", err)
	}

	if _, err := service.ChangeOwnPassword(
		ctx, updated, "wrong-password", "new-password-456",
	); accountErrorCode(err) != "invalid_current_password" {
		t.Fatalf("wrong current password error=%v", err)
	}
	changed, err := service.ChangeOwnPassword(
		ctx, updated, "password-123", "new-password-456",
	)
	if err != nil || changed.AccessToken == "" || changed.RefreshToken == "" {
		t.Fatalf("ChangeOwnPassword result=%#v err=%v", changed, err)
	}
	if _, err := service.Authenticate(ctx, registered.AccessToken); accountErrorCode(err) != "authentication_required" {
		t.Fatalf("old session authentication error=%v", err)
	}
	if _, err := service.Login(ctx, "new_login", "password-123"); accountErrorCode(err) != "invalid_credentials" {
		t.Fatalf("old password login error=%v", err)
	}
	if _, err := service.Login(ctx, "new_login", "new-password-456"); err != nil {
		t.Fatalf("new password login: %v", err)
	}
}

func TestAdministratorCanChangeManagedUsername(t *testing.T) {
	now := time.Unix(4_000, 0)
	service := mustAccountService(t, &now)
	ctx := context.Background()
	administrator, err := service.RegisterWithOptions(
		ctx, "admin_user", "管理员", "password-123",
		RegistrationOptions{RequestInitialAdmin: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	managed, err := service.CreateManagedUser(
		ctx, administrator.User, "managed_old", "好友", "password-123",
	)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.UpdateManagedUsername(
		ctx, administrator.User, managed.UserID, "managed_new",
	)
	if err != nil || updated.Username != "managed_new" {
		t.Fatalf("UpdateManagedUsername user=%#v err=%v", updated, err)
	}
}

type failingCreateRepository struct {
	*MemoryRepository
	err error
}

func (repository *failingCreateRepository) CreateUser(context.Context, User) error {
	return repository.err
}

func mustAccountService(t *testing.T, now *time.Time) *Service {
	t.Helper()
	hasher, err := security.NewPasswordHasher(1_000, cryptorand.Reader)
	if err != nil {
		t.Fatalf("NewPasswordHasher: %v", err)
	}
	service, err := NewService(NewMemoryRepository(), hasher, ServiceConfig{
		AccessTTL:  24 * time.Hour,
		RefreshTTL: 30 * 24 * time.Hour,
		Now:        func() time.Time { return *now },
		Random:     cryptorand.Reader,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service
}

func accountErrorCode(err error) string {
	var accountError Error
	if errors.As(err, &accountError) {
		return accountError.Code
	}
	return ""
}
