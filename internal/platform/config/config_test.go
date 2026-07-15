package config

import (
	"path/filepath"
	"testing"
)

func TestLoadUsesM0Defaults(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("AUTH_KEY", "test-auth-key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Server.Host != "0.0.0.0" {
		t.Fatalf("Host = %q, want 0.0.0.0", cfg.Server.Host)
	}
	if cfg.Server.Port != 3001 {
		t.Fatalf("Port = %d, want 3001", cfg.Server.Port)
	}
	if cfg.Server.GracefulShutdownTimeout != 10 {
		t.Fatalf("GracefulShutdownTimeout = %d, want 10", cfg.Server.GracefulShutdownTimeout)
	}
	if cfg.DataDir != "./data" {
		t.Fatalf("DataDir = %q, want ./data", cfg.DataDir)
	}
	if cfg.DatabaseDSN != filepath.Join("./data", "gpt-load.db") {
		t.Fatalf("DatabaseDSN = %q", cfg.DatabaseDSN)
	}
	if cfg.Log.Level != "info" || cfg.Log.Format != "text" {
		t.Fatalf("Log = %#v, want info/text", cfg.Log)
	}
}

func TestLoadAppliesEnvironmentOverrides(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("AUTH_KEY", "test-auth-key")
	t.Setenv("HOST", "127.0.0.1")
	t.Setenv("PORT", "4010")
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("DATABASE_DSN", ":memory:")
	t.Setenv("ENCRYPTION_KEY", "explicit-encryption-key")
	t.Setenv("REDIS_DSN", "redis://localhost:6379/2")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FORMAT", "json")
	t.Setenv("GRACEFUL_SHUTDOWN_TIMEOUT", "25")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Server.Host != "127.0.0.1" || cfg.Server.Port != 4010 {
		t.Fatalf("Server = %#v", cfg.Server)
	}
	if cfg.Server.GracefulShutdownTimeout != 25 {
		t.Fatalf("GracefulShutdownTimeout = %d", cfg.Server.GracefulShutdownTimeout)
	}
	if cfg.DatabaseDSN != ":memory:" || cfg.EncryptionKey != "explicit-encryption-key" {
		t.Fatalf("database/encryption overrides not loaded: %#v", cfg)
	}
	if cfg.RedisDSN != "redis://localhost:6379/2" {
		t.Fatalf("RedisDSN = %q", cfg.RedisDSN)
	}
	if cfg.Log.Level != "debug" || cfg.Log.Format != "json" {
		t.Fatalf("Log = %#v", cfg.Log)
	}
}

func TestLoadDerivesDatabaseDSNFromDataDir(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("AUTH_KEY", "test-auth-key")
	dataDir := t.TempDir()
	t.Setenv("DATA_DIR", dataDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if want := filepath.Join(dataDir, "gpt-load.db"); cfg.DatabaseDSN != want {
		t.Fatalf("DatabaseDSN = %q, want %q", cfg.DatabaseDSN, want)
	}
}

func TestLoadRejectsInvalidRequiredAndNumericValues(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{name: "missing auth key", env: map[string]string{}},
		{name: "invalid port", env: map[string]string{"AUTH_KEY": "x", "PORT": "nope"}},
		{name: "port out of range", env: map[string]string{"AUTH_KEY": "x", "PORT": "70000"}},
		{name: "invalid shutdown timeout", env: map[string]string{"AUTH_KEY": "x", "GRACEFUL_SHUTDOWN_TIMEOUT": "0"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnvironment(t)
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want error")
			}
		})
	}
}

func TestMergeSettingsUsesGroupOverridesWithoutMutatingInputs(t *testing.T) {
	system := Settings{"timeout": 30, "retry": 2}
	group := Settings{"timeout": 90}

	merged := MergeSettings(system, group)

	if merged["timeout"] != 90 || merged["retry"] != 2 {
		t.Fatalf("MergeSettings() = %#v", merged)
	}
	merged["retry"] = 9
	if system["retry"] != 2 {
		t.Fatalf("system settings mutated: %#v", system)
	}
}

func clearEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"HOST", "PORT", "DATA_DIR", "DATABASE_DSN", "ENCRYPTION_KEY", "AUTH_KEY",
		"REDIS_DSN", "LOG_LEVEL", "LOG_FORMAT", "GRACEFUL_SHUTDOWN_TIMEOUT",
	} {
		t.Setenv(key, "")
	}
}
