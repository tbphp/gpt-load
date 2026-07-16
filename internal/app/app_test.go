package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/version"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/store"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func TestNewEngineRecoversWithoutLoggingCredentials(t *testing.T) {
	previousMode := gin.Mode()
	gin.SetMode(gin.DebugMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })

	var logs bytes.Buffer
	previousGinErrorWriter := gin.DefaultErrorWriter
	previousLogOutput := logrus.StandardLogger().Out
	gin.DefaultErrorWriter = &logs
	logrus.SetOutput(&logs)
	t.Cleanup(func() {
		gin.DefaultErrorWriter = previousGinErrorWriter
		logrus.SetOutput(previousLogOutput)
	})

	engine := NewEngine()
	engine.GET("/panic", func(*gin.Context) {
		panic("panic-secret")
	})

	request := httptest.NewRequest(http.MethodGet, "/panic?token=query-secret", nil)
	request.Header.Set("Authorization", "Bearer authorization-secret")
	request.Header.Set("X-Api-Key", "x-api-key-secret")
	request.Header.Set("Api-Key", "api-key-secret")
	request.Header.Set("X-Goog-Api-Key", "google-api-key-secret")
	recorder := httptest.NewRecorder()

	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("panic status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode panic response: %v; body = %q", err, recorder.Body.String())
	}
	if body.Code != app_errors.ErrInternalServer.Code || body.Message != app_errors.ErrInternalServer.Message {
		t.Fatalf("panic response = %#v", body)
	}
	if gin.Mode() != gin.ReleaseMode {
		t.Fatalf("gin mode = %q, want %q", gin.Mode(), gin.ReleaseMode)
	}

	for _, secret := range []string{
		"panic-secret",
		"query-secret",
		"authorization-secret",
		"x-api-key-secret",
		"api-key-secret",
		"google-api-key-secret",
	} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("recovery logs contain secret %q:\n%s", secret, logs.String())
		}
	}
}

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
	if application.httpServer.ReadTimeout != 2*time.Second {
		t.Fatalf("ReadTimeout = %s, want 2s", application.httpServer.ReadTimeout)
	}
	if application.httpServer.ReadHeaderTimeout != 30*time.Second {
		t.Fatalf("ReadHeaderTimeout = %s, want 30s", application.httpServer.ReadHeaderTimeout)
	}
	if application.httpServer.IdleTimeout != 3*time.Second {
		t.Fatalf("IdleTimeout = %s, want 3s", application.httpServer.IdleTimeout)
	}
	if application.httpServer.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %s, want 0 for streaming responses", application.httpServer.WriteTimeout)
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

func TestAppReportsUnexpectedHTTPServeFailure(t *testing.T) {
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

	if err := application.listener.Close(); err != nil {
		t.Fatalf("close live listener: %v", err)
	}
	select {
	case err := <-application.ServeErrors():
		if err == nil || !strings.Contains(err.Error(), "serve HTTP") {
			t.Fatalf("ServeErrors() = %v, want wrapped HTTP Serve failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("unexpected HTTP Serve failure was not propagated")
	}
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Server: config.ServerConfig{
			Host:                    "127.0.0.1",
			Port:                    0,
			GracefulShutdownTimeout: 1,
			ReadTimeout:             2,
			IdleTimeout:             3,
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
