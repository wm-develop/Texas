package config

import (
	"strings"
	"testing"
	"time"
)

func TestShutdownDrainTimeoutDefaultsAndParses(t *testing.T) {
	t.Setenv("SHUTDOWN_DRAIN_TIMEOUT_SECONDS", "")
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ShutdownDrainTimeout != 120*time.Second {
		t.Fatalf("default drain timeout=%s, expected 120s", loaded.ShutdownDrainTimeout)
	}

	t.Setenv("SHUTDOWN_DRAIN_TIMEOUT_SECONDS", "0")
	loaded, err = Load()
	if err != nil || loaded.ShutdownDrainTimeout != 0 {
		t.Fatalf("0 must disable waiting: %s err=%v", loaded.ShutdownDrainTimeout, err)
	}

	t.Setenv("SHUTDOWN_DRAIN_TIMEOUT_SECONDS", "601")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SHUTDOWN_DRAIN_TIMEOUT_SECONDS") {
		t.Fatalf("values above 10 minutes must be rejected, err=%v", err)
	}
}
