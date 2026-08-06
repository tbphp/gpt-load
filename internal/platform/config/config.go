// Package config loads static process configuration and defines shared dynamic setting shapes.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"

	"gpt-load/internal/platform/authkey"
	"gpt-load/internal/platform/securefile"
)

const (
	defaultHost                    = "127.0.0.1"
	defaultPort                    = 3001
	defaultDataDir                 = "./data"
	defaultGracefulShutdownSeconds = 10
	defaultReadTimeoutSeconds      = 60
	defaultIdleTimeoutSeconds      = 120
)

// ServerConfig contains process-level HTTP server settings.
type ServerConfig struct {
	Host                    string
	Port                    int
	GracefulShutdownTimeout int
	ReadTimeout             int
	IdleTimeout             int
}

// LogConfig contains process-wide logger settings.
type LogConfig struct {
	Level  string
	Format string
}

// SecretSource identifies the non-secret source of process key material.
type SecretSource string

const (
	SecretSourceEnvironment SecretSource = "environment"
	SecretSourceKeyFile     SecretSource = "key_file"
)

// SecretMetadata describes where process key material was sourced without
// retaining the key material itself.
type SecretMetadata struct {
	Source SecretSource
	Path   string
}

// DatabaseSource identifies whether the operator selected the database
// location or the application supplied its managed default.
type DatabaseSource string

const (
	DatabaseSourceManaged  DatabaseSource = "managed"
	DatabaseSourceExternal DatabaseSource = "external"
)

// DatabaseDriver identifies the database driver selected by DATABASE_DSN.
// The values are stable because they are also consumed by the management API.
type DatabaseDriver string

const (
	DatabaseDriverSQLite     DatabaseDriver = "sqlite"
	DatabaseDriverMySQL      DatabaseDriver = "mysql"
	DatabaseDriverPostgreSQL DatabaseDriver = "postgres"
	DatabaseDriverPostgres                  = DatabaseDriverPostgreSQL
)

// DatabaseConfig is the normalized database connection target. DSN contains
// a driver-ready DSN; SQLite URLs are normalized to the native SQLite DSN
// while network database URLs remain URLs until storage opens them.
type DatabaseConfig struct {
	Driver DatabaseDriver
	DSN    string
}

// DatabaseMetadata describes database ownership without retaining its DSN or
// path.
type DatabaseMetadata struct {
	Source DatabaseSource
	Driver DatabaseDriver
}

// Config contains static environment configuration for the application process.
type Config struct {
	Server                    ServerConfig
	DataDir                   string
	DatabaseDSN               string
	DatabaseMetadata          DatabaseMetadata
	EncryptionKey             string
	AuthKey                   string
	AuthKeyMetadata           SecretMetadata
	EncryptionKeyMetadata     SecretMetadata
	Log                       LogConfig
	ModelsDevAutoSyncOverride *bool
}

// Settings is the dynamic settings shape shared by system and group layers.
// Values use the standard JSON-decoded representation: map[string]any, []any,
// and scalar values. Concrete fields are introduced with their consumers.
type Settings = map[string]any

// Load reads process configuration from the environment. A local .env file is
// loaded when present, but existing environment variables always win.
func Load() (*Config, error) {
	_ = godotenv.Load()

	port, err := parsePositiveInt("PORT", defaultPort)
	if err != nil {
		return nil, err
	}
	if port > 65535 {
		return nil, fmt.Errorf("PORT must be between 1 and 65535")
	}

	shutdownTimeout, err := parsePositiveInt("GRACEFUL_SHUTDOWN_TIMEOUT", defaultGracefulShutdownSeconds)
	if err != nil {
		return nil, err
	}
	readTimeout, err := parsePositiveInt("READ_TIMEOUT", defaultReadTimeoutSeconds)
	if err != nil {
		return nil, err
	}
	idleTimeout, err := parsePositiveInt("IDLE_TIMEOUT", defaultIdleTimeoutSeconds)
	if err != nil {
		return nil, err
	}

	dataDir := valueOrDefault("DATA_DIR", defaultDataDir)
	rawDatabaseDSN := strings.TrimSpace(os.Getenv("DATABASE_DSN"))
	databaseSource := DatabaseSourceExternal
	databaseDSN := rawDatabaseDSN
	if rawDatabaseDSN == "" {
		databaseSource = DatabaseSourceManaged
		if err := securefile.PrepareManagedDataDir(dataDir); err != nil {
			return nil, err
		}
		databaseDSN = filepath.Join(dataDir, "gpt-load.db")
	}
	database, err := ParseDatabaseDSN(databaseDSN)
	if err != nil {
		return nil, err
	}
	databaseDSN = database.DSN
	databaseMetadata := DatabaseMetadata{
		Source: databaseSource,
		Driver: database.Driver,
	}

	if databaseSource == DatabaseSourceManaged {
		// The empty-DATABASE_DSN path is the only application-managed database.
		// Keep this branch explicit so future driver additions cannot silently
		// inherit managed-file semantics.
		if database.Driver != DatabaseDriverSQLite {
			return nil, fmt.Errorf("managed database must use SQLite")
		}
	}
	if databaseSource == DatabaseSourceExternal && database.Driver == "" {
		return nil, fmt.Errorf("DATABASE_DSN did not select a database driver")
	}

	if databaseDSN == "" {
		return nil, fmt.Errorf("DATABASE_DSN resolved to an empty DSN")
	}

	explicitAuthKey := os.Getenv("AUTH_KEY")
	explicitEncryptionKey := os.Getenv("ENCRYPTION_KEY")
	authKey, err := authkey.Resolve(explicitAuthKey, dataDir)
	if err != nil {
		return nil, err
	}

	authKeyMetadata := SecretMetadata{Source: SecretSourceEnvironment}
	if explicitAuthKey == "" {
		authKeyMetadata = SecretMetadata{
			Source: SecretSourceKeyFile,
			Path:   filepath.Join(dataDir, authkey.FileName),
		}
	}
	encryptionKeyMetadata := SecretMetadata{Source: SecretSourceEnvironment}
	if explicitEncryptionKey == "" {
		// Keep this filename in sync with encryption.KeyFileName. Importing the
		// encryption implementation here would violate runtime-domain boundaries.
		encryptionKeyMetadata = SecretMetadata{
			Source: SecretSourceKeyFile,
			Path:   filepath.Join(dataDir, "encryption.key"),
		}
	}

	logFormat := valueOrDefault("LOG_FORMAT", "text")
	if logFormat != "text" && logFormat != "json" {
		return nil, fmt.Errorf("LOG_FORMAT must be text or json")
	}
	modelsDevAutoSyncOverride, err := parseOptionalBool("MODELS_DEV_AUTO_SYNC_ENABLED")
	if err != nil {
		return nil, err
	}

	return &Config{
		Server: ServerConfig{
			Host:                    valueOrDefault("HOST", defaultHost),
			Port:                    port,
			GracefulShutdownTimeout: shutdownTimeout,
			ReadTimeout:             readTimeout,
			IdleTimeout:             idleTimeout,
		},
		DataDir:               dataDir,
		DatabaseDSN:           databaseDSN,
		DatabaseMetadata:      databaseMetadata,
		EncryptionKey:         explicitEncryptionKey,
		AuthKey:               authKey,
		AuthKeyMetadata:       authKeyMetadata,
		EncryptionKeyMetadata: encryptionKeyMetadata,
		Log: LogConfig{
			Level:  valueOrDefault("LOG_LEVEL", "info"),
			Format: logFormat,
		},
		ModelsDevAutoSyncOverride: modelsDevAutoSyncOverride,
	}, nil
}

// ParseDatabaseDSN parses the single DATABASE_DSN configuration format. Bare
// paths and :memory: remain SQLite compatibility forms; network databases must
// use a URL with a supported scheme.
func ParseDatabaseDSN(rawDSN string) (DatabaseConfig, error) {
	dsn := strings.TrimSpace(rawDSN)
	if dsn == "" {
		return DatabaseConfig{}, fmt.Errorf("DATABASE_DSN must not be empty")
	}
	baseDSN, _, _ := strings.Cut(dsn, "?")
	if baseDSN == ":memory:" {
		return DatabaseConfig{Driver: DatabaseDriverSQLite, DSN: dsn}, nil
	}
	if filepath.VolumeName(dsn) != "" {
		return DatabaseConfig{Driver: DatabaseDriverSQLite, DSN: dsn}, nil
	}

	parsed, err := url.Parse(dsn)
	if err != nil {
		return DatabaseConfig{}, fmt.Errorf("DATABASE_DSN is invalid")
	}
	if parsed.Scheme == "" {
		return DatabaseConfig{Driver: DatabaseDriverSQLite, DSN: dsn}, nil
	}

	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "file":
		if !strings.HasPrefix(dsn, "file:") {
			return DatabaseConfig{}, fmt.Errorf("DATABASE_DSN uses an unsupported SQLite URI")
		}
		return DatabaseConfig{Driver: DatabaseDriverSQLite, DSN: dsn}, nil
	case "sqlite":
		normalizedDSN, err := normalizeSQLiteURL(parsed)
		if err != nil {
			return DatabaseConfig{}, err
		}
		return DatabaseConfig{Driver: DatabaseDriverSQLite, DSN: normalizedDSN}, nil
	case "mysql":
		if err := validateNetworkDatabaseURL(parsed, DatabaseDriverMySQL); err != nil {
			return DatabaseConfig{}, err
		}
		return DatabaseConfig{Driver: DatabaseDriverMySQL, DSN: dsn}, nil
	case "postgres", "postgresql":
		if err := validateNetworkDatabaseURL(parsed, DatabaseDriverPostgreSQL); err != nil {
			return DatabaseConfig{}, err
		}
		return DatabaseConfig{Driver: DatabaseDriverPostgreSQL, DSN: dsn}, nil
	default:
		return DatabaseConfig{}, fmt.Errorf("DATABASE_DSN uses unsupported database scheme")
	}
}

func normalizeSQLiteURL(parsed *url.URL) (string, error) {
	if parsed.User != nil || parsed.Fragment != "" {
		return "", fmt.Errorf("DATABASE_DSN uses an invalid SQLite URL")
	}

	databasePath := parsed.Opaque
	if databasePath == "" {
		databasePath = parsed.Path
		switch parsed.Host {
		case "":
		case ".":
			databasePath = "." + databasePath
		case "localhost":
		default:
			databasePath = parsed.Host + databasePath
		}
	}
	if databasePath == "/:memory:" {
		databasePath = ":memory:"
	}
	if databasePath == "" {
		return "", fmt.Errorf("DATABASE_DSN SQLite URL must include a database path")
	}
	if parsed.RawQuery != "" {
		if _, err := url.ParseQuery(parsed.RawQuery); err != nil {
			return "", fmt.Errorf("DATABASE_DSN has an invalid SQLite query")
		}
		databasePath += "?" + parsed.RawQuery
	}
	return databasePath, nil
}

func validateNetworkDatabaseURL(parsed *url.URL, driver DatabaseDriver) error {
	if parsed.Hostname() == "" {
		return fmt.Errorf("DATABASE_DSN %s URL must include a host", driver)
	}
	databaseName := strings.TrimPrefix(parsed.Path, "/")
	if databaseName == "" || strings.Contains(databaseName, "/") {
		return fmt.Errorf("DATABASE_DSN %s URL must include one database name", driver)
	}
	if parsed.Fragment != "" {
		return fmt.Errorf("DATABASE_DSN %s URL must not include a fragment", driver)
	}
	if _, err := url.ParseQuery(parsed.RawQuery); err != nil {
		return fmt.Errorf("DATABASE_DSN has an invalid %s query", driver)
	}
	return nil
}

func parseOptionalBool(key string) (*bool, error) {
	value := os.Getenv(key)
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return nil, fmt.Errorf("%s must be a boolean", key)
	}
	return &parsed, nil
}

func parsePositiveInt(key string, defaultValue int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return parsed, nil
}

func valueOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
