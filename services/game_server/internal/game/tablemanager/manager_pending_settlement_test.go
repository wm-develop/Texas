package tablemanager

import (
	"context"
	cryptorand "crypto/rand"
	"testing"
	"time"

	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/room"
	"texas/services/game_server/internal/security"
)

// pendingSettlementFixture 把牌桌带到「上一手已经打完、结算却没入账」的窗口：
// 三人桌，有人弃牌后离开，结算时给他退还的那一步数据库出错。
type pendingSettlementFixture struct {
	ctx       context.Context
	flaky     *flakyCashOutRepository
	chips     *bankroll.Service
	rooms     *room.Service
	manager   *Manager
	history   *history.InMemoryStore
	room      room.Room
	users     []string
	leaver    string
	remaining []string
	handID    string
}

func newPendingSettlementFixture(t *testing.T) pendingSettlementFixture {
	t.Helper()
	ctx := context.Background()
	flaky := &flakyCashOutRepository{Repository: bankroll.NewMemoryRepository()}
	chips, err := bankroll.NewService(flaky, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	users := []string{"owner", "guestA", "guestB"}
	for _, userID := range users {
		if _, err := chips.TopUp(ctx, userID, "topup-"+userID, 5_000); err != nil {
			t.Fatal(err)
		}
	}
	hasher, err := security.NewPasswordHasher(1_000, cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rooms, err := room.NewService(room.NewMemoryRepository(), hasher, room.ServiceConfig{Bankroll: chips})
	if err != nil {
		t.Fatal(err)
	}
	created, err := rooms.CreateConfigured(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.CreateOptions{
		Preset: room.PresetStandard, SmallBlind: 10, BigBlind: 20, MaxBuyIn: 2_000, BuyIn: 1_000, RequestID: "create-owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{"guestA", "guestB"} {
		if _, err := rooms.JoinWithBuyIn(ctx, room.Participant{UserID: userID, DisplayName: userID}, room.JoinOptions{
			Code: created.Code, BuyIn: 1_000, RequestID: "join-" + userID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	store := history.NewInMemoryStore()
	manager, err := NewWithConfig(rooms, zeroRandom{}, ManagerConfig{Bankroll: chips, History: store})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range users {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	inHand := startHand(t, ctx, manager, users)
	leaver := inHand.CurrentAction.UserID
	afterFold := submitFold(t, ctx, manager, created.RoomID, inHand, "fold-leaver")
	if _, err := manager.Leave(ctx, leaver); err != nil {
		t.Fatal(err)
	}
	flaky.failCashOut = true
	current, err := manager.Snapshot(ctx, afterFold.CurrentAction.UserID, created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.SubmitAction(ctx, current.CurrentAction.UserID, created.RoomID, holdem.ActionRequest{
		ActionID: "fold-to-end", HandID: current.HandID, TableRevision: current.TableRevision, Action: holdem.ActionFold,
	}); err == nil {
		t.Fatal("the failing cash-out must surface as an error")
	}
	flaky.failCashOut = false
	var remaining []string
	for _, userID := range users {
		if userID != leaver {
			remaining = append(remaining, userID)
		}
	}
	return pendingSettlementFixture{
		ctx: ctx, flaky: flaky, chips: chips, rooms: rooms, manager: manager, history: store,
		room: created, users: users, leaver: leaver, remaining: remaining, handID: inHand.HandID,
	}
}

func (fixture pendingSettlementFixture) totalChips(t *testing.T) int64 {
	t.Helper()
	var total int64
	for _, userID := range fixture.users {
		snapshot, err := fixture.chips.Snapshot(fixture.ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		total += snapshot.WalletChips + snapshot.TableChips
	}
	return total
}

// 此前：结算没入账时有人离开（或被房主请出、被管理员移出），他一离开引擎，
// 补做结算就少了一个人，守恒校验永远失败，牌桌再也开不了局。
// 现在离开前先把结算补做完。
func TestLeavingWhileASettlementIsPendingFinishesItFirst(t *testing.T) {
	fixture := newPendingSettlementFixture(t)
	goer, stayer := fixture.remaining[0], fixture.remaining[1]
	if _, err := fixture.manager.Leave(fixture.ctx, goer); err != nil {
		t.Fatalf("leave during the pending window: %v", err)
	}
	if _, found := fixture.history.Hand(fixture.handID); !found {
		t.Fatal("the pending hand must be recorded before anyone leaves")
	}
	if fixture.manager.LeavePending(fixture.leaver, fixture.room.RoomID) {
		t.Fatal("the earlier pending leave must be finished too")
	}
	if total := fixture.totalChips(t); total != 15_000 {
		t.Fatalf("total chips=%d", total)
	}
	// 留下的人照常补码，不再报「结算没入账」
	if _, err := fixture.manager.Rebuy(fixture.ctx, stayer, fixture.room.RoomID, "rebuy-after", 100); err != nil {
		t.Fatalf("rebuy after the settlement was finished: %v", err)
	}
}

// 此前：等结算的离开者在结算没入账时回到原房间，「撤销离开」按这一手之前的
// 筹码把他加回引擎，引擎与账户从此对不上，之后每一手都结算失败。
// 现在先补做结算、完成他的离开；他之后像新玩家一样重新带入加入。
func TestPendingLeaverComingBackWhileTheSettlementIsPendingDoesNotBreakTheTable(t *testing.T) {
	fixture := newPendingSettlementFixture(t)
	if _, err := fixture.manager.Join(fixture.ctx, fixture.leaver, fixture.room.RoomID); err == nil {
		t.Fatal("the pending leaver must not be reinstated with stale chips")
	}
	if _, found := fixture.history.Hand(fixture.handID); !found {
		t.Fatal("the pending hand must be recorded")
	}
	if _, err := fixture.rooms.Current(fixture.ctx, fixture.leaver); err == nil {
		t.Fatal("the leave must be completed")
	}
	if total := fixture.totalChips(t); total != 15_000 {
		t.Fatalf("total chips=%d", total)
	}
	// 他重新带入加入，三人再打一手，结算正常入账
	if _, err := fixture.rooms.JoinWithBuyIn(fixture.ctx, room.Participant{UserID: fixture.leaver, DisplayName: fixture.leaver},
		room.JoinOptions{Code: fixture.room.Code, BuyIn: 1_000, RequestID: "rejoin-leaver"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.Join(fixture.ctx, fixture.leaver, fixture.room.RoomID); err != nil {
		t.Fatal(err)
	}
	next := startHand(t, fixture.ctx, fixture.manager, fixture.users)
	for steps := 0; next.Phase != holdem.PhaseWaitingNextHand; steps++ {
		if steps > 10 || next.CurrentAction == nil {
			t.Fatalf("hand did not finish: phase=%s", next.Phase)
		}
		var err error
		_, next, err = fixture.manager.SubmitAction(fixture.ctx, next.CurrentAction.UserID, fixture.room.RoomID, holdem.ActionRequest{
			ActionID: "next-" + string(rune('a'+steps)), HandID: next.HandID,
			TableRevision: next.TableRevision, Action: holdem.ActionFold,
		})
		if err != nil {
			t.Fatalf("the next hand must settle: %v", err)
		}
	}
	if _, found := fixture.history.Hand(next.HandID); !found {
		t.Fatal("the next hand must be recorded")
	}
	if total := fixture.totalChips(t); total != 15_000 {
		t.Fatalf("total chips=%d", total)
	}
}

// 从观战上桌不受这个窗口影响：新座位不在上一手的结算名单里。
func TestTakingASeatWhileASettlementIsPendingIsAllowed(t *testing.T) {
	fixture := newPendingSettlementFixture(t)
	if _, err := fixture.manager.TakeSeat(fixture.ctx, fixture.remaining[0], fixture.room.RoomID); ruleCodeOf(err) == "settlement_not_persisted" {
		t.Fatalf("taking a seat must not be refused for a pending settlement: %v", err)
	}
	// 换座与接受换座仍然拒绝：补做要按这一手当时的座位写牌谱
	if _, err := fixture.manager.RequestSeatChange(fixture.ctx, fixture.remaining[0], fixture.room.RoomID, 9, "seat"); ruleCodeOf(err) != "settlement_not_persisted" {
		t.Fatalf("seat change: %v", err)
	}
}
