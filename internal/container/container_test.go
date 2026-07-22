package container

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gpt-load/internal/app"
	"gpt-load/internal/control"
	"gpt-load/internal/dialect"
	"gpt-load/internal/gateway"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/i18n"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage"
)

func TestBuildContainerDoesNotInitializeUnusedRuntimeStore(t *testing.T) {
	t.Setenv("AUTH_KEY", "test-auth-key")
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("DATABASE_DSN", ":memory:")
	t.Setenv("ENCRYPTION_KEY", "test-master-key-long")
	t.Setenv("REDIS_DSN", "://invalid-redis-dsn")

	dependencyContainer, err := BuildContainer()
	if err != nil {
		t.Fatalf("BuildContainer() must ignore REDIS_DSN, error = %v", err)
	}
	err = dependencyContainer.Invoke(func(_ *app.App, db *gorm.DB) {
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			t.Cleanup(func() { _ = sqlDB.Close() })
		}
	})
	if err != nil {
		t.Fatalf("resolve database: %v", err)
	}
}

func TestBuildContainerResolvesAllDialects(t *testing.T) {
	t.Setenv("AUTH_KEY", "test-auth-key")
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("DATABASE_DSN", ":memory:")
	t.Setenv("ENCRYPTION_KEY", "test-master-key-long")

	dependencyContainer, err := BuildContainer()
	if err != nil {
		t.Fatalf("BuildContainer() error = %v", err)
	}
	err = dependencyContainer.Invoke(func(
		openAI *dialect.OpenAI,
		anthropic *dialect.Anthropic,
		gemini *dialect.Gemini,
		values dialect.Set,
		db *gorm.DB,
	) {
		t.Cleanup(func() {
			sqlDB, dbErr := db.DB()
			if dbErr == nil {
				_ = sqlDB.Close()
			}
		})
		if values[protocol.OpenAI] != openAI ||
			values[protocol.Anthropic] != anthropic ||
			values[protocol.Gemini] != gemini || len(values) != 3 {
			t.Fatalf("dialect Set = %#v", values)
		}
	})
	if err != nil {
		t.Fatalf("resolve dialects: %v", err)
	}
}

func TestBuildContainerResolvesRuntimeDependencies(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AUTH_KEY", "test-auth-key")
	t.Setenv("DATA_DIR", dataDir)
	t.Setenv("DATABASE_DSN", "")
	t.Setenv("ENCRYPTION_KEY", "")

	dependencyContainer, err := BuildContainer()
	if err != nil {
		t.Fatalf("BuildContainer() error = %v", err)
	}

	var resolved bool
	err = dependencyContainer.Invoke(func(
		_ *app.App,
		cfg *config.Config,
		_ encryption.Service,
		db *gorm.DB,
		_ *gin.Engine,
		manager *state.Manager,
		registry *state.KeyRegistry,
		runtimeState app.RuntimeStateLoader,
		gatewayHandler *gateway.Handler,
		attemptForwarder gateway.AttemptForwarder,
		_ dialect.Set,
		_ *control.Service,
		_ *control.Server,
		statsStore *health.StatsStore,
		runtime *control.Runtime,
		_ app.ControlRuntime,
		_ *httpclient.HTTPClientManager,
		_ *redact.Redactor,
		_ *dialect.OpenAI,
	) {
		t.Cleanup(func() {
			sqlDB, dbErr := db.DB()
			if dbErr == nil {
				_ = sqlDB.Close()
			}
		})
		if err := storage.AutoMigrate(db); err != nil {
			t.Fatalf("AutoMigrate() error = %v", err)
		}
		if err := runtimeState.Load(context.Background()); err != nil {
			t.Fatalf("runtimeState.Load() error = %v", err)
		}
		snapshot := manager.Current()
		if snapshot == nil || snapshot.Revision != 1 {
			t.Fatalf("current snapshot = %#v, want revision 1", snapshot)
		}
		if got := registry.CollectCandidates(nil, nil, time.Time{}); len(got) != 0 {
			t.Fatalf("empty registry candidates = %#v", got)
		}
		if attemptForwarder == nil {
			t.Fatal("stream-capable attempt forwarder was not resolved")
		}
		if gatewayHandler == nil || runtime == nil || statsStore == nil {
			t.Fatalf("runtime dependencies were not resolved: gateway=%p runtime=%p stats=%p", gatewayHandler, runtime, statsStore)
		}
		if want := filepath.Join(dataDir, "gpt-load.db"); cfg.DatabaseDSN != want {
			t.Fatalf("DatabaseDSN = %q, want %q", cfg.DatabaseDSN, want)
		}
		for _, name := range []string{"gpt-load.db", encryption.KeyFileName} {
			if _, err := os.Stat(filepath.Join(dataDir, name)); err != nil {
				t.Fatalf("%s was not created in DATA_DIR: %v", name, err)
			}
		}
		resolved = true
	})
	if err != nil {
		t.Fatalf("resolve runtime dependency graph: %v", err)
	}
	if !resolved {
		t.Fatal("runtime dependency graph was not invoked")
	}
}

func TestBuildContainerUsesSingletonStatsStore(t *testing.T) {
	t.Setenv("AUTH_KEY", "test-auth-key")
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("DATABASE_DSN", ":memory:")
	t.Setenv("ENCRYPTION_KEY", "test-master-key-long")

	dependencyContainer, err := BuildContainer()
	if err != nil {
		t.Fatalf("BuildContainer() error = %v", err)
	}

	var first *health.StatsStore
	err = dependencyContainer.Invoke(func(store *health.StatsStore, db *gorm.DB) {
		first = store
		t.Cleanup(func() {
			sqlDB, dbErr := db.DB()
			if dbErr == nil {
				_ = sqlDB.Close()
			}
		})
	})
	if err != nil {
		t.Fatalf("resolve first StatsStore: %v", err)
	}

	var second *health.StatsStore
	if err := dependencyContainer.Invoke(func(store *health.StatsStore) { second = store }); err != nil {
		t.Fatalf("resolve second StatsStore: %v", err)
	}
	if first != second {
		t.Fatalf("StatsStore instances differ: first=%p second=%p", first, second)
	}
}

func TestBuildContainerRegistersControlRoutesWithoutAffectingGateway(t *testing.T) {
	t.Setenv("AUTH_KEY", "test-auth-key")
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("DATABASE_DSN", ":memory:")
	t.Setenv("ENCRYPTION_KEY", "test-master-key-long")
	if err := i18n.Init(); err != nil {
		t.Fatalf("i18n.Init() error = %v", err)
	}

	dependencyContainer, err := BuildContainer()
	if err != nil {
		t.Fatalf("BuildContainer() error = %v", err)
	}
	err = dependencyContainer.Invoke(func(
		engine *gin.Engine,
		db *gorm.DB,
		runtimeState app.RuntimeStateLoader,
	) {
		if err := storage.AutoMigrate(db); err != nil {
			t.Fatalf("AutoMigrate() error = %v", err)
		}
		if err := runtimeState.Load(context.Background()); err != nil {
			t.Fatalf("runtimeState.Load() error = %v", err)
		}

		groupsRecorder := httptest.NewRecorder()
		groupsRequest := httptest.NewRequest(http.MethodGet, "/api/groups", nil)
		groupsRequest.Header.Set("Authorization", "Bearer test-auth-key")
		engine.ServeHTTP(groupsRecorder, groupsRequest)
		if groupsRecorder.Code != http.StatusOK {
			t.Fatalf("groups status = %d, want 200; body=%s", groupsRecorder.Code, groupsRecorder.Body.String())
		}

		gatewayRecorder := httptest.NewRecorder()
		gatewayRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
		gatewayRequest.Header.Set("Authorization", "Bearer test-auth-key")
		engine.ServeHTTP(gatewayRecorder, gatewayRequest)
		if gatewayRecorder.Code != http.StatusUnauthorized || !strings.Contains(gatewayRecorder.Body.String(), "invalid_access_key") {
			t.Fatalf("gateway response = %d %s, want data-plane 401", gatewayRecorder.Code, gatewayRecorder.Body.String())
		}

		unknownRecorder := httptest.NewRecorder()
		unknownRequest := httptest.NewRequest(http.MethodGet, "/api/unknown", nil)
		unknownRequest.Header.Set("Authorization", "Bearer test-auth-key")
		engine.ServeHTTP(unknownRecorder, unknownRequest)
		if unknownRecorder.Code != http.StatusUnauthorized || !strings.Contains(unknownRecorder.Body.String(), "invalid_access_key") {
			t.Fatalf("unknown /api response = %d %s, want documented gateway NoRoute 401", unknownRecorder.Code, unknownRecorder.Body.String())
		}
	})
	if err != nil {
		t.Fatalf("resolve engine with control routes: %v", err)
	}
}

func TestBuildContainerRegistersGatewayRoute(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AUTH_KEY", "test-auth-key")
	t.Setenv("DATA_DIR", dataDir)
	t.Setenv("DATABASE_DSN", ":memory:")
	t.Setenv("ENCRYPTION_KEY", "test-master-key")

	dependencyContainer, err := BuildContainer()
	if err != nil {
		t.Fatalf("BuildContainer() error = %v", err)
	}
	err = dependencyContainer.Invoke(func(engine *gin.Engine) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("gateway status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
		}
		var body struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Code != "invalid_access_key" {
			t.Fatalf("gateway body = %s, error=%v", recorder.Body.String(), err)
		}
	})
	if err != nil {
		t.Fatalf("resolve engine: %v", err)
	}
}

func TestBuildContainerDoesNotRedirectTrailingSlashGatewayRoute(t *testing.T) {
	t.Setenv("AUTH_KEY", "test-auth-key")
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("DATABASE_DSN", ":memory:")
	t.Setenv("ENCRYPTION_KEY", "test-master-key-long")

	dependencyContainer, err := BuildContainer()
	if err != nil {
		t.Fatalf("BuildContainer() error = %v", err)
	}
	err = dependencyContainer.Invoke(func(
		engine *gin.Engine,
		manager *state.Manager,
		keyService encryption.Service,
	) {
		if _, err := manager.Publish(state.CompileInput{AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"),
			Status: state.AccessKeyStatusActive,
		}}}); err != nil {
			t.Fatalf("Publish() error = %v", err)
		}

		tests := []struct {
			name       string
			target     string
			wantStatus int
			wantCode   string
		}{
			{
				name:       "missing credential",
				target:     "/v1/chat/completions/",
				wantStatus: http.StatusUnauthorized,
				wantCode:   "invalid_access_key",
			},
			{
				name:       "valid query credential",
				target:     "/v1/chat/completions/?key=gl-client",
				wantStatus: http.StatusNotFound,
				wantCode:   "protocol_endpoint_not_found",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, tt.target, nil)
				engine.ServeHTTP(recorder, request)
				if recorder.Code != tt.wantStatus {
					t.Fatalf("gateway status = %d, want %d; location=%q body=%s",
						recorder.Code, tt.wantStatus, recorder.Header().Get("Location"), recorder.Body.String())
				}
				if location := recorder.Header().Get("Location"); location != "" {
					t.Fatalf("Location = %q, want empty", location)
				}
				var body struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Code != tt.wantCode {
					t.Fatalf("gateway body = %s, error=%v, want code %q", recorder.Body.String(), err, tt.wantCode)
				}
			})
		}
	})
	if err != nil {
		t.Fatalf("resolve engine: %v", err)
	}
}
