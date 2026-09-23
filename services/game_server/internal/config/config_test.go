package config

import (
	"testing"
	"time"
)

func TestLoadTRTCConfiguration(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("TRTC_SDK_APP_ID", "1400000000")
	t.Setenv("TRTC_SECRET_KEY", "test-secret")
	t.Setenv("TRTC_USER_SIG_EXPIRE_SECONDS", "1800")

	config, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !config.TRTCEnabled() {
		t.Fatal("TRTC should be enabled")
	}
	if config.TRTCExpire != 1800 {
		t.Fatalf("TRTCExpire = %d, want 1800", config.TRTCExpire)
	}
}

func TestLoadOptionalDatabaseConfiguration(t *testing.T) {
	t.Setenv("TRTC_SDK_APP_ID", "")
	t.Setenv("TRTC_SECRET_KEY", "")
	t.Setenv("DATABASE_URL", "postgres://texas:secret@localhost:5432/texas?sslmode=disable")
	t.Setenv("STORAGE_BACKEND", "postgres")
	t.Setenv("DATABASE_AUTO_MIGRATE", "true")

	config, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !config.DatabaseEnabled() || config.DatabaseURL == "" || !config.AutoMigrate {
		t.Fatalf("database configuration=%#v", config)
	}
}

func TestPostgresStorageRequiresDatabaseURL(t *testing.T) {
	t.Setenv("TRTC_SDK_APP_ID", "")
	t.Setenv("TRTC_SECRET_KEY", "")
	t.Setenv("STORAGE_BACKEND", "postgres")
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load should reject postgres storage without DATABASE_URL")
	}
}

func TestLoadAllowedOrigins(t *testing.T) {
	t.Setenv("TRTC_SDK_APP_ID", "")
	t.Setenv("TRTC_SECRET_KEY", "")
	t.Setenv("STORAGE_BACKEND", "memory")
	t.Setenv("ALLOWED_ORIGINS", "https://poker.example.com/, https://test.example.com:8443")
	config, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(config.AllowedOrigins) != 2 || config.AllowedOrigins[0] != "https://poker.example.com" {
		t.Fatalf("allowed origins=%#v", config.AllowedOrigins)
	}
}

func TestLoadRejectsPunctuatedAllowedOrigin(t *testing.T) {
	t.Setenv("TRTC_SDK_APP_ID", "")
	t.Setenv("TRTC_SECRET_KEY", "")
	t.Setenv("STORAGE_BACKEND", "memory")
	t.Setenv("ALLOWED_ORIGINS", "https://poker.example.com，")
	if _, err := Load(); err == nil {
		t.Fatal("Load should reject an origin with Chinese punctuation")
	}
}

func TestLoadAuthenticationTokenTTLs(t *testing.T) {
	t.Setenv("TRTC_SDK_APP_ID", "")
	t.Setenv("TRTC_SECRET_KEY", "")
	t.Setenv("AUTH_ACCESS_TOKEN_TTL_SECONDS", "600")
	t.Setenv("AUTH_REFRESH_TOKEN_TTL_SECONDS", "7200")

	config, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.AccessTokenTTL != 10*time.Minute || config.RefreshTokenTTL != 2*time.Hour {
		t.Fatalf("token TTLs access=%s refresh=%s", config.AccessTokenTTL, config.RefreshTokenTTL)
	}
}

func TestLoadRejectsInvalidAuthenticationTokenTTLs(t *testing.T) {
	t.Setenv("TRTC_SDK_APP_ID", "")
	t.Setenv("TRTC_SECRET_KEY", "")
	t.Setenv("AUTH_ACCESS_TOKEN_TTL_SECONDS", "3600")
	t.Setenv("AUTH_REFRESH_TOKEN_TTL_SECONDS", "3600")
	if _, err := Load(); err == nil {
		t.Fatal("Load should reject a refresh TTL that is not longer than the access TTL")
	}
}

// AI 复盘：不配密钥就不启用；默认接 DeepSeek；超时、JSON 模式与输出上限写错时拒绝启动，
// 免得管理员以为改好了却悄悄用了默认值。
func TestLoadReviewConfiguration(t *testing.T) {
	t.Setenv("TRTC_SDK_APP_ID", "")
	t.Setenv("TRTC_SECRET_KEY", "")
	for _, key := range []string{
		"REVIEW_API_KEY", "REVIEW_BASE_URL", "REVIEW_MODEL", "REVIEW_TIMEOUT_SECONDS", "REVIEW_JSON_MODE", "REVIEW_MAX_TOKENS",
	} {
		t.Setenv(key, "")
	}
	config, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.ReviewEnabled() || config.Review.BaseURL != "https://api.deepseek.com" ||
		config.Review.Model != "deepseek-reasoner" || !config.Review.JSONMode ||
		config.Review.Timeout != 5*time.Minute || config.Review.MaxTokens != 0 {
		t.Fatalf("defaults=%+v", config.Review)
	}

	t.Setenv("REVIEW_API_KEY", "test-key")
	t.Setenv("REVIEW_BASE_URL", "https://llm.example.com/v1")
	t.Setenv("REVIEW_MODEL", "qwen-plus")
	t.Setenv("REVIEW_TIMEOUT_SECONDS", "600")
	t.Setenv("REVIEW_JSON_MODE", "false")
	t.Setenv("REVIEW_MAX_TOKENS", "8000")
	if config, err = Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !config.ReviewEnabled() || config.Review.BaseURL != "https://llm.example.com/v1" || config.Review.Model != "qwen-plus" ||
		config.Review.JSONMode || config.Review.Timeout != 10*time.Minute || config.Review.MaxTokens != 8000 {
		t.Fatalf("configured=%+v", config.Review)
	}

	for key, value := range map[string]string{
		"REVIEW_TIMEOUT_SECONDS": "5",
		"REVIEW_JSON_MODE":       "maybe",
		"REVIEW_MAX_TOKENS":      "-1",
		"REVIEW_BASE_URL":        "api.deepseek.com",
	} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, value)
			if _, err := Load(); err == nil {
				t.Fatalf("%s=%s must be rejected", key, value)
			}
		})
	}
}
