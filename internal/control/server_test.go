package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
)

func TestManagementAuthRequiresConstantShapeBearerToken(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	tests := []struct {
		name       string
		header     string
		wantStatus int
	}{
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{name: "empty bearer", header: "Bearer", wantStatus: http.StatusUnauthorized},
		{name: "wrong different length", header: "Bearer x", wantStatus: http.StatusUnauthorized},
		{name: "extra field", header: "Bearer test-auth-key extra", wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme", header: "Basic test-auth-key", wantStatus: http.StatusUnauthorized},
		{name: "case insensitive", header: "bEaReR test-auth-key", wantStatus: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/groups", nil)
			if test.header != "" {
				request.Header.Set("Authorization", test.header)
			}
			engine.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if test.wantStatus == http.StatusUnauthorized {
				var body struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Code != "UNAUTHORIZED" {
					t.Fatalf("unauthorized body = %s, error=%v", recorder.Body.String(), err)
				}
			}
		})
	}
}

func TestManagementAuthLocalizesUnauthorized(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	for _, test := range []struct {
		language string
		message  string
	}{
		{language: "en-US", message: "Invalid authorization key"},
		{language: "zh-CN", message: "无效的授权密钥"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/groups", nil)
		request.Header.Set("Accept-Language", test.language)
		engine.ServeHTTP(recorder, request)
		if !strings.Contains(recorder.Body.String(), test.message) {
			t.Fatalf("%s body = %s, want message %q", test.language, recorder.Body.String(), test.message)
		}
	}
}

func TestManagementAuthDoesNotLogSecretOrDigest(t *testing.T) {
	initControlI18n(t)
	const authKey = "distinctive-control-auth-key"
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authKey}, fixture.service).RegisterRoutes(engine)

	var logs bytes.Buffer
	previousOutput := logrus.StandardLogger().Out
	logrus.SetOutput(&logs)
	t.Cleanup(func() { logrus.SetOutput(previousOutput) })

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/groups", nil)
	request.Header.Set("Authorization", "Bearer "+authKey+"-wrong")
	engine.ServeHTTP(recorder, request)

	digest := sha256.Sum256([]byte(authKey))
	for _, forbidden := range []string{authKey, hex.EncodeToString(digest[:])} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("auth logs expose %q: %s", forbidden, logs.String())
		}
	}
}

func TestManagementWritesRejectUnknownFieldsAndMultipleJSONValues(t *testing.T) {
	initControlI18n(t)

	t.Run("import rejects unknown top-level field", func(t *testing.T) {
		fixture := newServiceFixture(t)
		engine := gin.New()
		NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
		beforeRevision := fixture.manager.Current().Revision

		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/import", strings.NewReader(
			`{"upstream_url":"https://api.example.com","protocols":["openai"],"keys":"sk-test","unexpected":true}`,
		))
		request.Header.Set("Authorization", "Bearer test-auth-key")
		request.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/import = %d %s, want 400", recorder.Code, recorder.Body.String())
		}
		assertGroupCount(t, fixture.db, 0)
		if got := fixture.manager.Current().Revision; got != beforeRevision {
			t.Fatalf("snapshot revision = %d, want unchanged %d", got, beforeRevision)
		}
	})

	t.Run("access key rejects unknown nested filter field", func(t *testing.T) {
		fixture := newServiceFixture(t)
		engine := gin.New()
		NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
		beforeRevision := fixture.manager.Current().Revision

		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/access-keys", strings.NewReader(
			`{"name":"client","filters":{"gropus":[1]}}`,
		))
		request.Header.Set("Authorization", "Bearer test-auth-key")
		request.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/access-keys = %d %s, want 400", recorder.Code, recorder.Body.String())
		}
		var count int64
		if err := fixture.db.Table("access_keys").Count(&count).Error; err != nil {
			t.Fatalf("count access keys: %v", err)
		}
		if count != 0 {
			t.Fatalf("access key count = %d, want 0", count)
		}
		if got := fixture.manager.Current().Revision; got != beforeRevision {
			t.Fatalf("snapshot revision = %d, want unchanged %d", got, beforeRevision)
		}
	})

	t.Run("access key update rejects a second JSON value", func(t *testing.T) {
		fixture := newServiceFixture(t)
		fixture.service.random = bytes.NewReader(make([]byte, 16))
		created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "original"})
		if err != nil {
			t.Fatalf("CreateAccessKey() error = %v", err)
		}
		engine := gin.New()
		NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
		beforeRevision := fixture.manager.Current().Revision

		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(
			http.MethodPut,
			"/api/access-keys/"+strconv.FormatUint(uint64(created.ID), 10),
			strings.NewReader(`{"name":"changed"}{"status":"disabled"}`),
		)
		request.Header.Set("Authorization", "Bearer test-auth-key")
		request.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("PUT /api/access-keys/:id = %d %s, want 400", recorder.Code, recorder.Body.String())
		}
		row := loadAccessKeyRow(t, fixture.db, created.ID)
		if row.Name != "original" || row.Status != string(state.AccessKeyStatusActive) {
			t.Fatalf("access key row = %#v, want original active row", row)
		}
		if got := fixture.manager.Current().Revision; got != beforeRevision {
			t.Fatalf("snapshot revision = %d, want unchanged %d", got, beforeRevision)
		}
	})
}

func TestUpdateAccessKeyRoutesParseIDsAndPreservePointerSemantics(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(make([]byte, 16))
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "route-key"})
	if err != nil {
		t.Fatalf("CreateAccessKey() error = %v", err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	request := httptest.NewRequest(http.MethodPut, "/api/access-keys/"+strconv.FormatUint(uint64(created.ID), 10), strings.NewReader(`{"status":"disabled"}`))
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), created.Key) {
		t.Fatalf("PUT access key = %d %s", recorder.Code, recorder.Body.String())
	}
	row := loadAccessKeyRow(t, fixture.db, created.ID)
	if row.Name != "route-key" || row.Status != string(state.AccessKeyStatusDisabled) {
		t.Fatalf("PUT row = %#v", row)
	}

	for _, path := range []string{
		"/api/access-keys/0",
		"/api/access-keys/-1",
		"/api/access-keys/not-a-number",
		"/api/access-keys/18446744073709551616",
	} {
		request = httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{"name":"ignored"}`))
		request.Header.Set("Authorization", "Bearer test-auth-key")
		request.Header.Set("Content-Type", "application/json")
		recorder = httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("PUT %s = %d %s, want 400", path, recorder.Code, recorder.Body.String())
		}
	}

	request = httptest.NewRequest(http.MethodPut, "/api/access-keys/"+strconv.FormatUint(uint64(created.ID), 10), strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("empty PUT = %d %s, want 400", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/access-keys/"+strconv.FormatUint(uint64(created.ID), 10), nil)
	request.Header.Set("Authorization", "Bearer test-auth-key")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("DELETE access key = %d %s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/access-keys/"+strconv.FormatUint(uint64(created.ID), 10), nil)
	request.Header.Set("Authorization", "Bearer test-auth-key")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("repeated DELETE = %d %s, want 404", recorder.Code, recorder.Body.String())
	}
}
