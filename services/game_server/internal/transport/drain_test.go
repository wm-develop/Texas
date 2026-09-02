package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"texas/services/game_server/internal/game/tablemanager"
	"texas/services/game_server/internal/metrics"
	"texas/services/game_server/internal/protocol"
)

func authenticatedPayload(t *testing.T, ctx context.Context, serverURL, token string) map[string]any {
	t.Helper()
	connection := dialTestSocket(t, ctx, serverURL)
	defer connection.CloseNow()
	payload, _ := json.Marshal(protocol.SessionAuthenticatePayload{AccessToken: token, DeviceID: "device"})
	writeTestEnvelope(t, ctx, connection, protocol.Envelope{
		Version: 1, Type: string(protocol.TypeSessionAuthenticate), RequestID: "authenticate", Payload: payload,
	})
	envelope := readUntilType(t, ctx, connection, protocol.TypeSessionAuthenticated)
	var decoded map[string]any
	if err := json.Unmarshal(envelope.Payload, &decoded); err != nil {
		t.Fatalf("decode authenticated payload: %v", err)
	}
	return decoded
}

func TestAuthenticatedCarriesStableServerInstanceID(t *testing.T) {
	accounts, rooms := testApplicationServices(t)
	server := httptest.NewServer(NewHandler(testLogger(), Options{Accounts: accounts, Rooms: rooms}))
	defer server.Close()
	user := registerHTTPUser(t, server.URL, "instance_user", "看实例的人")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	first, _ := authenticatedPayload(t, ctx, server.URL, user.AccessToken)["serverInstanceId"].(string)
	second, _ := authenticatedPayload(t, ctx, server.URL, user.AccessToken)["serverInstanceId"].(string)
	if first == "" || first != second {
		t.Fatalf("instance id must be non-empty and stable within one process: %q vs %q", first, second)
	}

	// 另一个 handler 相当于另一次启动，标识必须不同，客户端才能识别重启
	other := httptest.NewServer(NewHandler(testLogger(), Options{Accounts: accounts, Rooms: rooms}))
	defer other.Close()
	if third, _ := authenticatedPayload(t, ctx, other.URL, user.AccessToken)["serverInstanceId"].(string); third == first {
		t.Fatal("a new process must present a different instance id")
	}
}

func TestDrainingMetricFollowsTableManager(t *testing.T) {
	accounts, rooms := testApplicationServices(t)
	tables, err := tablemanager.New(rooms, transportZeroRandom{})
	if err != nil {
		t.Fatal(err)
	}
	registry := metrics.NewRegistry()
	server := httptest.NewServer(NewHandler(testLogger(), Options{
		Accounts: accounts, Rooms: rooms, Tables: tables,
		Metrics: registry, MetricsToken: "metrics-token-1234567890",
	}))
	defer server.Close()

	scrape := func() string {
		response := doJSONRequest(t, http.MethodGet, server.URL+"/metrics", "metrics-token-1234567890", nil)
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("metrics status=%d", response.StatusCode)
		}
		return readBody(response)
	}
	if body := scrape(); !strings.Contains(body, "texas_draining 0") {
		t.Fatalf("expected texas_draining 0 before drain:\n%s", body)
	}
	tables.BeginDrain()
	if body := scrape(); !strings.Contains(body, "texas_draining 1") {
		t.Fatalf("expected texas_draining 1 after BeginDrain:\n%s", body)
	}
}
