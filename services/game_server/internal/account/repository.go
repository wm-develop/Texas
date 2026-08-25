package account

import (
	"context"
	"errors"
	"strings"
	"sync"
)

var ErrNotFound = errors.New("not found")

type Repository interface {
	CreateUser(ctx context.Context, user User) error
	UserByID(ctx context.Context, userID string) (User, error)
	UserByUsername(ctx context.Context, normalizedUsername string) (User, error)
	SaveSession(ctx context.Context, session Session) error
	SessionByAccessHash(ctx context.Context, tokenHash string) (Session, error)
	SessionByRefreshHash(ctx context.Context, tokenHash string) (Session, error)
	DeleteSession(ctx context.Context, sessionID string) error
}

type MemoryRepository struct {
	mu                 sync.RWMutex
	usersByID          map[string]User
	userIDByUsername   map[string]string
	sessionsByID       map[string]Session
	sessionIDByAccess  map[string]string
	sessionIDByRefresh map[string]string
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		usersByID:          make(map[string]User),
		userIDByUsername:   make(map[string]string),
		sessionsByID:       make(map[string]Session),
		sessionIDByAccess:  make(map[string]string),
		sessionIDByRefresh: make(map[string]string),
	}
}

func (repository *MemoryRepository) CreateUser(_ context.Context, user User) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	username := strings.ToLower(user.Username)
	if _, exists := repository.usersByID[user.UserID]; exists {
		return errors.New("user id already exists")
	}
	if _, exists := repository.userIDByUsername[username]; exists {
		return errors.New("username already exists")
	}
	repository.usersByID[user.UserID] = user
	repository.userIDByUsername[username] = user.UserID
	return nil
}

func (repository *MemoryRepository) UserByID(_ context.Context, userID string) (User, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	user, exists := repository.usersByID[userID]
	if !exists {
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
	return repository.usersByID[userID], nil
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
