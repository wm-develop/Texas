package transport

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"texas/services/game_server/internal/game/tablemanager"
	"texas/services/game_server/internal/protocol"
	"texas/services/game_server/internal/room"
)

// 手机网络切换后新连接先加入，旧的死连接要等 TCP 超时才被服务端察觉。
// 此前旧连接的关闭会把玩家标为断线并取消准备，玩家明明在线却显示已断线，
// 自动准备也随之失效。现在只有当前生效的连接断开才代表玩家离线。
func TestStaleConnectionCloseDoesNotMarkPlayerDisconnected(t *testing.T) {
	accounts, rooms := testApplicationServices(t)
	owner, err := accounts.Register(context.Background(), "owner", "房主", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	guest, err := accounts.Register(context.Background(), "guest", "好友", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	created, err := rooms.Create(context.Background(), participant(owner), room.PresetStandard, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rooms.Join(context.Background(), participant(guest), created.Code, ""); err != nil {
		t.Fatal(err)
	}
	tables, err := tablemanager.New(rooms, transportZeroRandom{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(testLogger(), Options{Accounts: accounts, Rooms: rooms, Tables: tables}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	guestConnection := dialTestSocket(t, ctx, server.URL)
	defer guestConnection.CloseNow()
	authenticateTestSocket(t, ctx, guestConnection, guest.AccessToken, "guest")
	joinTestTable(t, ctx, guestConnection, created.RoomID)

	// 房主先用旧连接加入
	stale := dialTestSocket(t, ctx, server.URL)
	authenticateTestSocket(t, ctx, stale, owner.AccessToken, "phone-old")
	joinTestTable(t, ctx, stale, created.RoomID)

	// 网络切换：新连接加入，旧连接此时在服务端看来还活着
	fresh := dialTestSocket(t, ctx, server.URL)
	defer fresh.CloseNow()
	authenticateTestSocket(t, ctx, fresh, owner.AccessToken, "phone-new")
	joinTestTable(t, ctx, fresh, created.RoomID)

	// 服务端应主动用自定义关闭码关掉旧连接，让它知道自己被取代。
	// 旧连接上可能还排着关闭前的广播，先读空再看关闭码。
	var readErr error
	for readErr == nil {
		_, _, readErr = stale.Read(ctx)
	}
	if status := websocket.CloseStatus(readErr); status != closeStatusSuperseded {
		t.Fatalf("stale connection should be closed with %d, got status=%d err=%v", closeStatusSuperseded, status, readErr)
	}

	// 旧连接的关闭被服务端处理后，房主必须仍然是「已连接」
	deadline := time.Now().Add(2 * time.Second)
	for {
		snapshot, err := tables.Snapshot(context.Background(), owner.User.UserID, created.RoomID)
		if err != nil {
			t.Fatal(err)
		}
		var connected bool
		for _, seat := range snapshot.Seats {
			if seat.UserID == owner.User.UserID {
				connected = seat.Connected
			}
		}
		if connected && time.Now().After(deadline) {
			break
		}
		if !connected {
			t.Fatalf("owner marked disconnected after a superseded connection closed: %#v", snapshot.Seats)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 新连接仍在正常工作：能收到快照
	writeTestEnvelope(t, ctx, fresh, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableSnapshotRequest), RequestID: "resync",
		Payload: json.RawMessage(`{"lastSequence":0,"reason":"manual"}`),
	})
	readUntilType(t, ctx, fresh, protocol.TypeTableSnapshot)
}

// 动作被 stale_revision 拒绝后客户端会带着已追平的序号请求快照。此前服务端
// 在没有可补发事件时只回一个空的 replay.completed，客户端手里的 tableRevision
// 依旧过期，再点任何按钮都被拒——表现为下注按钮怎么点都没反应。
func TestExplicitSnapshotRequestAlwaysReturnsSnapshot(t *testing.T) {
	accounts, rooms := testApplicationServices(t)
	owner, err := accounts.Register(context.Background(), "owner", "房主", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	created, err := rooms.Create(context.Background(), participant(owner), room.PresetStandard, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	tables, err := tablemanager.New(rooms, transportZeroRandom{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(testLogger(), Options{Accounts: accounts, Rooms: rooms, Tables: tables}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connection := dialTestSocket(t, ctx, server.URL)
	defer connection.CloseNow()
	authenticateTestSocket(t, ctx, connection, owner.AccessToken, "device")
	joinTestTable(t, ctx, connection, created.RoomID)
	first := readUntilType(t, ctx, connection, protocol.TypeTableSnapshot)

	// 序号已经追平，仍然显式请求快照
	writeTestEnvelope(t, ctx, connection, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeTableSnapshotRequest), RequestID: "after-reject",
		Payload: json.RawMessage(`{"lastSequence":` + jsonNumber(first.Sequence) + `,"reason":"action_rejected"}`),
	})
	envelope := readUntilType(t, ctx, connection, protocol.TypeTableSnapshot)
	if envelope.RequestID != "after-reject" {
		t.Fatalf("snapshot must answer the explicit request, got %#v", envelope)
	}
}

func jsonNumber(value uint64) string {
	data, _ := json.Marshal(value)
	return string(data)
}
