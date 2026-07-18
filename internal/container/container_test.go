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

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gpt-load/internal/app"
	"gpt-load/internal/control"
	"gpt-load/internal/dialect"
	"gpt-load/internal/gateway"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/i18n"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/store"
)

func TestBuildContainerResolvesAllM1Dialects(t *testing.T) {
	t.Setenv("AUTH_KEY", "test-auth-key")
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("DATABASE_DSN", ":memory:")
	t.Setenv("ENCRYPTION_KEY", "test-master-key-long")
	t.Setenv("REDIS_DSN", "")

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
		storageStore store.Store,
	) {
		t.Cleanup(func() {
			_ = storageStore.Close()
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
		t.Fatalf("resolve M1 dialects: %v", err)
	}
}

func TestBuildContainerResolvesS6DependencyGraph(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AUTH_KEY", "test-auth-key")
	t.Setenv("DATA_DIR", dataDir)
	t.Setenv("DATABASE_DSN", ":memory:")
	t.Setenv("ENCRYPTION_KEY", "")
	t.Setenv("REDIS_DSN", "")

	dependencyContainer, err := BuildContainer()
	if err != nil {
		t.Fatalf("BuildContainer() error = %v", err)
	}

	var resolved bool
	err = dependencyContainer.Invoke(func(
		_ *app.App,
		_ *config.Config,
		_ encryption.Service,
		db *gorm.DB,
		storageStore store.Store,
		_ *gin.Engine,
		manager *state.Manager,
		registry *state.KeyRegistry,
		runtimeState app.RuntimeStateLoader,
		_ *gateway.Handler,
		attemptForwarder gateway.AttemptForwarder,
		_ dialect.Set,
		_ *control.Service,
		_ *control.Server,
		_ *httpclient.HTTPClientManager,
		_ *redact.Redactor,
		_ *dialect.OpenAI,
	) {
		t.Cleanup(func() {
			_ = storageStore.Close()
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
		if got := registry.CollectCandidates(nil, nil); len(got) != 0 {
			t.Fatalf("empty registry candidates = %#v", got)
		}
		if attemptForwarder == nil {
			t.Fatal("stream-capable attempt forwarder was not resolved")
		}
		resolved = true
	})
	if err != nil {
		t.Fatalf("resolve S6 dependency graph: %v", err)
	}
	if !resolved {
		t.Fatal("S6 dependency graph was not invoked")
	}
	if _, err := os.Stat(filepath.Join(dataDir, encryption.KeyFileName)); err != nil {
		t.Fatalf("container did not initialize encryption keyfile: %v", err)
	}
}

func TestBuildContainerRegistersS6ControlRoutesWithoutAffectingGateway(t *testing.T) {
	t.Setenv("AUTH_KEY", "test-auth-key")
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("DATABASE_DSN", ":memory:")
	t.Setenv("ENCRYPTION_KEY", "test-master-key-long")
	t.Setenv("REDIS_DSN", "")
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
		t.Fatalf("resolve S6 engine: %v", err)
	}
}

func TestBuildContainerRegistersS5GatewayRoute(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AUTH_KEY", "test-auth-key")
	t.Setenv("DATA_DIR", dataDir)
	t.Setenv("DATABASE_DSN", ":memory:")
	t.Setenv("ENCRYPTION_KEY", "test-master-key")
	t.Setenv("REDIS_DSN", "")

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
	t.Setenv("REDIS_DSN", "")

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
