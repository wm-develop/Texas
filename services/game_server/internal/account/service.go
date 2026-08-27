package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"texas/services/game_server/internal/security"
)

var validUsername = regexp.MustCompile(`^[A-Za-z0-9_]{3,24}$`)

type ServiceConfig struct {
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	Now        func() time.Time
	Random     io.Reader
}

type Service struct {
	repository Repository
	passwords  *security.PasswordHasher
	config     ServiceConfig
}

func NewService(repository Repository, passwords *security.PasswordHasher, config ServiceConfig) (*Service, error) {
	if repository == nil || passwords == nil || config.AccessTTL <= 0 || config.RefreshTTL <= config.AccessTTL {
		return nil, errors.New("invalid account service configuration")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	return &Service{repository: repository, passwords: passwords, config: config}, nil
}

func (service *Service) Register(
	ctx context.Context,
	username string,
	displayName string,
	password string,
) (AuthResult, error) {
	return service.RegisterWithOptions(
		ctx, username, displayName, password, RegistrationOptions{},
	)
}

func (service *Service) RegisterWithOptions(
	ctx context.Context,
	username string,
	displayName string,
	password string,
	options RegistrationOptions,
) (AuthResult, error) {
	if !options.RequestInitialAdmin {
		enabled, err := service.repository.RegistrationEnabled(ctx)
		if err != nil {
			return AuthResult{}, err
		}
		if !enabled {
			return AuthResult{}, Error{Code: "registration_disabled"}
		}
	}
	user, err := service.newUser(username, displayName, password)
	if err != nil {
		return AuthResult{}, err
	}
	if options.RequestInitialAdmin {
		user.Role = RoleAdmin
	}
	if err := service.repository.CreateUser(ctx, user); err != nil {
		if errors.Is(err, ErrConflict) {
			return AuthResult{}, Error{Code: "username_taken"}
		}
		if errors.Is(err, ErrAdminExists) {
			return AuthResult{}, Error{Code: "admin_already_initialized"}
		}
		return AuthResult{}, err
	}
	result, err := service.createSession(ctx, user)
	if err != nil {
		return AuthResult{}, err
	}
	if user.Role == RoleAdmin {
		_ = service.recordAudit(ctx, user.UserID, "admin.bootstrap", map[string]any{
			"username": user.Username,
		})
	}
	return result, nil
}

func (service *Service) newUser(username, displayName, password string) (User, error) {
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)
	if !validUsername.MatchString(username) || !validDisplayName(displayName) {
		return User{}, Error{Code: "invalid_profile"}
	}
	passwordHash, err := service.passwords.Hash(password)
	if err != nil {
		return User{}, Error{Code: "invalid_password"}
	}
	userID, err := service.randomValue("usr_", 12)
	if err != nil {
		return User{}, err
	}
	return User{
		UserID:       userID,
		Username:     username,
		DisplayName:  displayName,
		Role:         RolePlayer,
		Status:       StatusActive,
		PasswordHash: passwordHash,
		CreatedAt:    service.config.Now(),
	}, nil
}

func (service *Service) Login(ctx context.Context, username string, password string) (AuthResult, error) {
	user, err := service.repository.UserByUsername(ctx, strings.TrimSpace(username))
	if err != nil || !service.passwords.Verify(password, user.PasswordHash) {
		return AuthResult{}, Error{Code: "invalid_credentials"}
	}
	return service.createSession(ctx, user)
}

func (service *Service) Refresh(ctx context.Context, refreshToken string) (AuthResult, error) {
	session, err := service.repository.SessionByRefreshHash(ctx, tokenHash(refreshToken))
	if err != nil || !service.config.Now().Before(session.RefreshExpiresAt) {
		return AuthResult{}, Error{Code: "invalid_refresh_token"}
	}
	user, err := service.repository.UserByID(ctx, session.UserID)
	if err != nil {
		return AuthResult{}, Error{Code: "invalid_refresh_token"}
	}
	if err := service.repository.DeleteSession(ctx, session.SessionID); err != nil {
		return AuthResult{}, err
	}
	return service.createSession(ctx, user)
}

func (service *Service) Authenticate(ctx context.Context, accessToken string) (User, error) {
	session, err := service.repository.SessionByAccessHash(ctx, tokenHash(accessToken))
	if err != nil || !service.config.Now().Before(session.AccessExpiresAt) {
		return User{}, Error{Code: "authentication_required"}
	}
	user, err := service.repository.UserByID(ctx, session.UserID)
	if err != nil {
		return User{}, Error{Code: "authentication_required"}
	}
	return user, nil
}

func (service *Service) Logout(ctx context.Context, accessToken string) error {
	if strings.TrimSpace(accessToken) == "" {
		return Error{Code: "authentication_required"}
	}
	session, err := service.repository.SessionByAccessHash(ctx, tokenHash(accessToken))
	if err != nil {
		return Error{Code: "authentication_required"}
	}
	if err := service.repository.DeleteSession(ctx, session.SessionID); err != nil {
		return err
	}
	return nil
}

func (service *Service) ResolveUser(ctx context.Context, accessToken string) (string, error) {
	user, err := service.Authenticate(ctx, accessToken)
	if err != nil {
		return "", err
	}
	return user.UserID, nil
}

func (service *Service) ListUsers(ctx context.Context, actor User) ([]User, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	return service.repository.ListUsers(ctx)
}

func (service *Service) ManagedUser(ctx context.Context, actor User, userID string) (User, error) {
	if err := requireAdmin(actor); err != nil {
		return User{}, err
	}
	user, err := service.repository.UserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return User{}, Error{Code: "user_not_found"}
		}
		return User{}, err
	}
	return user, nil
}

func (service *Service) UpdateOwnUsername(
	ctx context.Context,
	actor User,
	username string,
) (User, error) {
	return service.updateUsername(ctx, actor, actor.UserID, username, "user.username_changed")
}

func (service *Service) UpdateManagedUsername(
	ctx context.Context,
	actor User,
	userID, username string,
) (User, error) {
	if err := requireAdmin(actor); err != nil {
		return User{}, err
	}
	return service.updateUsername(ctx, actor, userID, username, "admin.username_changed")
}

func (service *Service) updateUsername(
	ctx context.Context,
	actor User,
	userID, username, eventType string,
) (User, error) {
	username = strings.TrimSpace(username)
	if !validUsername.MatchString(username) {
		return User{}, Error{Code: "invalid_profile"}
	}
	previous, err := service.repository.UserByID(ctx, userID)
	if err != nil {
		return User{}, Error{Code: "user_not_found"}
	}
	if err := service.repository.UpdateUsername(ctx, userID, username, service.config.Now()); err != nil {
		if errors.Is(err, ErrConflict) {
			return User{}, Error{Code: "username_taken"}
		}
		if errors.Is(err, ErrNotFound) {
			return User{}, Error{Code: "user_not_found"}
		}
		return User{}, err
	}
	updated, err := service.repository.UserByID(ctx, userID)
	if err != nil {
		return User{}, err
	}
	if err := service.recordAudit(ctx, actor.UserID, eventType, map[string]any{
		"targetUserId": userID,
		"oldUsername":  previous.Username,
		"newUsername":  updated.Username,
	}); err != nil {
		return User{}, err
	}
	return updated, nil
}

func (service *Service) ChangeOwnPassword(
	ctx context.Context,
	actor User,
	currentPassword, newPassword string,
) (AuthResult, error) {
	current, err := service.repository.UserByID(ctx, actor.UserID)
	if err != nil || !service.passwords.Verify(currentPassword, current.PasswordHash) {
		return AuthResult{}, Error{Code: "invalid_current_password"}
	}
	passwordHash, err := service.passwords.Hash(newPassword)
	if err != nil {
		return AuthResult{}, Error{Code: "invalid_password"}
	}
	if err := service.repository.UpdatePassword(
		ctx, actor.UserID, passwordHash, service.config.Now(),
	); err != nil {
		return AuthResult{}, err
	}
	updated, err := service.repository.UserByID(ctx, actor.UserID)
	if err != nil {
		return AuthResult{}, err
	}
	result, err := service.createSession(ctx, updated)
	if err != nil {
		return AuthResult{}, err
	}
	_ = service.recordAudit(ctx, actor.UserID, "user.password_changed", map[string]any{
		"targetUserId": actor.UserID,
	})
	return result, nil
}

func (service *Service) AuthorizeAdmin(actor User) error {
	return requireAdmin(actor)
}

func (service *Service) RecordManagedWalletChange(
	ctx context.Context,
	actor User,
	userID string,
	chips int64,
) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	return service.recordAudit(ctx, actor.UserID, "admin.wallet_changed", map[string]any{
		"targetUserId": userID,
		"walletChips":  chips,
	})
}

func (service *Service) RecordManagedRoomRemoval(
	ctx context.Context,
	actor User,
	userID, roomID, roomCode string,
) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	return service.recordAudit(ctx, actor.UserID, "admin.user_removed_from_room", map[string]any{
		"targetUserId": userID,
		"roomId":       roomID,
		"roomCode":     roomCode,
	})
}

func (service *Service) CreateManagedUser(
	ctx context.Context,
	actor User,
	username, displayName, password string,
) (User, error) {
	if err := requireAdmin(actor); err != nil {
		return User{}, err
	}
	user, err := service.newUser(username, displayName, password)
	if err != nil {
		return User{}, err
	}
	if err := service.repository.CreateUser(ctx, user); err != nil {
		if errors.Is(err, ErrConflict) {
			return User{}, Error{Code: "username_taken"}
		}
		return User{}, err
	}
	if err := service.recordAudit(ctx, actor.UserID, "admin.user_created", map[string]any{
		"targetUserId": user.UserID, "username": user.Username,
	}); err != nil {
		return User{}, err
	}
	return user, nil
}

func (service *Service) ResetManagedPassword(
	ctx context.Context,
	actor User,
	userID, password string,
) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	if actor.UserID == userID {
		return Error{Code: "protected_account"}
	}
	passwordHash, err := service.passwords.Hash(password)
	if err != nil {
		return Error{Code: "invalid_password"}
	}
	if err := service.repository.UpdatePassword(ctx, userID, passwordHash, service.config.Now()); err != nil {
		if errors.Is(err, ErrNotFound) {
			return Error{Code: "user_not_found"}
		}
		return err
	}
	return service.recordAudit(ctx, actor.UserID, "admin.password_reset", map[string]any{
		"targetUserId": userID,
	})
}

func (service *Service) UpdateManagedStatuses(
	ctx context.Context,
	actor User,
	userIDs []string,
	status Status,
) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	if len(userIDs) == 0 || len(userIDs) > 100 ||
		(status != StatusActive && status != StatusSuspended && status != StatusDeleted) {
		return Error{Code: "invalid_request"}
	}
	seen := make(map[string]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if strings.TrimSpace(userID) == "" {
			return Error{Code: "invalid_request"}
		}
		seen[userID] = struct{}{}
	}
	unique := make([]string, 0, len(seen))
	for userID := range seen {
		unique = append(unique, userID)
	}
	if err := service.repository.UpdateStatuses(ctx, actor.UserID, unique, status, service.config.Now()); err != nil {
		if errors.Is(err, ErrNotFound) {
			return Error{Code: "user_not_found"}
		}
		if errors.Is(err, ErrProtected) {
			return Error{Code: "protected_account"}
		}
		return err
	}
	return service.recordAudit(ctx, actor.UserID, "admin.user_status_changed", map[string]any{
		"targetUserIds": unique, "status": status,
	})
}

func (service *Service) RegistrationSettings(ctx context.Context, actor User) (RegistrationSettings, error) {
	if err := requireAdmin(actor); err != nil {
		return RegistrationSettings{}, err
	}
	enabled, err := service.repository.RegistrationEnabled(ctx)
	return RegistrationSettings{Enabled: enabled}, err
}

func (service *Service) SetRegistrationEnabled(ctx context.Context, actor User, enabled bool) (RegistrationSettings, error) {
	if err := requireAdmin(actor); err != nil {
		return RegistrationSettings{}, err
	}
	if err := service.repository.SetRegistrationEnabled(ctx, actor.UserID, enabled, service.config.Now()); err != nil {
		return RegistrationSettings{}, err
	}
	if err := service.recordAudit(ctx, actor.UserID, "admin.registration_changed", map[string]any{
		"enabled": enabled,
	}); err != nil {
		return RegistrationSettings{}, err
	}
	return RegistrationSettings{Enabled: enabled}, nil
}

func (service *Service) recordAudit(ctx context.Context, actorUserID, eventType string, metadata map[string]any) error {
	eventID, err := service.randomValue("aud_", 12)
	if err != nil {
		return err
	}
	return service.repository.RecordAudit(ctx, AuditEvent{
		EventID: eventID, ActorUserID: actorUserID, EventType: eventType,
		Metadata: metadata, CreatedAt: service.config.Now(),
	})
}

func requireAdmin(user User) error {
	if user.Role != RoleAdmin || user.Status != StatusActive {
		return Error{Code: "admin_required"}
	}
	return nil
}

func (service *Service) createSession(ctx context.Context, user User) (AuthResult, error) {
	now := service.config.Now()
	sessionID, err := service.randomValue("ses_", 12)
	if err != nil {
		return AuthResult{}, err
	}
	accessToken, err := service.randomValue("txa_", 32)
	if err != nil {
		return AuthResult{}, err
	}
	refreshToken, err := service.randomValue("txr_", 32)
	if err != nil {
		return AuthResult{}, err
	}
	session := Session{
		SessionID:        sessionID,
		UserID:           user.UserID,
		AccessTokenHash:  tokenHash(accessToken),
		RefreshTokenHash: tokenHash(refreshToken),
		AccessExpiresAt:  now.Add(service.config.AccessTTL),
		RefreshExpiresAt: now.Add(service.config.RefreshTTL),
		CreatedAt:        now,
	}
	if err := service.repository.SaveSession(ctx, session); err != nil {
		return AuthResult{}, err
	}
	return AuthResult{
		User:             user,
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		AccessExpiresAt:  session.AccessExpiresAt,
		RefreshExpiresAt: session.RefreshExpiresAt,
	}, nil
}

func (service *Service) randomValue(prefix string, byteCount int) (string, error) {
	value := make([]byte, byteCount)
	if _, err := io.ReadFull(service.config.Random, value); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(value), nil
}

func tokenHash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func validDisplayName(value string) bool {
	count := utf8.RuneCountInString(value)
	if count < 1 || count > 20 {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
