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
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/replay"
	"texas/services/game_server/internal/room"
	"texas/services/game_server/internal/security"
)

// 回放是从牌谱「推」出来的：牌谱只存动作与结果，每一步的筹码与底池都要重算。
// 这里用真实的牌桌管理层随机打几百手——下注、加注、全下、超时、发两次、
// 弃牌后离开再回来、抽水——然后从每一位参与者的视角回放每一手，要求：
//
//  1. 回放能生成（生成器会用牌谱记录的结束筹码逐人自检，推错了就报错）；
//  2. 任何一帧都不出现请求者看不到的底牌；
//  3. 旧牌谱没有盲注座位时，按规则推出来的座位与引擎实际用的一致；
//  4. 动作一个不少地出现在时间轴上。
func TestReplayRebuildsEveryRandomHandFromEverySeat(t *testing.T) {
	// 两人桌的盲注规则与多人桌不同（庄位下小盲），必须单独覆盖
	for _, players := range []int{2, 3, 6, 9} {
		for _, seed := range []int64{3, 11, 2026, 424242} {
			t.Run(fmt.Sprintf("players_%d_seed_%d", players, seed), func(t *testing.T) {
				runReplayProperty(t, seed, players, 120)
			})
		}
	}
}

type replayClock struct{ now time.Time }

func (clock *replayClock) Now() time.Time { return clock.now }

func runReplayProperty(t *testing.T, seed int64, playerCount, hands int) {
	t.Helper()
	ctx := context.Background()
	source := rand.New(rand.NewSource(seed))
	clock := &replayClock{now: time.Unix(1_800_000_000, 0)}
	chips, err := bankroll.NewService(bankroll.NewMemoryRepository(), clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	users := make([]string, playerCount)
	for index := range users {
		users[index] = fmt.Sprintf("replay_%d", index)
		if _, err := chips.TopUp(ctx, users[index], "topup-"+users[index], 1_000_000); err != nil {
			t.Fatal(err)
		}
	}
	hasher, err := security.NewPasswordHasher(1_000, cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rooms, err := room.NewService(room.NewMemoryRepository(), hasher, room.ServiceConfig{Bankroll: chips, Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	buyIn := func() int64 { return int64(1+source.Intn(40)) * 50 }
	created, err := rooms.CreateConfigured(ctx, room.Participant{UserID: users[0], DisplayName: "房主"}, room.CreateOptions{
		Preset: room.PresetStandard, SmallBlind: 10, BigBlind: 20, MaxBuyIn: 2_000, BuyIn: buyIn(), RequestID: "create",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range users[1:] {
		if _, err := rooms.JoinWithBuyIn(ctx, room.Participant{UserID: userID, DisplayName: userID}, room.JoinOptions{
			Code: created.Code, BuyIn: buyIn(), RequestID: "join-" + userID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := rooms.UpdateRakeSettings(ctx, created.RoomID, room.RakeSettings{
		Enabled: true, BasisPoints: 500, Cap: 60, PostflopEnabled: true, PostflopAmount: 10,
	}); err != nil {
		t.Fatal(err)
	}
	store := history.NewInMemoryStore()
	manager, err := NewWithConfig(rooms, churnRandom{source: rand.New(rand.NewSource(seed + 1))}, ManagerConfig{
		Bankroll: chips, History: store, Now: clock.Now,
		RakeRecipient: func(context.Context) (string, error) { return "rake_admin", nil },
		AfterFunc:     func(time.Duration, func()) ScheduledTimer { return &fakeScheduledTimer{} },
	})
	if err != nil {
		t.Fatal(err)
	}
	inRoom := make(map[string]bool, len(users))
	for _, userID := range users {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
		inRoom[userID] = true
	}

	step := 0
	counts := map[string]int{}
	fail := func(format string, args ...any) {
		t.Helper()
		t.Fatalf("seed=%d step=%d: "+format, append([]any{seed, step}, args...)...)
	}
	for played := 0; played < hands; {
		step++
		if step > hands*400 {
			fail("too many steps; played %d hands", played)
		}
		// 手间：离开过的人回来，输光的人补码，然后全员准备
		for _, userID := range users {
			if inRoom[userID] || manager.LeavePending(userID, created.RoomID) {
				continue
			}
			if _, err := rooms.JoinWithBuyIn(ctx, room.Participant{UserID: userID, DisplayName: userID}, room.JoinOptions{
				Code: created.Code, BuyIn: buyIn(), RequestID: fmt.Sprintf("rejoin-%s-%d", userID, step),
			}); err != nil {
				continue
			}
			if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
				fail("rejoin %s: %v", userID, err)
			}
			inRoom[userID] = true
			counts["rejoin"]++
		}
		snapshot, err := manager.Snapshot(ctx, firstIn(users, inRoom), created.RoomID)
		if err != nil {
			fail("snapshot: %v", err)
		}
		for _, seat := range snapshot.Seats {
			if seat.Stack == 0 {
				_, _ = manager.Rebuy(ctx, seat.UserID, created.RoomID, fmt.Sprintf("rebuy-%s-%d", seat.UserID, step), buyIn())
			}
		}
		for _, userID := range users {
			if inRoom[userID] {
				snapshot, _ = manager.SetReady(ctx, userID, true)
			}
		}
		// 短码在盲注位就全下时，一手开局就直接进入发牌次数选择
		if snapshot.Phase != holdem.PhasePreflop && snapshot.Phase != holdem.PhaseRunoutChoice {
			continue
		}
		handID := snapshot.HandID

		for snapshot.Phase != holdem.PhaseWaitingNextHand {
			step++
			if step > hands*400 {
				fail("hand %s never ended", handID)
			}
			if snapshot.Phase == holdem.PhaseRunoutChoice && snapshot.RunoutChoice != nil {
				for _, userID := range snapshot.RunoutChoice.EligiblePlayerIDs {
					if _, chosen := snapshot.RunoutChoice.Choices[userID]; chosen {
						continue
					}
					if next, err := manager.SubmitRunoutChoice(ctx, userID, created.RoomID, 1+source.Intn(2)); err == nil {
						snapshot = next
					}
					counts["runout_choice"]++
					break
				}
				snapshot, _ = manager.Snapshot(ctx, firstIn(users, inRoom), created.RoomID)
				continue
			}
			if snapshot.CurrentAction == nil {
				fail("no current action in phase %s", snapshot.Phase)
			}
			actor := snapshot.CurrentAction.UserID
			if source.Intn(15) == 0 {
				// 超时：拨快时钟越过截止时间。每手前两次超时只会自动用掉加时卡，
				// 所以一直拨到服务端真的代为过牌或弃牌为止
				runtime := manager.existingRuntime(created.RoomID)
				for attempt := 0; attempt < 5; attempt++ {
					runtime.mu.Lock()
					revision := runtime.engine.Revision()
					clock.now = runtime.deadline.Add(time.Second)
					generation := runtime.timerGeneration
					runtime.mu.Unlock()
					manager.handleTimeout(created.RoomID, generation)
					runtime.mu.Lock()
					acted := runtime.engine.Revision() != revision
					runtime.mu.Unlock()
					if acted {
						break
					}
				}
			} else {
				request := randomReplayAction(source, snapshot, fmt.Sprintf("act-%d", step))
				if _, _, err := manager.SubmitAction(ctx, actor, created.RoomID, request); err != nil {
					fail("action %s by %s: %v", request.Action, actor, err)
				}
				counts[string(request.Action)]++
				// 弃牌后偶尔直接离开，结算后再回来
				if request.Action == holdem.ActionFold && playerCount > 2 && source.Intn(6) == 0 {
					if _, err := manager.Leave(ctx, actor); err == nil {
						inRoom[actor] = false
						counts["leave_midhand"]++
					}
				}
			}
			snapshot, err = manager.Snapshot(ctx, firstIn(users, inRoom), created.RoomID)
			if err != nil {
				fail("snapshot: %v", err)
			}
		}
		played++
		verifyReplay(t, seed, store, handID, counts)
	}
	// 这些计数取自落库的牌谱，而不是测试自己做了什么：例如超时要真的记成
	// timedOut 动作（前两次超时只是用掉加时卡），发两次要真的有两块牌面
	required := []string{
		"recorded_bet", "recorded_raise", "recorded_all_in", "recorded_fold",
		"timed_out", "run_twice", "refund", "showdown", "raked",
	}
	if playerCount > 2 {
		required = append(required, "leave_midhand")
	}
	for _, required := range required {
		if counts[required] == 0 {
			t.Errorf("seed=%d 没有覆盖到 %s，随机序列可能失效：%v", seed, required, counts)
		}
	}
	t.Logf("seed=%d players=%d counts=%v", seed, playerCount, counts)
}

func firstIn(users []string, inRoom map[string]bool) string {
	for _, userID := range users {
		if inRoom[userID] {
			return userID
		}
	}
	return users[0]
}

func randomReplayAction(source *rand.Rand, snapshot Snapshot, actionID string) holdem.ActionRequest {
	options := snapshot.CurrentAction.Options
	request := holdem.ActionRequest{ActionID: actionID, HandID: snapshot.HandID, TableRevision: snapshot.TableRevision}
	var choices []holdem.ActionType
	if options.CanCheck {
		choices = append(choices, holdem.ActionCheck, holdem.ActionCheck)
	}
	if options.CanCall {
		choices = append(choices, holdem.ActionCall, holdem.ActionCall)
	}
	if options.CanFold {
		choices = append(choices, holdem.ActionFold)
	}
	if options.CanBet {
		choices = append(choices, holdem.ActionBet)
	}
	if options.CanRaise {
		choices = append(choices, holdem.ActionRaise)
	}
	if options.CanAllIn && source.Intn(4) == 0 {
		choices = append(choices, holdem.ActionAllIn)
	}
	request.Action = choices[source.Intn(len(choices))]
	if request.Action == holdem.ActionBet || request.Action == holdem.ActionRaise {
		// 普通下注/加注是小盲整数倍，取最小与最大之间的一个
		const unit = 10
		low := (options.MinRaiseTo + unit - 1) / unit
		high := options.MaxRaiseTo / unit
		if high < low {
			request.Action = holdem.ActionAllIn
			return request
		}
		request.RaiseTo = (low + source.Int63n(high-low+1)) * unit
	}
	return request
}

func verifyReplay(t *testing.T, seed int64, store *history.InMemoryStore, handID string, counts map[string]int) {
	t.Helper()
	full, found := store.Hand(handID)
	if !found {
		t.Fatalf("seed=%d: hand %s was not recorded", seed, handID)
	}
	for _, action := range full.Actions {
		counts["recorded_"+action.Type]++
		if action.TimedOut {
			counts["timed_out"]++
		}
	}
	if len(full.RunoutBoards) > 1 {
		counts["run_twice"]++
	}
	if full.Rake > 0 {
		counts["raked"]++
	}
	if full.SmallBlindSeat == 0 || full.BigBlindSeat == 0 || full.SmallBlind != 10 || full.BigBlind != 20 {
		t.Fatalf("seed=%d: hand %s did not record its blinds: %+v", seed, handID, full)
	}
	revealed := make(map[string]bool, len(full.RevealedHands))
	for _, shown := range full.RevealedHands {
		revealed[shown.PlayerID] = true
	}
	for _, player := range full.Players {
		redacted, err := store.HandForPlayer(player.UserID, handID)
		if err != nil {
			t.Fatalf("seed=%d: HandForPlayer(%s): %v", seed, player.UserID, err)
		}
		timeline, err := replay.Build(redacted, player.UserID)
		if err != nil {
			t.Fatalf("seed=%d: replay of %s for %s: %v\nhand=%+v", seed, handID, player.UserID, err, full)
		}
		actions := 0
		for _, step := range timeline.Steps {
			switch step.Kind {
			case replay.KindAction:
				actions++
			case replay.KindRefund:
				counts["refund"]++
			case replay.KindShowdown:
				counts["showdown"]++
			}
			for _, seat := range step.Seats {
				if len(seat.HoleCards) == 0 || seat.UserID == player.UserID {
					continue
				}
				publicNow := (step.Kind == replay.KindShowdown || step.Kind == replay.KindSettle) && revealed[seat.UserID]
				if !publicNow {
					t.Fatalf("seed=%d: %s sees %s's hidden cards %v at step %d (%s)",
						seed, player.UserID, seat.UserID, seat.HoleCards, step.Index, step.Kind)
				}
			}
		}
		if actions != len(full.Actions) {
			t.Fatalf("seed=%d: replay has %d actions, the record has %d", seed, actions, len(full.Actions))
		}
		// 旧牌谱没有盲注座位：按规则推出来的必须与引擎实际用的一样
		legacy := redacted
		legacy.SmallBlindSeat, legacy.BigBlindSeat = 0, 0
		derived, err := replay.Build(legacy, player.UserID)
		if err != nil {
			t.Fatalf("seed=%d: legacy replay of %s: %v", seed, handID, err)
		}
		if derived.SmallBlindSeat != full.SmallBlindSeat || derived.BigBlindSeat != full.BigBlindSeat {
			t.Fatalf("seed=%d: derived blinds %d/%d, engine used %d/%d (dealer %d, players %+v)",
				seed, derived.SmallBlindSeat, derived.BigBlindSeat, full.SmallBlindSeat, full.BigBlindSeat,
				full.DealerSeat, full.Players)
		}
	}
}
