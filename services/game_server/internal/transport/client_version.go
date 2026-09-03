package transport

import (
	"net/http"
	"strconv"
	"strings"
)

// clientVersionHeader 与 clientVersionQuery 是客户端上报自身版本的两种方式。
//
// 浏览器的 WebSocket API 不允许设置自定义请求头，因此 WS 走查询参数；
// 普通 HTTP 请求走请求头。服务端两者都认。
const (
	clientVersionHeader = "X-Client-Version"
	clientVersionQuery  = "clientVersion"
)

// clientVersionExemptPaths 是版本门禁必须放行的路径。
//
// 版本查询本身一定要放行，否则过旧的客户端连「该更新到哪个版本」都问不到；
// 健康检查与指标是运维接口，不来自客户端。
var clientVersionExemptPaths = map[string]struct{}{
	"/healthz":           {},
	"/readyz":            {},
	"/metrics":           {},
	"/v1/client/version": {},
}

// readClientVersion 读取客户端上报的版本号。缺失或格式非法时返回 0。
func readClientVersion(request *http.Request) int {
	raw := strings.TrimSpace(request.Header.Get(clientVersionHeader))
	if raw == "" {
		raw = strings.TrimSpace(request.URL.Query().Get(clientVersionQuery))
	}
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

// clientVersionGuard 拒绝低于最低要求的客户端。
//
// minimum 为 0 时完全不启用：没有配置 MINIMUM_CLIENT_VERSION 的部署行为不变。
// 启用后，不上报版本的客户端一律视为过旧——它们正是需要被拦下的旧版本。
//
// 门禁放在整个 mux 之外，WebSocket 升级同样被覆盖：只挡登录接口的话，
// 已经登录的旧客户端还能继续连牌桌。
func clientVersionGuard(minimum int, next http.Handler) http.Handler {
	if minimum <= 0 {
		return next
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if _, exempt := clientVersionExemptPaths[request.URL.Path]; exempt {
			next.ServeHTTP(writer, request)
			return
		}
		if readClientVersion(request) >= minimum {
			next.ServeHTTP(writer, request)
			return
		}
		// 426 的语义正是「需要升级后再来」。
		writeJSON(writer, http.StatusUpgradeRequired, map[string]any{
			"error":   "client_too_old",
			"minimum": minimum,
		})
	})
}

// registerClientVersionRoute 公布客户端版本要求，无需鉴权：
// 客户端在登录之前就要能判断自己是不是太旧。
func registerClientVersionRoute(mux *http.ServeMux, minimum, recommended int) {
	mux.HandleFunc("GET /v1/client/version", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"minimum":     minimum,
			"recommended": recommended,
		})
	})
}
