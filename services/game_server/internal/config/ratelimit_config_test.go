package config

import (
	"strings"
	"testing"
	"time"
)

func TestRateLimitsUseDefaultsWhenUnset(t *testing.T) {
	limits, err := loadRateLimits()
	if err != nil {
		t.Fatal(err)
	}
	if limits != defaultRateLimits {
		t.Fatalf("expected defaults, got %#v", limits)
	}
	if !limits.AuthPerIP.Enabled() || limits.WebSocketPerIP <= 0 {
		t.Fatal("defaults must enable protection out of the box")
	}
}

func TestRateLimitsParseOverridesAndOff(t *testing.T) {
	t.Setenv("RATE_LIMIT_AUTH_PER_IP", "10/2m")
	t.Setenv("RATE_LIMIT_REGISTER_PER_IP", "off")
	t.Setenv("RATE_LIMIT_LOGIN_FAILURES_PER_USER", "0")
	t.Setenv("RATE_LIMIT_WS_CONNECTIONS_PER_IP", "off")
	limits, err := loadRateLimits()
	if err != nil {
		t.Fatal(err)
	}
	if limits.AuthPerIP != (RateLimit{Burst: 10, Window: 2 * time.Minute}) {
		t.Fatalf("AuthPerIP=%#v", limits.AuthPerIP)
	}
	if limits.RegisterPerIP.Enabled() || limits.LoginFailuresPerUser.Enabled() {
		t.Fatal("off / 0 must disable the limit")
	}
	if limits.WebSocketPerIP != 0 {
		t.Fatal("off must disable websocket cap")
	}
	// 未覆盖的项保持默认
	if limits.TRTCPerUser != defaultRateLimits.TRTCPerUser {
		t.Fatal("untouched limits must keep defaults")
	}
}

func TestRateLimitsRejectMalformedValues(t *testing.T) {
	for _, raw := range []string{"abc", "10", "-5/1m", "10/0s", "10/soon", "/1m"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("RATE_LIMIT_AUTH_PER_IP", raw)
			if _, err := loadRateLimits(); err == nil {
				t.Fatalf("%q should be rejected", raw)
			} else if !strings.Contains(err.Error(), "RATE_LIMIT_AUTH_PER_IP") {
				t.Fatalf("error should name the variable: %v", err)
			}
		})
	}
	t.Setenv("RATE_LIMIT_AUTH_PER_IP", "")
	t.Setenv("RATE_LIMIT_WS_CONNECTIONS_PER_IP", "-1")
	if _, err := loadRateLimits(); err == nil {
		t.Fatal("negative websocket cap should be rejected")
	}
}

func TestTrustedProxiesAndMetricsTokenValidation(t *testing.T) {
	t.Setenv("STORAGE_BACKEND", "memory")
	t.Setenv("TRUSTED_PROXIES", "127.0.0.1, 172.16.0.0/12,::1")
	t.Setenv("METRICS_TOKEN", "0123456789abcdef")
	config, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(config.TrustedProxies) != 3 {
		t.Fatalf("proxies=%v", config.TrustedProxies)
	}
	if config.MetricsToken == "" {
		t.Fatal("metrics token should be loaded")
	}

	t.Setenv("TRUSTED_PROXIES", "not-an-ip")
	if _, err := Load(); err == nil {
		t.Fatal("invalid proxy entry must be rejected")
	}
	t.Setenv("TRUSTED_PROXIES", "")
	t.Setenv("METRICS_TOKEN", "short")
	if _, err := Load(); err == nil {
		t.Fatal("short metrics token must be rejected")
	}
}
