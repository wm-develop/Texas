package config

import (
	"bufio"
	"errors"
	"fmt"
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
