package transport

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"texas/services/game_server/internal/account"
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
//
// 登录与「调整门槛」这两个管理路径是逃生通道：门槛一旦设得比管理员自己的
// 客户端还高，他就会被自己挡在门外，再也改不回来——那时只能登服务器直接
// 改数据库，正是这个功能想避免的事。放行它们不影响实际防护：牌桌、房间、
// 钱包等所有真正的操作仍被拦住，而客户端启动时查到版本过旧就直接进阻断页，
// 玩家根本走不到登录之后。
var clientVersionExemptPaths = map[string]struct{}{
	"/healthz":                          {},
	"/readyz":                           {},
	"/metrics":                          {},
	"/v1/client/version":                {},
	"/v1/auth/login":                    {},
	"/v1/auth/refresh":                  {},
	"/v1/admin/settings/client-version": {},
}

// clientVersionGate 持有当前生效的最低客户端版本。
//
// 值存在数据库里，由管理员通过接口修改：只更新客户端时也能调整门槛，
// 不必登服务器改环境变量并重建容器。每个请求都要判断，因此在进程内缓存成
// 一个原子值，管理接口改动时同步刷新——运行中的牌桌本就是单实例内存单写者，
// 进程内缓存与之一致。
type clientVersionGate struct {
	minimum atomic.Int64
}

func (gate *clientVersionGate) current() int { return int(gate.minimum.Load()) }

func (gate *clientVersionGate) set(version int) {
	if version < 0 {
		version = 0
	}
	gate.minimum.Store(int64(version))
}

// load 从数据库读入当前门槛。读失败时保持不启用：宁可放行也不要因为一次
// 查询失败把所有客户端挡在门外。
func (gate *clientVersionGate) load(ctx context.Context, accounts *account.Service, logger *slog.Logger) {
	if accounts == nil {
		return
	}
	minimum, err := accounts.MinimumClientVersion(ctx)
	if err != nil {
		if logger != nil {
			logger.Warn("could not load the minimum client version", "error", err)
		}
		return
	}
	gate.set(minimum)
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

// clientVersionGuard 拒绝低于当前门槛的客户端。
//
// 门槛为 0 时完全不启用：没有设置过的部署行为不变。启用后，不上报版本的
// 客户端一律视为过旧——它们正是需要被拦下的旧版本。
//
// 门禁放在整个 mux 之外，WebSocket 升级同样被覆盖：只挡登录接口的话，
// 已经登录的旧客户端还能继续连牌桌。
func clientVersionGuard(gate *clientVersionGate, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		minimum := gate.current()
		if minimum <= 0 {
			next.ServeHTTP(writer, request)
			return
		}
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

// registerClientVersionRoutes 公布版本要求，并让管理员随时调整门槛。
//
// 查询端点无需鉴权：客户端在登录之前就要能判断自己是不是太旧。
func registerClientVersionRoutes(
	mux *http.ServeMux,
	accounts *account.Service,
	gate *clientVersionGate,
) {
	mux.HandleFunc("GET /v1/client/version", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{"minimum": gate.current()})
	})

	if accounts == nil {
		return
	}
	mux.HandleFunc("GET /v1/admin/settings/client-version", func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		settings, err := accounts.ClientVersionSettings(request.Context(), actor)
		if err != nil {
			writeAccountError(writer, err)
			return
		}
		gate.set(settings.Minimum)
		writeJSON(writer, http.StatusOK, settings)
	})
	mux.HandleFunc("POST /v1/admin/settings/client-version", func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := authenticateRequest(writer, request, accounts)
		if !ok {
			return
		}
		var body struct {
			Minimum int `json:"minimum"`
		}
		if !decodeJSONBody(writer, request, &body) {
			return
		}
		settings, err := accounts.SetMinimumClientVersion(request.Context(), actor, body.Minimum)
		if err != nil {
			writeAccountError(writer, err)
			return
		}
		// 立即生效：管理员改完不需要重启服务。
		gate.set(settings.Minimum)
		writeJSON(writer, http.StatusOK, settings)
	})
}
