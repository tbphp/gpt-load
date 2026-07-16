// Package config loads the static M0 process configuration and provides the
// merge primitive used by later control-plane settings.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/joho/godotenv"
)

const (
	defaultHost                    = "0.0.0.0"
	defaultPort                    = 3001
	defaultDataDir                 = "./data"
	defaultGracefulShutdownSeconds = 10
	defaultReadTimeoutSeconds      = 60
	defaultIdleTimeoutSeconds      = 120
)

// ServerConfig contains the process-level HTTP server settings needed in M0.
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

// Config contains the static environment configuration used by the M0
// application skeleton.
type Config struct {
	Server        ServerConfig
	DataDir       string
	DatabaseDSN   string
	EncryptionKey string
	AuthKey       string
	RedisDSN      string
	Log           LogConfig
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

	authKey := os.Getenv("AUTH_KEY")
	if authKey == "" {
		return nil, fmt.Errorf("AUTH_KEY is required")
	}

	dataDir := valueOrDefault("DATA_DIR", defaultDataDir)
	databaseDSN := os.Getenv("DATABASE_DSN")
	if databaseDSN == "" {
		databaseDSN = filepath.Join(dataDir, "gpt-load.db")
	}

	logFormat := valueOrDefault("LOG_FORMAT", "text")
	if logFormat != "text" && logFormat != "json" {
		return nil, fmt.Errorf("LOG_FORMAT must be text or json")
	}

	return &Config{
		Server: ServerConfig{
			Host:                    valueOrDefault("HOST", defaultHost),
			Port:                    port,
			GracefulShutdownTimeout: shutdownTimeout,
			ReadTimeout:             readTimeout,
			IdleTimeout:             idleTimeout,
		},
		DataDir:       dataDir,
		DatabaseDSN:   databaseDSN,
		EncryptionKey: os.Getenv("ENCRYPTION_KEY"),
		AuthKey:       authKey,
		RedisDSN:      os.Getenv("REDIS_DSN"),
		Log: LogConfig{
			Level:  valueOrDefault("LOG_LEVEL", "info"),
			Format: logFormat,
		},
	}, nil
}

// MergeSettings combines the DB-backed system layer with the Group override
// layer. The returned map is independent from both inputs.
func MergeSettings(system, group Settings) Settings {
	merged := make(Settings, len(system)+len(group))
	for key, value := range system {
		merged[key] = cloneSettingValue(value)
	}
	for key, value := range group {
		merged[key] = cloneSettingValue(value)
	}
	return merged
}

func cloneSettingValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, nested := range typed {
			cloned[key] = cloneSettingValue(nested)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for index, nested := range typed {
			cloned[index] = cloneSettingValue(nested)
		}
		return cloned
	default:
		return value
	}
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
