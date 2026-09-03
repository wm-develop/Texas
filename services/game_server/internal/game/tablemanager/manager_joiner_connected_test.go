package tablemanager

import (
	"context"
	"testing"
	"time"

	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/room"
)

// 牌局进行中加入的玩家在结算前没有引擎座位，Join 时的 SetConnected 落空。
// 此前结算后入座一律写成断线，他会一直显示「已断线」，服务端自动准备也会
// 跳过他。现在按真实在线状态入座。
func TestMidHandJoinerIsSeatedAsConnected(t *testing.T) {
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.PresetStandard, 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.Join(ctx, room.Participant{UserID: "guest", DisplayName: "好友"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(9_000, 0)
	scheduled := make(map[time.Duration]func())
	manager, err := NewWithConfig(rooms, zeroRandom{}, ManagerConfig{
		Now: func() time.Time { return now },
		AfterFunc: func(duration time.Duration, callback func()) ScheduledTimer {
			scheduled[duration] = callback
			return &fakeScheduledTimer{}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{"owner", "guest"} {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	inHand := startHand(t, ctx, manager, []string{"owner", "guest"})

	// 第三人在牌局进行中加入房间并连上牌桌
	if _, err := rooms.Join(ctx, room.Participant{UserID: "third", DisplayName: "三号"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Join(ctx, "third", created.RoomID); err != nil {
		t.Fatal(err)
	}

	// 本手结算，第三人获得正式座位
	settled := submitFold(t, ctx, manager, created.RoomID, inHand, "fold-1")
	if settled.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("expected WAITING_NEXT_HAND, got %s", settled.Phase)
	}
	after, err := manager.Snapshot(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	var third *SeatSnapshot
	for index := range after.Seats {
		if after.Seats[index].UserID == "third" {
			third = &after.Seats[index]
		}
	}
	// 结算后他拿到正式座位；参与下一手要等发牌时才置位
	if third == nil {
		t.Fatalf("third should hold a real seat after settlement: %#v", after.Seats)
	}
	if !third.Connected {
		t.Fatalf("mid-hand joiner must be seated as connected, got %#v", *third)
	}

	// 服务端自动准备不能跳过他
	autoReady := scheduled[autoReadyDelay]
	if autoReady == nil {
		t.Fatal("auto-ready callback missing")
	}
	now = now.Add(autoReadyDelay)
	autoReady()
	next, err := manager.Snapshot(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if next.Phase != holdem.PhasePreflop {
		t.Fatalf("auto-ready should have started the next hand with the joiner, phase=%s", next.Phase)
	}
	var seated bool
	for _, seat := range next.Seats {
		if seat.UserID == "third" && seat.Participating {
			seated = true
		}
	}
	if !seated {
		t.Fatalf("joiner should participate in the next hand: %#v", next.Seats)
	}
}

// 真正断开的玩家在入座时仍应标为断线：修复不能反过来把离线的人当在线。
func TestMidHandJoinerWhoDisconnectedIsSeatedAsDisconnected(t *testing.T) {
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.PresetStandard, 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.Join(ctx, room.Participant{UserID: "guest", DisplayName: "好友"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	manager, err := New(rooms, zeroRandom{})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{"owner", "guest"} {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	inHand := startHand(t, ctx, manager, []string{"owner", "guest"})
	if _, err := rooms.Join(ctx, room.Participant{UserID: "third", DisplayName: "三号"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Join(ctx, "third", created.RoomID); err != nil {
		t.Fatal(err)
	}
	manager.Disconnect(ctx, "third", created.RoomID)

	submitFold(t, ctx, manager, created.RoomID, inHand, "fold-1")
	after, err := manager.Snapshot(ctx, "owner", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	for _, seat := range after.Seats {
		if seat.UserID == "third" && seat.Connected {
			t.Fatalf("a joiner who disconnected must not be seated as connected: %#v", seat)
		}
	}
}
