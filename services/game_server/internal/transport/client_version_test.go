package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func versionTestServer(t *testing.T, minimum, recommended int) *httptest.Server {
	t.Helper()
	accounts, rooms := testApplicationServices(t)
	server := httptest.NewServer(NewHandler(testLogger(), Options{
		Accounts: accounts, Rooms: rooms,
		MinimumClientVersion: minimum, RecommendedClientVersion: recommended,
	}))
	t.Cleanup(server.Close)
	return server
}

func getWithVersion(t *testing.T, url, header, query string) *http.Response {
	t.Helper()
	if query != "" {
		url += "?clientVersion=" + query
	}
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if header != "" {
		request.Header.Set(clientVersionHeader, header)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

// 版本要求必须在登录之前就能查到，否则过旧的客户端连「该更新到哪个版本」
// 都问不到。
func TestClientVersionEndpointNeedsNoAuthentication(t *testing.T) {
	server := versionTestServer(t, 2001, 2002)
	response := getWithVersion(t, server.URL+"/v1/client/version", "", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("version endpoint must be public, got %d", response.StatusCode)
	}
	var body struct {
		Minimum     int `json:"minimum"`
		Recommended int `json:"recommended"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Minimum != 2001 || body.Recommended != 2002 {
		t.Fatalf("unexpected requirements: %#v", body)
	}
}

// 过旧的客户端在任何接口上都要被挡住。只挡登录的话，已经登录的旧客户端
// 还能继续连牌桌。
func TestTooOldClientsAreRefusedEverywhere(t *testing.T) {
	server := versionTestServer(t, 2001, 0)
	for _, path := range []string{"/v1/auth/login", "/v1/rooms/current", "/ws"} {
		response := getWithVersion(t, server.URL+path, "2000", "")
		if response.StatusCode != http.StatusUpgradeRequired {
			t.Fatalf("%s: expected 426 for an old client, got %d", path, response.StatusCode)
		}
		var body struct {
			Error   string `json:"error"`
			Minimum int    `json:"minimum"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Error != "client_too_old" || body.Minimum != 2001 {
			t.Fatalf("%s: unexpected body %#v", path, body)
		}
	}
}

// 不上报版本的客户端正是需要被拦下的旧版本。
func TestMissingClientVersionCountsAsTooOld(t *testing.T) {
	server := versionTestServer(t, 2001, 0)
	for _, value := range []string{"", "abc", "-1"} {
		response := getWithVersion(t, server.URL+"/v1/rooms/current", value, "")
		if response.StatusCode != http.StatusUpgradeRequired {
			t.Fatalf("version %q should be refused, got %d", value, response.StatusCode)
		}
	}
}

// 浏览器的 WebSocket API 不允许设置自定义请求头，因此 WS 走查询参数。
//
// /ws 对非 WebSocket 请求自身也会回 426，所以这里按响应体区分是谁拒绝的。
func TestWebSocketVersionMayComeFromQuery(t *testing.T) {
	server := versionTestServer(t, 2001, 0)
	refused := readBody(getWithVersion(t, server.URL+"/ws", "", "2000"))
	if !strings.Contains(refused, "client_too_old") {
		t.Fatalf("old client on the query parameter should be refused, got %s", refused)
	}
	passed := readBody(getWithVersion(t, server.URL+"/ws", "", "2001"))
	if strings.Contains(passed, "client_too_old") {
		t.Fatalf("a current client must pass the version gate, got %s", passed)
	}
}

// 运维接口与版本查询本身不能被门禁挡住。
func TestOperationalEndpointsBypassTheVersionGate(t *testing.T) {
	server := versionTestServer(t, 2001, 0)
	for _, path := range []string{"/healthz", "/readyz", "/v1/client/version"} {
		if response := getWithVersion(t, server.URL+path, "", ""); response.StatusCode == http.StatusUpgradeRequired {
			t.Fatalf("%s must stay reachable without a client version", path)
		}
	}
}

// 没有配置最低版本的部署行为完全不变。
func TestVersionGateIsOffByDefault(t *testing.T) {
	server := versionTestServer(t, 0, 0)
	response := getWithVersion(t, server.URL+"/v1/rooms/current", "", "")
	if response.StatusCode == http.StatusUpgradeRequired {
		t.Fatal("the gate must stay disabled when no minimum is configured")
	}
}
