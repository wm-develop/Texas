package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"texas/services/game_server/internal/config"
	"texas/services/game_server/internal/game/tablemanager"
	"texas/services/game_server/internal/metrics"
)

func postJSON(t *testing.T, server *httptest.Server, path string, body any, headers map[string]string) *http.Response {
	t.Helper()
	payload, _ := json.Marshal(body)
	request, _ := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(string(payload)))
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func newGuardedServer(t *testing.T, limits config.RateLimits, registry *metrics.Registry, token string) *httptest.Server {
	t.Helper()
	accounts, rooms := testApplicationServices(t)
	if _, err := accounts.Register(context.Background(), "victim", "受害者", "correct-password-1"); err != nil {
		t.Fatal(err)
	}
	tables, err := tablemanager.New(rooms, transportZeroRandom{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(testLogger(), Options{
		Accounts: accounts, Rooms: rooms, Tables: tables,
		RateLimits: limits, Metrics: registry, MetricsToken: token,
	}))
	t.Cleanup(server.Close)
	return server
}

func TestLoginIsRateLimitedPerIPWithRetryAfter(t *testing.T) {
	registry := metrics.NewRegistry()
	server := newGuardedServer(t, config.RateLimits{
		AuthPerIP: config.RateLimit{Burst: 2, Window: time.Minute},
	}, registry, "")

	body := map[string]string{"username": "victim", "password": "correct-password-1"}
	for index := 0; index < 2; index++ {
		if response := postJSON(t, server, "/v1/auth/login", body, nil); response.StatusCode != http.StatusOK {
			t.Fatalf("attempt %d: status %d", index+1, response.StatusCode)
		}
	}
	response := postJSON(t, server, "/v1/auth/login", body, nil)
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("third attempt should be limited, got %d", response.StatusCode)
	}
	if response.Header.Get("Retry-After") == "" {
		t.Fatal("429 must carry Retry-After")
	}
	var payload map[string]string
	_ = json.NewDecoder(response.Body).Decode(&payload)
	if payload["error"] != "rate_limited" {
		t.Fatalf("error code %q", payload["error"])
	}
	var buffer strings.Builder
	registry.Write(&buffer)
	if !strings.Contains(buffer.String(), `texas_rate_limited_total{scope="auth_ip"} 1`) {
		t.Fatalf("rate limit metric missing:\n%s", buffer.String())
	}
}

func TestLoginFailuresLockUsernameButNotOtherUsers(t *testing.T) {
	server := newGuardedServer(t, config.RateLimits{
		LoginFailuresPerUser: config.RateLimit{Burst: 2, Window: time.Hour},
	}, nil, "")

	wrong := map[string]string{"username": "Victim ", "password": "nope"}
	for index := 0; index < 2; index++ {
		if response := postJSON(t, server, "/v1/auth/login", wrong, nil); response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("failure %d: status %d", index+1, response.StatusCode)
		}
	}
	// 用户名归一化后与前两次相同，第三次即使密码正确也已被锁定
	locked := postJSON(t, server, "/v1/auth/login", map[string]string{
		"username": "victim", "password": "correct-password-1",
	}, nil)
	if locked.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("locked user should get 429, got %d", locked.StatusCode)
	}
	// 其他用户名不受影响（不存在的用户返回 401 而非 429）
	other := postJSON(t, server, "/v1/auth/login", map[string]string{
		"username": "someone-else", "password": "x",
	}, nil)
	if other.StatusCode == http.StatusTooManyRequests {
		t.Fatal("per-user lockout must not affect other usernames")
	}
}

func TestSuccessfulLoginResetsFailureCounter(t *testing.T) {
	server := newGuardedServer(t, config.RateLimits{
		LoginFailuresPerUser: config.RateLimit{Burst: 2, Window: time.Hour},
	}, nil, "")
	wrong := map[string]string{"username": "victim", "password": "nope"}
	right := map[string]string{"username": "victim", "password": "correct-password-1"}
	postJSON(t, server, "/v1/auth/login", wrong, nil)
	if response := postJSON(t, server, "/v1/auth/login", right, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("correct password should still work with one failure, got %d", response.StatusCode)
	}
	// 成功登录清零后，再次允许两次失败
	postJSON(t, server, "/v1/auth/login", wrong, nil)
	if response := postJSON(t, server, "/v1/auth/login", wrong, nil); response.StatusCode == http.StatusTooManyRequests {
		t.Fatal("counter should have been reset by the successful login")
	}
}

func TestTrustedProxyForwardedIPIsUsedForLimiting(t *testing.T) {
	accounts, rooms := testApplicationServices(t)
	tables, _ := tablemanager.New(rooms, transportZeroRandom{})
	// httptest 客户端来自 127.0.0.1，把它设为受信任代理后 X-Forwarded-For 生效
	server := httptest.NewServer(NewHandler(testLogger(), Options{
		Accounts: accounts, Rooms: rooms, Tables: tables,
		TrustedProxies: []string{"127.0.0.1"},
		RateLimits:     config.RateLimits{RegisterPerIP: config.RateLimit{Burst: 1, Window: time.Hour}},
	}))
	defer server.Close()

	register := func(forwardedFor, username string) int {
		return postJSON(t, server, "/v1/auth/register", map[string]any{
			"username": username, "displayName": "n", "password": "password-123",
		}, map[string]string{"X-Forwarded-For": forwardedFor}).StatusCode
	}
	if status := register("198.51.100.1", "alpha"); status != http.StatusCreated {
		t.Fatalf("first registration from IP A: %d", status)
	}
	if status := register("198.51.100.1", "beta"); status != http.StatusTooManyRequests {
		t.Fatalf("second registration from IP A should be limited, got %d", status)
	}
	if status := register("198.51.100.2", "gamma"); status != http.StatusCreated {
		t.Fatalf("a different forwarded IP must have its own quota, got %d", status)
	}
}

func TestMetricsEndpointRequiresTokenAndRendersRequests(t *testing.T) {
	registry := metrics.NewRegistry()
	server := newGuardedServer(t, config.RateLimits{}, registry, "metrics-token-0123456789")

	if response, _ := http.Get(server.URL + "/healthz"); response.StatusCode != http.StatusOK {
		t.Fatal("healthz should work")
	}
	unauthorized, _ := http.Get(server.URL + "/metrics")
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("metrics without token should be 401, got %d", unauthorized.StatusCode)
	}
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/metrics", nil)
	request.Header.Set("Authorization", "Bearer metrics-token-0123456789")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body strings.Builder
	buffer := make([]byte, 4096)
	for {
		n, readErr := response.Body.Read(buffer)
		body.Write(buffer[:n])
		if readErr != nil {
			break
		}
	}
	for _, expected := range []string{
		"texas_http_requests_total{route=\"GET /healthz\",status=\"200\"} 1",
		"texas_websocket_connections_active 0",
		"texas_tables_active 0",
	} {
		if !strings.Contains(body.String(), expected) {
			t.Errorf("missing %q in metrics output:\n%s", expected, body.String())
		}
	}
}

func TestMetricsEndpointAbsentWithoutToken(t *testing.T) {
	server := newGuardedServer(t, config.RateLimits{}, metrics.NewRegistry(), "")
	response, _ := http.Get(server.URL + "/metrics")
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("metrics must not exist without a token, got %d", response.StatusCode)
	}
}

func TestWebSocketConnectionsPerIPAreCapped(t *testing.T) {
	registry := metrics.NewRegistry()
	server := newGuardedServer(t, config.RateLimits{WebSocketPerIP: 1}, registry, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"

	first, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("first connection: %v", err)
	}
	defer first.CloseNow()

	_, response, err := websocket.Dial(ctx, url, nil)
	if err == nil {
		t.Fatal("second connection from the same IP should be rejected")
	}
	if response == nil || response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected HTTP 429 before upgrade, got %#v", response)
	}

	// 释放后再次允许
	first.CloseNow()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		third, _, err := websocket.Dial(ctx, url, nil)
		if err == nil {
			third.CloseNow()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("slot was not released after the first connection closed")
}
