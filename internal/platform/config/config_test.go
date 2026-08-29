package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gpt-load/internal/platform/authkey"
	"gpt-load/internal/platform/encryption"
)

func TestLoadUsesDefaultConfiguration(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("AUTH_KEY", "test-auth-key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Server.Host != "127.0.0.1" {
		t.Fatalf("Host = %q, want 127.0.0.1", cfg.Server.Host)
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
	if cfg.DatabaseMetadata.Driver != DatabaseDriverSQLite {
		t.Fatalf("DatabaseMetadata.Driver = %q, want %q", cfg.DatabaseMetadata.Driver, DatabaseDriverSQLite)
	}
	if cfg.DatabasePool.MaxOpenConnections != 10 || cfg.DatabasePool.MaxIdleConnections != 5 {
		t.Fatalf("DatabasePool = %#v, want max open/idle 10/5", cfg.DatabasePool)
	}
	if cfg.Log.Level != "info" || cfg.Log.Format != "text" {
		t.Fatalf("Log = %#v, want info/text", cfg.Log)
	}
}

func TestLoadPreservesExplicitAllInterfacesHost(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("AUTH_KEY", "test-auth-key")
	t.Setenv("HOST", "0.0.0.0")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Server.Host != "0.0.0.0" {
		t.Fatalf("Host = %q, want explicit 0.0.0.0", cfg.Server.Host)
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
	t.Setenv("DATABASE_MAX_OPEN_CONNECTIONS", "24")
	t.Setenv("DATABASE_MAX_IDLE_CONNECTIONS", "12")

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
	if cfg.DatabaseMetadata.Driver != DatabaseDriverSQLite {
		t.Fatalf("DatabaseMetadata.Driver = %q, want %q", cfg.DatabaseMetadata.Driver, DatabaseDriverSQLite)
	}
	if cfg.DatabasePool.MaxOpenConnections != 24 || cfg.DatabasePool.MaxIdleConnections != 12 {
		t.Fatalf("DatabasePool = %#v, want max open/idle 24/12", cfg.DatabasePool)
	}
	if cfg.Log.Level != "debug" || cfg.Log.Format != "json" {
		t.Fatalf("Log = %#v", cfg.Log)
	}
}

func TestLoadModelsDevAutoSyncOverrideIsOptionalAndStrict(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   string
		want    *bool
		wantErr bool
	}{
		{name: "unset"},
		{name: "true", value: "true", want: boolPointer(true)},
		{name: "false", value: "false", want: boolPointer(false)},
		{name: "strconv true syntax", value: "1", want: boolPointer(true)},
		{name: "invalid", value: "enabled", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv("AUTH_KEY", "test-auth-key")
			t.Setenv("MODELS_DEV_AUTO_SYNC_ENABLED", test.value)

			cfg, err := Load()
			if test.wantErr {
				if err == nil {
					t.Fatal("Load() error = nil, want strict boolean error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if (cfg.ModelsDevAutoSyncOverride == nil) != (test.want == nil) {
				t.Fatalf("ModelsDevAutoSyncOverride = %#v, want %#v", cfg.ModelsDevAutoSyncOverride, test.want)
			}
			if test.want != nil && *cfg.ModelsDevAutoSyncOverride != *test.want {
				t.Fatalf("ModelsDevAutoSyncOverride = %t, want %t", *cfg.ModelsDevAutoSyncOverride, *test.want)
			}
		})
	}
}

func boolPointer(value bool) *bool {
	return &value
}

func TestLoadReportsEnvironmentSecretSources(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("AUTH_KEY", "explicit-auth")
	t.Setenv("ENCRYPTION_KEY", "explicit-encryption")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.AuthKeyMetadata.Source != SecretSourceEnvironment ||
		cfg.AuthKeyMetadata.Path != "" ||
		cfg.EncryptionKeyMetadata.Source != SecretSourceEnvironment ||
		cfg.EncryptionKeyMetadata.Path != "" {
		t.Fatalf("metadata = %#v/%#v", cfg.AuthKeyMetadata, cfg.EncryptionKeyMetadata)
	}
}

func TestLoadReportsKeyFileSecretSources(t *testing.T) {
	clearEnvironment(t)
	dataDir := t.TempDir()
	t.Setenv("DATA_DIR", dataDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.AuthKeyMetadata.Source != SecretSourceKeyFile ||
		cfg.AuthKeyMetadata.Path != filepath.Join(dataDir, authkey.FileName) ||
		cfg.EncryptionKeyMetadata.Source != SecretSourceKeyFile ||
		cfg.EncryptionKeyMetadata.Path != filepath.Join(dataDir, encryption.KeyFileName) {
		t.Fatalf("metadata = %#v/%#v", cfg.AuthKeyMetadata, cfg.EncryptionKeyMetadata)
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

func TestLoadDefaultsDatabaseSourceToManaged(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("AUTH_KEY", "test-auth-key")
	dataDir := t.TempDir()
	t.Setenv("DATA_DIR", dataDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DatabaseMetadata.Source != DatabaseSourceManaged {
		t.Fatalf("DatabaseMetadata.Source = %q, want %q", cfg.DatabaseMetadata.Source, DatabaseSourceManaged)
	}
}

func TestLoadClassifiesNonEmptyDatabaseDSNAsExternal(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("AUTH_KEY", "test-auth-key")
	t.Setenv("DATABASE_DSN", ":memory:")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DatabaseMetadata.Source != DatabaseSourceExternal {
		t.Fatalf("DatabaseMetadata.Source = %q, want %q", cfg.DatabaseMetadata.Source, DatabaseSourceExternal)
	}
}

func TestLoadPreparesDataDirForExternalDatabaseWithExplicitSecrets(t *testing.T) {
	clearEnvironment(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	t.Setenv("DATA_DIR", dataDir)
	t.Setenv("DATABASE_DSN", ":memory:")
	t.Setenv("AUTH_KEY", "explicit-auth-key")
	t.Setenv("ENCRYPTION_KEY", "explicit-encryption-key")

	if _, err := Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	info, err := os.Stat(dataDir)
	if err != nil {
		t.Fatalf("stat DATA_DIR: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("DATA_DIR mode = %v, want directory", info.Mode())
	}
}

func TestLoadRejectsUnsafeDataDirForExternalDatabaseWithExplicitSecrets(t *testing.T) {
	clearEnvironment(t)
	targetDir := t.TempDir()
	dataDirLink := filepath.Join(t.TempDir(), "unsafe-data-dir")
	if err := os.Symlink(targetDir, dataDirLink); err != nil {
		t.Skipf("create DATA_DIR symlink: %v", err)
	}
	t.Setenv("DATA_DIR", dataDirLink)
	t.Setenv("DATABASE_DSN", ":memory:")
	t.Setenv("AUTH_KEY", "explicit-auth-key")
	t.Setenv("ENCRYPTION_KEY", "explicit-encryption-key")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want unsafe DATA_DIR rejection")
	} else if !strings.Contains(err.Error(), "prepare DATA_DIR") {
		t.Fatalf("Load() error = %q, want DATA_DIR preparation context", err)
	}
}

func TestLoadExplicitDefaultDatabaseDSNRemainsExternal(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("AUTH_KEY", "test-auth-key")
	dataDir := t.TempDir()
	t.Setenv("DATA_DIR", dataDir)
	t.Setenv("DATABASE_DSN", filepath.Join(dataDir, "gpt-load.db"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DatabaseMetadata.Source != DatabaseSourceExternal {
		t.Fatalf("DatabaseMetadata.Source = %q, want %q", cfg.DatabaseMetadata.Source, DatabaseSourceExternal)
	}
}

func TestLoadClassifiesNetworkDatabaseURLs(t *testing.T) {
	for _, test := range []struct {
		name   string
		dsn    string
		driver DatabaseDriver
	}{
		{name: "mysql", dsn: "mysql://user:password@db.example:3306/gpt_load", driver: DatabaseDriverMySQL},
		{name: "postgres", dsn: "postgres://user:password@db.example:5432/gpt_load", driver: DatabaseDriverPostgreSQL},
		{name: "postgresql alias", dsn: "postgresql://user:password@db.example:5432/gpt_load", driver: DatabaseDriverPostgreSQL},
	} {
		t.Run(test.name, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv("AUTH_KEY", "test-auth-key")
			t.Setenv("ENCRYPTION_KEY", "test-encryption-key")
			t.Setenv("DATABASE_DSN", test.dsn)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.DatabaseMetadata.Source != DatabaseSourceExternal {
				t.Fatalf("DatabaseMetadata.Source = %q, want %q", cfg.DatabaseMetadata.Source, DatabaseSourceExternal)
			}
			if cfg.DatabaseMetadata.Driver != test.driver {
				t.Fatalf("DatabaseMetadata.Driver = %q, want %q", cfg.DatabaseMetadata.Driver, test.driver)
			}
			if cfg.DatabaseDSN != test.dsn {
				t.Fatalf("DatabaseDSN = %q, want %q", cfg.DatabaseDSN, test.dsn)
			}
		})
	}
}

func TestParseDatabaseDSNSupportsURLAndSQLiteCompatibilityForms(t *testing.T) {
	tests := []struct {
		name       string
		dsn        string
		wantDriver DatabaseDriver
		wantDSN    string
	}{
		{name: "bare path", dsn: "data/gpt-load.db", wantDriver: DatabaseDriverSQLite, wantDSN: "data/gpt-load.db"},
		{name: "memory", dsn: ":memory:?cache=shared", wantDriver: DatabaseDriverSQLite, wantDSN: ":memory:?cache=shared"},
		{name: "sqlite URL", dsn: "sqlite:///var/lib/gpt-load/gpt-load.db", wantDriver: DatabaseDriverSQLite, wantDSN: "/var/lib/gpt-load/gpt-load.db"},
		{name: "mysql URL", dsn: "mysql://user:password@db.example:3306/gpt_load?tls=true", wantDriver: DatabaseDriverMySQL, wantDSN: "mysql://user:password@db.example:3306/gpt_load?tls=true"},
		{name: "postgres URL", dsn: "postgres://user:password@db.example:5432/gpt_load?sslmode=require", wantDriver: DatabaseDriverPostgreSQL, wantDSN: "postgres://user:password@db.example:5432/gpt_load?sslmode=require"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseDatabaseDSN(test.dsn)
			if err != nil {
				t.Fatalf("ParseDatabaseDSN(%q) error = %v", test.dsn, err)
			}
			if got.Driver != test.wantDriver || got.DSN != test.wantDSN {
				t.Fatalf("ParseDatabaseDSN(%q) = %#v, want driver %q DSN %q", test.dsn, got, test.wantDriver, test.wantDSN)
			}
		})
	}
}

func TestParseDatabaseDSNRejectsUnsupportedOrIncompleteURLs(t *testing.T) {
	for _, dsn := range []string{
		"",
		"redis://localhost/0",
		"mysql://localhost",
		"postgres://localhost",
		"mysql://localhost/gpt_load/%2Fextra",
	} {
		if _, err := ParseDatabaseDSN(dsn); err == nil {
			t.Fatalf("ParseDatabaseDSN(%q) error = nil, want validation error", dsn)
		}
	}
}

func TestLoadManagedDatabaseRejectsUnsafeDataDirBeforeCreatingSecrets(t *testing.T) {
	tests := []struct {
		name   string
		suffix string
	}{
		{name: "symlink"},
		{name: "symlink with trailing separator", suffix: string(os.PathSeparator)},
		{name: "symlink with dot suffix", suffix: string(os.PathSeparator) + "."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnvironment(t)
			targetDir := t.TempDir()
			dataDirLink := filepath.Join(t.TempDir(), "sensitive-managed-data-dir")
			if err := os.Symlink(targetDir, dataDirLink); err != nil {
				t.Skipf("create DATA_DIR symlink: %v", err)
			}
			dataDir := dataDirLink + tt.suffix
			t.Setenv("DATA_DIR", dataDir)

			_, err := Load()
			if err == nil {
				t.Fatal("Load() error = nil, want unsafe managed DATA_DIR rejection")
			}
			if strings.Contains(err.Error(), dataDir) {
				t.Fatalf("Load() error exposes DATA_DIR: %v", err)
			}
			for _, fileName := range []string{authkey.FileName, encryption.KeyFileName} {
				if _, statErr := os.Stat(filepath.Join(targetDir, fileName)); !os.IsNotExist(statErr) {
					t.Fatalf("%s created before DATA_DIR validation: %v", fileName, statErr)
				}
			}
		})
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
		{name: "invalid database max open connections", env: map[string]string{"AUTH_KEY": "x", "DATABASE_MAX_OPEN_CONNECTIONS": "0"}},
		{name: "invalid database max idle connections", env: map[string]string{"AUTH_KEY": "x", "DATABASE_MAX_IDLE_CONNECTIONS": "nope"}},
		{name: "database max idle connections exceed max open", env: map[string]string{
			"AUTH_KEY": "x", "DATABASE_MAX_OPEN_CONNECTIONS": "4", "DATABASE_MAX_IDLE_CONNECTIONS": "5",
		}},
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

func clearEnvironment(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	for _, key := range []string{
		"HOST", "PORT", "DATA_DIR", "DATABASE_DSN", "ENCRYPTION_KEY", "AUTH_KEY",
		"LOG_LEVEL", "LOG_FORMAT", "GRACEFUL_SHUTDOWN_TIMEOUT",
		"READ_TIMEOUT", "IDLE_TIMEOUT", "MODELS_DEV_AUTO_SYNC_ENABLED",
		"DATABASE_MAX_OPEN_CONNECTIONS", "DATABASE_MAX_IDLE_CONNECTIONS",
	} {
		t.Setenv(key, "")
	}
}
