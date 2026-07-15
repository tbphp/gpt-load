package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/platform/version"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/store"
)

func TestNewEngineServesHealth(t *testing.T) {
	engine := NewEngine()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)

	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /health status = %d, want 200", recorder.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if body["status"] != "ok" || body["version"] != version.Version {
		t.Fatalf("health response = %#v", body)
	}
}

func TestAppStartMigratesDatabaseAndServesHTTP(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	keyService, err := encryption.NewService("test-master-key")
	if err != nil {
		t.Fatalf("encryption.NewService() error = %v", err)
	}

	application := NewApp(AppParams{
		Engine:     NewEngine(),
		Config:     testConfig(t),
		Encryption: keyService,
		Store:      store.NewMemoryStore(),
		DB:         db,
	})
	if err := application.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := application.Stop(ctx); err != nil {
			t.Errorf("Stop() error = %v", err)
		}
	})

	for _, table := range []string{
		"groups", "upstream_keys", "access_keys", "request_logs", "usage_stats",
		"model_prices", "system_settings", "jobs", "schema_info",
	} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("Start() did not migrate table %q", table)
		}
	}

	response, err := http.Get("http://" + application.Address() + "/health")
	if err != nil {
		t.Fatalf("GET running /health: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("running /health status = %d", response.StatusCode)
	}
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Server: config.ServerConfig{
			Host:                    "127.0.0.1",
			Port:                    0,
			GracefulShutdownTimeout: 1,
		},
		DataDir:       t.TempDir(),
		DatabaseDSN:   ":memory:",
		EncryptionKey: "test-master-key",
		AuthKey:       "test-auth-key",
		Log: config.LogConfig{
			Level:  "info",
			Format: "text",
		},
	}
}
