package tablestate

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStoreRoundTripAndOverwrite(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	if _, found, err := store.Load(ctx, "room_1"); err != nil || found {
		t.Fatalf("an unknown room has no state: found=%v err=%v", found, err)
	}
	first := Record{RoomID: "room_1", HandID: "hand_1", Revision: 3, State: []byte(`{"a":1}`)}
	if err := store.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := store.Load(ctx, "room_1")
	if err != nil || !found {
		t.Fatalf("saved state must load back: found=%v err=%v", found, err)
	}
	if loaded.HandID != "hand_1" || loaded.Revision != 3 || string(loaded.State) != `{"a":1}` {
		t.Fatalf("state changed in storage: %#v", loaded)
	}

	// 一手牌里每个动作都会保存一次，只保留最新的
	second := Record{RoomID: "room_1", HandID: "hand_1", Revision: 4, State: []byte(`{"a":2}`)}
	if err := store.Save(ctx, second); err != nil {
		t.Fatal(err)
	}
	loaded, _, err = store.Load(ctx, "room_1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 4 || string(loaded.State) != `{"a":2}` {
		t.Fatalf("save must overwrite the previous state: %#v", loaded)
	}

	// 返回的字节切片必须是副本：调用方改它不能影响存储
	loaded.State[2] = 'X'
	again, _, _ := store.Load(ctx, "room_1")
	if string(again.State) != `{"a":2}` {
		t.Fatalf("callers must not be able to mutate stored state: %s", again.State)
	}

	if err := store.Delete(ctx, "room_1"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := store.Load(ctx, "room_1"); found {
		t.Fatal("deleted state must be gone")
	}
}

func TestMemoryStoreRejectsIncompleteRecords(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	for name, record := range map[string]Record{
		"没有房间号": {HandID: "hand_1", State: []byte("{}")},
		"没有手号":  {RoomID: "room_1", State: []byte("{}")},
		"没有状态":  {RoomID: "room_1", HandID: "hand_1"},
	} {
		if err := store.Save(ctx, record); err == nil {
			t.Fatalf("%s：必须拒绝", name)
		}
	}
}

// 没人回来的牌桌会留下孤儿记录：房间还在，所以外键级联删不掉它。
func TestDeleteOlderThanClearsOrphans(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Unix(100_000, 0).UTC()
	if err := store.Save(ctx, Record{
		RoomID: "stale", HandID: "hand_old", State: []byte("{}"),
		UpdatedAt: now.Add(-48 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, Record{
		RoomID: "fresh", HandID: "hand_new", State: []byte("{}"), UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	removed, err := store.DeleteOlderThan(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("only the stale record should go, removed=%d", removed)
	}
	if _, found, _ := store.Load(ctx, "stale"); found {
		t.Fatal("the stale record must be gone")
	}
	if _, found, _ := store.Load(ctx, "fresh"); !found {
		t.Fatal("a recent record must survive")
	}
}
