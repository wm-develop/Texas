package tablemanager

import (
	"context"
	"testing"
	"time"

	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/room"
)

// 看牌权按手计：付不起的那一手看不到，补上筹码后下一手恢复。
func TestSpectatorAccessIsGrantedPerHand(t *testing.T) {
	ctx := context.Background()
	manager, rooms, created := spectatorFixture(t, 3, "owner", "guest", "third")
	fee := int64(room.DefaultSpectatorFeeBigBlinds) * created.Rules.BigBlind
	if _, err := manager.EnterSpectate(ctx, "third", created.RoomID); err != nil {
		t.Fatal(err)
	}
	// 只够付一手
	if _, err := rooms.SetStack(ctx, created.RoomID, "third", fee); err != nil {
		t.Fatal(err)
	}
	first := startHand(t, ctx, manager, []string{"owner", "guest"})
	paid, err := manager.Snapshot(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if third := spectatorOf(paid, "third"); third == nil || !third.CanSeeHoleCards || third.Stack != 0 {
		t.Fatalf("first hand should be paid for: %#v", third)
	}
	settled := submitFold(t, ctx, manager, created.RoomID, first, "fold-1")
	if settled.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("hand did not settle: %s", settled.Phase)
	}
	// 结算展示期间仍保留本手的看牌权与明细
	if third := spectatorOf(settled, "third"); third == nil || !third.CanSeeHoleCards {
		t.Fatalf("access must last through the settlement of the paid hand: %#v", third)
	}
	if settled.SpectatorFees == nil {
		t.Fatal("fee record must still be shown while the settled hand is on screen")
	}

	startHand(t, ctx, manager, []string{"owner", "guest"})
	second, err := manager.Snapshot(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if third := spectatorOf(second, "third"); third == nil || third.CanSeeHoleCards {
		t.Fatalf("with no chips left the next hand must not be visible: %#v", third)
	}
	if second.SpectatorFees != nil {
		t.Fatalf("no one paid this hand, record must be cleared: %#v", second.SpectatorFees)
	}
	for _, seat := range second.Seats {
		if len(seat.HoleCards) != 0 {
			t.Fatalf("unpaid hand must not leak hole cards: %#v", seat)
		}
	}
}

// 牌局进行中才转成观战的人（本手不参与）不会在本手中途获得看牌权：
// 费用只在开局时收，他没付过。
func TestSpectatorJoiningMidHandWaitsForNextHand(t *testing.T) {
	ctx := context.Background()
	manager, rooms, created := spectatorFixture(t, 3, "owner", "guest")
	inHand := startHand(t, ctx, manager, []string{"owner", "guest"})
	if _, err := rooms.Join(ctx, room.Participant{UserID: "third", DisplayName: "玩家third"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Join(ctx, "third", created.RoomID); err != nil {
		t.Fatal(err)
	}
	// 中途加入者还没参与本手，进观战立即生效
	snapshot, err := manager.EnterSpectate(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Spectating {
		t.Fatal("a non-participating member may spectate at once")
	}
	if third := spectatorOf(snapshot, "third"); third == nil || third.CanSeeHoleCards {
		t.Fatalf("no fee was paid for the running hand, so no access: %#v", third)
	}
	for _, seat := range snapshot.Seats {
		if len(seat.HoleCards) != 0 {
			t.Fatalf("mid-hand spectator must not see cards of the hand they did not pay for: %#v", seat)
		}
	}
	submitFold(t, ctx, manager, created.RoomID, inHand, "fold-1")
	startHand(t, ctx, manager, []string{"owner", "guest"})
	next, err := manager.Snapshot(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if third := spectatorOf(next, "third"); third == nil || !third.CanSeeHoleCards {
		t.Fatalf("from the next hand on the spectator pays and sees: %#v", third)
	}
}

// 房主进观战仍是房主；所有人都进观战时牌桌为空、无法开局，但不能崩。
func TestOwnerSpectatesAndEmptyTableIsStable(t *testing.T) {
	ctx := context.Background()
	manager, _, created := spectatorFixture(t, 3, "owner", "guest")
	for _, userID := range []string{"owner", "guest"} {
		if _, err := manager.EnterSpectate(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := manager.Snapshot(ctx, "owner", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.OwnerUserID != "owner" {
		t.Fatalf("ownership must survive spectating: %s", snapshot.OwnerUserID)
	}
	if len(snapshot.Seats) != 0 || len(snapshot.Spectators) != 2 {
		t.Fatalf("everyone is spectating: seats=%d spectators=%d", len(snapshot.Seats), len(snapshot.Spectators))
	}
	if _, err := manager.SetReady(ctx, "owner", true); err == nil {
		t.Fatal("nobody can ready up from the spectator bench")
	}
}

// 观战者不能发起私下看牌申请——他要看牌只有付费这一条路。
func TestSpectatorCannotRequestPrivateHoleCardView(t *testing.T) {
	ctx := context.Background()
	manager, _, created := spectatorFixture(t, 3, "owner", "guest", "third")
	if _, err := manager.EnterSpectate(ctx, "third", created.RoomID); err != nil {
		t.Fatal(err)
	}
	startHand(t, ctx, manager, []string{"owner", "guest"})
	if _, err := manager.RequestHoleCardView(ctx, "third", created.RoomID, "owner", "view-1"); err == nil {
		t.Fatal("spectators must not be able to request a private view")
	}
}

// 观战者断线与重连只影响在线标记，不影响身份与筹码。
func TestSpectatorDisconnectAndReconnect(t *testing.T) {
	ctx := context.Background()
	manager, _, created := spectatorFixture(t, 3, "owner", "guest", "third")
	if _, err := manager.EnterSpectate(ctx, "third", created.RoomID); err != nil {
		t.Fatal(err)
	}
	manager.Disconnect(ctx, "third", created.RoomID)
	offline, err := manager.Snapshot(ctx, "owner", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if third := spectatorOf(offline, "third"); third == nil || third.Connected {
		t.Fatalf("disconnected spectator must show as offline but stay listed: %#v", third)
	}
	online, err := manager.Join(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if third := spectatorOf(online, "third"); third == nil || !third.Connected || !online.Spectating {
		t.Fatalf("reconnecting must restore the spectator as online: %#v", third)
	}
}

// 带着切换意向离开房间：意向随人一起消失，手结束时不能因为找不到人而出错。
func TestLeavingWithPendingIntentsIsHarmless(t *testing.T) {
	ctx := context.Background()
	manager, rooms, created := spectatorFixture(t, 4, "owner", "guest", "third", "fourth")
	if _, err := manager.EnterSpectate(ctx, "fourth", created.RoomID); err != nil {
		t.Fatal(err)
	}
	inHand := startHand(t, ctx, manager, []string{"owner", "guest", "third"})

	// 观战者申请上桌后直接离开房间
	if _, err := manager.TakeSeat(ctx, "fourth", created.RoomID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Leave(ctx, "fourth"); err != nil {
		t.Fatal(err)
	}
	// 一名参与者申请观战，随后弃牌并离开房间（弃牌者可以中途离开）。
	// 上面的几步都推进了牌桌版本号，动作前要拿最新快照。
	actor := inHand.CurrentAction.UserID
	// 行动者可能正是房主；他离开后不再是成员，后面的查看要换一个留下的人
	viewer := "owner"
	if actor == viewer {
		viewer = "guest"
	}
	snapshot, err := manager.EnterSpectate(ctx, actor, created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot = submitFold(t, ctx, manager, created.RoomID, snapshot, "fold-1")
	if _, err := manager.Leave(ctx, actor); err != nil {
		t.Fatalf("a folded player may leave mid-hand: %v", err)
	}
	if snapshot, err = manager.Snapshot(ctx, viewer, created.RoomID); err != nil {
		t.Fatal(err)
	}
	for round := 0; snapshot.Phase != holdem.PhaseWaitingNextHand && round < 6; round++ {
		snapshot = submitFold(t, ctx, manager, created.RoomID, snapshot, "fold")
	}
	if snapshot.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("hand did not settle: %s", snapshot.Phase)
	}
	after, err := manager.Snapshot(ctx, viewer, created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if spectatorOf(after, "fourth") != nil || seatOf(after, "fourth") != nil ||
		spectatorOf(after, actor) != nil || seatOf(after, actor) != nil {
		t.Fatalf("members who left must be gone entirely: seats=%#v spectators=%#v", after.Seats, after.Spectators)
	}
	if current, err := rooms.Current(ctx, viewer); err != nil || len(current.Members) != 2 {
		t.Fatalf("two members should remain: %v", err)
	}
}

// 手间上桌时座位已满：立即拒绝，不排队。
func TestTakeSeatBetweenHandsWhenFullIsRefused(t *testing.T) {
	ctx := context.Background()
	manager, rooms, created := spectatorFixture(t, 2, "owner", "guest")
	if _, err := manager.EnterSpectate(ctx, "guest", created.RoomID); err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.Join(ctx, room.Participant{UserID: "third", DisplayName: "玩家third"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Join(ctx, "third", created.RoomID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.TakeSeat(ctx, "guest", created.RoomID); err == nil {
		t.Fatal("no free seat: taking a seat must be refused at once")
	}
}

// 服务端自动准备只针对上桌玩家；观战者在场不阻碍下一手自动开始。
func TestAutoReadySkipsSpectators(t *testing.T) {
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.PresetStandard, 3, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{"guest", "third"} {
		if _, err := rooms.Join(ctx, room.Participant{UserID: userID, DisplayName: "玩家" + userID}, created.Code, ""); err != nil {
			t.Fatal(err)
		}
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
	for _, userID := range []string{"owner", "guest", "third"} {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := manager.EnterSpectate(ctx, "third", created.RoomID); err != nil {
		t.Fatal(err)
	}
	inHand := startHand(t, ctx, manager, []string{"owner", "guest"})
	settled := submitFold(t, ctx, manager, created.RoomID, inHand, "fold-1")
	if settled.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("hand did not settle: %s", settled.Phase)
	}
	autoReady := scheduled[autoReadyDelay]
	if autoReady == nil {
		t.Fatal("auto-ready must be scheduled")
	}
	now = now.Add(autoReadyDelay)
	autoReady()
	next, err := manager.Snapshot(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if next.Phase != holdem.PhasePreflop {
		t.Fatalf("auto-ready should start the next hand without the spectator, phase=%s", next.Phase)
	}
	if third := spectatorOf(next, "third"); third == nil || !third.CanSeeHoleCards {
		t.Fatalf("the spectator pays again and sees the auto-started hand: %#v", third)
	}
}

// 快照带上看牌费的筹码数：真机上带入 100 个大盲的观战者一进观战就被提示
// 「筹码不足」，因为客户端把「手间还没发放看牌权」当成了「付不起」。
func TestSnapshotCarriesSpectatorFeeAmount(t *testing.T) {
	ctx := context.Background()
	manager, rooms, created := spectatorFixture(t, 3, "owner", "guest", "third")
	if _, err := manager.EnterSpectate(ctx, "third", created.RoomID); err != nil {
		t.Fatal(err)
	}
	between, err := manager.Snapshot(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	want := int64(room.DefaultSpectatorFeeBigBlinds) * created.Rules.BigBlind
	if between.SpectatorFee != want {
		t.Fatalf("fee amount should be %d between hands, got %d", want, between.SpectatorFee)
	}
	// 手间没人付过费：看不到牌，但筹码充足，客户端不得据此提示不足
	if third := spectatorOf(between, "third"); third == nil || third.CanSeeHoleCards || third.Stack < want {
		t.Fatalf("between hands access is not granted yet while chips are plentiful: %#v", third)
	}
	free := room.DefaultSpectatorSettings()
	free.FeeBigBlinds = 0
	if _, err := rooms.UpdateSpectatorSettings(ctx, "owner", free); err != nil {
		t.Fatal(err)
	}
	freeSnapshot, err := manager.Snapshot(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if freeSnapshot.SpectatorFee != 0 {
		t.Fatalf("free mode must report a zero fee, got %d", freeSnapshot.SpectatorFee)
	}
}
