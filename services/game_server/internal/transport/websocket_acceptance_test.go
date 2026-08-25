package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/game/holdem"
	"texas/services/game_server/internal/game/tablemanager"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/ledger"
	"texas/services/game_server/internal/protocol"
	"texas/services/game_server/internal/room"
)

type acceptanceClient struct {
	auth       account.AuthResult
	connection *websocket.Conn
}

func TestTenWebSocketClientsCompleteOneHundredHands(t *testing.T) {
	accounts, rooms := testApplicationServices(t)
	clients := make([]acceptanceClient, 10)
	for index := range clients {
		result, err := accounts.Register(
			context.Background(),
			fmt.Sprintf("acceptance_%02d", index+1),
			fmt.Sprintf("玩家%02d", index+1),
			"password-123",
		)
		if err != nil {
			t.Fatalf("register %d: %v", index, err)
		}
		clients[index].auth = result
	}
	created, err := rooms.Create(context.Background(), participant(clients[0].auth), room.PresetStandard, 10, "")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	for index := 1; index < len(clients); index++ {
		if _, err := rooms.Join(context.Background(), participant(clients[index].auth), created.Code, ""); err != nil {
			t.Fatalf("join %d: %v", index, err)
		}
	}
	ledgerStore := ledger.NewInMemoryStore()
	historyStore := history.NewInMemoryStore()
	tables, err := tablemanager.NewWithConfig(rooms, transportZeroRandom{}, tablemanager.ManagerConfig{
		Ledger: ledgerStore, History: historyStore,
	})
	if err != nil {
		t.Fatalf("table manager: %v", err)
	}
	server := httptest.NewServer(NewHandler(testLogger(), Options{
		Accounts: accounts, Rooms: rooms, Tables: tables, History: historyStore,
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	byUserID := make(map[string]*websocket.Conn, len(clients))
	for index := range clients {
		connection := dialTestSocket(t, ctx, server.URL)
		clients[index].connection = connection
		defer connection.CloseNow()
		authenticateTestSocket(t, ctx, connection, clients[index].auth.AccessToken, fmt.Sprintf("device-%d", index))
		joinTestTable(t, ctx, connection, created.RoomID)
		byUserID[clients[index].auth.User.UserID] = connection
	}

	previousHandID := ""
	seenHands := make(map[string]struct{}, 100)
	for handIndex := 0; handIndex < 100; handIndex++ {
		for index, client := range clients {
			writeTestEnvelope(t, ctx, client.connection, protocol.Envelope{
				Version: 1, Type: string(protocol.TypeTableReadySet),
				RequestID: fmt.Sprintf("ready-%d-%d", handIndex, index),
				Payload:   json.RawMessage(`{"ready":true}`),
			})
			readUntilType(t, ctx, client.connection, protocol.TypeTableReadySet)
		}

		var snapshot tablemanager.Snapshot
		for _, client := range clients {
			candidate := readSnapshotForNewHand(t, ctx, client.connection, previousHandID)
			if snapshot.HandID == "" {
				snapshot = candidate
			}
		}
		if snapshot.CurrentAction == nil || snapshot.Phase != holdem.PhasePreflop {
			t.Fatalf("hand %d start snapshot=%#v", handIndex, snapshot)
		}

		for actionIndex := 0; snapshot.CurrentAction != nil; actionIndex++ {
			if actionIndex > 20 {
				t.Fatalf("hand %d exceeded action limit", handIndex)
			}
			actorConnection := byUserID[snapshot.CurrentAction.UserID]
			if actorConnection == nil {
				t.Fatalf("missing actor connection %s", snapshot.CurrentAction.UserID)
			}
			action := holdem.ActionFold
			if snapshot.CurrentAction.Options.CanCheck {
				action = holdem.ActionCheck
			}
			previousRevision := snapshot.TableRevision
			payload, _ := json.Marshal(protocol.ActionSubmitPayload{
				ActionID: fmt.Sprintf("action-%d-%d", handIndex, actionIndex), Action: string(action),
			})
			writeTestEnvelope(t, ctx, actorConnection, protocol.Envelope{
				Version: 1, Type: string(protocol.TypeTableActionSubmit),
				RequestID: fmt.Sprintf("request-%d-%d", handIndex, actionIndex),
				TableID:   created.RoomID, HandID: snapshot.HandID,
				TableRevision: snapshot.TableRevision, Payload: payload,
			})
			readUntilType(t, ctx, actorConnection, protocol.TypeTableActionAccepted)
			for index, client := range clients {
				candidate := readSnapshotAfterRevision(t, ctx, client.connection, previousRevision)
				if index == 0 {
					snapshot = candidate
				}
			}
		}

		if snapshot.Phase != holdem.PhaseWaitingNextHand || snapshot.Settlement == nil {
			t.Fatalf("hand %d settlement=%#v", handIndex, snapshot)
		}
		previousHandID = snapshot.HandID
		seenHands[snapshot.HandID] = struct{}{}
		entries := ledgerStore.EntriesForHand(snapshot.HandID)
		if len(entries) != 10 {
			t.Fatalf("hand %d ledger entries=%d", handIndex, len(entries))
		}
		var delta int64
		for _, entry := range entries {
			delta += entry.Delta
		}
		if delta != 0 {
			t.Fatalf("hand %d ledger delta=%d", handIndex, delta)
		}
	}

	if len(seenHands) != 100 {
		t.Fatalf("unique hands=%d", len(seenHands))
	}
	for _, client := range clients {
		if recent := historyStore.RecentForPlayer(client.auth.User.UserID, 100); len(recent) != 100 {
			t.Fatalf("%s recent hands=%d", client.auth.User.UserID, len(recent))
		}
	}
}

func readSnapshotForNewHand(
	t *testing.T,
	ctx context.Context,
	connection *websocket.Conn,
	previousHandID string,
) tablemanager.Snapshot {
	t.Helper()
	for {
		message := readUntilType(t, ctx, connection, protocol.TypeTableSnapshot)
		var snapshot tablemanager.Snapshot
		if err := json.Unmarshal(message.Payload, &snapshot); err != nil {
			t.Fatalf("decode snapshot: %v", err)
		}
		if snapshot.Phase == holdem.PhasePreflop && snapshot.HandID != previousHandID {
			return snapshot
		}
	}
}

func readSnapshotAfterRevision(
	t *testing.T,
	ctx context.Context,
	connection *websocket.Conn,
	previousRevision uint64,
) tablemanager.Snapshot {
	t.Helper()
	for {
		message := readUntilType(t, ctx, connection, protocol.TypeTableSnapshot)
		var snapshot tablemanager.Snapshot
		if err := json.Unmarshal(message.Payload, &snapshot); err != nil {
			t.Fatalf("decode snapshot: %v", err)
		}
		if snapshot.TableRevision > previousRevision {
			return snapshot
		}
	}
}
