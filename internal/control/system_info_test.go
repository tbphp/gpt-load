package control

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/version"
)

func TestSystemInfoResponseContainsOnlySafeMetadata(t *testing.T) {
	cfg := &config.Config{
		DataDir:     "./safe-data",
		DatabaseDSN: "file:distinctive-secret-dsn",
		DatabaseMetadata: config.DatabaseMetadata{
			Source: config.DatabaseSourceExternal,
		},
		AuthKey:       "distinctive-auth-secret",
		EncryptionKey: "distinctive-encryption-secret",
		AuthKeyMetadata: config.SecretMetadata{
			Source: config.SecretSourceKeyFile,
			Path:   "safe-data/auth.key",
		},
		EncryptionKeyMetadata: config.SecretMetadata{
			Source: config.SecretSourceEnvironment,
		},
	}

	encoded, err := json.Marshal(newSystemInfoResponse(cfg))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, forbidden := range []string{
		"distinctive-auth-secret",
		"distinctive-encryption-secret",
		"distinctive-secret-dsn",
		"external",
	} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("response exposed %q: %s", forbidden, encoded)
		}
	}

	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	want := map[string]any{
		"version": version.Version,
		"deployment": map[string]any{
			"instance_mode": "single",
			"database":      "sqlite",
			"distribution":  "single_binary",
		},
		"data_dir": "./safe-data",
		"auth_key": map[string]any{
			"source": "key_file",
			"path":   "safe-data/auth.key",
		},
		"encryption": map[string]any{
			"enabled": true,
			"source":  "environment",
			"path":    nil,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("system info = %#v, want %#v", got, want)
	}
}

func TestSystemInfoResponseUsesNullPathsForEnvironmentSources(t *testing.T) {
	encoded, err := json.Marshal(newSystemInfoResponse(&config.Config{
		AuthKeyMetadata: config.SecretMetadata{
			Source: config.SecretSourceEnvironment,
		},
		EncryptionKeyMetadata: config.SecretMetadata{
			Source: config.SecretSourceEnvironment,
		},
	}))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got struct {
		AuthKey struct {
			Path *string `json:"path"`
		} `json:"auth_key"`
		Encryption struct {
			Path *string `json:"path"`
		} `json:"encryption"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got.AuthKey.Path != nil || got.Encryption.Path != nil {
		t.Fatalf("environment paths = %#v/%#v, want nil/nil", got.AuthKey.Path, got.Encryption.Path)
	}
}

func TestSystemInfoResponseReportsSelectedDatabaseDriver(t *testing.T) {
	for _, test := range []struct {
		name   string
		driver config.DatabaseDriver
		want   string
	}{
		{name: "sqlite", driver: config.DatabaseDriverSQLite, want: "sqlite"},
		{name: "mysql", driver: config.DatabaseDriverMySQL, want: "mysql"},
		{name: "postgres", driver: config.DatabaseDriverPostgreSQL, want: "postgres"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := newSystemInfoResponse(&config.Config{
				DatabaseMetadata: config.DatabaseMetadata{Driver: test.driver},
			})
			if response.Deployment.Database != test.want {
				t.Fatalf("database = %q, want %q", response.Deployment.Database, test.want)
			}
		})
	}
}
