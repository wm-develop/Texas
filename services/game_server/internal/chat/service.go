package chat

import (
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type Kind string

const (
	KindText      Kind = "text"
	KindQuickText Kind = "quick_text"
	KindEmoji     Kind = "emoji"
)

type Policy struct {
	MaximumRunes      int
	MaximumPerWindow  int
	RateWindow        time.Duration
	HistoryLimit      int
	AllowedQuickTexts map[string]struct{}
	AllowedEmoji      map[string]struct{}
}

type Sender struct {
	UserID      string
	DisplayName string
	TableID     string
	CanChat     bool
	Muted       bool
}

type Request struct {
	ClientMessageID string
	Kind            Kind
	Content         string
}

type Message struct {
	MessageID       string
	ClientMessageID string
	UserID          string
	DisplayName     string
	TableID         string
	Kind            Kind
	Content         string
	SentAt          time.Time
}

type Error struct {
	Code string
}

func (chatError Error) Error() string { return chatError.Code }

type Clock func() time.Time
type IDGenerator func() string

type Service struct {
	mu     sync.Mutex
	policy Policy
	now    Clock
	nextID IDGenerator
	store  Store
	recent map[string][]time.Time
}

func NewService(policy Policy, clock Clock, idGenerator IDGenerator) (*Service, error) {
	return NewServiceWithStore(policy, clock, idGenerator, nil)
}

func NewServiceWithStore(policy Policy, clock Clock, idGenerator IDGenerator, store Store) (*Service, error) {
	if policy.MaximumRunes <= 0 || policy.MaximumPerWindow <= 0 || policy.RateWindow <= 0 ||
		clock == nil || idGenerator == nil {
		return nil, errors.New("invalid chat service configuration")
	}
	if policy.HistoryLimit <= 0 {
		policy.HistoryLimit = 50
	}
	if store == nil {
		store = NewMemoryStore(policy.HistoryLimit)
	}
	return &Service{
		policy: policy,
		now:    clock,
		nextID: idGenerator,
		store:  store,
		recent: make(map[string][]time.Time),
	}, nil
}

func (service *Service) Send(sender Sender, request Request) (Message, error) {
	if sender.UserID == "" || sender.TableID == "" || !sender.CanChat {
		return Message{}, Error{Code: "permission_denied"}
	}
	if request.ClientMessageID == "" {
		return Message{}, Error{Code: "invalid_message"}
	}

	service.mu.Lock()
	defer service.mu.Unlock()
	if previous, exists, err := service.store.ByClientMessage(sender.TableID, sender.UserID, request.ClientMessageID); err != nil {
		return Message{}, err
	} else if exists {
		return previous, nil
	}
	muted, err := service.store.IsMuted(sender.UserID)
	if err != nil {
		return Message{}, err
	}
	if sender.Muted || muted {
		return Message{}, Error{Code: "chat_muted"}
	}

	content := strings.TrimSpace(request.Content)
	if err := service.validateContent(request.Kind, content); err != nil {
		return Message{}, err
	}

	now := service.now()
	rateKey := sender.TableID + "\x00" + sender.UserID
	cutoff := now.Add(-service.policy.RateWindow)
	timestamps := service.recent[rateKey][:0]
	for _, timestamp := range service.recent[rateKey] {
		if timestamp.After(cutoff) {
			timestamps = append(timestamps, timestamp)
		}
	}
	if len(timestamps) >= service.policy.MaximumPerWindow {
		service.recent[rateKey] = timestamps
		return Message{}, Error{Code: "rate_limited"}
	}
	service.recent[rateKey] = append(timestamps, now)

	messageID := service.nextID()
	if messageID == "" {
		return Message{}, errors.New("chat message id generator returned empty id")
	}
	message := Message{
		MessageID:       messageID,
		ClientMessageID: request.ClientMessageID,
		UserID:          sender.UserID,
		DisplayName:     sender.DisplayName,
		TableID:         sender.TableID,
		Kind:            request.Kind,
		Content:         content,
		SentAt:          now,
	}
	return service.store.Save(message)
}

func (service *Service) SetMuted(actorUserID, userID string, muted bool) error {
	actorUserID = strings.TrimSpace(actorUserID)
	userID = strings.TrimSpace(userID)
	if actorUserID == "" || userID == "" {
		return Error{Code: "invalid_user"}
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	auditEventID := service.nextID()
	if auditEventID == "" {
		return errors.New("chat moderation id generator returned empty id")
	}
	return service.store.SetMuted(ModerationChange{
		AuditEventID: moderationAuditID(auditEventID),
		ActorUserID:  actorUserID,
		TargetUserID: userID,
		Muted:        muted,
		ChangedAt:    service.now(),
	})
}

func (service *Service) IsMuted(userID string) (bool, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.store.IsMuted(strings.TrimSpace(userID))
}

func (service *Service) History(tableID string, limit int) []Message {
	service.mu.Lock()
	defer service.mu.Unlock()
	if limit <= 0 || limit > service.policy.HistoryLimit {
		limit = service.policy.HistoryLimit
	}
	history, err := service.store.History(tableID, limit)
	if err != nil {
		return nil
	}
	return history
}

func (service *Service) validateContent(kind Kind, content string) error {
	if content == "" || !utf8.ValidString(content) || utf8.RuneCountInString(content) > service.policy.MaximumRunes {
		return Error{Code: "content_rejected"}
	}
	for _, value := range content {
		if unicode.IsControl(value) {
			return Error{Code: "content_rejected"}
		}
	}
	switch kind {
	case KindText:
		return nil
	case KindQuickText:
		if _, allowed := service.policy.AllowedQuickTexts[content]; allowed {
			return nil
		}
	case KindEmoji:
		if _, allowed := service.policy.AllowedEmoji[content]; allowed {
			return nil
		}
	}
	return Error{Code: "content_rejected"}
}

func ShouldDeliver(senderUserID string, recipientUserID string, blockedByRecipient map[string]struct{}) bool {
	if senderUserID == "" || recipientUserID == "" {
		return false
	}
	_, blocked := blockedByRecipient[senderUserID]
	return !blocked
}

func moderationAuditID(value string) string {
	value = strings.TrimPrefix(value, "msg_")
	return "aud_chat_" + value
}
