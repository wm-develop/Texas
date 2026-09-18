package tablemanager

// 离开与重新进入同一房间的回归用例。
//
// 线上事故：房主弃牌后中途离开，桌上 8530 筹码没有退回钱包；重新进入再离开，
// 又丢了 10240。两个根因——弃牌中途离开时成员记录被提前删除（PostgreSQL 的桌上
// 筹码就记在那一行上），以及离桌退还的幂等编号按「房间 + 用户」固定、第二次离开
// 被当成重复请求跳过。内存仓储把余额另存一处，前一个问题在这里复现不出来，真实
// 库上的用例见 internal/room/postgres_integration_test.go。

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"testing"
	"time"

	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/room"
	"texas/services/game_server/internal/security"
)

type leaveFixture struct {
	chips   *bankroll.Service
	rooms   *room.Service
	manager *Manager
	room    room.Room
	users   []string
}

func newLeaveFixture(t *testing.T) leaveFixture {
	t.Helper()
	chips, err := bankroll.NewService(bankroll.NewMemoryRepository(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return newLeaveFixtureWithBankroll(t, chips)
}

// newLeaveFixtureWithBankroll 用给定的账户服务搭桌，便于注入会按需失败的仓储。
func newLeaveFixtureWithBankroll(t *testing.T, chips *bankroll.Service) leaveFixture {
	t.Helper()
	ctx := context.Background()
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
	// 入座时间逐次递增：退还编号按入座时间区分，真实时钟下两次入座不可能同一微秒，
	// 测试里用步进时钟把这一点固定下来。
	tick := time.Unix(1_700_000_000, 0).UTC()
	rooms, err := room.NewService(room.NewMemoryRepository(), hasher, room.ServiceConfig{
		Bankroll: chips,
		Now: func() time.Time {
			tick = tick.Add(time.Second)
			return tick
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := rooms.CreateConfigured(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.CreateOptions{
		Preset: room.PresetStandard, MaxPlayers: 3, SmallBlind: 10, BigBlind: 20,
		MaxBuyIn: 2_000, BuyIn: 1_000, RequestID: "create-owner",
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
	manager, err := NewWithConfig(rooms, zeroRandom{}, ManagerConfig{Bankroll: chips})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range users {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	return leaveFixture{chips: chips, rooms: rooms, manager: manager, room: created, users: users}
}

func (fixture leaveFixture) rejoin(t *testing.T, userID, requestID string, buyIn int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := fixture.rooms.JoinWithBuyIn(ctx, room.Participant{UserID: userID, DisplayName: userID}, room.JoinOptions{
		Code: fixture.room.Code, BuyIn: buyIn, RequestID: requestID,
	}); err != nil {
		t.Fatalf("rejoin buy-in: %v", err)
	}
	if _, err := fixture.manager.Join(ctx, userID, fixture.room.RoomID); err != nil {
		t.Fatalf("rejoin table: %v", err)
	}
}

func (fixture leaveFixture) wallet(t *testing.T, userID string) bankroll.Snapshot {
	t.Helper()
	snapshot, err := fixture.chips.Snapshot(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func (fixture leaveFixture) result(t *testing.T, userID string) bankroll.RoomResult {
	t.Helper()
	result, err := fixture.chips.RoomResult(context.Background(), userID, fixture.room.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (fixture leaveFixture) totalChips(t *testing.T) int64 {
	t.Helper()
	var total int64
	for _, userID := range fixture.users {
		snapshot := fixture.wallet(t, userID)
		total += snapshot.WalletChips + snapshot.TableChips
	}
	return total
}

// 同一房间离开、重新进入、再离开：第二次离开也必须退还。
// 此前退还编号只按「房间 + 用户」，第二次被当成重复请求跳过，筹码留在桌上没人管。
func TestLeavingTheSameRoomTwiceCashesOutBothTimes(t *testing.T) {
	ctx := context.Background()
	fixture := newLeaveFixture(t)
	if _, err := fixture.manager.Leave(ctx, "owner"); err != nil {
		t.Fatal(err)
	}
	if got := fixture.wallet(t, "owner"); got.WalletChips != 5_000 || got.TableChips != 0 {
		t.Fatalf("first leave: %#v", got)
	}
	fixture.rejoin(t, "owner", "rejoin-1", 1_500)
	if got := fixture.wallet(t, "owner"); got.WalletChips != 3_500 || got.TableChips != 1_500 {
		t.Fatalf("after rejoin: %#v", got)
	}
	if _, err := fixture.manager.Leave(ctx, "owner"); err != nil {
		t.Fatal(err)
	}
	if got := fixture.wallet(t, "owner"); got.WalletChips != 5_000 || got.TableChips != 0 {
		t.Fatalf("second leave must cash out too: %#v", got)
	}
	// 第三次也一样，确认不是只修了「第二次」
	fixture.rejoin(t, "owner", "rejoin-2", 700)
	if _, err := fixture.manager.Leave(ctx, "owner"); err != nil {
		t.Fatal(err)
	}
	if got := fixture.wallet(t, "owner"); got.WalletChips != 5_000 || got.TableChips != 0 {
		t.Fatalf("third leave must cash out too: %#v", got)
	}
	// 本房间战绩跨多次进出延续计算：没打过一手，净胜负为 0
	result := fixture.result(t, "owner")
	if result.BoughtIn != 3_200 || result.ReturnedToWallet != 3_200 || result.Net != 0 {
		t.Fatalf("room result across stays: %#v", result)
	}
	if total := fixture.totalChips(t); total != 15_000 {
		t.Fatalf("total chips=%d", total)
	}
}

// 同一次入座内重复调用退还（网络重试）只退一次。
func TestCashOutRequestIDIsStablePerStayAndDistinctAcrossStays(t *testing.T) {
	joined := time.Unix(1_700_000_000, 123_456_789).UTC()
	member := room.Member{UserID: "user_abc", JoinedAt: joined}
	first := cashOutRequestID("table_xyz", member)
	if first != cashOutRequestID("table_xyz", member) {
		t.Fatal("same stay must produce the same request id")
	}
	// PostgreSQL 只存到微秒：读回来的时间少了纳秒，编号不能因此改变
	member.JoinedAt = joined.Truncate(time.Microsecond)
	if first != cashOutRequestID("table_xyz", member) {
		t.Fatal("request id must survive a microsecond round trip through the database")
	}
	member.JoinedAt = joined.Add(time.Microsecond)
	if first == cashOutRequestID("table_xyz", member) {
		t.Fatal("a new stay must produce a new request id")
	}
	if !bankroll.ValidRequestID(first) {
		t.Fatalf("request id %q is rejected by the bankroll service", first)
	}
	long := room.Member{UserID: "user_0123456789abcdefghijklmnop", JoinedAt: joined}
	if id := cashOutRequestID("table_0123456789abcdef", long); !bankroll.ValidRequestID(id) {
		t.Fatalf("request id %q (len %d) is rejected by the bankroll service", id, len(id))
	}
}

// 弃牌后中途离开、本手结算后重新进入：第一段的筹码已退回，战绩延续。
func TestFoldLeaveSettleThenRejoinContinuesTheRoomResult(t *testing.T) {
	ctx := context.Background()
	fixture := newLeaveFixture(t)
	inHand := startHand(t, ctx, fixture.manager, fixture.users)
	leaver := inHand.CurrentAction.UserID
	afterFold := submitFold(t, ctx, fixture.manager, fixture.room.RoomID, inHand, "fold-leaver")
	if _, err := fixture.manager.Leave(ctx, leaver); err != nil {
		t.Fatal(err)
	}
	current, err := fixture.manager.Snapshot(ctx, afterFold.CurrentAction.UserID, fixture.room.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	ended := submitFold(t, ctx, fixture.manager, fixture.room.RoomID, current, "fold-to-end")
	if ended.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("phase=%s", ended.Phase)
	}
	if got := fixture.wallet(t, leaver); got.WalletChips != 5_000 || got.TableChips != 0 {
		t.Fatalf("leaver folded without betting, wallet must be whole again: %#v", got)
	}
	fixture.rejoin(t, leaver, "rejoin-after-settle", 1_000)
	result := fixture.result(t, leaver)
	if result.BoughtIn != 2_000 || result.ReturnedToWallet != 1_000 || result.TableChips != 1_000 || result.Net != 0 {
		t.Fatalf("room result must continue across stays: %#v", result)
	}
	// 再离开一次：第二次退还不能被第一次的编号挡掉
	if _, err := fixture.manager.Leave(ctx, leaver); err != nil {
		t.Fatal(err)
	}
	if got := fixture.wallet(t, leaver); got.WalletChips != 5_000 || got.TableChips != 0 {
		t.Fatalf("second leave after a deferred first one: %#v", got)
	}
	if total := fixture.totalChips(t); total != 15_000 {
		t.Fatalf("total chips=%d", total)
	}
}

// 弃牌后中途离开、本手还没打完又回到同一房间：撤销离开，筹码原样延续，不重复带入。
// 此前这种情形会让本手结算报「筹码不守恒」，整桌卡住。
func TestRejoiningBeforeSettlementCancelsTheLeave(t *testing.T) {
	ctx := context.Background()
	fixture := newLeaveFixture(t)
	inHand := startHand(t, ctx, fixture.manager, fixture.users)
	leaver := inHand.CurrentAction.UserID
	afterFold := submitFold(t, ctx, fixture.manager, fixture.room.RoomID, inHand, "fold-leaver")
	if _, err := fixture.manager.Leave(ctx, leaver); err != nil {
		t.Fatal(err)
	}
	if !fixture.manager.LeavePending(leaver, fixture.room.RoomID) {
		t.Fatal("leave should be pending")
	}
	walletBefore := fixture.wallet(t, leaver)
	// 客户端照常走「加入房间」：他仍是成员，服务端原样返回房间，不再扣一次带入
	fixture.rejoin(t, leaver, "rejoin-before-settle", 1_000)
	if got := fixture.wallet(t, leaver); got != walletBefore {
		t.Fatalf("rejoining while pending must not charge a second buy-in: before=%#v after=%#v", walletBefore, got)
	}
	if fixture.manager.LeavePending(leaver, fixture.room.RoomID) {
		t.Fatal("rejoining must cancel the pending leave")
	}
	current, err := fixture.manager.Snapshot(ctx, afterFold.CurrentAction.UserID, fixture.room.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	ended := submitFold(t, ctx, fixture.manager, fixture.room.RoomID, current, "fold-to-end")
	if ended.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("the hand must settle, phase=%s", ended.Phase)
	}
	// 他还在桌上，筹码是弃牌后的筹码，没有被退还也没有被清零
	stillSeated := false
	for _, seat := range ended.Seats {
		if seat.UserID == leaver {
			stillSeated = true
			if seat.Stack != fixture.wallet(t, leaver).TableChips {
				t.Fatalf("seat stack %d != table balance %d", seat.Stack, fixture.wallet(t, leaver).TableChips)
			}
		}
	}
	if !stillSeated {
		t.Fatalf("the returning player must keep their seat: %#v", ended.Seats)
	}
	if _, err := fixture.rooms.Current(ctx, leaver); err != nil {
		t.Fatalf("the returning player must still be a member: %v", err)
	}
	if total := fixture.totalChips(t); total != 15_000 {
		t.Fatalf("total chips=%d", total)
	}
	// 下一手照常能开
	next := startHand(t, ctx, fixture.manager, fixture.users)
	if next.Phase != holdem.PhasePreflop || next.HandID == ended.HandID {
		t.Fatalf("next hand did not start: phase=%s", next.Phase)
	}
}

// 离开者在这一手里下过注（大盲）：结算必须守恒，退回的是弃牌后的筹码而不是带入额。
func TestFoldLeaveWithChipsCommittedSettlesAndConserves(t *testing.T) {
	ctx := context.Background()
	fixture := newLeaveFixture(t)
	inHand := startHand(t, ctx, fixture.manager, fixture.users)
	// 让行动一路弃到只剩两人之前，找一个已经下了盲注的人来离开：
	// 先由当前行动者（枪口位，未下注）跟注留在局里，再让下一位（小盲）弃牌离开。
	first := inHand.CurrentAction.UserID
	_, afterCall, err := fixture.manager.SubmitAction(ctx, first, fixture.room.RoomID, holdem.ActionRequest{
		ActionID: "call-first", HandID: inHand.HandID, TableRevision: inHand.TableRevision, Action: holdem.ActionCall,
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	blind := afterCall.CurrentAction.UserID
	var committed int64
	for _, seat := range afterCall.Seats {
		if seat.UserID == blind {
			committed = seat.TotalBet
		}
	}
	if committed <= 0 {
		t.Fatalf("expected the next actor to have posted a blind, seats=%#v", afterCall.Seats)
	}
	afterFold := submitFold(t, ctx, fixture.manager, fixture.room.RoomID, afterCall, "fold-blind")
	if _, err := fixture.manager.Leave(ctx, blind); err != nil {
		t.Fatal(err)
	}
	if afterFold.Phase == holdem.PhaseWaitingNextHand {
		t.Fatal("hand should still be running with two players")
	}
	// 把这一手打完：剩下两人一路过牌到摊牌或一方弃牌
	snapshot, err := fixture.manager.Snapshot(ctx, first, fixture.room.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	for steps := 0; snapshot.Phase != holdem.PhaseWaitingNextHand; steps++ {
		if steps > 20 || snapshot.CurrentAction == nil {
			t.Fatalf("hand did not finish: phase=%s", snapshot.Phase)
		}
		action := holdem.ActionFold
		if snapshot.CurrentAction.Options.CanCheck {
			action = holdem.ActionCheck
		}
		_, snapshot, err = fixture.manager.SubmitAction(ctx, snapshot.CurrentAction.UserID, fixture.room.RoomID, holdem.ActionRequest{
			ActionID: "finish-" + string(rune('a'+steps)), HandID: snapshot.HandID,
			TableRevision: snapshot.TableRevision, Action: action,
		})
		if err != nil {
			t.Fatalf("finishing the hand: %v", err)
		}
	}
	got := fixture.wallet(t, blind)
	if got.TableChips != 0 || got.WalletChips != 5_000-committed {
		t.Fatalf("leaver must get back exactly the post-fold stack: committed=%d wallet=%#v", committed, got)
	}
	if result := fixture.result(t, blind); result.Net != -committed {
		t.Fatalf("room result net=%d, expected %d", result.Net, -committed)
	}
	if total := fixture.totalChips(t); total != 15_000 {
		t.Fatalf("total chips=%d", total)
	}
}

// 牌谱里必须有离开者这一行：他下过注而牌谱里没有他，整手输赢对不上账，
// 牌谱写入失败，结算跟着报错——此前盲注位弃牌离开就会这样。
func TestHandHistoryKeepsTheLeaverWhoCommittedChips(t *testing.T) {
	ctx := context.Background()
	fixture := newLeaveFixture(t)
	inHand := startHand(t, ctx, fixture.manager, fixture.users)
	first := inHand.CurrentAction.UserID
	_, afterCall, err := fixture.manager.SubmitAction(ctx, first, fixture.room.RoomID, holdem.ActionRequest{
		ActionID: "call-first", HandID: inHand.HandID, TableRevision: inHand.TableRevision, Action: holdem.ActionCall,
	})
	if err != nil {
		t.Fatal(err)
	}
	blind := afterCall.CurrentAction.UserID
	submitFold(t, ctx, fixture.manager, fixture.room.RoomID, afterCall, "fold-blind")
	if _, err := fixture.manager.Leave(ctx, blind); err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.manager.Snapshot(ctx, first, fixture.room.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	ended := submitFold(t, ctx, fixture.manager, fixture.room.RoomID, snapshot, "fold-to-end")
	if ended.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("phase=%s", ended.Phase)
	}
	record, found := fixture.manager.history.Hand(inHand.HandID)
	if !found {
		t.Fatal("the hand must be written to history")
	}
	var total int64
	var leaverRow *history.PlayerResult
	for index := range record.Players {
		total += record.Players[index].Delta
		if record.Players[index].UserID == blind {
			leaverRow = &record.Players[index]
		}
	}
	if leaverRow == nil || leaverRow.Delta >= 0 || leaverRow.Seat == 0 || len(leaverRow.HoleCards) != 2 {
		t.Fatalf("leaver row=%#v players=%#v", leaverRow, record.Players)
	}
	if total != 0 {
		t.Fatalf("history deltas must sum to zero, got %d", total)
	}
}

// 弃牌中途离开后服务端崩溃重启：恢复出来的牌局仍记得他要走，结算后照常移出，
// 牌谱里也有他这一行（座位与底牌随牌桌状态一起持久化）。
func TestPendingLeaveSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	users := []string{"owner", "guest", "third"}
	manager, rooms, created, states, _ := restartFixture(t, users...)
	before := startHand(t, ctx, manager, users)
	leaver := before.CurrentAction.UserID
	afterFold := submitFold(t, ctx, manager, created.RoomID, before, "fold-before-restart")
	if _, err := manager.Leave(ctx, leaver); err != nil {
		t.Fatal(err)
	}
	manager.waitForStatePersistence()

	// 重启后只有留下的人回来；离开者不会重连
	revived, err := NewWithConfig(rooms, zeroRandom{}, ManagerConfig{TableStates: states})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range users {
		if userID == leaver {
			continue
		}
		if _, err := revived.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	if !revived.LeavePending(leaver, created.RoomID) {
		t.Fatal("the pending leave was lost across the restart")
	}
	current, err := revived.Snapshot(ctx, afterFold.CurrentAction.UserID, created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if current.HandID != before.HandID {
		t.Fatalf("the same hand must continue: %s != %s", current.HandID, before.HandID)
	}
	ended := submitFold(t, ctx, revived, created.RoomID, current, "fold-after-restart")
	if ended.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("phase=%s", ended.Phase)
	}
	if _, err := rooms.Current(ctx, leaver); err == nil {
		t.Fatal("leaver must be removed from the room once the restored hand settles")
	}
	record, found := revived.history.Hand(before.HandID)
	if !found {
		t.Fatal("restored hand must be written to history")
	}
	hasLeaver := false
	for _, player := range record.Players {
		if player.UserID == leaver && player.Seat > 0 && len(player.HoleCards) == 2 {
			hasLeaver = true
		}
	}
	if !hasLeaver {
		t.Fatalf("history lost the leaver across the restart: %#v", record.Players)
	}
}

// flakyCashOutRepository 让离桌退还按需失败，模拟数据库一时出错。
type flakyCashOutRepository struct {
	bankroll.Repository
	failCashOut bool
}

func (repository *flakyCashOutRepository) CashOut(
	ctx context.Context, userID, tableID, requestID string, now time.Time,
) (bankroll.Snapshot, error) {
	if repository.failCashOut {
		return bankroll.Snapshot{}, errors.New("database unavailable")
	}
	return repository.Repository.CashOut(ctx, userID, tableID, requestID, now)
}

// 结算时退还失败：离开者还留在成员里且永远不会准备，不补做的话下一手永远开不了。
// 下一次有人准备时整段落盘重做一遍，退还完成、他被移出，之后照常开局，筹码不多不少。
func TestFailedCashOutAtSettlementIsRetriedBeforeTheNextHand(t *testing.T) {
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
	manager, err := NewWithConfig(rooms, zeroRandom{}, ManagerConfig{Bankroll: chips})
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
	if !manager.LeavePending(leaver, created.RoomID) {
		t.Fatal("the leave must stay pending after a failed cash-out")
	}
	if _, found := manager.history.Hand(inHand.HandID); !found {
		t.Fatal("history is written before the cash-out, a cash-out failure must not lose the hand")
	}

	flaky.failCashOut = false
	var remaining []string
	for _, userID := range users {
		if userID != leaver {
			remaining = append(remaining, userID)
		}
	}
	// 第一次准备触发补做：退还完成、移出房间，这一次不开局
	retried, err := manager.SetReady(ctx, remaining[0], true)
	if err != nil {
		t.Fatalf("retry on ready: %v", err)
	}
	if retried.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("the retry call itself must not start a hand, phase=%s", retried.Phase)
	}
	if manager.LeavePending(leaver, created.RoomID) {
		t.Fatal("retry must finish the pending leave")
	}
	if _, err := rooms.Current(ctx, leaver); err == nil {
		t.Fatal("leaver must be out of the room after the retry")
	}
	wallet, err := chips.Snapshot(ctx, leaver)
	if err != nil || wallet.WalletChips != 5_000 || wallet.TableChips != 0 {
		t.Fatalf("leaver wallet after retry=%#v err=%v", wallet, err)
	}
	for _, seat := range retried.Seats {
		if seat.UserID == leaver {
			t.Fatalf("leaver must not come back as a ghost seat: %#v", seat)
		}
	}
	next := startHand(t, ctx, manager, remaining)
	if next.HandID == inHand.HandID {
		t.Fatal("the next hand must start")
	}
	var total int64
	for _, userID := range users {
		snapshot, err := chips.Snapshot(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		total += snapshot.WalletChips + snapshot.TableChips
	}
	if total != 15_000 {
		t.Fatalf("total chips=%d", total)
	}
}

// 退还失败之后、补做之前不允许补码；补做之后补码照常，且补做不会拿上一手的旧结算
// 再写一遍筹码（那会把补进来的筹码在成员记录里盖掉，PostgreSQL 上就是凭空消失）。
func TestSettlementRetryDoesNotOverwriteARebuyMadeInBetween(t *testing.T) {
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
	manager, err := NewWithConfig(rooms, zeroRandom{}, ManagerConfig{Bankroll: chips})
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

	var rebuyer string
	for _, userID := range users {
		if userID != leaver {
			rebuyer = userID
			break
		}
	}
	// 上一手没入账时补码被拒：补进去的钱会被补做的结算覆盖。
	if _, err := manager.Rebuy(ctx, rebuyer, created.RoomID, "rebuy-in-between", 500); ruleCodeOf(err) != "settlement_not_persisted" {
		t.Fatalf("rebuy must be refused until the settlement is persisted, err=%v", err)
	}
	// 有人准备触发补做，之后补码照常，成员记录与账户一致
	if _, err := manager.SetReady(ctx, rebuyer, true); err != nil {
		t.Fatalf("retry on ready: %v", err)
	}
	if _, err := manager.Rebuy(ctx, rebuyer, created.RoomID, "rebuy-after-retry", 500); err != nil {
		t.Fatalf("rebuy after the retry: %v", err)
	}
	balanceAfterRebuy, err := chips.Snapshot(ctx, rebuyer)
	if err != nil {
		t.Fatal(err)
	}
	roomValue, err := rooms.Current(ctx, rebuyer)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range roomValue.Members {
		if member.UserID == rebuyer && member.Stack != balanceAfterRebuy.TableChips {
			t.Fatalf("the retry overwrote the rebuy: member stack=%d, table balance=%d", member.Stack, balanceAfterRebuy.TableChips)
		}
	}
	// 引擎、成员记录与账户三处一致，下一手才能正常结算
	var remaining []string
	for _, userID := range users {
		if userID != leaver {
			remaining = append(remaining, userID)
		}
	}
	next := startHand(t, ctx, manager, remaining)
	ended := submitFold(t, ctx, manager, created.RoomID, next, "fold-next-hand")
	if ended.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("the next hand must settle cleanly, phase=%s", ended.Phase)
	}
	var total int64
	for _, userID := range users {
		snapshot, err := chips.Snapshot(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		total += snapshot.WalletChips + snapshot.TableChips
	}
	if total != 15_000 {
		t.Fatalf("total chips=%d", total)
	}
}
