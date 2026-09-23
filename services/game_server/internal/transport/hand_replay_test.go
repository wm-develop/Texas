package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/replay"
)

// 回放接口端到端：两人打一手到摊牌，各自取回放。本人的底牌每一帧都在，
// 对手的底牌只有摊牌之后才出现；局外人、未登录、不存在的手号都拿不到。
func TestHandReplayIsRedactedPerViewerAndOnlyForParticipants(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newOwnerFixture(t)
	outsider, err := fixture.accounts.Register(ctx, "replay_outsider", "局外人", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	roomID := fixture.created.RoomID
	players := map[string]account.AuthResult{
		fixture.owner.User.UserID: fixture.owner, fixture.guest.User.UserID: fixture.guest,
	}
	for userID := range players {
		if _, err := fixture.tables.Join(ctx, userID, roomID); err != nil {
			t.Fatal(err)
		}
	}
	for userID := range players {
		if _, err := fixture.tables.SetReady(ctx, userID, true); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := fixture.tables.Snapshot(ctx, fixture.owner.User.UserID, roomID)
	if err != nil {
		t.Fatal(err)
	}
	handID := snapshot.HandID
	for steps := 0; snapshot.Phase != holdem.PhaseWaitingNextHand; steps++ {
		if steps > 20 || snapshot.CurrentAction == nil {
			t.Fatalf("hand did not finish: phase=%s", snapshot.Phase)
		}
		action := holdem.ActionCall
		if snapshot.CurrentAction.Options.CanCheck {
			action = holdem.ActionCheck
		}
		_, snapshot, err = fixture.tables.SubmitAction(ctx, snapshot.CurrentAction.UserID, roomID, holdem.ActionRequest{
			ActionID: "replay-step-" + string(rune('a'+steps)), HandID: snapshot.HandID,
			TableRevision: snapshot.TableRevision, Action: action,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	path := fixture.server.URL + "/v1/hands/" + handID + "/replay"
	for viewerID, viewer := range players {
		response := doJSONRequest(t, http.MethodGet, path, viewer.AccessToken, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("replay status=%d body=%s", response.StatusCode, readBody(response))
		}
		var timeline replay.Timeline
		decodeBody(t, response, &timeline)
		if timeline.HandID != handID || len(timeline.Steps) < 5 || !timeline.Showdown ||
			timeline.SmallBlind != 10 || timeline.BigBlind != 20 {
			t.Fatalf("timeline=%+v", timeline)
		}
		first, last := timeline.Steps[0], timeline.Steps[len(timeline.Steps)-1]
		if first.Kind != replay.KindBlinds || first.Pot != 30 || last.Kind != replay.KindSettle {
			t.Fatalf("first=%+v last=%+v", first, last)
		}
		for _, step := range timeline.Steps {
			for _, seat := range step.Seats {
				switch {
				case seat.UserID == viewerID && len(seat.HoleCards) != 2:
					t.Fatalf("%s must see own cards at step %d", viewerID, step.Index)
				case seat.UserID != viewerID && step.Kind != replay.KindShowdown && step.Kind != replay.KindSettle &&
					len(seat.HoleCards) != 0:
					t.Fatalf("%s sees the opponent's cards before showdown at step %d", viewerID, step.Index)
				case seat.UserID != viewerID && step.Kind == replay.KindShowdown && len(seat.HoleCards) != 2:
					t.Fatalf("%s must see the cards shown at showdown", viewerID)
				}
			}
		}
	}

	// 翻页：第一页带着这手；拿它当游标再翻就没有了
	var page struct {
		Hands []struct {
			HandID string `json:"handId"`
		} `json:"hands"`
	}
	decodeBody(t, doJSONRequest(t, http.MethodGet, fixture.server.URL+"/v1/hands/recent?limit=5", fixture.owner.AccessToken, nil), &page)
	if len(page.Hands) != 1 || page.Hands[0].HandID != handID {
		t.Fatalf("first page=%+v", page)
	}
	decodeBody(t, doJSONRequest(t, http.MethodGet, fixture.server.URL+"/v1/hands/recent?limit=5&before="+handID, fixture.owner.AccessToken, nil), &page)
	if len(page.Hands) != 0 {
		t.Fatalf("second page=%+v", page)
	}

	for name, check := range map[string]struct {
		url, token string
		status     int
	}{
		"局外人":    {path, outsider.AccessToken, http.StatusNotFound},
		"未登录":    {path, "", http.StatusUnauthorized},
		"不存在的手号": {fixture.server.URL + "/v1/hands/no_such_hand/replay", fixture.owner.AccessToken, http.StatusNotFound},
		"局外人当游标": {fixture.server.URL + "/v1/hands/recent?before=" + handID, outsider.AccessToken, http.StatusNotFound},
	} {
		response := doJSONRequest(t, http.MethodGet, check.url, check.token, nil)
		body := readBody(response)
		if response.StatusCode != check.status {
			t.Fatalf("%s: status=%d body=%s", name, response.StatusCode, body)
		}
		if check.status == http.StatusNotFound {
			var payload struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal([]byte(body), &payload); err != nil || payload.Error != "hand_not_found" {
				t.Fatalf("%s: body=%s", name, body)
			}
		}
	}
}

// 牌谱推不出可信的录像时返回 422 replay_unavailable，而不是给一份算错的。
func TestHandReplayRefusesARecordThatDoesNotAddUp(t *testing.T) {
	fixture := newOwnerFixture(t)
	owner, guest := fixture.owner.User.UserID, fixture.guest.User.UserID
	now := time.Now().UTC()
	// 输赢之和为 0，牌谱本身能存进去；但动作说房主只投了 10，结束筹码却少了 500
	if err := fixture.hands.Append(history.Hand{
		HandID: "broken_hand", RoomID: fixture.created.RoomID, RoomCode: fixture.created.Code,
		DealerSeat: 1, StartedAt: now, EndedAt: now, SmallBlind: 10, BigBlind: 20,
		Players: []history.PlayerResult{
			{UserID: owner, DisplayName: "房主", Seat: 1, StartingStack: 2000, EndingStack: 1500, Delta: -500},
			{UserID: guest, DisplayName: "客人", Seat: 2, StartingStack: 2000, EndingStack: 2500, Delta: 500},
		},
		Actions: []history.Action{{UserID: owner, Sequence: 1, Street: "preflop", Type: "fold"}},
		PotAwards: []holdem.PotAward{{
			Amount: 30, WinnerPlayerIDs: []string{guest},
			Payouts: []holdem.Payout{{PlayerID: guest, Amount: 30}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	response := doJSONRequest(t, http.MethodGet, fixture.server.URL+"/v1/hands/broken_hand/replay", fixture.owner.AccessToken, nil)
	body := readBody(response)
	if response.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "replay_unavailable") {
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
}
