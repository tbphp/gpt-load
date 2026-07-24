// Package config loads static process configuration and defines shared dynamic setting shapes.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gpt-load/internal/platform/authkey"

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

// Config contains static environment configuration for the application process.
type Config struct {
	Server                ServerConfig
	DataDir               string
	DatabaseDSN           string
	EncryptionKey         string
	AuthKey               string
	AuthKeyMetadata       SecretMetadata
	EncryptionKeyMetadata SecretMetadata
	Log                   LogConfig
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
		encryptionKeyMetadata = SecretMetadata{
			Source: SecretSourceKeyFile,
			Path:   filepath.Join(dataDir, "encryption.key"),
		}
	}

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
		DataDir:               dataDir,
		DatabaseDSN:           databaseDSN,
		EncryptionKey:         explicitEncryptionKey,
		AuthKey:               authKey,
		AuthKeyMetadata:       authKeyMetadata,
		EncryptionKeyMetadata: encryptionKeyMetadata,
		Log: LogConfig{
			Level:  valueOrDefault("LOG_LEVEL", "info"),
			Format: logFormat,
		},
	}, nil
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
