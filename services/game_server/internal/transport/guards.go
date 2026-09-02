package transport

import (
	"net/http"
	"strconv"
	"strings"

	"texas/services/game_server/internal/config"
	"texas/services/game_server/internal/metrics"
	"texas/services/game_server/internal/ratelimit"
)

// guards 汇总入口保护：按来源 IP 或已登录用户的分层限流、单 IP 的 WebSocket
// 并发上限，以及配套的可信代理解析与指标计数。
//
// 所有限流器允许为 nil（对应配置 off），nil 限流器永远放行，
// 因此各处调用不需要判空。
type guards struct {
	ips       *clientIPResolver
	auth      *ratelimit.Limiter // 登录/注册/刷新合计，按 IP
	register  *ratelimit.Limiter // 注册，按 IP
	loginFail *ratelimit.Limiter // 密码错误，按用户名
	userOps   *ratelimit.Limiter // 房间与钱包操作，按用户
	trtc      *ratelimit.Limiter // TRTC 凭证签发，按用户或 IP
	wsPerIP   *ratelimit.Concurrent
	limited   *metrics.Counter
}

func newGuards(trustedProxies []string, limits config.RateLimits, registry *metrics.Registry) *guards {
	result := &guards{
		ips:       newClientIPResolver(trustedProxies),
		auth:      newLimiter(limits.AuthPerIP),
		register:  newLimiter(limits.RegisterPerIP),
		loginFail: newLimiter(limits.LoginFailuresPerUser),
		userOps:   newLimiter(limits.UserOpsPerUser),
		trtc:      newLimiter(limits.TRTCPerUser),
	}
	if limits.WebSocketPerIP > 0 {
		result.wsPerIP = ratelimit.NewConcurrent(limits.WebSocketPerIP)
	}
	if registry != nil {
		result.limited = registry.NewCounter(
			"texas_rate_limited_total",
			"Requests rejected by rate limiting, by scope.",
			"scope",
		)
	}
	return result
}

func newLimiter(limit config.RateLimit) *ratelimit.Limiter {
	if !limit.Enabled() {
		return nil
	}
	return ratelimit.New(limit.Burst, limit.Window)
}

// clientIP 返回用于限流的客户端 IP；guards 为 nil 时退回 RemoteAddr。
func (guard *guards) clientIP(request *http.Request) string {
	if guard == nil {
		return request.RemoteAddr
	}
	return guard.ips.resolve(request)
}

// allow 在配额允许时返回 true；否则写出 429 响应并返回 false。
// scope 用于指标标签与日志，例如 "auth_ip"、"trtc_user"。
func (guard *guards) allow(
	writer http.ResponseWriter, limiter *ratelimit.Limiter, key, scope string,
) bool {
	if guard == nil || limiter == nil || limiter.Allow(key) {
		return true
	}
	guard.reject(writer, limiter, key, scope)
	return false
}

func (guard *guards) reject(
	writer http.ResponseWriter, limiter *ratelimit.Limiter, key, scope string,
) {
	if wait := limiter.RetryAfter(key); wait > 0 {
		writer.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
	}
	guard.limited.Inc(scope)
	writeJSONError(writer, http.StatusTooManyRequests, "rate_limited")
}

// normalizeUsername 使登录失败计数对大小写与首尾空白不敏感。
func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}
