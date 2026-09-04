package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"texas/services/game_server/internal/account"
)

// 门槛存在数据库里：测试通过管理员接口设置，与真实用法一致。
func versionTestServer(t *testing.T, minimum int) (*httptest.Server, account.AuthResult) {
	t.Helper()
	accounts, rooms := testApplicationServices(t)
	admin, err := accounts.RegisterWithOptions(
		context.Background(), "gatekeeper", "管理员", "password-123",
		account.RegistrationOptions{RequestInitialAdmin: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(testLogger(), Options{
		Accounts: accounts, Rooms: rooms,
	}))
	t.Cleanup(server.Close)
	if minimum > 0 {
		setMinimumClientVersion(t, server, admin.AccessToken, minimum)
	}
	return server, admin
}

func setMinimumClientVersion(t *testing.T, server *httptest.Server, token string, minimum int) {
	t.Helper()
	response := doJSONRequest(
		t, http.MethodPost, server.URL+"/v1/admin/settings/client-version",
		token, map[string]any{"minimum": minimum},
	)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("set minimum: status %d body %s", response.StatusCode, readBody(response))
	}
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
	server, _ := versionTestServer(t, 2001)
	response := getWithVersion(t, server.URL+"/v1/client/version", "", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("version endpoint must be public, got %d", response.StatusCode)
	}
	var body struct {
		Minimum int `json:"minimum"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Minimum != 2001 {
		t.Fatalf("unexpected requirements: %#v", body)
	}
}

// 过旧的客户端在任何接口上都要被挡住。只挡登录的话，已经登录的旧客户端
// 还能继续连牌桌。
func TestTooOldClientsAreRefusedEverywhere(t *testing.T) {
	server, _ := versionTestServer(t, 2001)
	for _, path := range []string{"/v1/rooms/current", "/v1/users/me/heartbeat", "/ws"} {
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
	server, _ := versionTestServer(t, 2001)
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
	server, _ := versionTestServer(t, 2001)
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
	server, _ := versionTestServer(t, 2001)
	// 登录与「调整门槛」是逃生通道，见 clientVersionExemptPaths
	for _, path := range []string{
		"/healthz", "/readyz", "/v1/client/version",
		"/v1/auth/login", "/v1/admin/settings/client-version",
	} {
		if response := getWithVersion(t, server.URL+path, "", ""); response.StatusCode == http.StatusUpgradeRequired {
			t.Fatalf("%s must stay reachable without a client version", path)
		}
	}
}

// 没有配置最低版本的部署行为完全不变。
func TestVersionGateIsOffByDefault(t *testing.T) {
	server, _ := versionTestServer(t, 0)
	response := getWithVersion(t, server.URL+"/v1/rooms/current", "", "")
	if response.StatusCode == http.StatusUpgradeRequired {
		t.Fatal("the gate must stay disabled when no minimum is configured")
	}
}

// 门槛存在数据库里而不是环境变量：只更新客户端时也能在管理界面里调整，
// 不必登服务器改 env 并重建容器。改完必须立刻生效。
func TestAdminCanChangeTheGateWithoutRestarting(t *testing.T) {
	server, admin := versionTestServer(t, 0)
	// 一开始不启用
	if response := getWithVersion(t, server.URL+"/v1/rooms/current", "", ""); response.StatusCode == http.StatusUpgradeRequired {
		t.Fatal("the gate must start disabled")
	}

	setMinimumClientVersion(t, server, admin.AccessToken, 2002)

	// 无需重启，同一个进程立刻开始拒绝旧客户端
	response := getWithVersion(t, server.URL+"/v1/rooms/current", "2001", "")
	if response.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("raising the minimum must take effect at once, got %d", response.StatusCode)
	}
	// 公开端点也要跟着变，否则客户端会拿到过期的要求
	var published struct {
		Minimum int `json:"minimum"`
	}
	versionResponse := getWithVersion(t, server.URL+"/v1/client/version", "", "")
	if err := json.NewDecoder(versionResponse.Body).Decode(&published); err != nil {
		t.Fatal(err)
	}
	if published.Minimum != 2002 {
		t.Fatalf("published requirement should follow the change, got %#v", published)
	}

	// 调回 0 同样立刻生效，管理员不会把自己锁在门外
	setMinimumClientVersion(t, server, admin.AccessToken, 0)
	if response := getWithVersion(t, server.URL+"/v1/rooms/current", "", ""); response.StatusCode == http.StatusUpgradeRequired {
		t.Fatal("lowering the minimum must take effect at once too")
	}
}

// 只有管理员能改门槛。
func TestOnlyAdministratorsMayChangeTheGate(t *testing.T) {
	server, _ := versionTestServer(t, 0)
	player := registerHTTPUser(t, server.URL, "just_a_player", "玩家")
	response := doJSONRequest(
		t, http.MethodPost, server.URL+"/v1/admin/settings/client-version",
		player.AccessToken, map[string]any{"minimum": 9999},
	)
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		t.Fatal("a normal player must not be able to lock everyone out")
	}
}

// 门槛设得比管理员自己的客户端还高时，他必须还能改回来；否则只能登服务器
// 直接改数据库，正是这个功能想避免的事。
func TestAdminIsNotLockedOutByTheirOwnGate(t *testing.T) {
	server, admin := versionTestServer(t, 0)
	// 设成一个远高于任何真实客户端的门槛
	setMinimumClientVersion(t, server, admin.AccessToken, 999_000_000)

	// 普通操作确实被挡住了
	if response := getWithVersion(t, server.URL+"/v1/rooms/current", "2001", ""); response.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("the gate should be in force, got %d", response.StatusCode)
	}
	// 但管理员仍能把它调回来
	setMinimumClientVersion(t, server, admin.AccessToken, 0)
	if response := getWithVersion(t, server.URL+"/v1/rooms/current", "", ""); response.StatusCode == http.StatusUpgradeRequired {
		t.Fatal("the administrator must be able to undo their own lockout")
	}
}

// 浏览器对自定义请求头会先发预检。服务端不声明允许 X-Client-Version 时，
// 真正的请求会被浏览器拦下——Web 端表现为「无法连接游戏服务」，而其他
// 平台不走 CORS 一切正常，非常难查。
func TestPreflightAllowsTheClientVersionHeader(t *testing.T) {
	server, _ := versionTestServer(t, 0)
	request, err := http.NewRequest(http.MethodOptions, server.URL+"/v1/auth/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "http://localhost:5173")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", clientVersionHeader)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	allowed := response.Header.Get("Access-Control-Allow-Headers")
	if !strings.Contains(strings.ToLower(allowed), strings.ToLower(clientVersionHeader)) {
		t.Fatalf("preflight must allow %s, got %q", clientVersionHeader, allowed)
	}
}

// 预检本身不带任何自定义请求头，按版本判断必然被当成过旧。挡掉预检等于
// 挡掉 Web 端的每一个请求，而浏览器只会报一句含糊的跨域失败。
func TestPreflightIsNotBlockedByTheVersionGate(t *testing.T) {
	server, _ := versionTestServer(t, 999_000_000)
	request, err := http.NewRequest(http.MethodOptions, server.URL+"/v1/rooms/current", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "http://localhost:5173")
	request.Header.Set("Access-Control-Request-Method", "GET")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusUpgradeRequired {
		t.Fatal("the gate must let preflight through, or Web clients cannot talk to the server at all")
	}
	// 真正的请求随后仍要过这道门
	if actual := getWithVersion(t, server.URL+"/v1/rooms/current", "3000", ""); actual.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("the real request must still be gated, got %d", actual.StatusCode)
	}
}
