package config

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port            string
	StorageBackend  string
	DatabaseURL     string
	AutoMigrate     bool
	AllowedOrigins  []string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	TRTCSDKAppID    int
	TRTCSecretKey   string
	TRTCDebugToken  string
	TRTCExpire      int
	// TrustedProxies 是可以采信 X-Forwarded-For 的代理地址或网段。
	// 为空时永远使用直接连接方 IP，防止客户端伪造来源绕过限流。
	TrustedProxies []string
	// MetricsToken 非空时启用 /metrics，并要求 Bearer 令牌；为空时端点不存在。
	MetricsToken string
	RateLimits   RateLimits
	// ShutdownDrainTimeout 是收到停机信号后等待所有牌桌打完当前手的上限；
	// 到期仍在进行的手会随进程退出而作废。为 0 时不等待。
	ShutdownDrainTimeout time.Duration
}

// RateLimit 表示「每 Window 内最多 Burst 次」。
type RateLimit struct {
	Burst  int
	Window time.Duration
}

// RateLimits 汇总各入口的限流参数。零值表示不限制。
type RateLimits struct {
	// AuthPerIP 覆盖登录、注册、刷新令牌的合计频率，按客户端 IP 计。
	AuthPerIP RateLimit
	// RegisterPerIP 单独收紧注册，按客户端 IP 计。
	RegisterPerIP RateLimit
	// LoginFailuresPerUser 只在密码错误时计数，按用户名计；成功登录后清零。
	LoginFailuresPerUser RateLimit
	// UserOpsPerUser 覆盖建房、入房、离桌、准备与虚拟充值，按已登录用户计。
	UserOpsPerUser RateLimit
	// TRTCPerUser 覆盖 TRTC 凭证签发，按已登录用户计（未登录的调试路径按 IP 计）。
	TRTCPerUser RateLimit
	// WebSocketPerIP 是单个客户端 IP 允许同时保持的 WebSocket 连接数。
	WebSocketPerIP int
}

// 默认值面向熟人牌局的真实使用强度，既拦住脚本滥用，也不会影响正常玩家。
var defaultRateLimits = RateLimits{
	AuthPerIP:            RateLimit{Burst: 30, Window: 5 * time.Minute},
	RegisterPerIP:        RateLimit{Burst: 5, Window: time.Hour},
	LoginFailuresPerUser: RateLimit{Burst: 8, Window: 15 * time.Minute},
	UserOpsPerUser:       RateLimit{Burst: 30, Window: time.Minute},
	TRTCPerUser:          RateLimit{Burst: 12, Window: time.Minute},
	WebSocketPerIP:       20,
}

func Load() (Config, error) {
	loadLocalEnvironment()

	config := Config{
		Port:            valueOrDefault("PORT", "8080"),
		StorageBackend:  strings.ToLower(valueOrDefault("STORAGE_BACKEND", "memory")),
		DatabaseURL:     strings.TrimSpace(os.Getenv("DATABASE_URL")),
		TRTCSecretKey:   strings.TrimSpace(os.Getenv("TRTC_SECRET_KEY")),
		TRTCDebugToken:  strings.TrimSpace(os.Getenv("TRTC_DEBUG_TOKEN")),
		TRTCExpire:      3600,
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 30 * 24 * time.Hour,
	}
	accessTTL, err := durationFromSeconds(
		"AUTH_ACCESS_TOKEN_TTL_SECONDS", config.AccessTokenTTL, 60, 24*time.Hour,
	)
	if err != nil {
		return Config{}, err
	}
	refreshTTL, err := durationFromSeconds(
		"AUTH_REFRESH_TOKEN_TTL_SECONDS", config.RefreshTokenTTL,
		60*time.Minute, 365*24*time.Hour,
	)
	if err != nil {
		return Config{}, err
	}
	if refreshTTL <= accessTTL {
		return Config{}, errors.New("AUTH_REFRESH_TOKEN_TTL_SECONDS must be greater than AUTH_ACCESS_TOKEN_TTL_SECONDS")
	}
	config.AccessTokenTTL = accessTTL
	config.RefreshTokenTTL = refreshTTL
	drainTimeout, err := durationFromSeconds(
		"SHUTDOWN_DRAIN_TIMEOUT_SECONDS", 120*time.Second, 0, 10*time.Minute,
	)
	if err != nil {
		return Config{}, err
	}
	config.ShutdownDrainTimeout = drainTimeout
	if origins := strings.TrimSpace(os.Getenv("ALLOWED_ORIGINS")); origins != "" {
		for _, origin := range strings.Split(origins, ",") {
			if origin = strings.TrimSuffix(strings.TrimSpace(origin), "/"); origin != "" {
				if err := validateAllowedOrigin(origin); err != nil {
					return Config{}, err
				}
				config.AllowedOrigins = append(config.AllowedOrigins, origin)
			}
		}
	}
	if config.StorageBackend != "memory" && config.StorageBackend != "postgres" {
		return Config{}, errors.New("STORAGE_BACKEND must be memory or postgres")
	}
	if config.StorageBackend == "postgres" && config.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required when STORAGE_BACKEND=postgres")
	}
	if autoMigrateText := strings.TrimSpace(os.Getenv("DATABASE_AUTO_MIGRATE")); autoMigrateText != "" {
		autoMigrate, err := strconv.ParseBool(autoMigrateText)
		if err != nil {
			return Config{}, errors.New("DATABASE_AUTO_MIGRATE must be true or false")
		}
		config.AutoMigrate = autoMigrate
	}
	if proxies := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES")); proxies != "" {
		for _, entry := range strings.Split(proxies, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			if err := validateProxyEntry(entry); err != nil {
				return Config{}, err
			}
			config.TrustedProxies = append(config.TrustedProxies, entry)
		}
	}
	config.MetricsToken = strings.TrimSpace(os.Getenv("METRICS_TOKEN"))
	if config.MetricsToken != "" && len(config.MetricsToken) < 16 {
		return Config{}, errors.New("METRICS_TOKEN must be at least 16 characters")
	}
	rateLimits, err := loadRateLimits()
	if err != nil {
		return Config{}, err
	}
	config.RateLimits = rateLimits

	appIDText := strings.TrimSpace(os.Getenv("TRTC_SDK_APP_ID"))
	if appIDText == "" && config.TRTCSecretKey == "" {
		return config, nil
	}
	if appIDText == "" || config.TRTCSecretKey == "" {
		return Config{}, errors.New("TRTC_SDK_APP_ID and TRTC_SECRET_KEY must both be configured")
	}

	appID, err := strconv.Atoi(appIDText)
	if err != nil || appID <= 0 {
		return Config{}, errors.New("TRTC_SDK_APP_ID must be a positive integer")
	}
	config.TRTCSDKAppID = appID

	if expireText := strings.TrimSpace(os.Getenv("TRTC_USER_SIG_EXPIRE_SECONDS")); expireText != "" {
		expire, parseErr := strconv.Atoi(expireText)
		if parseErr != nil || expire < 300 || expire > 86400 {
			return Config{}, errors.New("TRTC_USER_SIG_EXPIRE_SECONDS must be between 300 and 86400")
		}
		config.TRTCExpire = expire
	}

	return config, nil
}

func validateProxyEntry(entry string) error {
	if _, _, err := net.ParseCIDR(entry); err == nil {
		return nil
	}
	if net.ParseIP(entry) != nil {
		return nil
	}
	return fmt.Errorf("TRUSTED_PROXIES contains invalid address %q (expected IP or CIDR)", entry)
}

// loadRateLimits 读取 RATE_LIMIT_* 环境变量，格式为「次数/时长」，例如 30/5m。
// 显式写成 0 或 off 表示关闭该项限制。未设置时使用内置默认值。
func loadRateLimits() (RateLimits, error) {
	limits := defaultRateLimits
	entries := []struct {
		name   string
		target *RateLimit
	}{
		{"RATE_LIMIT_AUTH_PER_IP", &limits.AuthPerIP},
		{"RATE_LIMIT_REGISTER_PER_IP", &limits.RegisterPerIP},
		{"RATE_LIMIT_LOGIN_FAILURES_PER_USER", &limits.LoginFailuresPerUser},
		{"RATE_LIMIT_USER_OPS_PER_USER", &limits.UserOpsPerUser},
		{"RATE_LIMIT_TRTC_PER_USER", &limits.TRTCPerUser},
	}
	for _, entry := range entries {
		raw := strings.TrimSpace(os.Getenv(entry.name))
		if raw == "" {
			continue
		}
		parsed, err := parseRateLimit(raw)
		if err != nil {
			return RateLimits{}, fmt.Errorf("%s: %w", entry.name, err)
		}
		*entry.target = parsed
	}
	if raw := strings.TrimSpace(os.Getenv("RATE_LIMIT_WS_CONNECTIONS_PER_IP")); raw != "" {
		if strings.EqualFold(raw, "off") {
			limits.WebSocketPerIP = 0
		} else {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 0 {
				return RateLimits{}, errors.New("RATE_LIMIT_WS_CONNECTIONS_PER_IP must be a non-negative integer or off")
			}
			limits.WebSocketPerIP = value
		}
	}
	return limits, nil
}

func parseRateLimit(raw string) (RateLimit, error) {
	if strings.EqualFold(raw, "off") || raw == "0" {
		return RateLimit{}, nil
	}
	parts := strings.SplitN(raw, "/", 2)
	if len(parts) != 2 {
		return RateLimit{}, errors.New(`expected "count/duration" such as 30/5m, or off`)
	}
	burst, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || burst <= 0 {
		return RateLimit{}, errors.New("count must be a positive integer")
	}
	window, err := time.ParseDuration(strings.TrimSpace(parts[1]))
	if err != nil || window <= 0 {
		return RateLimit{}, errors.New("duration must be a positive Go duration such as 5m or 1h")
	}
	return RateLimit{Burst: burst, Window: window}, nil
}

// Enabled 报告该限流项是否生效。
func (limit RateLimit) Enabled() bool { return limit.Burst > 0 && limit.Window > 0 }

func validateAllowedOrigin(origin string) error {
	if strings.ContainsAny(origin, "，； \t\r\n") {
		return fmt.Errorf("ALLOWED_ORIGINS contains invalid origin %q", origin)
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil || parsed.Path != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("ALLOWED_ORIGINS contains invalid origin %q", origin)
	}
	return nil
}

func (config Config) TRTCEnabled() bool {
	return config.TRTCSDKAppID > 0 && config.TRTCSecretKey != ""
}

func (config Config) DatabaseEnabled() bool {
	return config.StorageBackend == "postgres"
}

func (config Config) DatabaseConfigured() bool { return config.DatabaseURL != "" }

func loadLocalEnvironment() {
	paths := []string{os.Getenv("TEXAS_ENV_FILE"), ".env", "../../.env"}
	for _, path := range paths {
		if path == "" {
			continue
		}
		if err := loadEnvironmentFile(path); err == nil {
			return
		}
	}
}

func loadEnvironmentFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			return fmt.Errorf("invalid environment line for key %q", line)
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key == "" {
			return errors.New("environment key cannot be empty")
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func valueOrDefault(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func durationFromSeconds(
	key string,
	fallback time.Duration,
	minimum time.Duration,
	maximum time.Duration,
) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer number of seconds", key)
	}
	minimumSeconds := int64(minimum / time.Second)
	maximumSeconds := int64(maximum / time.Second)
	if seconds < minimumSeconds || seconds > maximumSeconds {
		return 0, fmt.Errorf(
			"%s must be between %d and %d seconds",
			key, minimumSeconds, maximumSeconds,
		)
	}
	return time.Duration(seconds) * time.Second, nil
}
