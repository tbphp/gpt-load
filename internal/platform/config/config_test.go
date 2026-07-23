package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gpt-load/internal/platform/authkey"
)

func TestLoadUsesDefaultConfiguration(t *testing.T) {
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
	if cfg.Server.ReadTimeout != 60 {
		t.Fatalf("ReadTimeout = %d, want 60", cfg.Server.ReadTimeout)
	}
	if cfg.Server.IdleTimeout != 120 {
		t.Fatalf("IdleTimeout = %d, want 120", cfg.Server.IdleTimeout)
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
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FORMAT", "json")
	t.Setenv("GRACEFUL_SHUTDOWN_TIMEOUT", "25")
	t.Setenv("READ_TIMEOUT", "45")
	t.Setenv("IDLE_TIMEOUT", "90")

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
	if cfg.Server.ReadTimeout != 45 || cfg.Server.IdleTimeout != 90 {
		t.Fatalf("read/idle timeouts not loaded: %#v", cfg.Server)
	}
	if cfg.DatabaseDSN != ":memory:" || cfg.EncryptionKey != "explicit-encryption-key" {
		t.Fatalf("database/encryption overrides not loaded: %#v", cfg)
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

func TestLoadGeneratesAuthKeyInsideConfiguredDataDir(t *testing.T) {
	clearEnvironment(t)
	dataDir := t.TempDir()
	t.Setenv("DATA_DIR", dataDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.AuthKey) != 64 {
		t.Fatalf("AuthKey length = %d, want 64", len(cfg.AuthKey))
	}
	contents, err := os.ReadFile(filepath.Join(dataDir, authkey.FileName))
	if err != nil {
		t.Fatalf("read auth.key: %v", err)
	}
	if strings.TrimSpace(string(contents)) != cfg.AuthKey {
		t.Fatal("Config.AuthKey does not match generated auth.key")
	}
}

func TestLoadExplicitAuthKeyDoesNotCreateFile(t *testing.T) {
	clearEnvironment(t)
	dataDir := t.TempDir()
	t.Setenv("DATA_DIR", dataDir)
	t.Setenv("AUTH_KEY", "explicit-auth-key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.AuthKey != "explicit-auth-key" {
		t.Fatalf("AuthKey = %q", cfg.AuthKey)
	}
	if _, err := os.Stat(filepath.Join(dataDir, authkey.FileName)); !os.IsNotExist(err) {
		t.Fatalf("explicit AUTH_KEY created auth.key: %v", err)
	}
}

func TestLoadRejectsInvalidRequiredAndNumericValues(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{name: "whitespace-only auth key", env: map[string]string{"AUTH_KEY": "   "}},
		{name: "auth key with internal space", env: map[string]string{"AUTH_KEY": "admin key"}},
		{name: "auth key with tab", env: map[string]string{"AUTH_KEY": "admin\tkey"}},
		{name: "auth key with unicode whitespace", env: map[string]string{"AUTH_KEY": "admin\u00a0key"}},
		{name: "invalid port", env: map[string]string{"AUTH_KEY": "x", "PORT": "nope"}},
		{name: "port out of range", env: map[string]string{"AUTH_KEY": "x", "PORT": "70000"}},
		{name: "invalid shutdown timeout", env: map[string]string{"AUTH_KEY": "x", "GRACEFUL_SHUTDOWN_TIMEOUT": "0"}},
		{name: "invalid read timeout", env: map[string]string{"AUTH_KEY": "x", "READ_TIMEOUT": "0"}},
		{name: "invalid idle timeout", env: map[string]string{"AUTH_KEY": "x", "IDLE_TIMEOUT": "nope"}},
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

func TestMergeSettingsDeepCopiesNestedJSONValues(t *testing.T) {
	systemRule := map[string]any{"name": "system"}
	systemRules := []any{systemRule}
	groupFilters := []any{"group-a", "group-b"}
	system := Settings{"headers": map[string]any{"rules": systemRules}}
	group := Settings{"filters": groupFilters}

	merged := MergeSettings(system, group)
	mergedRule := merged["headers"].(map[string]any)["rules"].([]any)[0].(map[string]any)
	mergedFilters := merged["filters"].([]any)

	systemRule["name"] = "input-mutated"
	groupFilters[0] = "input-mutated"
	if mergedRule["name"] != "system" || mergedFilters[0] != "group-a" {
		t.Fatalf("merged settings changed with inputs: %#v", merged)
	}

	mergedRule["name"] = "output-mutated"
	mergedFilters[0] = "output-mutated"
	if systemRule["name"] != "input-mutated" || groupFilters[0] != "input-mutated" {
		t.Fatalf("input settings changed with merged output: system=%#v group=%#v", system, group)
	}
}

func clearEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"HOST", "PORT", "DATA_DIR", "DATABASE_DSN", "ENCRYPTION_KEY", "AUTH_KEY",
		"LOG_LEVEL", "LOG_FORMAT", "GRACEFUL_SHUTDOWN_TIMEOUT",
		"READ_TIMEOUT", "IDLE_TIMEOUT",
	} {
		t.Setenv(key, "")
	}
}
