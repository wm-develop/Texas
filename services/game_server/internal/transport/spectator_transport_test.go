package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/protocol"
	"texas/services/game_server/internal/room"
)

// 观战者能看到所有人的手牌，房主可以不让他们发言。客户端隐藏入口只是体验，
// 服务端必须自己校验。
func TestSpectatorChatEmoteAndVoiceFollowOwnerSettings(t *testing.T) {
	ctx := context.Background()
	fixture := newOwnerFixture(t)
	for _, user := range []string{fixture.owner.User.UserID, fixture.guest.User.UserID} {
		if _, err := fixture.tables.Join(ctx, user, fixture.created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.tables.EnterSpectate(ctx, fixture.guest.User.UserID, fixture.created.RoomID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.rooms.UpdateSpectatorSettings(ctx, fixture.owner.User.UserID, room.SpectatorSettings{
		FeeBigBlinds: 10, VoiceAllowed: false, ChatAllowed: false, EmoteAllowed: false,
	}); err != nil {
		t.Fatal(err)
	}

	socketCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	guestSocket := dialTestSocket(t, socketCtx, fixture.server.URL)
	defer guestSocket.CloseNow()
	authenticateTestSocket(t, socketCtx, guestSocket, fixture.guest.AccessToken, "guest-device")
	joinTestTable(t, socketCtx, guestSocket, fixture.created.RoomID)

	writeTestEnvelope(t, socketCtx, guestSocket, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableChatSend), RequestID: "chat-1",
		Payload: json.RawMessage(`{"clientMessageId":"m1","kind":"text","content":"hi"}`),
	})
	rejected := readUntilType(t, socketCtx, guestSocket, protocol.TypeTableChatRejected)
	if code := errorCodeOf(t, rejected); code != "spectator_chat_disabled" {
		t.Fatalf("chat should be refused for a muted spectator, got %s", code)
	}

	writeTestEnvelope(t, socketCtx, guestSocket, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTablePlayerInteract), RequestID: "emote-1",
		Payload: json.RawMessage(`{"kind":"praise","targetUserId":"` + fixture.owner.User.UserID + `"}`),
	})
	if code := errorCodeOf(t, readUntilType(t, socketCtx, guestSocket, protocol.TypeSystemError)); code != "spectator_emote_disabled" {
		t.Fatalf("emote should be refused, got %s", code)
	}

	writeTestEnvelope(t, socketCtx, guestSocket, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableVoiceStateSet), RequestID: "voice-1",
		Payload: json.RawMessage(`{"joined":true,"microphoneEnabled":true}`),
	})
	if code := errorCodeOf(t, readUntilType(t, socketCtx, guestSocket, protocol.TypeSystemError)); code != "spectator_voice_disabled" {
		t.Fatalf("microphone should be refused, got %s", code)
	}

	// 只听不说仍然允许：观战者本来就该能听语音
	writeTestEnvelope(t, socketCtx, guestSocket, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableVoiceStateSet), RequestID: "voice-2",
		Payload: json.RawMessage(`{"joined":true,"microphoneEnabled":false}`),
	})
	readUntilType(t, socketCtx, guestSocket, protocol.TypeTableVoiceStateSet)
}

// 进观战与上桌都走 WebSocket；手间立即生效。
func TestSpectateAndTakeSeatOverWebSocket(t *testing.T) {
	ctx := context.Background()
	fixture := newOwnerFixture(t)
	for _, user := range []string{fixture.owner.User.UserID, fixture.guest.User.UserID} {
		if _, err := fixture.tables.Join(ctx, user, fixture.created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	socketCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	guestSocket := dialTestSocket(t, socketCtx, fixture.server.URL)
	defer guestSocket.CloseNow()
	authenticateTestSocket(t, socketCtx, guestSocket, fixture.guest.AccessToken, "guest-device")
	joinTestTable(t, socketCtx, guestSocket, fixture.created.RoomID)

	writeTestEnvelope(t, socketCtx, guestSocket, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableSpectateEnter), RequestID: "spectate-1",
		Payload: json.RawMessage(`{}`),
	})
	entered := readUntilType(t, socketCtx, guestSocket, protocol.TypeTableSpectateEntered)
	var result protocol.SpectateResultPayload
	if err := json.Unmarshal(entered.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.Pending {
		t.Fatal("between hands the switch must apply at once")
	}
	snapshot, err := fixture.tables.Snapshot(ctx, fixture.guest.User.UserID, fixture.created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Spectating || len(snapshot.Spectators) != 1 || len(snapshot.Seats) != 1 {
		t.Fatalf("guest should be the only spectator with the owner alone at the table: %#v", snapshot)
	}

	writeTestEnvelope(t, socketCtx, guestSocket, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableSeatTake), RequestID: "seat-1",
		Payload: json.RawMessage(`{}`),
	})
	readUntilType(t, socketCtx, guestSocket, protocol.TypeTableSeatTaken)
	snapshot, err = fixture.tables.Snapshot(ctx, fixture.guest.User.UserID, fixture.created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Spectating || len(snapshot.Spectators) != 0 || len(snapshot.Seats) != 2 {
		t.Fatalf("guest should be back at the table: %#v", snapshot)
	}
}

// 观战设置只有房主能改，且看牌费有上限。
func TestSpectatorSettingsRequireOwnerAndStayInRange(t *testing.T) {
	ctx := context.Background()
	fixture := newOwnerFixture(t)
	for _, user := range []string{fixture.owner.User.UserID, fixture.guest.User.UserID} {
		if _, err := fixture.tables.Join(ctx, user, fixture.created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	socketCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	guestSocket := dialTestSocket(t, socketCtx, fixture.server.URL)
	defer guestSocket.CloseNow()
	authenticateTestSocket(t, socketCtx, guestSocket, fixture.guest.AccessToken, "guest-device")
	joinTestTable(t, socketCtx, guestSocket, fixture.created.RoomID)
	writeTestEnvelope(t, socketCtx, guestSocket, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableSpectatorSettingsSet), RequestID: "settings-guest",
		Payload: json.RawMessage(`{"feeBigBlinds":0,"voiceAllowed":true,"chatAllowed":true,"emoteAllowed":true}`),
	})
	if code := errorCodeOf(t, readUntilType(t, socketCtx, guestSocket, protocol.TypeSystemError)); code != "owner_required" {
		t.Fatalf("a guest must not change spectator settings, got %s", code)
	}

	ownerSocket := dialTestSocket(t, socketCtx, fixture.server.URL)
	defer ownerSocket.CloseNow()
	authenticateTestSocket(t, socketCtx, ownerSocket, fixture.owner.AccessToken, "owner-device")
	joinTestTable(t, socketCtx, ownerSocket, fixture.created.RoomID)
	writeTestEnvelope(t, socketCtx, ownerSocket, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableSpectatorSettingsSet), RequestID: "settings-too-high",
		Payload: json.RawMessage(`{"feeBigBlinds":101,"voiceAllowed":true,"chatAllowed":true,"emoteAllowed":true}`),
	})
	if code := errorCodeOf(t, readUntilType(t, socketCtx, ownerSocket, protocol.TypeSystemError)); code != "invalid_spectator_settings" {
		t.Fatalf("fee above 100 big blinds must be refused, got %s", code)
	}
	writeTestEnvelope(t, socketCtx, ownerSocket, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableSpectatorSettingsSet), RequestID: "settings-ok",
		Payload: json.RawMessage(`{"feeBigBlinds":0,"voiceAllowed":false,"chatAllowed":true,"emoteAllowed":true}`),
	})
	readUntilType(t, socketCtx, ownerSocket, protocol.TypeTableSpectatorSettingsSet)
	snapshot, err := fixture.tables.Snapshot(ctx, fixture.owner.User.UserID, fixture.created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SpectatorSettings.FeeBigBlinds != 0 || snapshot.SpectatorSettings.VoiceAllowed {
		t.Fatalf("settings should be applied and broadcast: %#v", snapshot.SpectatorSettings)
	}
}

// 观战者也能补码，且不必等手间：看牌费不够时正需要马上补上。
func TestSpectatorCanRebuy(t *testing.T) {
	ctx := context.Background()
	fixture := newOwnerFixture(t)
	for _, user := range []string{fixture.owner.User.UserID, fixture.guest.User.UserID} {
		if _, err := fixture.tables.Join(ctx, user, fixture.created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.tables.EnterSpectate(ctx, fixture.guest.User.UserID, fixture.created.RoomID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.tables.Rebuy(ctx, fixture.guest.User.UserID, fixture.created.RoomID, "spectator-rebuy", 500)
	if err != nil {
		t.Fatalf("spectator rebuy failed: %v", err)
	}
	var stack int64
	for _, spectator := range snapshot.Spectators {
		if spectator.UserID == fixture.guest.User.UserID {
			stack = spectator.Stack
		}
	}
	if stack != 2_500 {
		t.Fatalf("spectator stack should include the rebuy, got %d", stack)
	}
	chips, _ := fixture.chips.Snapshot(ctx, fixture.guest.User.UserID)
	if chips.WalletChips+chips.TableChips != 10_000 {
		t.Fatalf("rebuy must move chips, not create them: %#v", chips)
	}
}

// 房主踢人对观战者同样有效，筹码照常退回钱包。
func TestOwnerCanRemoveSpectator(t *testing.T) {
	ctx := context.Background()
	fixture := newOwnerFixture(t)
	for _, user := range []string{fixture.owner.User.UserID, fixture.guest.User.UserID} {
		if _, err := fixture.tables.Join(ctx, user, fixture.created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.tables.EnterSpectate(ctx, fixture.guest.User.UserID, fixture.created.RoomID); err != nil {
		t.Fatal(err)
	}
	response := fixture.post(t, "/v1/rooms/members/"+fixture.guest.User.UserID+"/remove",
		fixture.owner.AccessToken, map[string]any{})
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("remove spectator: status %d body %s", response.StatusCode, readBody(response))
	}
	chips, _ := fixture.chips.Snapshot(ctx, fixture.guest.User.UserID)
	if chips.TableChips != 0 || chips.WalletChips != 10_000 {
		t.Fatalf("spectator chips must return to the wallet exactly once: %#v", chips)
	}
}

func errorCodeOf(t *testing.T, envelope protocol.Envelope) string {
	t.Helper()
	var payload protocol.ErrorPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("decode error payload: %v (%s)", err, envelope.Payload)
	}
	return payload.Code
}

// 给夹具再加一名带筹码的成员并连上牌桌，用于需要两名上桌玩家加一名观战者的场景。
func addThirdMember(t *testing.T, fixture ownerFixture) account.AuthResult {
	t.Helper()
	ctx := context.Background()
	third, err := fixture.accounts.Register(ctx, "room_third", "第三人", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.chips.TopUp(ctx, third.User.UserID, "topup-third", 10_000); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.rooms.JoinWithBuyIn(ctx, participant(third), room.JoinOptions{
		Code: fixture.created.Code, BuyIn: 2_000, RequestID: "join-third",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.tables.Join(ctx, third.User.UserID, fixture.created.RoomID); err != nil {
		t.Fatal(err)
	}
	return third
}

// 有真实账本时：看牌费从观战者的牌桌余额转到玩家的牌桌余额，钱包分文不动，
// 本房间战绩里观战者的净胜负正好是负的看牌费。
func TestSpectatorFeeMovesTableBalancesAndShowsInRoomResult(t *testing.T) {
	ctx := context.Background()
	fixture := newOwnerFixture(t)
	third := addThirdMember(t, fixture)
	for _, user := range []string{fixture.owner.User.UserID, fixture.guest.User.UserID} {
		if _, err := fixture.tables.Join(ctx, user, fixture.created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.tables.EnterSpectate(ctx, fixture.guest.User.UserID, fixture.created.RoomID); err != nil {
		t.Fatal(err)
	}
	fee := int64(room.DefaultSpectatorFeeBigBlinds) * fixture.created.Rules.BigBlind
	for _, user := range []string{fixture.owner.User.UserID, third.User.UserID} {
		if _, err := fixture.tables.SetReady(ctx, user, true); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := fixture.tables.Snapshot(ctx, fixture.guest.User.UserID, fixture.created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Phase != holdem.PhasePreflop {
		t.Fatalf("hand should have started, phase=%s", snapshot.Phase)
	}
	guestChips, _ := fixture.chips.Snapshot(ctx, fixture.guest.User.UserID)
	if guestChips.TableChips != 2_000-fee || guestChips.WalletChips != 8_000 {
		t.Fatalf("fee must leave the table balance, never the wallet: %#v", guestChips)
	}
	var playersTable int64
	for _, user := range []string{fixture.owner.User.UserID, third.User.UserID} {
		chips, _ := fixture.chips.Snapshot(ctx, user)
		playersTable += chips.TableChips
	}
	if playersTable != 4_000+fee {
		t.Fatalf("players' table balances must absorb exactly the fee: %d", playersTable)
	}
	result, err := fixture.chips.RoomResult(ctx, fixture.guest.User.UserID, fixture.created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Net != -fee {
		t.Fatalf("spectator's room result must show the fee as a loss: %#v", result)
	}
}

// 观战者在牌局进行中也能补码——看牌费不够时正需要马上补上。
func TestSpectatorRebuysWhileHandIsRunning(t *testing.T) {
	ctx := context.Background()
	fixture := newOwnerFixture(t)
	third := addThirdMember(t, fixture)
	for _, user := range []string{fixture.owner.User.UserID, fixture.guest.User.UserID} {
		if _, err := fixture.tables.Join(ctx, user, fixture.created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.tables.EnterSpectate(ctx, fixture.guest.User.UserID, fixture.created.RoomID); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{fixture.owner.User.UserID, third.User.UserID} {
		if _, err := fixture.tables.SetReady(ctx, user, true); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := fixture.tables.Rebuy(ctx, fixture.guest.User.UserID, fixture.created.RoomID, "mid-hand-rebuy", 1_000)
	if err != nil {
		t.Fatalf("spectator rebuy during a hand must be allowed: %v", err)
	}
	if snapshot.Phase != holdem.PhasePreflop {
		t.Fatalf("the running hand must be untouched, phase=%s", snapshot.Phase)
	}
	// 上桌玩家在牌局中补码仍然被拒，规则没有放松
	if _, err := fixture.tables.Rebuy(ctx, fixture.owner.User.UserID, fixture.created.RoomID, "player-rebuy", 500); err == nil {
		t.Fatal("seated players still may not rebuy mid-hand")
	}
}

// 权限默认放开时观战者可以正常聊天；赞赏/嘲讽的目标必须是上桌玩家。
func TestSpectatorChatAllowedByDefaultAndCannotBeEmoteTarget(t *testing.T) {
	ctx := context.Background()
	fixture := newOwnerFixture(t)
	for _, user := range []string{fixture.owner.User.UserID, fixture.guest.User.UserID} {
		if _, err := fixture.tables.Join(ctx, user, fixture.created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.tables.EnterSpectate(ctx, fixture.guest.User.UserID, fixture.created.RoomID); err != nil {
		t.Fatal(err)
	}
	socketCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	guestSocket := dialTestSocket(t, socketCtx, fixture.server.URL)
	defer guestSocket.CloseNow()
	authenticateTestSocket(t, socketCtx, guestSocket, fixture.guest.AccessToken, "guest-device")
	joinTestTable(t, socketCtx, guestSocket, fixture.created.RoomID)
	writeTestEnvelope(t, socketCtx, guestSocket, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableChatSend), RequestID: "chat-ok",
		Payload: json.RawMessage(`{"clientMessageId":"m1","kind":"text","content":"围观中"}`),
	})
	readUntilType(t, socketCtx, guestSocket, protocol.TypeTableChatAccepted)

	ownerSocket := dialTestSocket(t, socketCtx, fixture.server.URL)
	defer ownerSocket.CloseNow()
	authenticateTestSocket(t, socketCtx, ownerSocket, fixture.owner.AccessToken, "owner-device")
	joinTestTable(t, socketCtx, ownerSocket, fixture.created.RoomID)
	writeTestEnvelope(t, socketCtx, ownerSocket, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTablePlayerInteract), RequestID: "emote-spectator",
		Payload: json.RawMessage(`{"kind":"taunt","targetUserId":"` + fixture.guest.User.UserID + `"}`),
	})
	if code := errorCodeOf(t, readUntilType(t, socketCtx, ownerSocket, protocol.TypeSystemError)); code != "player_not_at_table" {
		t.Fatalf("a spectator is not a valid emote target, got %s", code)
	}
}
