package room

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"testing"
	"time"

	"texas/services/game_server/internal/security"
)

func TestCreateJoinReadyVoiceAndOwnerClose(t *testing.T) {
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
	if err != nil || !closed {
		t.Fatalf("owner Leave closed=%v error=%v", closed, err)
	}
	if _, err := service.Current(ctx, "guest"); roomErrorCode(err) != "room_not_found" {
		t.Fatalf("closed room remained: %v", err)
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
