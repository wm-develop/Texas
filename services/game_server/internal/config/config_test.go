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
