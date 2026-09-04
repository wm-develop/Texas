package tablemanager

import (
	"context"
	"testing"

	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/room"
)

// 建一个 maxPlayers 人的房间，所有人都加入房间并连上牌桌。
func spectatorFixture(t *testing.T, maxPlayers int, userIDs ...string) (*Manager, *room.Service, room.Room) {
	t.Helper()
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: userIDs[0], DisplayName: "玩家" + userIDs[0]}, room.PresetStandard, maxPlayers, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range userIDs[1:] {
		if _, err := rooms.Join(ctx, room.Participant{UserID: userID, DisplayName: "玩家" + userID}, created.Code, ""); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := New(rooms, zeroRandom{})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range userIDs {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	return manager, rooms, created
}

func spectatorOf(snapshot Snapshot, userID string) *SpectatorSnapshot {
	for index := range snapshot.Spectators {
		if snapshot.Spectators[index].UserID == userID {
			return &snapshot.Spectators[index]
		}
	}
	return nil
}

func seatOf(snapshot Snapshot, userID string) *SeatSnapshot {
	for index := range snapshot.Seats {
		if snapshot.Seats[index].UserID == userID {
			return &snapshot.Seats[index]
		}
	}
	return nil
}

// totalChipsOnTable 把桌上所有筹码加起来。牌局进行中投入的部分还在
// TotalBet 里；结算后底池已回到赢家的 Stack，而 TotalBet 要到下一次发牌才
// 清零，此时再加就会重复计算。
func totalChipsOnTable(snapshot Snapshot) int64 {
	inHand := snapshot.Phase != holdem.PhaseWaiting && snapshot.Phase != holdem.PhaseWaitingNextHand
	var total int64
	for _, seat := range snapshot.Seats {
		total += seat.Stack
		if inHand {
			total += seat.TotalBet
		}
	}
	for _, spectator := range snapshot.Spectators {
		total += spectator.Stack
	}
	return total
}

// 看牌费在发牌前从观战者转给即将参与本手的玩家：总量不变，结算守恒仍成立，
// 付费的观战者能看到所有人的手牌，而上桌玩家永远看不到别人的。
func TestSpectatorFeeIsSplitAndChipsAreConserved(t *testing.T) {
	ctx := context.Background()
	manager, _, created := spectatorFixture(t, 3, "owner", "guest", "third")
	fee := int64(room.DefaultSpectatorFeeBigBlinds) * created.Rules.BigBlind

	before, err := manager.Snapshot(ctx, "owner", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	chipsBefore := totalChipsOnTable(before)

	if _, err := manager.EnterSpectate(ctx, "third", created.RoomID); err != nil {
		t.Fatal(err)
	}
	inHand := startHand(t, ctx, manager, []string{"owner", "guest"})
	if inHand.Phase != holdem.PhasePreflop {
		t.Fatalf("hand should start with one spectator present, phase=%s", inHand.Phase)
	}

	// 观战者视角：付了费，看得到两名参与者的手牌
	asSpectator, err := manager.Snapshot(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	third := spectatorOf(asSpectator, "third")
	if third == nil || !asSpectator.Spectating {
		t.Fatalf("third should be a spectator: %#v", asSpectator.Spectators)
	}
	if third.Stack != 2_000-fee || !third.CanSeeHoleCards {
		t.Fatalf("spectator should have paid %d and gained access: %#v", fee, *third)
	}
	for _, seat := range asSpectator.Seats {
		if len(seat.HoleCards) != 2 {
			t.Fatalf("paying spectator must see every participant's hole cards: %#v", seat)
		}
	}
	// 上桌玩家视角：看不到对手的牌
	asPlayer, err := manager.Snapshot(ctx, "owner", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	for _, seat := range asPlayer.Seats {
		if len(seat.HoleCards) != 0 {
			t.Fatalf("players must never receive other players' hole cards: %#v", seat)
		}
	}
	// 费用明细与守恒
	fees := asPlayer.SpectatorFees
	if fees == nil || len(fees.Payers) != 1 || fees.Payers[0].Amount != fee {
		t.Fatalf("fee record should list one payer of %d: %#v", fee, fees)
	}
	var distributed int64
	for _, share := range fees.Recipients {
		distributed += share.Amount
	}
	if distributed != fee || len(fees.Recipients) != 2 {
		t.Fatalf("fee must be split across the two participants: %#v", fees.Recipients)
	}
	if got := totalChipsOnTable(asPlayer); got != chipsBefore {
		t.Fatalf("chips must be conserved: before=%d after=%d", chipsBefore, got)
	}

	// 结算必须通过引擎的守恒校验——这是把收费放在发牌前的原因
	settled := submitFold(t, ctx, manager, created.RoomID, inHand, "fold-1")
	if settled.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("hand should settle normally with fee income in the stacks, phase=%s", settled.Phase)
	}
	if got := totalChipsOnTable(settled); got != chipsBefore {
		t.Fatalf("chips must still be conserved after settlement: before=%d after=%d", chipsBefore, got)
	}
}

// 余额不足的观战者本手不收费也不给看牌，而不是收走他剩下的全部筹码。
func TestSpectatorWithoutEnoughChipsPaysNothingAndSeesNothing(t *testing.T) {
	ctx := context.Background()
	manager, rooms, created := spectatorFixture(t, 3, "owner", "guest", "third")
	if _, err := manager.EnterSpectate(ctx, "third", created.RoomID); err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.SetStack(ctx, created.RoomID, "third", 50); err != nil {
		t.Fatal(err)
	}
	startHand(t, ctx, manager, []string{"owner", "guest"})

	snapshot, err := manager.Snapshot(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	third := spectatorOf(snapshot, "third")
	if third == nil || third.Stack != 50 || third.CanSeeHoleCards {
		t.Fatalf("broke spectator must keep chips and get no access: %#v", third)
	}
	for _, seat := range snapshot.Seats {
		if len(seat.HoleCards) != 0 {
			t.Fatalf("unpaid spectator must not see hole cards: %#v", seat)
		}
	}
	if snapshot.SpectatorFees != nil {
		t.Fatalf("no fee should be recorded when nobody paid: %#v", snapshot.SpectatorFees)
	}
}

// 免费模式下所有观战者都能看牌，与筹码无关（D5）。
func TestFreeSpectatingGrantsAccessRegardlessOfChips(t *testing.T) {
	ctx := context.Background()
	manager, rooms, created := spectatorFixture(t, 3, "owner", "guest", "third")
	if _, err := manager.EnterSpectate(ctx, "third", created.RoomID); err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.UpdateSpectatorSettings(ctx, "owner", room.SpectatorSettings{
		FeeBigBlinds: 0, VoiceAllowed: true, ChatAllowed: true, EmoteAllowed: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.SetStack(ctx, created.RoomID, "third", 0); err != nil {
		t.Fatal(err)
	}
	startHand(t, ctx, manager, []string{"owner", "guest"})

	snapshot, err := manager.Snapshot(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if third := spectatorOf(snapshot, "third"); third == nil || !third.CanSeeHoleCards {
		t.Fatalf("free mode must grant access even with zero chips: %#v", third)
	}
	for _, seat := range snapshot.Seats {
		if len(seat.HoleCards) != 2 {
			t.Fatalf("free spectator should see hole cards: %#v", seat)
		}
	}
}

// 除不尽的余数逐枚分配，总额精确等于收取的费用。
func TestSpectatorFeeRemainderIsDistributedExactly(t *testing.T) {
	ctx := context.Background()
	manager, _, created := spectatorFixture(t, 4, "owner", "guest", "third", "fourth")
	if _, err := manager.EnterSpectate(ctx, "fourth", created.RoomID); err != nil {
		t.Fatal(err)
	}
	fee := int64(room.DefaultSpectatorFeeBigBlinds) * created.Rules.BigBlind
	if fee%3 == 0 {
		t.Skipf("fee %d divides evenly among three players; remainder rule not exercised", fee)
	}
	startHand(t, ctx, manager, []string{"owner", "guest", "third"})

	snapshot, err := manager.Snapshot(ctx, "owner", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	fees := snapshot.SpectatorFees
	if fees == nil || len(fees.Recipients) != 3 {
		t.Fatalf("expected three recipients: %#v", fees)
	}
	var total, minShare, maxShare int64 = 0, fees.Recipients[0].Amount, fees.Recipients[0].Amount
	for _, share := range fees.Recipients {
		total += share.Amount
		if share.Amount < minShare {
			minShare = share.Amount
		}
		if share.Amount > maxShare {
			maxShare = share.Amount
		}
	}
	if total != fee || maxShare-minShare > 1 {
		t.Fatalf("remainder must be spread one chip at a time and sum exactly: %#v", fees.Recipients)
	}
}

// 牌局进行中申请观战只记录意向，本手结束后才生效；期间他仍在座位上打完这手。
func TestEnterSpectateMidHandAppliesAfterSettlement(t *testing.T) {
	ctx := context.Background()
	manager, _, created := spectatorFixture(t, 3, "owner", "guest", "third")
	inHand := startHand(t, ctx, manager, []string{"owner", "guest", "third"})

	pending, err := manager.EnterSpectate(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Spectating {
		t.Fatal("a participating player cannot leave the table mid-hand")
	}
	if seat := seatOf(pending, "third"); seat == nil || !seat.PendingSpectate {
		t.Fatalf("intent should be visible on the seat: %#v", seat)
	}

	snapshot := inHand
	for round := 0; snapshot.Phase != holdem.PhaseWaitingNextHand && round < 6; round++ {
		snapshot = submitFold(t, ctx, manager, created.RoomID, snapshot, "fold")
	}
	if snapshot.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("hand did not settle: %s", snapshot.Phase)
	}
	after, err := manager.Snapshot(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Spectating || seatOf(after, "third") != nil || spectatorOf(after, "third") == nil {
		t.Fatalf("third should be spectating once the hand settled: seats=%#v spectators=%#v", after.Seats, after.Spectators)
	}
	if len(after.Seats) != 2 {
		t.Fatalf("the vacated seat must not linger: %#v", after.Seats)
	}
}

// 牌局进行中申请上桌同样等到手结束；座位被别人先占了就放弃，不排队。
func TestTakeSeatMidHandAppliesOrDropsWhenFull(t *testing.T) {
	ctx := context.Background()
	manager, rooms, created := spectatorFixture(t, 3, "owner", "guest", "third")
	if _, err := manager.EnterSpectate(ctx, "third", created.RoomID); err != nil {
		t.Fatal(err)
	}
	inHand := startHand(t, ctx, manager, []string{"owner", "guest"})

	pending, err := manager.TakeSeat(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if third := spectatorOf(pending, "third"); third == nil || !third.PendingSeat {
		t.Fatalf("seat intent should be recorded: %#v", third)
	}

	// 手还没结束时第四人加入并占掉最后一个座位
	if _, err := rooms.Join(ctx, room.Participant{UserID: "fourth", DisplayName: "玩家fourth"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Join(ctx, "fourth", created.RoomID); err != nil {
		t.Fatal(err)
	}
	settled := submitFold(t, ctx, manager, created.RoomID, inHand, "fold-1")
	if settled.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("hand did not settle: %s", settled.Phase)
	}
	after, err := manager.Snapshot(ctx, "third", created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Spectating {
		t.Fatal("with the table full the seat intent must be dropped, not queued")
	}
	if third := spectatorOf(after, "third"); third == nil || third.PendingSeat {
		t.Fatalf("dropped intent must not stay pending forever: %#v", third)
	}
	if seatOf(after, "fourth") == nil {
		t.Fatalf("the newcomer who arrived first keeps the seat: %#v", after.Seats)
	}
}

// 观战者没有准备态：点准备被拒，取消准备静默接受；开局人数只数上桌的人。
func TestSpectatorsAreExcludedFromReadiness(t *testing.T) {
	ctx := context.Background()
	manager, rooms, created := spectatorFixture(t, 3, "owner", "guest", "third")
	if _, err := manager.EnterSpectate(ctx, "third", created.RoomID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetReady(ctx, "third", true); err == nil {
		t.Fatal("a spectator must not be able to ready up")
	}
	if _, err := rooms.SetReady(ctx, "third", false); err != nil {
		t.Fatalf("clearing ready on a spectator must be a harmless no-op: %v", err)
	}
	// 两名上桌玩家准备即可开局，不必等观战者
	snapshot := startHand(t, ctx, manager, []string{"owner", "guest"})
	if snapshot.Phase != holdem.PhasePreflop {
		t.Fatalf("hand should start without the spectator, phase=%s", snapshot.Phase)
	}
	if len(snapshot.Seats) != 2 || len(snapshot.Spectators) != 1 {
		t.Fatalf("spectator must not appear as a seat: seats=%d spectators=%d", len(snapshot.Seats), len(snapshot.Spectators))
	}
}

// 观战位最多 10 人。
func TestSpectatorCapIsEnforced(t *testing.T) {
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: "u0", DisplayName: "玩家0"}, room.PresetStandard, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	// 满员的房间进不来新人，所以先让 10 人入座再全部转去观战，腾出座位后
	// 第 11 人才能加入并尝试成为第 11 名观战者。
	users := []string{"u0"}
	for index := 1; index < 10; index++ {
		userID := "u" + string(rune('0'+index))
		if _, err := rooms.Join(ctx, room.Participant{UserID: userID, DisplayName: "玩家" + userID}, created.Code, ""); err != nil {
			t.Fatal(err)
		}
		users = append(users, userID)
	}
	for _, userID := range users {
		if _, err := rooms.EnterSpectate(ctx, userID); err != nil {
			t.Fatalf("spectator %s: %v", userID, err)
		}
	}
	// 座位全空了，第 11 人可以入座；他再想观战就该被拒
	if _, err := rooms.Join(ctx, room.Participant{UserID: "u10", DisplayName: "玩家u10"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.EnterSpectate(ctx, "u10"); err == nil {
		t.Fatal("an 11th spectator must be refused")
	}
}
