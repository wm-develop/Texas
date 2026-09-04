package account

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound    = errors.New("not found")
	ErrConflict    = errors.New("repository conflict")
	ErrAdminExists = errors.New("administrator already exists")
	ErrProtected   = errors.New("protected account")
)

type Repository interface {
	CreateUser(ctx context.Context, user User) error
	UserByID(ctx context.Context, userID string) (User, error)
	UserByUsername(ctx context.Context, normalizedUsername string) (User, error)
	ListUsers(ctx context.Context) ([]User, error)
	UpdateUsername(ctx context.Context, userID, username string, now time.Time) error
	UpdateDisplayName(ctx context.Context, userID, displayName string, now time.Time) error
	UpdatePassword(ctx context.Context, userID, passwordHash string, now time.Time) error
	UpdateStatuses(ctx context.Context, actorUserID string, userIDs []string, status Status, now time.Time) error
	// SelfDelete 原子地完成用户自行注销：改写用户名与昵称以释放原用户名、
	// 状态置为 deleted、撤销全部会话。已注销或不存在的账号返回 ErrNotFound。
	SelfDelete(ctx context.Context, userID, anonymizedUsername, anonymizedDisplayName string, now time.Time) error
	RegistrationEnabled(ctx context.Context) (bool, error)
	SetRegistrationEnabled(ctx context.Context, actorUserID string, enabled bool, now time.Time) error
	// MinimumClientVersion 是允许连接的最低客户端版本号，0 表示不启用门禁。
	// 存在数据库而不是环境变量：只更新客户端时也能在管理界面里调整，
	// 不必登服务器改 env 并重建容器。
	MinimumClientVersion(ctx context.Context) (int, error)
	SetMinimumClientVersion(ctx context.Context, actorUserID string, version int, now time.Time) error
	RecordAudit(ctx context.Context, event AuditEvent) error
	// ListAudit 按时间倒序返回审计事件；query.UserID 非空时只返回该用户作为操作者或对象的事件。
	ListAudit(ctx context.Context, query AuditQuery) ([]AuditEvent, error)
	SaveSession(ctx context.Context, session Session) error
	SessionByAccessHash(ctx context.Context, tokenHash string) (Session, error)
	SessionByRefreshHash(ctx context.Context, tokenHash string) (Session, error)
	DeleteSession(ctx context.Context, sessionID string) error
}

type MemoryRepository struct {
	mu                   sync.RWMutex
	usersByID            map[string]User
	userIDByUsername     map[string]string
	sessionsByID         map[string]Session
	sessionIDByAccess    map[string]string
	sessionIDByRefresh   map[string]string
	registrationEnabled  bool
	minimumClientVersion int
	auditEvents          []AuditEvent
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		usersByID:           make(map[string]User),
		userIDByUsername:    make(map[string]string),
		sessionsByID:        make(map[string]Session),
		sessionIDByAccess:   make(map[string]string),
		sessionIDByRefresh:  make(map[string]string),
		registrationEnabled: true,
	}
}

func (repository *MemoryRepository) CreateUser(_ context.Context, user User) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	username := strings.ToLower(user.Username)
	if _, exists := repository.usersByID[user.UserID]; exists {
		return fmt.Errorf("%w: user id already exists", ErrConflict)
	}
	if _, exists := repository.userIDByUsername[username]; exists {
		return fmt.Errorf("%w: username already exists", ErrConflict)
	}
	if user.Role == RoleAdmin {
		for _, existing := range repository.usersByID {
			if existing.Role == RoleAdmin && existing.Status != StatusDeleted {
				return ErrAdminExists
			}
		}
	}
	if user.Role == "" {
		user.Role = RolePlayer
	}
	if user.Status == "" {
		user.Status = StatusActive
	}
	repository.usersByID[user.UserID] = user
	repository.userIDByUsername[username] = user.UserID
	return nil
}

func (repository *MemoryRepository) UserByID(_ context.Context, userID string) (User, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	user, exists := repository.usersByID[userID]
	if !exists || user.Status != StatusActive {
		return User{}, ErrNotFound
	}
	return user, nil
}

func (repository *MemoryRepository) UserByUsername(_ context.Context, username string) (User, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	userID, exists := repository.userIDByUsername[strings.ToLower(username)]
	if !exists {
		return User{}, ErrNotFound
	}
	user := repository.usersByID[userID]
	if user.Status != StatusActive {
		return User{}, ErrNotFound
	}
	return user, nil
}

func (repository *MemoryRepository) ListUsers(_ context.Context) ([]User, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	users := make([]User, 0, len(repository.usersByID))
	for _, user := range repository.usersByID {
		users = append(users, user)
	}
	sort.Slice(users, func(i, j int) bool {
		return users[i].CreatedAt.Before(users[j].CreatedAt)
	})
	return users, nil
}

func (repository *MemoryRepository) UpdateUsername(
	_ context.Context,
	userID, username string,
	_ time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	user, exists := repository.usersByID[userID]
	if !exists || user.Status == StatusDeleted {
		return ErrNotFound
	}
	normalized := strings.ToLower(username)
	if existingID, exists := repository.userIDByUsername[normalized]; exists && existingID != userID {
		return ErrConflict
	}
	delete(repository.userIDByUsername, strings.ToLower(user.Username))
	user.Username = username
	repository.usersByID[userID] = user
	repository.userIDByUsername[normalized] = userID
	return nil
}

func (repository *MemoryRepository) UpdateDisplayName(
	_ context.Context,
	userID, displayName string,
	_ time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	user, exists := repository.usersByID[userID]
	if !exists || user.Status == StatusDeleted {
		return ErrNotFound
	}
	user.DisplayName = displayName
	repository.usersByID[userID] = user
	return nil
}

func (repository *MemoryRepository) UpdatePassword(_ context.Context, userID, passwordHash string, _ time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	user, exists := repository.usersByID[userID]
	if !exists || user.Status == StatusDeleted {
		return ErrNotFound
	}
	user.PasswordHash = passwordHash
	repository.usersByID[userID] = user
	repository.deleteUserSessionsLocked(userID)
	return nil
}

func (repository *MemoryRepository) UpdateStatuses(_ context.Context, actorUserID string, userIDs []string, status Status, _ time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for _, userID := range userIDs {
		user, exists := repository.usersByID[userID]
		if !exists {
			return ErrNotFound
		}
		if userID == actorUserID || user.Role == RoleAdmin {
			return ErrProtected
		}
	}
	for _, userID := range userIDs {
		user := repository.usersByID[userID]
		user.Status = status
		repository.usersByID[userID] = user
		if status != StatusActive {
			repository.deleteUserSessionsLocked(userID)
		}
	}
	return nil
}

func (repository *MemoryRepository) MinimumClientVersion(_ context.Context) (int, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	return repository.minimumClientVersion, nil
}

func (repository *MemoryRepository) SetMinimumClientVersion(_ context.Context, _ string, version int, _ time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.minimumClientVersion = version
	return nil
}

func (repository *MemoryRepository) RegistrationEnabled(_ context.Context) (bool, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	return repository.registrationEnabled, nil
}

func (repository *MemoryRepository) SetRegistrationEnabled(_ context.Context, _ string, enabled bool, _ time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.registrationEnabled = enabled
	return nil
}

func (repository *MemoryRepository) RecordAudit(_ context.Context, event AuditEvent) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.auditEvents = append(repository.auditEvents, event)
	return nil
}

func (repository *MemoryRepository) SaveSession(_ context.Context, session Session) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if previous, exists := repository.sessionsByID[session.SessionID]; exists {
		delete(repository.sessionIDByAccess, previous.AccessTokenHash)
		delete(repository.sessionIDByRefresh, previous.RefreshTokenHash)
	}
	repository.sessionsByID[session.SessionID] = session
	repository.sessionIDByAccess[session.AccessTokenHash] = session.SessionID
	repository.sessionIDByRefresh[session.RefreshTokenHash] = session.SessionID
	return nil
}

func (repository *MemoryRepository) SessionByAccessHash(_ context.Context, tokenHash string) (Session, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	sessionID, exists := repository.sessionIDByAccess[tokenHash]
	if !exists {
		return Session{}, ErrNotFound
	}
	return repository.sessionsByID[sessionID], nil
}

func (repository *MemoryRepository) SessionByRefreshHash(_ context.Context, tokenHash string) (Session, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	sessionID, exists := repository.sessionIDByRefresh[tokenHash]
	if !exists {
		return Session{}, ErrNotFound
	}
	return repository.sessionsByID[sessionID], nil
}

func (repository *MemoryRepository) SelfDelete(
	_ context.Context,
	userID, anonymizedUsername, anonymizedDisplayName string,
	_ time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	user, exists := repository.usersByID[userID]
	if !exists || user.Status == StatusDeleted {
		return ErrNotFound
	}
	// 释放原用户名，使其可以被重新注册
	delete(repository.userIDByUsername, strings.ToLower(user.Username))
	user.Username = anonymizedUsername
	user.DisplayName = anonymizedDisplayName
	user.Status = StatusDeleted
	repository.usersByID[userID] = user
	repository.userIDByUsername[strings.ToLower(anonymizedUsername)] = userID
	repository.deleteUserSessionsLocked(userID)
	return nil
}

func (repository *MemoryRepository) DeleteSession(_ context.Context, sessionID string) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	session, exists := repository.sessionsByID[sessionID]
	if !exists {
		return nil
	}
	delete(repository.sessionsByID, sessionID)
	delete(repository.sessionIDByAccess, session.AccessTokenHash)
	delete(repository.sessionIDByRefresh, session.RefreshTokenHash)
	return nil
}

func (repository *MemoryRepository) deleteUserSessionsLocked(userID string) {
	for sessionID, session := range repository.sessionsByID {
		if session.UserID != userID {
			continue
		}
		delete(repository.sessionsByID, sessionID)
		delete(repository.sessionIDByAccess, session.AccessTokenHash)
		delete(repository.sessionIDByRefresh, session.RefreshTokenHash)
	}
}

// AuditQuery 描述审计查询条件。Limit 由服务层裁剪到合法范围。
type AuditQuery struct {
	UserID string
	Limit  int
}

// auditMatchesUser 判断事件是否与某用户相关：作为操作者、对象或筹码接收方。
func auditMatchesUser(event AuditEvent, userID string) bool {
	if userID == "" || event.ActorUserID == userID {
		return true
	}
	for _, key := range []string{"targetUserId", "recipientUserId"} {
		if value, _ := event.Metadata[key].(string); value == userID {
			return true
		}
	}
	switch targets := event.Metadata["targetUserIds"].(type) {
	case []string:
		for _, target := range targets {
			if target == userID {
				return true
			}
		}
	case []any:
		for _, target := range targets {
			if value, _ := target.(string); value == userID {
				return true
			}
		}
	}
	return false
}

func (repository *MemoryRepository) ListAudit(_ context.Context, query AuditQuery) ([]AuditEvent, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	result := make([]AuditEvent, 0, query.Limit)
	for index := len(repository.auditEvents) - 1; index >= 0 && len(result) < query.Limit; index-- {
		event := repository.auditEvents[index]
		if auditMatchesUser(event, query.UserID) {
			result = append(result, event)
		}
	}
	return result, nil
}
