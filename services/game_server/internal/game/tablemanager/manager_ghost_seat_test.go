package tablemanager

// 幽灵座位的回归用例。
//
// 线上事故：某手结算后 10 秒自动准备倒计时到点，服务端替每个人点准备；恰在这一
// 瞬间有人离开房间。点准备的路径先在锁外写房间服务、拿到一份成员表，再上锁同步
// 引擎——这份成员表里还有那个人，引擎里已经没有，于是他被当成新玩家加回引擎。
// 这个数据库里不存在的座位让之后每一手结算都因为筹码对不上账而失败，一个房间
// 打了 8 手、另一个打了 46 手，全场输赢一分没进账户。

import (
	"context"
	"errors"
	"testing"
	"time"

	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/game/holdem"
)

func settleOneHand(t *testing.T, fixture leaveFixture, users []string) Snapshot {
	t.Helper()
	ctx := context.Background()
	inHand := startHand(t, ctx, fixture.manager, users)
	snapshot := inHand
	for steps := 0; snapshot.Phase != holdem.PhaseWaitingNextHand; steps++ {
		if steps > 20 || snapshot.CurrentAction == nil {
			t.Fatalf("hand did not finish: phase=%s", snapshot.Phase)
		}
		snapshot = submitFold(t, ctx, fixture.manager, fixture.room.RoomID, snapshot, "fold-"+string(rune('a'+steps)))
	}
	return snapshot
}

func enginePlayerIDs(manager *Manager, roomID string) []string {
	runtime := manager.existingRuntime(roomID)
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	var ids []string
	for _, player := range runtime.engine.Players() {
		ids = append(ids, player.PlayerID)
	}
	return ids
}

// 自动准备替 guestA 点准备的同一瞬间 owner 离开：owner 不能被加回引擎。
func TestReadyRacingWithLeaveDoesNotResurrectTheLeaver(t *testing.T) {
	ctx := context.Background()
	fixture := newLeaveFixture(t)
	settleOneHand(t, fixture, fixture.users)

	left := false
	fixture.manager.beforeLockForTest = func() {
		if left {
			return
		}
		left = true
		if _, err := fixture.manager.Leave(ctx, "owner"); err != nil {
			t.Fatalf("owner leave during ready: %v", err)
		}
	}
	if _, err := fixture.manager.SetReady(ctx, "guestA", true); err != nil {
		t.Fatalf("ready racing with a leave: %v", err)
	}
	fixture.manager.beforeLockForTest = nil
	for _, id := range enginePlayerIDs(fixture.manager, fixture.room.RoomID) {
		if id == "owner" {
			t.Fatal("the leaver came back as a ghost seat")
		}
	}
	// 剩下两人的下一手要能正常结算入账
	remaining := []string{"guestA", "guestB"}
	ended := settleOneHand(t, fixture, remaining)
	if ended.Settlement == nil {
		t.Fatal("next hand did not settle")
	}
	if total := fixture.totalChips(t); total != 15_000 {
		t.Fatalf("total chips=%d", total)
	}
}

// 有人重连（table.join）的同一瞬间另一个人离开：同样不能加回来。
func TestJoinRacingWithLeaveDoesNotResurrectTheLeaver(t *testing.T) {
	ctx := context.Background()
	fixture := newLeaveFixture(t)
	settleOneHand(t, fixture, fixture.users)

	left := false
	fixture.manager.beforeLockForTest = func() {
		if left {
			return
		}
		left = true
		if _, err := fixture.manager.Leave(ctx, "owner"); err != nil {
			t.Fatalf("owner leave during join: %v", err)
		}
	}
	if _, err := fixture.manager.Join(ctx, "guestA", fixture.room.RoomID); err != nil {
		t.Fatalf("join racing with a leave: %v", err)
	}
	fixture.manager.beforeLockForTest = nil
	for _, id := range enginePlayerIDs(fixture.manager, fixture.room.RoomID) {
		if id == "owner" {
			t.Fatal("the leaver came back as a ghost seat")
		}
	}
	ended := settleOneHand(t, fixture, []string{"guestA", "guestB"})
	if ended.Settlement == nil {
		t.Fatal("next hand did not settle")
	}
}

// flakySettlementRepository 让账户结算按需失败。
type flakySettlementRepository struct {
	bankroll.Repository
	failApplySettlement bool
}

func (repository *flakySettlementRepository) ApplySettlement(
	ctx context.Context, tableID, handID string, balances map[string]int64, maximum int64, now time.Time,
) error {
	if repository.failApplySettlement {
		return errors.New("database unavailable")
	}
	return repository.Repository.ApplySettlement(ctx, tableID, handID, balances, maximum, now)
}

// 上一手没入账时不开下一手；数据库恢复后补做入账，再照常开局。
// 此前会照常开局，引擎继续发牌而账户一分不记，线上一个房间这样打了 46 手。
func TestNoNewHandUntilTheLastSettlementIsPersisted(t *testing.T) {
	ctx := context.Background()
	flaky := &flakySettlementRepository{Repository: bankroll.NewMemoryRepository()}
	chips, err := bankroll.NewService(flaky, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	fixture := newLeaveFixtureWithBankroll(t, chips)

	flaky.failApplySettlement = true
	inHand := startHand(t, ctx, fixture.manager, fixture.users)
	folded := submitFold(t, ctx, fixture.manager, fixture.room.RoomID, inHand, "fold-1")
	current, err := fixture.manager.Snapshot(ctx, folded.CurrentAction.UserID, fixture.room.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.manager.SubmitAction(ctx, current.CurrentAction.UserID, fixture.room.RoomID, holdem.ActionRequest{
		ActionID: "fold-2", HandID: current.HandID, TableRevision: current.TableRevision, Action: holdem.ActionFold,
	}); err == nil {
		t.Fatal("the failing settlement must surface as an error")
	}
	// 数据库还没恢复：所有人点准备也开不了下一手，错误抛给点准备的人
	var readyErr error
	for _, userID := range fixture.users {
		if _, readyErr = fixture.manager.SetReady(ctx, userID, true); readyErr != nil {
			break
		}
	}
	if readyErr == nil {
		t.Fatal("a new hand must not start while the last settlement is not persisted")
	}
	snapshot, err := fixture.manager.Snapshot(ctx, "owner", fixture.room.RoomID)
	if err != nil || snapshot.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("table must stay between hands: phase=%s err=%v", snapshot.Phase, err)
	}

	// 数据库恢复：下一次准备补做入账，这一次不开局；之后照常
	flaky.failApplySettlement = false
	retried, err := fixture.manager.SetReady(ctx, "owner", true)
	if err != nil {
		t.Fatalf("retry on ready: %v", err)
	}
	if retried.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("the retry call must not start a hand, phase=%s", retried.Phase)
	}
	if _, found := fixture.manager.history.Hand(inHand.HandID); !found {
		t.Fatal("the retried hand must be written to history")
	}
	// 账户与引擎一致：第一手的输赢已入账
	var total int64
	for _, userID := range fixture.users {
		position := fixture.wallet(t, userID)
		total += position.WalletChips + position.TableChips
	}
	if total != 15_000 {
		t.Fatalf("total chips=%d", total)
	}
	next := settleOneHand(t, fixture, fixture.users)
	if next.HandID == inHand.HandID || next.Settlement == nil {
		t.Fatal("the next hand must start and settle normally")
	}
	if _, found := fixture.manager.history.Hand(next.HandID); !found {
		t.Fatal("the next hand must be written to history")
	}
}

// 生成快照的同一瞬间另一个人离开：快照路径同样不能把他加回来。
// 广播快照时每个客户端都会走一遍这条路径，是最容易撞上交错的地方。
func TestSnapshotRacingWithLeaveDoesNotResurrectTheLeaver(t *testing.T) {
	ctx := context.Background()
	fixture := newLeaveFixture(t)
	settleOneHand(t, fixture, fixture.users)

	left := false
	fixture.manager.beforeLockForTest = func() {
		if left {
			return
		}
		left = true
		if _, err := fixture.manager.Leave(ctx, "owner"); err != nil {
			t.Fatalf("owner leave during snapshot: %v", err)
		}
	}
	if _, err := fixture.manager.Snapshot(ctx, "guestA", fixture.room.RoomID); err != nil {
		t.Fatalf("snapshot racing with a leave: %v", err)
	}
	fixture.manager.beforeLockForTest = nil
	for _, id := range enginePlayerIDs(fixture.manager, fixture.room.RoomID) {
		if id == "owner" {
			t.Fatal("the leaver came back as a ghost seat")
		}
	}
	ended := settleOneHand(t, fixture, []string{"guestA", "guestB"})
	if ended.Settlement == nil {
		t.Fatal("next hand did not settle")
	}
}

// 上一手没入账时不能补码：补进去的钱会被补做的结算覆盖，之后永远对不上账。
func TestRebuyIsRefusedWhileTheLastSettlementIsNotPersisted(t *testing.T) {
	ctx := context.Background()
	flaky := &flakySettlementRepository{Repository: bankroll.NewMemoryRepository()}
	chips, err := bankroll.NewService(flaky, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	fixture := newLeaveFixtureWithBankroll(t, chips)
	flaky.failApplySettlement = true
	inHand := startHand(t, ctx, fixture.manager, fixture.users)
	folded := submitFold(t, ctx, fixture.manager, fixture.room.RoomID, inHand, "fold-1")
	current, err := fixture.manager.Snapshot(ctx, folded.CurrentAction.UserID, fixture.room.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.manager.SubmitAction(ctx, current.CurrentAction.UserID, fixture.room.RoomID, holdem.ActionRequest{
		ActionID: "fold-2", HandID: current.HandID, TableRevision: current.TableRevision, Action: holdem.ActionFold,
	}); err == nil {
		t.Fatal("the failing settlement must surface as an error")
	}
	if _, err := fixture.manager.Rebuy(ctx, "owner", fixture.room.RoomID, "rebuy-while-broken", 500); ruleCodeOf(err) != "settlement_not_persisted" {
		t.Fatalf("rebuy must be refused while the settlement is not persisted, err=%v", err)
	}
	walletBefore := fixture.wallet(t, "owner")
	if walletBefore.WalletChips != 4_000 {
		t.Fatalf("refused rebuy must not move chips: %#v", walletBefore)
	}
	// 数据库恢复、补做入账之后，补码照常
	flaky.failApplySettlement = false
	if _, err := fixture.manager.SetReady(ctx, "owner", true); err != nil {
		t.Fatalf("retry on ready: %v", err)
	}
	if _, err := fixture.manager.Rebuy(ctx, "owner", fixture.room.RoomID, "rebuy-after-retry", 500); err != nil {
		t.Fatalf("rebuy after the retry: %v", err)
	}
	if total := fixture.totalChips(t); total != 15_000 {
		t.Fatalf("total chips=%d", total)
	}
}
