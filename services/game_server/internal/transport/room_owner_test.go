package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/game/tablemanager"
	"texas/services/game_server/internal/room"
)

type ownerFixture struct {
	server   *httptest.Server
	accounts *account.Service
	rooms    *room.Service
	tables   *tablemanager.Manager
	chips    *bankroll.Service
	created  room.Room
	owner    account.AuthResult
	guest    account.AuthResult
}

func newOwnerFixture(t *testing.T) ownerFixture {
	t.Helper()
	ctx := context.Background()
	chips, err := bankroll.NewService(bankroll.NewMemoryRepository(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	accounts, _ := testApplicationServices(t)
	rooms, err := room.NewService(room.NewMemoryRepository(), mustHasher(t), room.ServiceConfig{Bankroll: chips})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := accounts.Register(ctx, "room_owner", "房主", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	guest, err := accounts.Register(ctx, "room_guest", "好友", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []account.AuthResult{owner, guest} {
		if _, err := chips.TopUp(ctx, user.User.UserID, "topup-"+user.User.UserID, 10_000); err != nil {
			t.Fatal(err)
		}
	}
	created, err := rooms.CreateConfigured(ctx, participant(owner), room.CreateOptions{
		Preset: room.PresetStandard, SmallBlind: 10, BigBlind: 20,
		MaxBuyIn: 5_000, BuyIn: 2_000, RequestID: "create-room",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.JoinWithBuyIn(ctx, participant(guest), room.JoinOptions{
		Code: created.Code, BuyIn: 2_000, RequestID: "join-room",
	}); err != nil {
		t.Fatal(err)
	}
	tables, err := tablemanager.NewWithConfig(rooms, transportZeroRandom{}, tablemanager.ManagerConfig{Bankroll: chips})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(testLogger(), Options{
		Accounts: accounts, Rooms: rooms, Tables: tables, Bankroll: chips,
	}))
	t.Cleanup(server.Close)
	return ownerFixture{
		server: server, accounts: accounts, rooms: rooms, tables: tables, chips: chips,
		created: created, owner: owner, guest: guest,
	}
}

func (fixture ownerFixture) post(t *testing.T, path, token string, body any) *http.Response {
	t.Helper()
	return doJSONRequest(t, http.MethodPost, fixture.server.URL+path, token, body)
}

func TestOwnerRemovesMemberAndChipsAreReturned(t *testing.T) {
	ctx := context.Background()
	fixture := newOwnerFixture(t)
	// 两人都已连上牌桌，但尚未全部准备，因此处于手间
	for _, user := range []string{fixture.owner.User.UserID, fixture.guest.User.UserID} {
		if _, err := fixture.tables.Join(ctx, user, fixture.created.RoomID); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := fixture.chips.Snapshot(ctx, fixture.guest.User.UserID)
	if before.TableChips != 2_000 {
		t.Fatalf("precondition: guest table chips=%d", before.TableChips)
	}

	response := fixture.post(t, "/v1/rooms/members/"+fixture.guest.User.UserID+"/remove",
		fixture.owner.AccessToken, map[string]any{})
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("remove: status %d body %s", response.StatusCode, readBody(response))
	}

	// 走的是玩家自己离桌的同一条路径：牌桌筹码必须完整回到钱包，且只回一次
	after, _ := fixture.chips.Snapshot(ctx, fixture.guest.User.UserID)
	if after.TableChips != 0 || after.WalletChips != 10_000 {
		t.Fatalf("kicked player chips not returned exactly once: %#v", after)
	}
	if _, err := fixture.rooms.Current(ctx, fixture.guest.User.UserID); err == nil {
		t.Fatal("kicked player should no longer be in the room")
	}
}

func TestOwnerCannotRemoveDuringHandOrRemoveProtectedUsers(t *testing.T) {
	ctx := context.Background()
	fixture := newOwnerFixture(t)
	for _, user := range []string{fixture.owner.User.UserID, fixture.guest.User.UserID} {
		if _, err := fixture.tables.Join(ctx, user, fixture.created.RoomID); err != nil {
			t.Fatal(err)
		}
	}

	// 自己不能踢自己
	if response := fixture.post(t, "/v1/rooms/members/"+fixture.owner.User.UserID+"/remove",
		fixture.owner.AccessToken, map[string]any{}); response.StatusCode == http.StatusOK {
		t.Fatal("owner must not be able to remove themselves")
	}
	// 非房主不能踢人
	if response := fixture.post(t, "/v1/rooms/members/"+fixture.owner.User.UserID+"/remove",
		fixture.guest.AccessToken, map[string]any{}); response.StatusCode == http.StatusOK {
		t.Fatal("only the owner may remove members")
	}

	// 牌局进行中不能踢人：会牵扯底池归属与行动顺序
	for _, user := range []string{fixture.owner.User.UserID, fixture.guest.User.UserID} {
		if _, err := fixture.tables.SetReady(ctx, user, true); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := fixture.tables.Snapshot(ctx, fixture.owner.User.UserID, fixture.created.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Phase != holdem.PhasePreflop {
		t.Fatalf("precondition: expected a running hand, phase=%s", snapshot.Phase)
	}
	response := fixture.post(t, "/v1/rooms/members/"+fixture.guest.User.UserID+"/remove",
		fixture.owner.AccessToken, map[string]any{})
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		t.Fatal("removing a member mid-hand must be refused")
	}
	// 被拒后筹码分文未动
	after, _ := fixture.chips.Snapshot(ctx, fixture.guest.User.UserID)
	if after.WalletChips+after.TableChips != 10_000 {
		t.Fatalf("refused removal must not move chips: %#v", after)
	}
	if _, err := fixture.rooms.Current(ctx, fixture.guest.User.UserID); err != nil {
		t.Fatal("refused removal must keep the member in the room")
	}
}

func TestJoinLockBlocksNewPlayersOnly(t *testing.T) {
	ctx := context.Background()
	fixture := newOwnerFixture(t)
	response := fixture.post(t, "/v1/rooms/settings/join-lock",
		fixture.owner.AccessToken, map[string]any{"locked": true})
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("join-lock: status %d body %s", response.StatusCode, readBody(response))
	}

	// 新玩家被挡在门外
	newcomer := registerHTTPUser(t, fixture.server.URL, "late_joiner", "迟到的人")
	if _, err := fixture.chips.TopUp(ctx, newcomer.User.UserID, "topup-newcomer", 10_000); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.rooms.JoinWithBuyIn(ctx, participant(newcomer), room.JoinOptions{
		Code: fixture.created.Code, BuyIn: 1_000, RequestID: "blocked-join",
	})
	if err == nil {
		t.Fatal("a locked room must refuse new players")
	}
	// 房内成员完全不受影响
	if _, err := fixture.tables.Join(ctx, fixture.guest.User.UserID, fixture.created.RoomID); err != nil {
		t.Fatalf("existing member must keep playing while the room is locked: %v", err)
	}

	// 重新开放后可以加入
	reopen := fixture.post(t, "/v1/rooms/settings/join-lock",
		fixture.owner.AccessToken, map[string]any{"locked": false})
	defer reopen.Body.Close()
	if reopen.StatusCode != http.StatusOK {
		t.Fatalf("unlock: status %d", reopen.StatusCode)
	}
	if _, err := fixture.rooms.JoinWithBuyIn(ctx, participant(newcomer), room.JoinOptions{
		Code: fixture.created.Code, BuyIn: 1_000, RequestID: "allowed-join",
	}); err != nil {
		t.Fatalf("unlocked room should accept players again: %v", err)
	}
}

func TestOnlyOwnerCanToggleJoinLock(t *testing.T) {
	fixture := newOwnerFixture(t)
	response := fixture.post(t, "/v1/rooms/settings/join-lock",
		fixture.guest.AccessToken, map[string]any{"locked": true})
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		t.Fatal("only the owner may lock the room")
	}
}

func TestRoomResultCountsChipsStillOnTheTable(t *testing.T) {
	ctx := context.Background()
	fixture := newOwnerFixture(t)

	// 刚带入 2000，尚未有输赢：净胜负为 0
	response := doJSONRequest(t, http.MethodGet,
		fixture.server.URL+"/v1/rooms/current/result", fixture.guest.AccessToken, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("result: status %d body %s", response.StatusCode, readBody(response))
	}
	var result bankroll.RoomResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.BoughtIn != 2_000 || result.TableChips != 2_000 || result.Net != 0 {
		t.Fatalf("fresh buy-in should be break-even: %#v", result)
	}

	// 走真实的结算路径把筹码在两人之间转移（总量守恒）：净胜负随之变化。
	// 玩家通常在牌局中途查看，此时盈亏还没有通过离桌返还落回钱包。
	if err := fixture.chips.ApplySettlement(
		ctx, fixture.created.RoomID, "hand_1",
		map[string]int64{
			fixture.guest.User.UserID: 3_200,
			fixture.owner.User.UserID: 800,
		},
		5_000,
	); err != nil {
		t.Fatal(err)
	}
	second := doJSONRequest(t, http.MethodGet,
		fixture.server.URL+"/v1/rooms/current/result", fixture.guest.AccessToken, nil)
	defer second.Body.Close()
	var updated bankroll.RoomResult
	if err := json.NewDecoder(second.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Net != 1_200 {
		t.Fatalf("net should follow the chips on the table, got %#v", updated)
	}
}
