package tablestate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/postgres"
	"texas/services/game_server/migrations"
)

// 生产事故回归：行动后的引擎状态带 actionResults，其幂等键曾用 \x00 分隔，
// Go 序列化成 \u0000 后 PostgreSQL jsonb 整行拒收（SQLSTATE 22P05）——一手牌
// 自第一个动作起的状态落盘全部失败，崩溃恢复形同虚设，而日志只亮 Warn。
// 内存仓储与纯 Go 往返都拦不住这种错配，必须用真实引擎产出的行动后状态
// 对着真实库写一次再读回来。
func TestPostgresStoreSavesStateAfterActions(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	database.SetMaxOpenConns(1)
	schema := fmt.Sprintf("tablestate_store_test_%d", time.Now().UnixNano())
	if _, err := database.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
		_ = database.Close()
	})
	if _, err := database.ExecContext(ctx, `SET search_path TO `+schema); err != nil {
		t.Fatalf("set search path: %v", err)
	}
	migrator, err := postgres.NewMigrator(migrations.Files)
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	if _, err := migrator.Up(ctx, database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// table_states.room_id 外键指向 rooms，房间又需要房主。
	if _, err := database.ExecContext(ctx,
		`INSERT INTO users (user_id, username, display_name, password_hash)
		 VALUES ('usr_state_pg', 'usr_state_pg', 'usr_state_pg', 'hash')`); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := database.ExecContext(ctx,
		`INSERT INTO rooms (room_id, room_code, owner_user_id, preset, max_players,
		 small_blind, big_blind, max_buy_in, action_seconds, status)
		 VALUES ('room_state_pg', '333333', 'usr_state_pg', 'standard', 2, 10, 20, 2000, 30, 'open')`); err != nil {
		t.Fatalf("create room: %v", err)
	}

	// 用真实引擎打出一手行动后的状态：只有行动后 actionResults 才非空，
	// 事故的触发条件正是这个字段。
	table, err := holdem.NewTable(holdem.Config{TableID: "room_state_pg", MaxSeats: 2, SmallBlind: 10, BigBlind: 20})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	for _, player := range []struct {
		id   string
		seat int
	}{{"usr_state_pg", 1}, {"usr_state_pg2", 2}} {
		if err := table.AddPlayer(player.id, player.seat, 1_000); err != nil {
			t.Fatalf("AddPlayer: %v", err)
		}
		if err := table.SetReady(player.id, true); err != nil {
			t.Fatalf("SetReady: %v", err)
		}
	}
	if err := table.StartHand(holdem.CryptoRandom{}); err != nil {
		t.Fatalf("StartHand: %v", err)
	}
	var actorID string
	for _, player := range table.Players() {
		if player.Seat == table.CurrentSeat() {
			actorID = player.PlayerID
		}
	}
	if actorID == "" {
		t.Fatal("no current actor after StartHand")
	}
	if _, err := table.SubmitAction(holdem.ActionRequest{
		ActionID: "pg-state-call-1", PlayerID: actorID,
		HandID: table.HandID(), TableRevision: table.Revision(), Action: holdem.ActionCall,
	}); err != nil {
		t.Fatalf("SubmitAction: %v", err)
	}
	encoded, err := json.Marshal(table.State())
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if !bytes.Contains(encoded, []byte(`"actionResults"`)) {
		t.Fatalf("post-action state must carry actionResults, the field that triggered the incident:\n%s", encoded)
	}

	store, err := NewPostgresStore(database)
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	record := Record{
		RoomID: "room_state_pg", HandID: table.HandID(),
		Revision: table.Revision(), State: encoded,
	}
	if err := store.Save(ctx, record); err != nil {
		t.Fatalf("Save must accept a real post-action state (jsonb rejected \\u0000 in the incident): %v", err)
	}
	loaded, found, err := store.Load(ctx, record.RoomID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !found {
		t.Fatal("saved state not found")
	}
	// jsonb 不保键序与空白，按语义比较。
	var want, got any
	if err := json.Unmarshal(record.State, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(loaded.State, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("state changed across the postgres round trip:\nwant=%s\ngot =%s", record.State, loaded.State)
	}
}
