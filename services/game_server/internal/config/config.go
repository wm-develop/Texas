package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port           string
	StorageBackend string
	DatabaseURL    string
	AutoMigrate    bool
	AllowedOrigins []string
	TRTCSDKAppID   int
	TRTCSecretKey  string
	TRTCDebugToken string
	TRTCExpire     int
}

func Load() (Config, error) {
	loadLocalEnvironment()

	config := Config{
		Port:           valueOrDefault("PORT", "8080"),
		StorageBackend: strings.ToLower(valueOrDefault("STORAGE_BACKEND", "memory")),
		DatabaseURL:    strings.TrimSpace(os.Getenv("DATABASE_URL")),
		TRTCSecretKey:  strings.TrimSpace(os.Getenv("TRTC_SECRET_KEY")),
		TRTCDebugToken: strings.TrimSpace(os.Getenv("TRTC_DEBUG_TOKEN")),
		TRTCExpire:     3600,
	}
	if origins := strings.TrimSpace(os.Getenv("ALLOWED_ORIGINS")); origins != "" {
		for _, origin := range strings.Split(origins, ",") {
			if origin = strings.TrimSpace(origin); origin != "" {
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
