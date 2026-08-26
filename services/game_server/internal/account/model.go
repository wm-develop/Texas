package account

import "time"

type Role string

const (
	RolePlayer Role = "player"
	RoleAdmin  Role = "admin"
)

type Status string

const (
	StatusActive    Status = "active"
	StatusSuspended Status = "suspended"
	StatusDeleted   Status = "deleted"
)

type User struct {
	UserID       string    `json:"userId"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"displayName"`
	Role         Role      `json:"role"`
	Status       Status    `json:"status"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"createdAt"`
}

type RegistrationOptions struct {
	RequestInitialAdmin bool
}

type RegistrationSettings struct {
	Enabled bool `json:"enabled"`
}

type AuditEvent struct {
	EventID     string         `json:"eventId"`
	ActorUserID string         `json:"actorUserId"`
	EventType   string         `json:"eventType"`
	Metadata    map[string]any `json:"metadata"`
	CreatedAt   time.Time      `json:"createdAt"`
}

type Session struct {
	SessionID        string
	UserID           string
	AccessTokenHash  string
	RefreshTokenHash string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
	CreatedAt        time.Time
}

type AuthResult struct {
	User             User      `json:"user"`
	AccessToken      string    `json:"accessToken"`
	RefreshToken     string    `json:"refreshToken"`
	AccessExpiresAt  time.Time `json:"accessExpiresAt"`
	RefreshExpiresAt time.Time `json:"refreshExpiresAt"`
}

type Error struct {
	Code string
}

func (accountError Error) Error() string { return accountError.Code }
