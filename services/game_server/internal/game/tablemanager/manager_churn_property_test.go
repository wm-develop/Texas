package tablemanager

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/room"
	"texas/services/game_server/internal/security"
)

// 引擎的十万手模拟只覆盖“开局前一次性加入全部玩家、之后阵容不变”的场景，
// 完全不涉及成员进出。真实事故（牌局进行中有人加入导致全桌快照生成失败）正好
// 落在这个空档里。本文件用随机操作序列覆盖成员生命周期，并在每一步之后断言
// 两条不变量：
//
//  1. 任何房间成员的快照都必须能成功生成。一旦失败，该玩家就会停在旧状态上；
//     若对全体失败，整桌都会卡住，而这在协议上没有任何其他可见症状。
//  2. 筹码守恒。除虚拟充值外，任何操作都只在钱包与牌桌之间搬运筹码。
//
// 使用固定种子，失败可复现；种子会打印在错误信息中。

type churnRandom struct{ source *rand.Rand }

func (random churnRandom) Intn(limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	return random.source.Intn(limit), nil
}

type churnHarness struct {
	t          *testing.T
	ctx        context.Context
	rooms      *room.Service
	chips      *bankroll.Service
	manager    *Manager
	roomID     string
	seed       int64
	userIDs    []string
	inRoom     map[string]bool
	totalChips int64
	step       int
	operations map[string]int
}

const (
	churnPlayerCount = 6
	churnStartChips  = 5_000
	churnBuyIn       = 1_000
	churnMaxBuyIn    = 2_000
)

func newChurnHarness(t *testing.T, seed int64) *churnHarness {
	t.Helper()
	ctx := context.Background()
	source := rand.New(rand.NewSource(seed))

	chips, err := bankroll.NewService(bankroll.NewMemoryRepository(), time.Now)
	if err != nil {
		t.Fatalf("bankroll service: %v", err)
	}
	userIDs := make([]string, 0, churnPlayerCount)
	for index := 0; index < churnPlayerCount; index++ {
		userID := fmt.Sprintf("churn_user_%d", index)
		userIDs = append(userIDs, userID)
		if _, err := chips.TopUp(ctx, userID, "topup-"+userID, churnStartChips); err != nil {
			t.Fatalf("top up %s: %v", userID, err)
		}
	}
	hasher, err := security.NewPasswordHasher(1_000, cryptorand.Reader)
	if err != nil {
		t.Fatalf("password hasher: %v", err)
	}
	rooms, err := room.NewService(room.NewMemoryRepository(), hasher, room.ServiceConfig{Bankroll: chips})
	if err != nil {
		t.Fatalf("room service: %v", err)
	}
	created, err := rooms.CreateConfigured(ctx, room.Participant{
		UserID: userIDs[0], DisplayName: "房主",
	}, room.CreateOptions{
		Preset: room.PresetStandard, MaxPlayers: churnPlayerCount,
		SmallBlind: 10, BigBlind: 20, MaxBuyIn: churnMaxBuyIn,
		BuyIn: churnBuyIn, RequestID: "create-owner",
	})
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	manager, err := NewWithConfig(rooms, churnRandom{source: source}, ManagerConfig{Bankroll: chips})
	if err != nil {
		t.Fatalf("table manager: %v", err)
	}
	if _, err := manager.Join(ctx, userIDs[0], created.RoomID); err != nil {
		t.Fatalf("owner join table: %v", err)
	}

	harness := &churnHarness{
		t: t, ctx: ctx, rooms: rooms, chips: chips, manager: manager,
		roomID: created.RoomID, seed: seed, userIDs: userIDs,
		inRoom:     map[string]bool{userIDs[0]: true},
		totalChips: int64(churnPlayerCount) * churnStartChips,
		operations: make(map[string]int),
	}
	return harness
}

// fatalf 在错误信息中带上种子与步数，便于精确复现。
func (harness *churnHarness) fatalf(format string, args ...any) {
	harness.t.Helper()
	harness.t.Fatalf("seed=%d step=%d: "+format, append([]any{harness.seed, harness.step}, args...)...)
}

// checkInvariants 是本测试的核心：每一步操作之后都必须成立。
func (harness *churnHarness) checkInvariants(operation string) {
	harness.t.Helper()

	roomValue, err := harness.rooms.GetForMember(harness.ctx, harness.ownerOrAnyMember(), harness.roomID)
	if err != nil {
		// 房间可能因最后一人离开而关闭，这是合法状态。
		return
	}

	// 不变量 1：每个成员都必须能拿到自己的快照。
	for _, member := range roomValue.Members {
		snapshot, err := harness.manager.Snapshot(harness.ctx, member.UserID, harness.roomID)
		if err != nil {
			harness.fatalf("操作 %s 之后，成员 %s 的快照生成失败：%v", operation, member.UserID, err)
		}
		// 底牌只能出现在本人的快照里，且只有参局者才有。
		if len(snapshot.HoleCards) != 0 && len(snapshot.HoleCards) != 2 {
			harness.fatalf("操作 %s 之后，%s 的底牌数量异常：%d",
				operation, member.UserID, len(snapshot.HoleCards))
		}
		for _, seat := range snapshot.Seats {
			if seat.Stack < 0 {
				harness.fatalf("操作 %s 之后，座位 %d 出现负筹码 %d",
					operation, seat.Seat, seat.Stack)
			}
		}
	}

	// 不变量 2：筹码守恒。虚拟充值之外没有任何操作可以创造或销毁筹码。
	var total int64
	for _, userID := range harness.userIDs {
		snapshot, err := harness.chips.Snapshot(harness.ctx, userID)
		if err != nil {
			harness.fatalf("操作 %s 之后，读取 %s 钱包失败：%v", operation, userID, err)
		}
		if snapshot.WalletChips < 0 || snapshot.TableChips < 0 {
			harness.fatalf("操作 %s 之后，%s 出现负余额 wallet=%d table=%d",
				operation, userID, snapshot.WalletChips, snapshot.TableChips)
		}
		total += snapshot.WalletChips + snapshot.TableChips
	}
	if total != harness.totalChips {
		harness.fatalf("操作 %s 之后筹码不守恒：期望 %d，实际 %d",
			operation, harness.totalChips, total)
	}
}

func (harness *churnHarness) ownerOrAnyMember() string {
	for _, userID := range harness.userIDs {
		if harness.inRoom[userID] {
			return userID
		}
	}
	return harness.userIDs[0]
}

func (harness *churnHarness) membersInRoom() []string {
	result := make([]string, 0, len(harness.userIDs))
	for _, userID := range harness.userIDs {
		if harness.inRoom[userID] {
			result = append(result, userID)
		}
	}
	return result
}

func (harness *churnHarness) outsideRoom() []string {
	result := make([]string, 0, len(harness.userIDs))
	for _, userID := range harness.userIDs {
		if !harness.inRoom[userID] {
			result = append(result, userID)
		}
	}
	return result
}

func (harness *churnHarness) pick(source *rand.Rand, candidates []string) (string, bool) {
	if len(candidates) == 0 {
		return "", false
	}
	return candidates[source.Intn(len(candidates))], true
}

// 下面每个操作都容忍业务错误：随机序列必然产生大量非法操作（未到自己行动、
// 牌局进行中补码等），服务端拒绝它们是正确行为。测试关心的是拒绝之后状态
// 仍然自洽，而不是每个操作都成功。
func (harness *churnHarness) run(source *rand.Rand, operation string) {
	harness.step++
	if harness.perform(source, operation) {
		harness.operations[operation]++
	}
	// 即使本次是空操作，也重新校验一遍不变量：状态不应因无关操作而漂移。
	harness.checkInvariants(operation)
}

// perform 返回本次是否真的执行了操作。
func (harness *churnHarness) perform(source *rand.Rand, operation string) bool {
	switch operation {
	case "join":
		userID, ok := harness.pick(source, harness.outsideRoom())
		if !ok {
			return false
		}
		if _, err := harness.rooms.JoinWithBuyIn(harness.ctx, room.Participant{
			UserID: userID, DisplayName: userID,
		}, room.JoinOptions{
			Code: harness.roomCode(), BuyIn: churnBuyIn,
			RequestID: fmt.Sprintf("join-%s-%d", userID, harness.step),
		}); err != nil {
			return false
		}
		harness.inRoom[userID] = true
		if _, err := harness.manager.Join(harness.ctx, userID, harness.roomID); err != nil {
			harness.fatalf("已入房成员 %s 无法加入牌桌：%v", userID, err)
		}

	case "leave":
		// 保底留两人，避免房间被清空后关闭、后续迭代全部空转，
		// 使随机序列失去意义。房间关闭本身由其他用例覆盖。
		if len(harness.membersInRoom()) <= 2 {
			return false
		}
		userID, ok := harness.pick(source, harness.membersInRoom())
		if !ok {
			return false
		}
		if _, err := harness.manager.Leave(harness.ctx, userID); err != nil {
			return false // 未弃牌的参局玩家不能中途离桌，属正常拒绝
		}
		harness.inRoom[userID] = false

	case "ready":
		userID, ok := harness.pick(source, harness.membersInRoom())
		if !ok {
			return false
		}
		_, _ = harness.manager.SetReady(harness.ctx, userID, source.Intn(4) != 0)

	case "action":
		return harness.submitRandomAction(source)

	case "disconnect":
		userID, ok := harness.pick(source, harness.membersInRoom())
		if !ok {
			return false
		}
		harness.manager.Disconnect(harness.ctx, userID, harness.roomID)

	case "reconnect":
		userID, ok := harness.pick(source, harness.membersInRoom())
		if !ok {
			return false
		}
		if _, err := harness.manager.Join(harness.ctx, userID, harness.roomID); err != nil {
			harness.fatalf("成员 %s 重连失败：%v", userID, err)
		}

	case "rebuy":
		userID, ok := harness.pick(source, harness.membersInRoom())
		if !ok {
			return false
		}
		_, _ = harness.manager.Rebuy(
			harness.ctx, userID, harness.roomID,
			fmt.Sprintf("rebuy-%s-%d", userID, harness.step), 500,
		)

	case "seat_change":
		members := harness.membersInRoom()
		if len(members) < 2 {
			return false
		}
		requester := members[source.Intn(len(members))]
		snapshot, err := harness.manager.Snapshot(harness.ctx, requester, harness.roomID)
		if err != nil || len(snapshot.Seats) == 0 {
			return false
		}
		target := snapshot.Seats[source.Intn(len(snapshot.Seats))]
		_, _ = harness.manager.RequestSeatChange(
			harness.ctx, requester, harness.roomID, target.Seat,
			fmt.Sprintf("swap-%s-%d", requester, harness.step),
		)

	case "reveal":
		userID, ok := harness.pick(source, harness.membersInRoom())
		if !ok {
			return false
		}
		_, _ = harness.manager.ShowHoleCards(harness.ctx, userID, harness.roomID)
	}
	return true
}

func (harness *churnHarness) roomCode() string {
	value, err := harness.rooms.GetForMember(harness.ctx, harness.ownerOrAnyMember(), harness.roomID)
	if err != nil {
		return ""
	}
	return value.Code
}

// submitRandomAction 让当前行动玩家提交一个服务端声明为合法的动作，
// 使随机序列能真正推进牌局，而不是一直停在同一个决策点上。
func (harness *churnHarness) submitRandomAction(source *rand.Rand) bool {
	members := harness.membersInRoom()
	if len(members) == 0 {
		return false
	}
	snapshot, err := harness.manager.Snapshot(harness.ctx, members[0], harness.roomID)
	if err != nil || snapshot.CurrentAction == nil {
		return false
	}
	actor := snapshot.CurrentAction.UserID
	if !harness.inRoom[actor] {
		return false
	}
	options := snapshot.CurrentAction.Options
	choices := make([]holdem.ActionType, 0, 4)
	if options.CanCheck {
		choices = append(choices, holdem.ActionCheck)
	}
	if options.CanCall {
		choices = append(choices, holdem.ActionCall)
	}
	if options.CanFold {
		choices = append(choices, holdem.ActionFold)
	}
	// 全下保持低概率，否则牌局会过快退化为全员全下
	if options.CanAllIn && source.Intn(8) == 0 {
		choices = append(choices, holdem.ActionAllIn)
	}
	if len(choices) == 0 {
		return false
	}
	action := choices[source.Intn(len(choices))]
	_, _, _ = harness.manager.SubmitAction(harness.ctx, actor, harness.roomID, holdem.ActionRequest{
		ActionID:      fmt.Sprintf("action-%d", harness.step),
		HandID:        snapshot.HandID,
		TableRevision: snapshot.TableRevision,
		Action:        action,
	})
	return true
}

// 随机成员进出 + 牌局推进，验证快照始终可生成且筹码始终守恒。
func TestRandomMemberChurnKeepsSnapshotsAndChipsConsistent(t *testing.T) {
	operations := []string{
		"join", "join", "ready", "ready", "ready", "action", "action", "action", "action",
		"leave", "disconnect", "reconnect", "rebuy", "seat_change", "reveal",
	}
	for _, seed := range []int64{1, 7, 20260901, 42, 99991} {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			source := rand.New(rand.NewSource(seed))
			harness := newChurnHarness(t, seed)
			for iteration := 0; iteration < 400; iteration++ {
				harness.run(source, operations[source.Intn(len(operations))])
			}
			// 确认随机序列确实覆盖到了关心的操作，避免测试空转。
			for _, required := range []string{"join", "leave", "action", "disconnect"} {
				if harness.operations[required] == 0 {
					t.Errorf("seed=%d 未覆盖操作 %s，随机序列可能失效", seed, required)
				}
			}
		})
	}
}

// 单独固定住最初的事故场景：牌局进行中有新成员入房时，
// 已在牌桌上的每个人都必须仍能拿到快照。
func TestMidHandJoinNeverBreaksExistingPlayersSnapshots(t *testing.T) {
	harness := newChurnHarness(t, 20260901)
	source := rand.New(rand.NewSource(20260901))

	// 先让两名玩家开局
	for _, userID := range []string{harness.userIDs[1]} {
		if _, err := harness.rooms.JoinWithBuyIn(harness.ctx, room.Participant{
			UserID: userID, DisplayName: userID,
		}, room.JoinOptions{
			Code: harness.roomCode(), BuyIn: churnBuyIn, RequestID: "join-" + userID,
		}); err != nil {
			t.Fatalf("join room: %v", err)
		}
		harness.inRoom[userID] = true
		if _, err := harness.manager.Join(harness.ctx, userID, harness.roomID); err != nil {
			t.Fatalf("join table: %v", err)
		}
	}
	for _, userID := range harness.membersInRoom() {
		if _, err := harness.manager.SetReady(harness.ctx, userID, true); err != nil {
			t.Fatalf("set ready: %v", err)
		}
	}
	snapshot, err := harness.manager.Snapshot(harness.ctx, harness.userIDs[0], harness.roomID)
	if err != nil || snapshot.Phase != holdem.PhasePreflop {
		t.Fatalf("expected a running hand, phase=%s err=%v", snapshot.Phase, err)
	}

	// 牌局进行中，其余玩家逐个入房；每加入一个，所有既有成员都必须仍能拿到快照。
	for index := 2; index < churnPlayerCount; index++ {
		userID := harness.userIDs[index]
		if _, err := harness.rooms.JoinWithBuyIn(harness.ctx, room.Participant{
			UserID: userID, DisplayName: userID,
		}, room.JoinOptions{
			Code: harness.roomCode(), BuyIn: churnBuyIn, RequestID: "midjoin-" + userID,
		}); err != nil {
			t.Fatalf("mid-hand room join for %s: %v", userID, err)
		}
		harness.inRoom[userID] = true
		if _, err := harness.manager.Join(harness.ctx, userID, harness.roomID); err != nil {
			t.Fatalf("mid-hand table join for %s: %v", userID, err)
		}
		harness.checkInvariants("mid_hand_join_" + userID)

		// 新成员不参与本手，也不应拿到底牌
		newcomer, err := harness.manager.Snapshot(harness.ctx, userID, harness.roomID)
		if err != nil {
			t.Fatalf("newcomer snapshot: %v", err)
		}
		if len(newcomer.HoleCards) != 0 {
			t.Fatalf("mid-hand newcomer %s received hole cards", userID)
		}
	}

	// 牌局仍能正常推进到结算
	for iteration := 0; iteration < 200; iteration++ {
		harness.run(source, "action")
		current, err := harness.manager.Snapshot(harness.ctx, harness.userIDs[0], harness.roomID)
		if err != nil {
			t.Fatalf("snapshot while finishing hand: %v", err)
		}
		if current.Phase == holdem.PhaseWaitingNextHand {
			return
		}
	}
	t.Fatal("hand did not reach settlement within the action budget")
}
