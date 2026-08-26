package config

import "testing"

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
