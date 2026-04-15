package config

import (
	"os"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should be valid: %v", err)
	}
	if cfg.Profile != ProfileDev {
		t.Fatalf("expected dev profile, got %s", cfg.Profile)
	}
}

func TestForProfile(t *testing.T) {
	tests := []struct {
		profile  Profile
		inMemory bool
	}{
		{ProfileDev, false},
		{ProfileTest, true},
		{ProfileProd, false},
	}
	for _, tt := range tests {
		cfg := ForProfile(tt.profile)
		if cfg.InMemory != tt.inMemory {
			t.Errorf("profile %s: expected InMemory=%v, got %v", tt.profile, tt.inMemory, cfg.InMemory)
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("profile %s: invalid config: %v", tt.profile, err)
		}
	}
}

func TestLoadFromEnv(t *testing.T) {
	cfg := DefaultConfig()

	os.Setenv("COSMOS_EXEC_LISTEN_ADDR", "127.0.0.1:9999")
	os.Setenv("COSMOS_EXEC_IN_MEMORY", "true")
	os.Setenv("COSMOS_EXEC_LOG_LEVEL", "debug")
	os.Setenv("COSMOS_EXEC_QUERY_GAS_MAX", "100000000")
	defer func() {
		os.Unsetenv("COSMOS_EXEC_LISTEN_ADDR")
		os.Unsetenv("COSMOS_EXEC_IN_MEMORY")
		os.Unsetenv("COSMOS_EXEC_LOG_LEVEL")
		os.Unsetenv("COSMOS_EXEC_QUERY_GAS_MAX")
	}()

	cfg.LoadFromEnv()

	if cfg.ListenAddr != "127.0.0.1:9999" {
		t.Errorf("expected listen addr 127.0.0.1:9999, got %s", cfg.ListenAddr)
	}
	if !cfg.InMemory {
		t.Error("expected InMemory=true")
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected log level debug, got %s", cfg.LogLevel)
	}
	if cfg.QueryGasMax != 100_000_000 {
		t.Errorf("expected query gas max 100000000, got %d", cfg.QueryGasMax)
	}
}

func TestResolveDataDir(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Home = "/tmp/test-home"
	cfg.DataDir = ""
	if got := cfg.ResolveDataDir(); got != "/tmp/test-home/data" {
		t.Errorf("expected /tmp/test-home/data, got %s", got)
	}

	cfg.DataDir = "/custom/data"
	if got := cfg.ResolveDataDir(); got != "/custom/data" {
		t.Errorf("expected /custom/data, got %s", got)
	}
}

func TestValidateErrors(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ListenAddr = ""
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for empty listen addr")
	}

	cfg = DefaultConfig()
	cfg.QueryGasMax = 0
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for zero query gas max")
	}
}
