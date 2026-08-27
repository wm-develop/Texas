package room

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"testing"
	"time"

	"texas/services/game_server/internal/security"
)

func TestCreateJoinReadyVoiceAndOwnerTransfer(t *testing.T) {
	service := mustRoomService(t)
	ctx := context.Background()
	owner := Participant{UserID: "owner", DisplayName: "房主"}
	guest := Participant{UserID: "guest", DisplayName: "好友"}

	created, err := service.Create(ctx, owner, PresetStandard, 2, "room-pass")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.PasswordHash != "" || created.Rules.StartingChips != 2000 || len(created.Members) != 1 {
		t.Fatalf("created room=%#v", created)
	}
	if _, err := service.Join(ctx, guest, created.Code, "wrong-pass"); roomErrorCode(err) != "invalid_room_password" {
		t.Fatalf("wrong password error=%v", err)
	}
	joined, err := service.Join(ctx, guest, created.Code, "room-pass")
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(joined.Members) != 2 || joined.Members[1].Seat != 2 {
		t.Fatalf("joined room=%#v", joined)
	}
	if _, err := service.Join(ctx, Participant{UserID: "third", DisplayName: "第三人"}, created.Code, "room-pass"); roomErrorCode(err) != "room_full" {
		t.Fatalf("full room error=%v", err)
	}
	ready, err := service.SetReady(ctx, "guest", true)
	if err != nil || !ready.Members[1].Ready {
		t.Fatalf("SetReady room=%#v error=%v", ready, err)
	}
	allowed, err := service.CanJoinVoice(ctx, "guest", created.RoomID)
	if err != nil || !allowed {
		t.Fatalf("CanJoinVoice allowed=%v error=%v", allowed, err)
	}
	closed, err := service.Leave(ctx, "owner")
	if err != nil || closed {
		t.Fatalf("owner Leave closed=%v error=%v", closed, err)
	}
	transferred, err := service.Current(ctx, "guest")
	if err != nil || transferred.OwnerUserID != "guest" || len(transferred.Members) != 1 {
		t.Fatalf("transferred room=%#v error=%v", transferred, err)
	}
	closed, err = service.Leave(ctx, "guest")
	if err != nil || !closed {
		t.Fatalf("final owner Leave closed=%v error=%v", closed, err)
	}
}

func TestOwnerTransfersByJoinOrder(t *testing.T) {
	service := mustRoomService(t)
	ctx := context.Background()
	created, err := service.Create(ctx, Participant{UserID: "owner", DisplayName: "房主"}, PresetCasual, 4, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Join(ctx, Participant{UserID: "second", DisplayName: "第二位"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Join(ctx, Participant{UserID: "third", DisplayName: "第三位"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	if closed, err := service.Leave(ctx, "owner"); err != nil || closed {
		t.Fatalf("owner leave closed=%v err=%v", closed, err)
	}
	current, err := service.Current(ctx, "second")
	if err != nil || current.OwnerUserID != "second" {
		t.Fatalf("first transfer room=%#v err=%v", current, err)
	}
	if closed, err := service.Leave(ctx, "second"); err != nil || closed {
		t.Fatalf("second leave closed=%v err=%v", closed, err)
	}
	current, err = service.Current(ctx, "third")
	if err != nil || current.OwnerUserID != "third" {
		t.Fatalf("second transfer room=%#v err=%v", current, err)
	}
}

func TestMemberLeaveKeepsRoom(t *testing.T) {
	service := mustRoomService(t)
	ctx := context.Background()
	created, err := service.Create(ctx, Participant{UserID: "owner", DisplayName: "房主"}, PresetCasual, 3, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := service.Join(ctx, Participant{UserID: "guest", DisplayName: "好友"}, created.Code, ""); err != nil {
		t.Fatalf("Join: %v", err)
	}
	closed, err := service.Leave(ctx, "guest")
	if err != nil || closed {
		t.Fatalf("guest Leave closed=%v error=%v", closed, err)
	}
	current, err := service.Current(ctx, "owner")
	if err != nil || len(current.Members) != 1 {
		t.Fatalf("current room=%#v error=%v", current, err)
	}
}

func TestBlindLevelHasMinimumAndUsesSmallBlindDenomination(t *testing.T) {
	tests := []struct {
		small int64
		big   int64
		valid bool
	}{
		{10, 20, true},
		{25, 50, true},
		{10, 30, true},
		{5, 20, false},
		{10, 10, false},
		{10, 15, false},
		{20, 30, false},
	}
	for _, test := range tests {
		if actual := validBlindLevel(test.small, test.big); actual != test.valid {
			t.Fatalf("validBlindLevel(%d, %d)=%v want=%v", test.small, test.big, actual, test.valid)
		}
	}
	rules, ok := rulesForPreset(PresetCasual)
	if !ok || rules.SmallBlind != 10 || rules.BigBlind != 20 {
		t.Fatalf("casual rules=%#v ok=%v", rules, ok)
	}
}

func mustRoomService(t *testing.T) *Service {
	t.Helper()
	hasher, err := security.NewPasswordHasher(1_000, cryptorand.Reader)
	if err != nil {
		t.Fatalf("NewPasswordHasher: %v", err)
	}
	service, err := NewService(NewMemoryRepository(), hasher, ServiceConfig{
		Now:    func() time.Time { return time.Unix(1_000, 0) },
		Random: cryptorand.Reader,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service
}

func roomErrorCode(err error) string {
	var roomError Error
	if errors.As(err, &roomError) {
		return roomError.Code
	}
	return ""
}
