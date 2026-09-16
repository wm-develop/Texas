package tablemanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/room"
)

// ruleCodeOf 取出 RuleError 的错误码；不是 RuleError 时返回 err 本身的文本，方便 Fatalf。
func ruleCodeOf(err error) string {
	var rule holdem.RuleError
	if errors.As(err, &rule) {
		return rule.Code
	}
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// newPreferenceTable 建一张 seats 人的桌子并让所有人连上（不准备），返回管理器与房间。
func newPreferenceTable(t *testing.T, seats int) (*Manager, *room.Service, room.Room) {
	t.Helper()
	ctx := context.Background()
	rooms := testRoomService(t)
	created, err := rooms.Create(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, room.PresetStandard, seats, "")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{"guest": "好友", "third": "三号", "fourth": "四号"}
	users := []string{"owner"}
	for _, userID := range []string{"guest", "third", "fourth"}[:seats-1] {
		if _, err := rooms.Join(ctx, room.Participant{UserID: userID, DisplayName: names[userID]}, created.Code, ""); err != nil {
			t.Fatal(err)
		}
		users = append(users, userID)
	}
	manager, err := NewWithConfig(rooms, zeroRandom{}, ManagerConfig{
		AfterFunc: func(_ time.Duration, _ func()) ScheduledTimer { return &fakeScheduledTimer{} },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range users {
		if _, err := manager.Join(ctx, userID, created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	return manager, rooms, created
}

func seatNumberOf(snapshot Snapshot, userID string) int {
	if seat := seatOf(snapshot, userID); seat != nil {
		return seat.Seat
	}
	return 0
}

func TestSeatSwapPreferenceAndRequesterBlock(t *testing.T) {
	ctx := context.Background()
	manager, rooms, created := newPreferenceTable(t, 3)
	snapshot, _ := manager.Snapshot(ctx, "guest", created.RoomID)
	guestSeat := seatNumberOf(snapshot, "guest")
	if snapshot.RequestPreferences != DefaultRequestPreferences() {
		t.Fatalf("default preferences=%#v", snapshot.RequestPreferences)
	}

	// 关掉「允许别人申请换座」后，申请在服务端直接被拒，好友的快照里不会出现待处理申请。
	updated, err := manager.SetRequestPreferences(ctx, "guest", created.RoomID, RequestPreferences{
		AllowSeatSwapRequests: false, AllowHoleCardViewRequests: true,
	})
	if err != nil || updated.RequestPreferences.AllowSeatSwapRequests {
		t.Fatalf("preferences=%#v err=%v", updated.RequestPreferences, err)
	}
	if _, err := manager.RequestSeatChange(ctx, "owner", created.RoomID, guestSeat, "swap-disabled"); ruleCodeOf(err) != "seat_swap_requests_disabled" {
		t.Fatalf("disabled swap err=%v", err)
	}
	snapshot, _ = manager.Snapshot(ctx, "guest", created.RoomID)
	if len(snapshot.SeatSwapRequests) != 0 {
		t.Fatalf("requests leaked through disabled preference: %#v", snapshot.SeatSwapRequests)
	}

	// 恢复默认后申请能到达；好友选「不再接受此人」，申请者被拒且此后再申请直接失败。
	if _, err := manager.SetRequestPreferences(ctx, "guest", created.RoomID, DefaultRequestPreferences()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RequestSeatChange(ctx, "owner", created.RoomID, guestSeat, "swap-1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.RespondSeatSwap(ctx, "guest", created.RoomID, "swap-1", false, DeclineEveryone); ruleCodeOf(err) != "invalid_decline_scope" {
		t.Fatalf("swap must not accept the everyone scope, err=%v", err)
	}
	_, declined, err := manager.RespondSeatSwap(ctx, "guest", created.RoomID, "swap-1", false, DeclineRequester)
	if err != nil || declined == nil || declined.RequesterUserID != "owner" ||
		declined.TargetDisplayName != "好友" || declined.Scope != DeclineRequester {
		t.Fatalf("declined=%#v err=%v", declined, err)
	}
	if _, err := manager.RequestSeatChange(ctx, "owner", created.RoomID, guestSeat, "swap-2"); ruleCodeOf(err) != "seat_swap_requester_blocked" {
		t.Fatalf("blocked requester err=%v", err)
	}
	// 屏蔽只针对那一个人：三号照常能申请。
	if _, err := manager.RequestSeatChange(ctx, "third", created.RoomID, guestSeat, "swap-3"); err != nil {
		t.Fatal(err)
	}
	_, declined, err = manager.RespondSeatSwap(ctx, "guest", created.RoomID, "swap-3", false, "")
	if err != nil || declined == nil || declined.Scope != DeclineOnce {
		t.Fatalf("once decline=%#v err=%v", declined, err)
	}
	if _, err := manager.RequestSeatChange(ctx, "third", created.RoomID, guestSeat, "swap-4"); err != nil {
		t.Fatalf("a once-decline must not block the next request: %v", err)
	}
	// 同意时没有 DeclinedRequest。
	_, declined, err = manager.RespondSeatSwap(ctx, "guest", created.RoomID, "swap-4", true, "")
	if err != nil || declined != nil {
		t.Fatalf("accept declined=%#v err=%v", declined, err)
	}

	// 申请者自己进出房间不会洗掉屏蔽；被申请者离开再回来，屏蔽和偏好才失效。
	if _, err := manager.Leave(ctx, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.Join(ctx, room.Participant{UserID: "owner", DisplayName: "房主"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Join(ctx, "owner", created.RoomID); err != nil {
		t.Fatal(err)
	}
	snapshot, _ = manager.Snapshot(ctx, "owner", created.RoomID)
	guestSeat = seatNumberOf(snapshot, "guest")
	if _, err := manager.RequestSeatChange(ctx, "owner", created.RoomID, guestSeat, "swap-5"); ruleCodeOf(err) != "seat_swap_requester_blocked" {
		t.Fatalf("requester re-entering must stay blocked, err=%v", err)
	}
	if _, err := manager.SetRequestPreferences(ctx, "guest", created.RoomID, RequestPreferences{
		AllowSeatSwapRequests: false, AllowHoleCardViewRequests: false,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Leave(ctx, "guest"); err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.Join(ctx, room.Participant{UserID: "guest", DisplayName: "好友"}, created.Code, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Join(ctx, "guest", created.RoomID); err != nil {
		t.Fatal(err)
	}
	snapshot, _ = manager.Snapshot(ctx, "guest", created.RoomID)
	if snapshot.RequestPreferences != DefaultRequestPreferences() {
		t.Fatalf("preferences must reset after leaving: %#v", snapshot.RequestPreferences)
	}
	guestSeat = seatNumberOf(snapshot, "guest")
	if _, err := manager.RequestSeatChange(ctx, "owner", created.RoomID, guestSeat, "swap-6"); err != nil {
		t.Fatalf("block must be gone after the target left: %v", err)
	}
}

func TestSeatSwapDisablingPreferenceWithdrawsQueuedRequests(t *testing.T) {
	ctx := context.Background()
	manager, _, created := newPreferenceTable(t, 3)
	snapshot, _ := manager.Snapshot(ctx, "guest", created.RoomID)
	guestSeat := seatNumberOf(snapshot, "guest")
	if _, err := manager.RequestSeatChange(ctx, "owner", created.RoomID, guestSeat, "queued"); err != nil {
		t.Fatal(err)
	}
	updated, err := manager.SetRequestPreferences(ctx, "guest", created.RoomID, RequestPreferences{
		AllowSeatSwapRequests: false, AllowHoleCardViewRequests: true,
	})
	if err != nil || len(updated.SeatSwapRequests) != 0 {
		t.Fatalf("queued request survived disabling: %#v err=%v", updated.SeatSwapRequests, err)
	}
	if _, _, err := manager.RespondSeatSwap(ctx, "guest", created.RoomID, "queued", true, ""); ruleCodeOf(err) != "seat_swap_request_not_found" {
		t.Fatalf("withdrawn request still answerable: %v", err)
	}
}

// foldTwo 让四人桌开局并让前两位行动者弃牌，返回两位弃牌者与两位仍在手中的玩家。
func foldTwo(t *testing.T, manager *Manager, roomID string) (folded [2]string, active [2]string) {
	t.Helper()
	ctx := context.Background()
	for _, userID := range []string{"owner", "guest", "third", "fourth"} {
		if _, err := manager.SetReady(ctx, userID, true); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, _ := manager.Snapshot(ctx, "owner", roomID)
	for index := 0; index < 2; index++ {
		actor := snapshot.CurrentAction.UserID
		folded[index] = actor
		var err error
		_, snapshot, err = manager.SubmitAction(ctx, actor, roomID, holdem.ActionRequest{
			ActionID: "fold-" + actor, HandID: snapshot.HandID,
			TableRevision: snapshot.TableRevision, Action: holdem.ActionFold,
		})
		if err != nil || snapshot.Phase == holdem.PhaseWaitingNextHand {
			t.Fatalf("fold %s: phase=%s err=%v", actor, snapshot.Phase, err)
		}
	}
	count := 0
	for _, seat := range snapshot.Seats {
		if seat.Participating && !seat.Folded {
			active[count] = seat.UserID
			count++
		}
	}
	if count != 2 {
		t.Fatalf("active players=%d", count)
	}
	return folded, active
}

func TestHoleCardViewPreferenceGrantAndHandScopedBlocks(t *testing.T) {
	ctx := context.Background()
	manager, _, created := newPreferenceTable(t, 4)
	folded, active := foldTwo(t, manager, created.RoomID)
	first, second := folded[0], folded[1]
	targetA, targetB := active[0], active[1]

	// 已经给看过了：本手不能再向同一个人申请。
	if _, err := manager.RequestHoleCardView(ctx, first, created.RoomID, targetA, "view-a"); err != nil {
		t.Fatal(err)
	}
	if _, declined, err := manager.RespondHoleCardView(ctx, targetA, created.RoomID, "view-a", true, ""); err != nil || declined != nil {
		t.Fatalf("accept declined=%#v err=%v", declined, err)
	}
	if _, err := manager.RequestHoleCardView(ctx, first, created.RoomID, targetA, "view-a-again"); ruleCodeOf(err) != "hole_card_view_already_granted" {
		t.Fatalf("already granted err=%v", err)
	}

	// 两位弃牌者都向 B 申请；B 只拒第一位并屏蔽他，第二位的申请还在。
	if _, err := manager.RequestHoleCardView(ctx, second, created.RoomID, targetB, "view-b-second"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RequestHoleCardView(ctx, first, created.RoomID, targetB, "view-b-first"); err != nil {
		t.Fatal(err)
	}
	afterDecline, declined, err := manager.RespondHoleCardView(ctx, targetB, created.RoomID, "view-b-first", false, DeclineRequester)
	if err != nil || declined == nil || declined.RequesterUserID != first || declined.Scope != DeclineRequester || declined.TargetDisplayName == "" {
		t.Fatalf("declined=%#v err=%v", declined, err)
	}
	if len(afterDecline.HoleCardRequests) != 1 || afterDecline.HoleCardRequests[0].RequesterUserID != second {
		t.Fatalf("other requester's request must survive: %#v", afterDecline.HoleCardRequests)
	}
	if _, err := manager.RequestHoleCardView(ctx, first, created.RoomID, targetB, "view-b-first-2"); ruleCodeOf(err) != "hole_card_view_requester_blocked" {
		t.Fatalf("blocked requester err=%v", err)
	}
	// 屏蔽的是「向 B 申请」，向 A 申请不受影响（second 还没申请过 A）。
	if _, err := manager.RequestHoleCardView(ctx, second, created.RoomID, targetA, "view-a-second"); err != nil {
		t.Fatal(err)
	}

	// B 对第二位选「本手不再接受任何人」：两人都申请不了。
	_, declined, err = manager.RespondHoleCardView(ctx, targetB, created.RoomID, "view-b-second", false, DeclineEveryone)
	if err != nil || declined == nil || declined.Scope != DeclineEveryone {
		t.Fatalf("declined=%#v err=%v", declined, err)
	}
	for _, requester := range []string{first, second} {
		if _, err := manager.RequestHoleCardView(ctx, requester, created.RoomID, targetB, "view-b-"+requester); ruleCodeOf(err) != "hole_card_view_blocked_this_hand" {
			t.Fatalf("%s: blocked this hand err=%v", requester, err)
		}
	}

	// A 在设置里关掉看牌申请：已排队的申请被撤掉，新申请直接被拒。
	updated, err := manager.SetRequestPreferences(ctx, targetA, created.RoomID, RequestPreferences{
		AllowSeatSwapRequests: true, AllowHoleCardViewRequests: false,
	})
	if err != nil || len(updated.HoleCardRequests) != 0 || updated.RequestPreferences.AllowHoleCardViewRequests {
		t.Fatalf("snapshot=%#v err=%v", updated.HoleCardRequests, err)
	}
	if _, err := manager.RequestHoleCardView(ctx, second, created.RoomID, targetA, "view-a-disabled"); ruleCodeOf(err) != "hole_card_view_requests_disabled" {
		t.Fatalf("disabled err=%v", err)
	}
	// 已经拿到的私下看牌不受偏好影响。
	firstSnapshot, _ := manager.Snapshot(ctx, first, created.RoomID)
	if len(firstSnapshot.PrivateReveals) != 1 || firstSnapshot.PrivateReveals[0].PlayerID != targetA {
		t.Fatalf("private reveals=%#v", firstSnapshot.PrivateReveals)
	}

	// 本手结束：看牌屏蔽清空，房间级偏好保留。
	snapshot, _ := manager.Snapshot(ctx, "owner", created.RoomID)
	if _, ended, err := manager.SubmitAction(ctx, snapshot.CurrentAction.UserID, created.RoomID, holdem.ActionRequest{
		ActionID: "fold-to-end", HandID: snapshot.HandID,
		TableRevision: snapshot.TableRevision, Action: holdem.ActionFold,
	}); err != nil || ended.Phase != holdem.PhaseWaitingNextHand {
		t.Fatalf("phase=%s err=%v", ended.Phase, err)
	}
	runtime := manager.existingRuntime(created.RoomID)
	runtime.mu.Lock()
	blocks, preferences := len(runtime.holeCardViewBlocks), runtime.preferencesFor(targetA)
	runtime.mu.Unlock()
	if blocks != 0 {
		t.Fatalf("hand-scoped blocks survived the hand: %d", blocks)
	}
	if preferences.AllowHoleCardViewRequests {
		t.Fatalf("room-scoped preference must survive the hand: %#v", preferences)
	}
}

func TestDeclineScopeValidation(t *testing.T) {
	for _, testCase := range []struct {
		scope         string
		allowEveryone bool
		want          string
		wantErr       bool
	}{
		{"", false, DeclineOnce, false},
		{DeclineOnce, true, DeclineOnce, false},
		{DeclineRequester, false, DeclineRequester, false},
		{DeclineEveryone, true, DeclineEveryone, false},
		{DeclineEveryone, false, "", true},
		{"forever", true, "", true},
	} {
		got, err := normalizeDeclineScope(testCase.scope, testCase.allowEveryone)
		if (err != nil) != testCase.wantErr || got != testCase.want {
			t.Fatalf("scope=%q everyone=%v: got=%q err=%v", testCase.scope, testCase.allowEveryone, got, err)
		}
	}
}

func TestRequestPreferencesAndBlocksSurviveARestart(t *testing.T) {
	ctx := context.Background()
	manager, _, created, _, restart := restartFixture(t, "owner", "guest", "third")
	before := startHand(t, ctx, manager, []string{"owner", "guest", "third"})
	folder := before.CurrentAction.UserID
	before = submitFold(t, ctx, manager, created.RoomID, before, "fold-before-restart")
	var target string
	for _, seat := range before.Seats {
		if seat.UserID != folder && seat.Participating && !seat.Folded {
			target = seat.UserID
			break
		}
	}
	if _, err := manager.RequestHoleCardView(ctx, folder, created.RoomID, target, "view-before-restart"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.RespondHoleCardView(ctx, target, created.RoomID, "view-before-restart", false, DeclineRequester); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetRequestPreferences(ctx, folder, created.RoomID, RequestPreferences{
		AllowSeatSwapRequests: false, AllowHoleCardViewRequests: true,
	}); err != nil {
		t.Fatal(err)
	}
	manager.waitForStatePersistence()

	revived := restart(t)
	// 屏蔽跨重启保留：崩溃前被拒过的人，恢复后照样申请不了。
	if _, err := revived.RequestHoleCardView(ctx, folder, created.RoomID, target, "view-after-restart"); ruleCodeOf(err) != "hole_card_view_requester_blocked" {
		t.Fatalf("block lost across the restart: err=%v", err)
	}
	after, err := revived.Snapshot(ctx, folder, created.RoomID)
	if err != nil || after.RequestPreferences.AllowSeatSwapRequests || !after.RequestPreferences.AllowHoleCardViewRequests {
		t.Fatalf("preferences lost across the restart: %#v err=%v", after.RequestPreferences, err)
	}
}
