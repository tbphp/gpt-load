package demo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gpt-load/internal/app"
	"gpt-load/internal/container"
	"gpt-load/internal/gateway"
	"gpt-load/internal/platform/i18n"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/storage/models"
)

func TestDemoRejectsExternalDatabaseWithoutTouchingData(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "gpt-load.db")
	if err := os.WriteFile(sentinel, []byte("existing user database"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATA_DIR", dir)
	for _, dsn := range []string{"sqlite:///existing.db", "postgres://example/database", "mysql://example/database"} {
		t.Setenv("DATABASE_DSN", dsn)
		if err := validateEnvironment(); err == nil {
			t.Errorf("demo accepted an external database: %s", dsn)
		}
	}
	content, err := os.ReadFile(sentinel)
	if err != nil || string(content) != "existing user database" {
		t.Fatalf("demo touched user data: %q, %v", content, err)
	}
}

func TestDemoCloseRemovesTemporaryDirectoryAfterStopError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gpt-load.db"), []byte("demo data"), 0600); err != nil {
		t.Fatal(err)
	}
	stopErr := errors.New("request log shutdown failed")
	current := &instance{
		app: app.NewApp(app.AppParams{RequestLogs: failingStopLogs{err: stopErr}}),
		dir: dir,
	}
	if err := current.close(); !errors.Is(err, stopErr) {
		t.Fatalf("close lost the shutdown error: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("temporary directory remains after failed shutdown: %v", err)
	}
}

type failingStopLogs struct{ err error }

func (failingStopLogs) Start() error { return nil }

func (logs failingStopLogs) Stop(context.Context) error { return logs.err }

func startTestInstance(t *testing.T) *instance {
	t.Helper()
	current, err := newInstance("127.0.0.1", 0, resetInterval)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := current.close(); err != nil {
			t.Error(err)
		}
	})
	if err := current.app.Start(); err != nil {
		t.Fatal(err)
	}
	return current
}

var requestSequence atomic.Uint64

func request(current *instance, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+AuthKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", fmt.Sprintf("00000000-0000-4000-8000-%012d", requestSequence.Add(1)))
	recorder := httptest.NewRecorder()
	current.engine.ServeHTTP(recorder, req)
	return recorder
}

func TestDemoStartsWithUsableSampleData(t *testing.T) {
	userDir := t.TempDir()
	t.Chdir(userDir)
	t.Setenv("DATA_DIR", userDir)
	t.Setenv("AUTH_KEY", "private-user-key")
	t.Setenv("ENCRYPTION_KEY", "invalid-if-demo-reads-it")
	if err := os.WriteFile("gpt-load.db", []byte("user database"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".env", []byte("DATABASE_DSN=postgres://example/never-connect\n"), 0600); err != nil {
		t.Fatal(err)
	}
	current := startTestInstance(t)
	if content, err := os.ReadFile("gpt-load.db"); err != nil || string(content) != "user database" || current.dir == userDir {
		t.Fatalf("demo touched normal deployment data: %q %v", content, err)
	}
	var count int64
	if err := current.db.Model(&models.Group{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count < 3 {
		t.Errorf("demo has %d groups, want at least three initialized groups", count)
	}
	if err := current.db.Model(&models.RequestLog{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Error("demo request logs are empty")
	}
	if _, err := requestlog.NewService(current.db, nil, nil).List(t.Context(), requestlog.ListQuery{Limit: 50}); err != nil {
		t.Errorf("list seeded logs: %v", err)
	}
	for _, path := range []string{"/api/auth/session", "/api/groups", "/api/home", "/api/logs", "/api/modern/groups"} {
		response := request(current, http.MethodGet, path, "")
		if response.Code != http.StatusOK {
			t.Errorf("%s: %d %s", path, response.Code, response.Body)
		}
	}
	usageURL := fmt.Sprintf("/api/usage?from_ms=%d&to_ms=%d", time.Now().Add(-24*time.Hour).UnixMilli(), time.Now().UnixMilli())
	if response := request(current, http.MethodGet, usageURL, ""); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "estimated_cost") {
		t.Errorf("demo usage is unusable: %d %s", response.Code, response.Body)
	}
	var latest models.RequestLog
	if err := current.db.Order("completed_at_ms DESC").First(&latest).Error; err == nil {
		if _, err := requestlog.NewService(current.db, nil, nil).Get(t.Context(), latest.ID); err != nil {
			t.Errorf("decode seeded log: %v", err)
		}
		response := request(current, http.MethodGet, "/api/logs/"+latest.ID, "")
		if response.Code != http.StatusOK || time.Since(time.UnixMilli(latest.CompletedAtMS)) > time.Minute {
			t.Errorf("latest demo request is unusable: %d %s", response.Code, response.Body)
		}
	}
}

func TestDemoAllowsCRUDAndRestoresDeletedData(t *testing.T) {
	current := startTestInstance(t)
	create := request(current, "POST", "/api/groups", `{"name":"Visitor group","channel_id":"openai_compatible","connection_type":"api_key","params":{"base_url":"https://example.invalid/v1"},"models":[{"id":"visitor-model","alias":"","alias_enabled":false}],"credentials":"demo-visitor-key"}`)
	if create.Code != http.StatusOK {
		t.Fatalf("create group: %d %s", create.Code, create.Body)
	}
	var result struct {
		Data struct {
			GroupID uint `json:"group_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/groups/%d", result.Data.GroupID)
	update := request(current, "PUT", path+"/settings", `{"name":"Changed by visitor","enabled":false}`)
	if update.Code != http.StatusOK {
		t.Fatalf("edit group: %d %s", update.Code, update.Body)
	}
	for _, target := range []string{path, "/api/groups/1"} {
		response := request(current, "DELETE", target, "")
		if response.Code != http.StatusOK {
			t.Fatalf("delete group: %d %s", response.Code, response.Body)
		}
	}
	var keys []models.AccessKey
	if err := current.db.Find(&keys).Error; err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		response := request(current, "DELETE", fmt.Sprintf("/api/access-keys/%d", key.ID), "")
		if response.Code != http.StatusOK {
			t.Fatalf("delete key: %d %s", response.Code, response.Body)
		}
	}
	if response := request(current, "GET", "/api/auth/session", ""); response.Code != http.StatusOK {
		t.Fatal("deleting demo access keys broke the public administrator login")
	}
	if err := current.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(current.dir); !os.IsNotExist(err) {
		t.Fatalf("old demo directory remains: %v", err)
	}
	restored := startTestInstance(t)
	if restored.dir == current.dir {
		t.Fatal("reset reused the previous database directory")
	}
	response := request(restored, "GET", "/api/groups/1", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Demo · OpenAI") {
		t.Fatalf("deleted seed group was not restored: %d %s", response.Code, response.Body)
	}
}

func TestDemoAutomaticallyResetsAndCleansUpOnCancellation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- run(ctx, "127.0.0.1", 0, time.Second) }()
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	first := ""
	reset := false
	for !reset {
		select {
		case err := <-result:
			t.Fatalf("demo stopped before reset: %v", err)
		case <-deadline:
			t.Fatal("demo did not reset automatically")
		case <-ticker.C:
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if !strings.HasPrefix(entry.Name(), "gpt-load-demo-") {
					continue
				}
				if first == "" {
					first = entry.Name()
				} else if entry.Name() != first {
					reset = true
				}
			}
		}
	}
	cancel()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("demo left temporary data: %v %v", entries, err)
	}
}

func TestNormalContainerDoesNotEnableDemo(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := i18n.Init(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("DATABASE_DSN", "")
	t.Setenv("AUTH_KEY", "normal-instance-key")
	t.Setenv("ENCRYPTION_KEY", "")
	d, err := container.BuildContainer()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Invoke(func(engine *gin.Engine, db *gorm.DB) {
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := sqlDB.Close(); err != nil {
				t.Error(err)
			}
		})
		req := httptest.NewRequest("GET", "/api/auth/session", nil)
		req.Header.Set("Authorization", "Bearer normal-instance-key")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, req)
		if response.Code != http.StatusOK || response.Header().Get("X-GPT-Load-Demo-Reset-At") != "" {
			t.Fatalf("normal instance was affected by demo: %d %s", response.Code, response.Body)
		}
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDemoBlocksExternalActions(t *testing.T) {
	current := startTestInstance(t)
	for _, route := range (&gateway.Handler{}).HTTPModule().Routes {
		parts := strings.Split(route.Path, "/")
		for index, part := range parts {
			if strings.HasPrefix(part, ":") || strings.HasPrefix(part, "*") {
				parts[index] = "demo"
			}
		}
		for _, method := range route.Methods {
			response := request(current, method, strings.Join(parts, "/"), "{}")
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "DEMO_RESTRICTED") {
				t.Errorf("data route %s %s escaped demo restriction: %d %s", method, route.Path, response.Code, response.Body)
			}
		}
	}
	for _, target := range []struct{ method, path string }{
		{"POST", "/v1/chat/completions"},
		{"GET", "/v1/responses"},
		{"POST", "/api/models/discover"},
		{"POST", "/api/groups/1/credentials/1/test"},
		{"POST", "/api/groups/1/credentials/1/refresh"},
		{"POST", "/api/groups/1/credentials/1/observation-refresh"},
		{"POST", "/api/credential-stages/authorizations"},
		{"POST", "/api/credential-stages/import-batch"},
		{"POST", "/api/model-prices/sync"},
		{"POST", "/api/proxies/1/test"},
		{"GET", "/api/system/update"},
	} {
		response := request(current, target.method, target.path, "{}")
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "DEMO_RESTRICTED") {
			t.Errorf("%s %s did not reject demo operation: %d %s", target.method, target.path, response.Code, response.Body)
		}
	}
}

func TestCanceledDemoDoesNotStart(t *testing.T) {
	t.Setenv("DATABASE_DSN", "")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Run(ctx); err != nil {
		t.Fatalf("canceled demo should exit cleanly: %v", err)
	}
}
