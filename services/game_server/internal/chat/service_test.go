package chat

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestSendValidatesPermissionContentAndIdempotency(t *testing.T) {
	now := time.Unix(100, 0)
	nextID := 0
	service := mustService(t, &now, &nextID)
	sender := Sender{UserID: "user_1", DisplayName: "玩家一", TableID: "table_1", CanChat: true}
	request := Request{ClientMessageID: "local_1", Kind: KindText, Content: "  好牌！  "}

	first, err := service.Send(sender, request)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	second, err := service.Send(sender, request)
	if err != nil {
		t.Fatalf("duplicate Send: %v", err)
	}
	if first != second || first.Content != "好牌！" || nextID != 1 {
		t.Fatalf("first=%#v second=%#v ids=%d", first, second, nextID)
	}

	_, err = service.Send(Sender{UserID: "outsider", TableID: "table_1"}, Request{
		ClientMessageID: "denied", Kind: KindText, Content: "hello",
	})
	assertChatCode(t, err, "permission_denied")

	_, err = service.Send(Sender{UserID: "muted", TableID: "table_1", CanChat: true, Muted: true}, Request{
		ClientMessageID: "muted", Kind: KindText, Content: "hello",
	})
	assertChatCode(t, err, "chat_muted")
}

func TestSendEnforcesRuneLimitQuickTextAndEmojiAllowLists(t *testing.T) {
	now := time.Unix(100, 0)
	nextID := 0
	service := mustService(t, &now, &nextID)
	sender := Sender{UserID: "user_1", TableID: "table_1", CanChat: true}

	requests := []Request{
		{ClientMessageID: "long", Kind: KindText, Content: "一二三四五六七八九"},
		{ClientMessageID: "control", Kind: KindText, Content: "line\nbreak"},
		{ClientMessageID: "quick", Kind: KindQuickText, Content: "未登记快捷语"},
		{ClientMessageID: "emoji", Kind: KindEmoji, Content: "🃏"},
	}
	for _, request := range requests {
		_, err := service.Send(sender, request)
		assertChatCode(t, err, "content_rejected")
	}
	if _, err := service.Send(sender, Request{ClientMessageID: "ok-quick", Kind: KindQuickText, Content: "好牌"}); err != nil {
		t.Fatalf("allowed quick text: %v", err)
	}
	if _, err := service.Send(sender, Request{ClientMessageID: "ok-emoji", Kind: KindEmoji, Content: "👍"}); err != nil {
		t.Fatalf("allowed emoji: %v", err)
	}
}

func TestSendRateLimitsPerUserAndTable(t *testing.T) {
	now := time.Unix(100, 0)
	nextID := 0
	service := mustService(t, &now, &nextID)
	sender := Sender{UserID: "user_1", TableID: "table_1", CanChat: true}

	for index := 0; index < 3; index++ {
		_, err := service.Send(sender, Request{
			ClientMessageID: fmt.Sprintf("message_%d", index), Kind: KindText, Content: "hello",
		})
		if err != nil {
			t.Fatalf("Send %d: %v", index, err)
		}
	}
	_, err := service.Send(sender, Request{ClientMessageID: "limited", Kind: KindText, Content: "hello"})
	assertChatCode(t, err, "rate_limited")

	now = now.Add(11 * time.Second)
	if _, err := service.Send(sender, Request{ClientMessageID: "after-window", Kind: KindText, Content: "hello"}); err != nil {
		t.Fatalf("Send after window: %v", err)
	}
}

func TestShouldDeliverHonorsRecipientBlock(t *testing.T) {
	blocked := map[string]struct{}{"blocked_sender": {}}
	if ShouldDeliver("blocked_sender", "recipient", blocked) {
		t.Fatal("blocked sender should not be delivered")
	}
	if !ShouldDeliver("friend", "recipient", blocked) {
		t.Fatal("unblocked sender should be delivered")
	}
}

func TestServerMuteStateIsEnforcedUntilCleared(t *testing.T) {
	now := time.Unix(100, 0)
	nextID := 0
	store := NewMemoryStore(50)
	service, err := NewServiceWithStore(Policy{
		MaximumRunes: 8, MaximumPerWindow: 3, RateWindow: 10 * time.Second,
	}, func() time.Time { return now }, func() string {
		nextID++
		return fmt.Sprintf("message_%d", nextID)
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetMuted("admin_1", "user_1", true); err != nil {
		t.Fatalf("SetMuted: %v", err)
	}
	if muted, err := service.IsMuted("user_1"); err != nil || !muted {
		t.Fatalf("IsMuted=%v err=%v", muted, err)
	}
	_, err = service.Send(Sender{UserID: "user_1", TableID: "table_1", CanChat: true}, Request{
		ClientMessageID: "muted-by-service", Kind: KindText, Content: "hello",
	})
	assertChatCode(t, err, "chat_muted")
	if err := service.SetMuted("admin_1", "user_1", false); err != nil {
		t.Fatalf("clear mute: %v", err)
	}
	if muted, err := service.IsMuted("user_1"); err != nil || muted {
		t.Fatalf("IsMuted after clear=%v err=%v", muted, err)
	}
	if len(store.changes) != 2 || !store.changes[0].Muted || store.changes[1].Muted ||
		store.changes[0].ActorUserID != "admin_1" {
		t.Fatalf("moderation changes=%#v", store.changes)
	}
	if _, err := service.Send(Sender{UserID: "user_1", TableID: "table_1", CanChat: true}, Request{
		ClientMessageID: "unmuted", Kind: KindText, Content: "hello",
	}); err != nil {
		t.Fatalf("Send after clearing mute: %v", err)
	}
}

func TestHistoryKeepsOnlyConfiguredRecentMessages(t *testing.T) {
	now := time.Unix(100, 0)
	nextID := 0
	service := mustService(t, &now, &nextID)
	service.policy.HistoryLimit = 2
	sender := Sender{UserID: "user_1", TableID: "table_1", CanChat: true}
	for index := 0; index < 3; index++ {
		_, err := service.Send(sender, Request{
			ClientMessageID: fmt.Sprintf("history_%d", index), Kind: KindText, Content: "hello",
		})
		if err != nil {
			t.Fatalf("Send %d: %v", index, err)
		}
	}
	history := service.History("table_1", 10)
	if len(history) != 2 || history[0].ClientMessageID != "history_1" || history[1].ClientMessageID != "history_2" {
		t.Fatalf("history=%#v", history)
	}
}

func mustService(t *testing.T, now *time.Time, nextID *int) *Service {
	t.Helper()
	service, err := NewService(Policy{
		MaximumRunes:      8,
		MaximumPerWindow:  3,
		RateWindow:        10 * time.Second,
		AllowedQuickTexts: map[string]struct{}{"好牌": {}},
		AllowedEmoji:      map[string]struct{}{"👍": {}},
	}, func() time.Time { return *now }, func() string {
		*nextID++
		return fmt.Sprintf("message_%d", *nextID)
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service
}

func assertChatCode(t *testing.T, err error, code string) {
	t.Helper()
	var chatError Error
	if !errors.As(err, &chatError) || chatError.Code != code {
		t.Fatalf("error=%v, want %s", err, code)
	}
}
