package room

import (
	"context"
	"testing"
)

func spectatorRoom(t *testing.T, maxPlayers int, userIDs ...string) (*Service, Room) {
	t.Helper()
	ctx := context.Background()
	service := mustRoomService(t)
	created, err := service.Create(ctx, Participant{UserID: userIDs[0], DisplayName: "玩家" + userIDs[0]}, PresetStandard, maxPlayers, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range userIDs[1:] {
		if _, err := service.Join(ctx, Participant{UserID: userID, DisplayName: "玩家" + userID}, created.Code, ""); err != nil {
			t.Fatal(err)
		}
	}
	return service, created
}

func memberOf(value Room, userID string) Member {
	for _, member := range value.Members {
		if member.UserID == userID {
			return member
		}
	}
	return Member{}
}

func TestEnterSpectateReleasesSeatAndKeepsChips(t *testing.T) {
	ctx := context.Background()
	service, created := spectatorRoom(t, 3, "owner", "guest")
	if _, err := service.SetReady(ctx, "guest", true); err != nil {
		t.Fatal(err)
	}

	updated, err := service.EnterSpectate(ctx, "guest")
	if err != nil {
		t.Fatal(err)
	}
	guest := memberOf(updated, "guest")
	if !guest.Spectating || guest.Seat != 0 || guest.Ready {
		t.Fatalf("spectator must have no seat and no ready state: %#v", guest)
	}
	if guest.Stack != created.Rules.StartingChips {
		t.Fatalf("chips must follow the member into spectating: %#v", guest)
	}
	if len(updated.SeatedMembers()) != 1 || len(updated.SpectatorMembers()) != 1 {
		t.Fatalf("one seated, one spectating: %#v", updated.Members)
	}
	// 再次进入观战是幂等的
	again, err := service.EnterSpectate(ctx, "guest")
	if err != nil || again.Revision != updated.Revision {
		t.Fatalf("entering spectate twice must be a no-op: err=%v", err)
	}
}

func TestTakeSeatUsesFirstFreeSeatAndRefusesWhenFull(t *testing.T) {
	ctx := context.Background()
	service, _ := spectatorRoom(t, 2, "owner", "guest")
	if _, err := service.EnterSpectate(ctx, "guest"); err != nil {
		t.Fatal(err)
	}
	// 座位 2 空出来了，第三人可以坐进去：观战者不占座位，不阻止新人加入
	created, _ := service.Current(ctx, "owner")
	if _, err := service.Join(ctx, Participant{UserID: "third", DisplayName: "玩家third"}, created.Code, ""); err != nil {
		t.Fatalf("a spectator must not block newcomers from taking the free seat: %v", err)
	}
	// 现在两个座位都满了，观战者上桌被拒
	if _, err := service.TakeSeat(ctx, "guest"); err == nil {
		t.Fatal("taking a seat at a full table must be refused")
	}
	// 第三人离开后座位空出，上桌成功并拿到那个座位
	if _, err := service.Leave(ctx, "third"); err != nil {
		t.Fatal(err)
	}
	updated, err := service.TakeSeat(ctx, "guest")
	if err != nil {
		t.Fatal(err)
	}
	guest := memberOf(updated, "guest")
	if guest.Spectating || guest.Seat != 2 || guest.Ready {
		t.Fatalf("guest should sit at seat 2, not ready: %#v", guest)
	}
	// 已经在座位上时再上桌是幂等的
	if _, err := service.TakeSeat(ctx, "guest"); err != nil {
		t.Fatal(err)
	}
}

func TestSpectatorReadinessAndSeatMoves(t *testing.T) {
	ctx := context.Background()
	service, _ := spectatorRoom(t, 3, "owner", "guest")
	if _, err := service.EnterSpectate(ctx, "guest"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetReady(ctx, "guest", true); err == nil {
		t.Fatal("a spectator must not be able to ready up")
	}
	// 取消准备静默接受：结算后统一重置会对所有成员调用
	if _, err := service.SetReady(ctx, "guest", false); err != nil {
		t.Fatalf("clearing ready on a spectator must not fail: %v", err)
	}
	if _, err := service.MoveSeat(ctx, "guest", 3); err == nil {
		t.Fatal("a spectator must not move seats")
	}
}

func TestSpectatorSettingsValidationAndOwnership(t *testing.T) {
	ctx := context.Background()
	service, created := spectatorRoom(t, 3, "owner", "guest")
	if created.Spectator != DefaultSpectatorSettings() {
		t.Fatalf("new rooms must start with the default spectator settings: %#v", created.Spectator)
	}
	if _, err := service.UpdateSpectatorSettings(ctx, "guest", DefaultSpectatorSettings()); err == nil {
		t.Fatal("only the owner may change spectator settings")
	}
	tooHigh := DefaultSpectatorSettings()
	tooHigh.FeeBigBlinds = MaximumSpectatorFeeBigBlinds + 1
	if _, err := service.UpdateSpectatorSettings(ctx, "owner", tooHigh); err == nil {
		t.Fatal("fee above the maximum must be refused")
	}
	negative := DefaultSpectatorSettings()
	negative.FeeBigBlinds = -1
	if _, err := service.UpdateSpectatorSettings(ctx, "owner", negative); err == nil {
		t.Fatal("negative fee must be refused")
	}
	wanted := SpectatorSettings{FeeBigBlinds: 0, VoiceAllowed: false, ChatAllowed: true, EmoteAllowed: false}
	updated, err := service.UpdateSpectatorSettings(ctx, "owner", wanted)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Spectator != wanted {
		t.Fatalf("settings not applied: %#v", updated.Spectator)
	}
	// 相同设置再提交不推进版本号
	same, err := service.UpdateSpectatorSettings(ctx, "owner", wanted)
	if err != nil || same.Revision != updated.Revision {
		t.Fatalf("re-applying identical settings must be a no-op: err=%v", err)
	}
}

func TestPreviewAndFullnessCountSeatedMembersOnly(t *testing.T) {
	ctx := context.Background()
	service, created := spectatorRoom(t, 2, "owner", "guest")
	if _, err := service.EnterSpectate(ctx, "guest"); err != nil {
		t.Fatal(err)
	}
	preview, err := service.Preview(ctx, created.Code)
	if err != nil {
		t.Fatal(err)
	}
	if preview.CurrentPlayers != 1 {
		t.Fatalf("preview must count seated players only, got %d", preview.CurrentPlayers)
	}
}

func TestSpectatorCapAtRoomLevel(t *testing.T) {
	ctx := context.Background()
	users := make([]string, 0, MaximumSpectators+1)
	for index := 0; index <= MaximumSpectators; index++ {
		users = append(users, "u"+string(rune('a'+index)))
	}
	// 11 人的房间：10 人先进观战，第 11 人再想进就该被拒
	service, _ := spectatorRoom(t, MaximumPlayers, users[:MaximumPlayers]...)
	for _, userID := range users[:MaximumPlayers] {
		if _, err := service.EnterSpectate(ctx, userID); err != nil {
			t.Fatalf("spectator %s: %v", userID, err)
		}
	}
	created, _ := service.Current(ctx, users[0])
	if _, err := service.Join(ctx, Participant{UserID: users[MaximumPlayers], DisplayName: "玩家"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := service.EnterSpectate(ctx, users[MaximumPlayers]); err == nil {
		t.Fatal("the 11th spectator must be refused")
	}
}

func TestOwnerWhoSpectatesRemainsOwner(t *testing.T) {
	ctx := context.Background()
	service, _ := spectatorRoom(t, 3, "owner", "guest")
	updated, err := service.EnterSpectate(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if updated.OwnerUserID != "owner" {
		t.Fatalf("spectating must not transfer ownership: %s", updated.OwnerUserID)
	}
}
