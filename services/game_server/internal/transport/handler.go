package transport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"texas/services/game_server/internal/account"
	"texas/services/game_server/internal/bankroll"
	"texas/services/game_server/internal/chat"
	"texas/services/game_server/internal/config"
	"texas/services/game_server/internal/game/tablemanager"
	"texas/services/game_server/internal/history"
	"texas/services/game_server/internal/metrics"
	"texas/services/game_server/internal/room"
	"texas/services/game_server/internal/trtc"
)

type Options struct {
	TRTCIssuer     trtc.CredentialIssuer
	TRTCDebugToken string
	TRTCAuthorizer trtc.AccessAuthorizer
	Accounts       *account.Service
	Bankroll       *bankroll.Service
	Rooms          *room.Service
	Tables         *tablemanager.Manager
	Chat           *chat.Service
	History        history.Store
	Readiness      func(context.Context) error
	AllowedOrigins []string
	// TrustedProxies 见 config.Config.TrustedProxies。
	TrustedProxies []string
	// RateLimits 为零值时全部关闭，便于测试；生产由 config 提供默认值。
	RateLimits config.RateLimits
	// Metrics 为 nil 时不采集指标；MetricsToken 为空时不暴露 /metrics。
	Metrics      *metrics.Registry
	MetricsToken string
	// InstanceID 标识本进程，随 session.authenticated 下发；客户端据此判断
	// 重连后服务端是否已重启。为空时 NewHandler 生成随机值。
	InstanceID string
}

func NewHandler(logger *slog.Logger, options Options) http.Handler {
	mux := http.NewServeMux()
	presence := newPresenceTracker()
	if options.InstanceID == "" {
		options.InstanceID = randomInstanceID()
	}
	guard := newGuards(options.TrustedProxies, options.RateLimits, options.Metrics)
	webSockets := newWebSocketServer(logger, options, presence, guard)
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("GET /readyz", handleReadiness(options.Readiness))
	registerAccountRoutes(mux, options.Accounts, presence, guard)
	registerAccountDeletionRoute(mux, options.Accounts, options.Bankroll, options.Rooms, guard)
	registerBankrollRoutes(mux, options.Accounts, options.Bankroll, guard)
	registerAdminRoutes(
		mux, options.Accounts, options.Bankroll, options.Rooms, options.Tables,
		options.Chat, presence, webSockets.disconnectUsers,
	)
	registerAdminAuditRoute(mux, options.Accounts)
	registerRoomRoutes(mux, options.Accounts, options.Rooms, options.Tables, guard)
	registerRoomOwnerRoutes(
		mux, options.Accounts, options.Rooms, options.Tables, options.Bankroll,
		guard, webSockets.disconnectUsers,
	)
	registerHistoryRoutes(mux, options.Accounts, options.History)
	mux.Handle("GET /ws", webSockets)
	mux.Handle("POST /v1/trtc/credentials", trtcCredentialsHandler(options, guard))
	if options.Metrics != nil && options.MetricsToken != "" {
		// 指标含房间数、连接数等运营信息，必须持令牌访问；未配置令牌时端点不存在。
		mux.Handle("GET /metrics", bearerTokenGuard(options.MetricsToken, options.Metrics.Handler()))
	}
	var handler http.Handler = mux
	if options.Metrics != nil {
		handler = httpMetrics(options.Metrics, handler)
	}
	return securityHeaders(configuredCORS(handler, options.AllowedOrigins))
}

// bearerTokenGuard 要求 Authorization: Bearer <token> 与配置值完全一致。
func bearerTokenGuard(expected string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if readBearerToken(request.Header.Get("Authorization")) != expected {
			writeJSONError(writer, http.StatusUnauthorized, "authentication_required")
			return
		}
		next.ServeHTTP(writer, request)
	})
}

// httpMetrics 按路由模式与状态码统计每个 HTTP 请求。
// Go 1.22+ 的 ServeMux 会把匹配到的模式写回 request.Pattern，外层中间件在
// next.ServeHTTP 返回后即可读到，无需在每个处理器里手工打点。
func httpMetrics(registry *metrics.Registry, next http.Handler) http.Handler {
	requests := registry.NewCounter(
		"texas_http_requests_total",
		"HTTP requests by route pattern and status code.",
		"route", "status",
	)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		recorder := &statusRecorder{ResponseWriter: writer, status: http.StatusOK}
		next.ServeHTTP(recorder, request)
		route := request.Pattern
		if route == "" {
			route = "unmatched"
		}
		requests.Inc(route, strconv.Itoa(recorder.status))
	})
}

// statusRecorder 记录响应状态码。WebSocket 升级会劫持连接，此时状态保持 200。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *statusRecorder) WriteHeader(status int) {
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

// Unwrap 让 http.ResponseController 与 WebSocket 库能拿到底层 Hijacker/Flusher。
func (recorder *statusRecorder) Unwrap() http.ResponseWriter { return recorder.ResponseWriter }

func configuredCORS(next http.Handler, allowedOrigins []string) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[strings.TrimSpace(origin)] = struct{}{}
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		_, explicitlyAllowed := allowed[origin]
		if explicitlyAllowed || isLocalDevelopmentOrigin(origin) {
			writer.Header().Set("Access-Control-Allow-Origin", origin)
			writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			writer.Header().Add("Vary", "Origin")
			if request.Method == http.MethodOptions {
				writer.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Permissions-Policy", "camera=(), geolocation=()")
		next.ServeHTTP(writer, request)
	})
}

func isLocalDevelopmentOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	host := parsed.Hostname()
	return host == "localhost" || net.ParseIP(host).IsLoopback()
}

func handleHealth(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(`{"status":"ok"}`))
}

func handleReadiness(check func(context.Context) error) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if check != nil {
			ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
			err := check(ctx)
			cancel()
			if err != nil {
				writeJSONError(writer, http.StatusServiceUnavailable, "not_ready")
				return
			}
		}
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ready"})
	}
}

type trtcCredentialsRequest struct {
	UserID string `json:"userId"`
	RoomID string `json:"roomId"`
}

func trtcCredentialsHandler(options Options, guard *guards) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if options.TRTCIssuer == nil {
			writeJSONError(writer, http.StatusServiceUnavailable, "trtc_not_configured")
			return
		}
		var body trtcCredentialsRequest
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 4*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			writeJSONError(writer, http.StatusBadRequest, "invalid_request")
			return
		}
		// 每张 UserSig 都对应云服务成本，限流键在鉴权之后才可信：
		// 已登录路径按用户计，调试令牌路径按来源 IP 计。
		limitKey := "ip:" + guard.clientIP(request)
		if !allowDebugCredentialRequest(
			request,
			options.TRTCDebugToken,
			options.TRTCAuthorizer == nil,
		) {
			if options.TRTCAuthorizer == nil {
				writeJSONError(writer, http.StatusUnauthorized, "authentication_required")
				return
			}
			err := options.TRTCAuthorizer.AuthorizeVoice(
				request.Context(),
				readBearerToken(request.Header.Get("Authorization")),
				body.UserID,
				body.RoomID,
			)
			if err != nil {
				var accessError trtc.AccessError
				if errors.As(err, &accessError) && accessError.Code == "permission_denied" {
					writeJSONError(writer, http.StatusForbidden, accessError.Code)
				} else {
					writeJSONError(writer, http.StatusUnauthorized, "authentication_required")
				}
				return
			}
			limitKey = "user:" + body.UserID
		}
		if !guard.allow(writer, guard.trtc, limitKey, "trtc") {
			return
		}

		credentials, err := options.TRTCIssuer.Issue(body.UserID, body.RoomID)
		if err != nil {
			writeJSONError(writer, http.StatusBadRequest, "invalid_identity")
			return
		}

		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(writer).Encode(credentials)
	})
}

func allowDebugCredentialRequest(request *http.Request, expectedToken string, allowLoopback bool) bool {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if allowLoopback && err == nil && net.ParseIP(host).IsLoopback() {
		return true
	}
	if expectedToken == "" {
		return false
	}
	providedToken := readBearerToken(request.Header.Get("Authorization"))
	return providedToken == expectedToken
}

func readBearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func writeJSONError(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]string{"error": code})
}

// randomInstanceID 生成进程级随机标识；失败时退回启动时间，仍能区分重启。
func randomInstanceID() string {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return "inst_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "inst_" + hex.EncodeToString(value)
}
