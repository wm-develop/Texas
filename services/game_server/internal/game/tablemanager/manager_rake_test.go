package tablemanager

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"testing"
	"time"

	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/room"
	"texas/services/game_server/internal/security"
	"texas/services/game_server/internal/tablestate"
)

type rakeFixture struct {
	chips   *bankroll.Service
	rooms   *room.Service
	manager *Manager
	room    room.Room
	users   []string
	states  tablestate.Store
}

func newRakeFixture(t *testing.T, recipient func(context.Context) (string, error)) rakeFixture {
	t.Helper()
	ctx := context.Background()
	chips, err := bankroll.NewService(bankroll.NewMemoryRepository(), time.Now)
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
	states := tablestate.NewMemoryStore()
	manager, err := NewWithConfig(rooms, zeroRandom{}, ManagerConfig{
		Bankroll: chips, TableStates: states, RakeRecipient: recipient,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range users {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	return rakeFixture{chips: chips, rooms: rooms, manager: manager, room: created, users: users, states: states}
}

func adminRecipient(context.Context) (string, error) { return "admin", nil }

// playToShowdown 一路跟注过牌打到摊牌，返回结算快照。
func playToShowdown(t *testing.T, fixture rakeFixture) Snapshot {
	t.Helper()
	ctx := context.Background()
	snapshot := startHand(t, ctx, fixture.manager, fixture.users)
	for steps := 0; snapshot.Phase != holdem.PhaseWaitingNextHand; steps++ {
		if steps > 30 || snapshot.CurrentAction == nil {
			t.Fatalf("hand did not finish: phase=%s", snapshot.Phase)
		}
		action := holdem.ActionCall
		if snapshot.CurrentAction.Options.CanCheck {
			action = holdem.ActionCheck
		}
		var err error
		_, snapshot, err = fixture.manager.SubmitAction(ctx, snapshot.CurrentAction.UserID, fixture.room.RoomID, holdem.ActionRequest{
			ActionID: "step-" + string(rune('a'+steps)), HandID: snapshot.HandID,
			TableRevision: snapshot.TableRevision, Action: action,
		})
		if err != nil {
			t.Fatalf("step %d: %v", steps, err)
		}
	}
	return snapshot
}

func (fixture rakeFixture) playerChips(t *testing.T) int64 {
	t.Helper()
	var total int64
	for _, userID := range fixture.users {
		position, err := fixture.chips.Snapshot(context.Background(), userID)
		if err != nil {
			t.Fatal(err)
		}
		total += position.WalletChips + position.TableChips
	}
	return total
}

// 管理员开了抽水：下一手的底池被抽走一部分，进管理员钱包；结算快照与牌谱都写明抽了多少，
// 玩家与管理员的筹码合计不变。
func TestRakeGoesToTheAdminWalletAndIsReported(t *testing.T) {
	ctx := context.Background()
	fixture := newRakeFixture(t, adminRecipient)
	settings := room.RakeSettings{Enabled: true, BasisPoints: 500, Cap: 2, PostflopEnabled: true, PostflopAmount: 20}
	if _, changed, err := fixture.rooms.UpdateRakeSettings(ctx, fixture.room.RoomID, settings); err != nil || !changed {
		t.Fatalf("update rake: changed=%v err=%v", changed, err)
	}
	ended := playToShowdown(t, fixture)
	// 三人各投 20，底池 60：5% 是 3，封顶 2；打到了翻后再加 20。
	if ended.Settlement == nil || ended.Settlement.Rake != 22 || ended.Settlement.RakeBase != 60 {
		t.Fatalf("settlement=%#v", ended.Settlement)
	}
	// 快照带着房间当前的规则：聊天公告会被刷走，牌桌信息栏靠它随时显示
	if ended.Rake != settings {
		t.Fatalf("snapshot rake=%#v", ended.Rake)
	}
	admin, err := fixture.chips.Snapshot(ctx, "admin")
	if err != nil || admin.WalletChips != 22 {
		t.Fatalf("admin wallet=%#v err=%v", admin, err)
	}
	if players := fixture.playerChips(t); players != 15_000-22 {
		t.Fatalf("players hold %d, want %d", players, 15_000-22)
	}
	record, found := fixture.manager.history.Hand(ended.HandID)
	if !found || record.Rake != 22 {
		t.Fatalf("history rake=%d found=%v", record.Rake, found)
	}
	summary, err := fixture.chips.RakeByRoom(ctx)
	if err != nil || len(summary) != 1 || summary[0].RoomID != fixture.room.RoomID || summary[0].Total != 22 || summary[0].Hands != 1 {
		t.Fatalf("rake summary=%#v err=%v", summary, err)
	}
}

// 牌局进行中改规则：这一手按旧规则结算，下一手才按新规则。
func TestRakeChangesApplyFromTheNextHand(t *testing.T) {
	ctx := context.Background()
	fixture := newRakeFixture(t, adminRecipient)
	snapshot := startHand(t, ctx, fixture.manager, fixture.users)
	if _, _, err := fixture.rooms.UpdateRakeSettings(ctx, fixture.room.RoomID, room.RakeSettings{Enabled: true, BasisPoints: 1000}); err != nil {
		t.Fatal(err)
	}
	for steps := 0; snapshot.Phase != holdem.PhaseWaitingNextHand; steps++ {
		snapshot = submitFold(t, ctx, fixture.manager, fixture.room.RoomID, snapshot, "first-"+string(rune('a'+steps)))
	}
	if snapshot.Settlement.Rake != 0 {
		t.Fatalf("the running hand must keep the old rules, rake=%d", snapshot.Settlement.Rake)
	}
	next := startHand(t, ctx, fixture.manager, fixture.users)
	for steps := 0; next.Phase != holdem.PhaseWaitingNextHand; steps++ {
		next = submitFold(t, ctx, fixture.manager, fixture.room.RoomID, next, "second-"+string(rune('a'+steps)))
	}
	if next.Settlement.Rake != 2 {
		t.Fatalf("the next hand must use the new rules, rake=%d", next.Settlement.Rake)
	}
}

// 找不到收款的管理员账户时不抽：宁可少收，也不能把筹码抽进一个不存在的账户。
func TestNoRakeWithoutARecipient(t *testing.T) {
	ctx := context.Background()
	for name, recipient := range map[string]func(context.Context) (string, error){
		"没有配置":   nil,
		"查不到管理员": func(context.Context) (string, error) { return "", errors.New("admin_unavailable") },
		"返回空账户":  func(context.Context) (string, error) { return "", nil },
		// 升级前就坐在桌上的管理员：收抽水的人不能同时在这张桌上打牌
		"收款人就在这个房间里": func(context.Context) (string, error) { return "owner", nil },
	} {
		fixture := newRakeFixture(t, recipient)
		if _, _, err := fixture.rooms.UpdateRakeSettings(ctx, fixture.room.RoomID, room.RakeSettings{Enabled: true, BasisPoints: 1000}); err != nil {
			t.Fatal(err)
		}
		ended := playToShowdown(t, fixture)
		if ended.Settlement.Rake != 0 {
			t.Fatalf("%s: rake=%d", name, ended.Settlement.Rake)
		}
		if players := fixture.playerChips(t); players != 15_000 {
			t.Fatalf("%s: players hold %d", name, players)
		}
	}
}

// 抽水规则与收款账户随牌局状态保存：打到一半崩溃，恢复后这一手照原规则抽、抽给同一个人。
func TestRakeSurvivesARestartMidHand(t *testing.T) {
	ctx := context.Background()
	fixture := newRakeFixture(t, adminRecipient)
	if _, _, err := fixture.rooms.UpdateRakeSettings(ctx, fixture.room.RoomID, room.RakeSettings{Enabled: true, BasisPoints: 1000}); err != nil {
		t.Fatal(err)
	}
	snapshot := startHand(t, ctx, fixture.manager, fixture.users)
	snapshot = submitFold(t, ctx, fixture.manager, fixture.room.RoomID, snapshot, "fold-before-restart")
	fixture.manager.waitForStatePersistence()

	// 重启后的进程查不到管理员：收款账户必须来自保存的状态，而不是重新查询
	revived, err := NewWithConfig(fixture.rooms, zeroRandom{}, ManagerConfig{
		Bankroll: fixture.chips, TableStates: fixture.states,
		RakeRecipient: func(context.Context) (string, error) { return "", errors.New("admin_unavailable") },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range fixture.users {
		if _, err := revived.Join(ctx, userID, fixture.room.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	current, err := revived.Snapshot(ctx, snapshot.CurrentAction.UserID, fixture.room.RoomID)
	if err != nil || current.HandID != snapshot.HandID {
		t.Fatalf("hand must continue: %s vs %s err=%v", current.HandID, snapshot.HandID, err)
	}
	_, ended, err := revived.SubmitAction(ctx, current.CurrentAction.UserID, fixture.room.RoomID, holdem.ActionRequest{
		ActionID: "fold-after-restart", HandID: current.HandID, TableRevision: current.TableRevision, Action: holdem.ActionFold,
	})
	if err != nil || ended.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("phase=%s err=%v", ended.Phase, err)
	}
	if ended.Settlement.Rake != 2 {
		t.Fatalf("restored hand must keep its rake rules, rake=%d", ended.Settlement.Rake)
	}
	admin, err := fixture.chips.Snapshot(ctx, "admin")
	if err != nil || admin.WalletChips != 2 {
		t.Fatalf("admin wallet=%#v err=%v", admin, err)
	}
}

// 规则校验：比例不超过 10%，翻后加抽不超过一个大盲；没变化时不算修改。
func TestRakeSettingsValidation(t *testing.T) {
	ctx := context.Background()
	fixture := newRakeFixture(t, adminRecipient)
	for name, settings := range map[string]room.RakeSettings{
		"比例超过 10%": {Enabled: true, BasisPoints: 1001},
		"加抽超过一个大盲": {Enabled: true, PostflopEnabled: true, PostflopAmount: 21},
		"上限为负":     {Enabled: true, Cap: -1},
	} {
		if _, _, err := fixture.rooms.UpdateRakeSettings(ctx, fixture.room.RoomID, settings); err == nil {
			t.Fatalf("%s: must be rejected", name)
		}
	}
	valid := room.RakeSettings{Enabled: true, BasisPoints: 1000, Cap: 500, PostflopEnabled: true, PostflopAmount: 20}
	if _, changed, err := fixture.rooms.UpdateRakeSettings(ctx, fixture.room.RoomID, valid); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if _, changed, err := fixture.rooms.UpdateRakeSettings(ctx, fixture.room.RoomID, valid); err != nil || changed {
		t.Fatalf("an identical update must report no change: changed=%v err=%v", changed, err)
	}
	if _, _, err := fixture.rooms.UpdateRakeSettings(ctx, "table_missing", valid); err == nil {
		t.Fatal("unknown room must be rejected")
	}
}
