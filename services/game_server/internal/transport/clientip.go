package transport

import (
	"net"
	"net/http"
	"strings"
)

// clientIPResolver 在反向代理后面还原真实客户端 IP。
//
// 只有当直接连接方（RemoteAddr）属于受信任代理时，才采信 X-Forwarded-For /
// X-Real-IP；否则这些头可以被任何客户端伪造，用它们做限流等于没有限流。
// 从 X-Forwarded-For 右端向左跳过所有受信任代理，第一个不受信任的地址即客户端。
type clientIPResolver struct {
	trusted []*net.IPNet
}

func newClientIPResolver(trustedProxies []string) *clientIPResolver {
	resolver := &clientIPResolver{}
	for _, raw := range trustedProxies {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if _, network, err := net.ParseCIDR(raw); err == nil {
			resolver.trusted = append(resolver.trusted, network)
			continue
		}
		if ip := net.ParseIP(raw); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			resolver.trusted = append(resolver.trusted, &net.IPNet{
				IP: ip, Mask: net.CIDRMask(bits, bits),
			})
		}
	}
	return resolver
}

func (resolver *clientIPResolver) isTrusted(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, network := range resolver.trusted {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// resolve 返回用于限流与日志的客户端 IP 字符串。解析失败时退回 RemoteAddr
// 原文，保证限流键永远非空。
func (resolver *clientIPResolver) resolve(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	remote := net.ParseIP(strings.TrimSpace(host))
	if remote == nil {
		return host
	}
	if resolver == nil || !resolver.isTrusted(remote) {
		return remote.String()
	}

	// 受信任代理：从右向左寻找第一个不受信任的地址
	forwarded := request.Header.Get("X-Forwarded-For")
	if forwarded != "" {
		parts := strings.Split(forwarded, ",")
		for index := len(parts) - 1; index >= 0; index-- {
			candidate := net.ParseIP(strings.TrimSpace(parts[index]))
			if candidate == nil {
				continue
			}
			if !resolver.isTrusted(candidate) {
				return candidate.String()
			}
		}
	}
	if real := net.ParseIP(strings.TrimSpace(request.Header.Get("X-Real-IP"))); real != nil {
		return real.String()
	}
	return remote.String()
}
