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
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)
	if !validUsername.MatchString(username) || !validDisplayName(displayName) {
		return AuthResult{}, Error{Code: "invalid_profile"}
	}
	if _, err := service.repository.UserByUsername(ctx, username); err == nil {
		return AuthResult{}, Error{Code: "username_taken"}
	} else if !errors.Is(err, ErrNotFound) {
		return AuthResult{}, err
	}
	passwordHash, err := service.passwords.Hash(password)
	if err != nil {
		return AuthResult{}, Error{Code: "invalid_password"}
	}
	userID, err := service.randomValue("usr_", 12)
	if err != nil {
		return AuthResult{}, err
	}
	user := User{
		UserID:       userID,
		Username:     username,
		DisplayName:  displayName,
		PasswordHash: passwordHash,
		CreatedAt:    service.config.Now(),
	}
	if err := service.repository.CreateUser(ctx, user); err != nil {
		if errors.Is(err, ErrConflict) {
			return AuthResult{}, Error{Code: "username_taken"}
		}
		return AuthResult{}, err
	}
	return service.createSession(ctx, user)
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
