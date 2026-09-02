package transport

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPIgnoresForwardedHeadersFromUntrustedPeers(t *testing.T) {
	resolver := newClientIPResolver(nil)
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "203.0.113.10:4321"
	request.Header.Set("X-Forwarded-For", "198.51.100.1")
	request.Header.Set("X-Real-IP", "198.51.100.2")
	if got := resolver.resolve(request); got != "203.0.113.10" {
		t.Fatalf("untrusted peer must not be able to spoof its IP, got %s", got)
	}
}

func TestClientIPWalksForwardedChainPastTrustedProxies(t *testing.T) {
	resolver := newClientIPResolver([]string{"127.0.0.1", "172.16.0.0/12"})
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "172.18.0.1:80"
	// 客户端 -> 边缘代理(172.18.0.5) -> 本机 nginx(172.18.0.1)
	request.Header.Set("X-Forwarded-For", "198.51.100.7, 172.18.0.5")
	if got := resolver.resolve(request); got != "198.51.100.7" {
		t.Fatalf("expected first untrusted hop, got %s", got)
	}
}

func TestClientIPFallsBackToRealIPThenRemote(t *testing.T) {
	resolver := newClientIPResolver([]string{"127.0.0.1"})
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "127.0.0.1:9000"
	request.Header.Set("X-Real-IP", "198.51.100.9")
	if got := resolver.resolve(request); got != "198.51.100.9" {
		t.Fatalf("expected X-Real-IP, got %s", got)
	}
	request.Header.Del("X-Real-IP")
	if got := resolver.resolve(request); got != "127.0.0.1" {
		t.Fatalf("expected remote address fallback, got %s", got)
	}
}

func TestClientIPHandlesIPv6AndMalformedRemote(t *testing.T) {
	resolver := newClientIPResolver([]string{"::1"})
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "[::1]:443"
	request.Header.Set("X-Forwarded-For", "2001:db8::1")
	if got := resolver.resolve(request); got != "2001:db8::1" {
		t.Fatalf("expected forwarded IPv6 client, got %s", got)
	}
	request.RemoteAddr = "not-an-address"
	if got := resolver.resolve(request); got != "not-an-address" {
		t.Fatalf("malformed remote must still yield a non-empty key, got %q", got)
	}
}
