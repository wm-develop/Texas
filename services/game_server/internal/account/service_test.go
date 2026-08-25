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
