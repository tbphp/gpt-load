package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
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

func TestServerDiscoverModelsEndpoint(t *testing.T) {
	initControlI18n(t)
	const authKey = "test-auth-key"

	newServer := func(value *recordingDiscoveryDialect) (*Service, *gin.Engine) {
		t.Helper()
		fixture := newServiceFixture(t)
		if value == nil {
			fixture.service.dialects = dialect.NewSet()
		} else {
			fixture.service.dialects = dialect.NewSet(value)
		}
		engine := gin.New()
		NewServer(&config.Config{AuthKey: authKey}, fixture.service).RegisterRoutes(engine)
		return fixture.service, engine
	}

	t.Run("success preserves order", func(t *testing.T) {
		_, engine := newServer(&recordingDiscoveryDialect{
			value: protocol.OpenAI,
			listFn: func(
				context.Context,
				string,
				string,
				state.HeaderRules,
			) ([]string, error) {
				return []string{"z-model", "a-model"}, nil
			},
		})
		recorder := serveDiscoveryRequest(t, engine, authKey,
			`{"upstream_url":"https://api.example.com","protocol":"openai","key":"sk-upstream"}`,
		)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
		}
		var response struct {
			Code int                  `json:"code"`
			Data ModelDiscoveryResult `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if response.Code != 0 || len(response.Data.Models) != 2 ||
			response.Data.Models[0] != "z-model" || response.Data.Models[1] != "a-model" {
			t.Fatalf("response = %#v", response)
		}
	})

	t.Run("empty list remains an array", func(t *testing.T) {
		_, engine := newServer(&recordingDiscoveryDialect{
			value: protocol.OpenAI,
			listFn: func(
				context.Context,
				string,
				string,
				state.HeaderRules,
			) ([]string, error) {
				return nil, nil
			},
		})
		recorder := serveDiscoveryRequest(t, engine, authKey,
			`{"upstream_url":"https://api.example.com","protocol":"openai","key":"sk-upstream"}`,
		)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"models":[]`) {
			t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("authentication is inherited", func(t *testing.T) {
		_, engine := newServer(&recordingDiscoveryDialect{
			value: protocol.OpenAI,
			listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
				t.Fatal("ListModels called without valid management authentication")
				return nil, nil
			},
		})
		for _, token := range []string{"", "wrong-key"} {
			recorder := serveDiscoveryRequest(t, engine, token,
				`{"upstream_url":"https://api.example.com","protocol":"openai","key":"sk-upstream"}`,
			)
			if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), `"code":"UNAUTHORIZED"`) {
				t.Fatalf("auth %q response = %d %s", token, recorder.Code, recorder.Body.String())
			}
		}
	})

	t.Run("strict JSON", func(t *testing.T) {
		_, engine := newServer(&recordingDiscoveryDialect{
			value: protocol.OpenAI,
			listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
				t.Fatal("ListModels called for invalid JSON")
				return nil, nil
			},
		})
		for _, payload := range []string{
			`{"upstream_url":"https://api.example.com","protocol":"openai","key":"sk-upstream","unknown":true}`,
			`{"upstream_url":"https://api.example.com","protocol":"openai","key":"sk-upstream"}{}`,
		} {
			recorder := serveDiscoveryRequest(t, engine, authKey, payload)
			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"INVALID_JSON"`) {
				t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
			}
		}
	})

	t.Run("validation and invariant errors are fixed", func(t *testing.T) {
		_, engine := newServer(nil)
		for _, test := range []struct {
			payload string
			status  int
			code    string
		}{
			{payload: `{"upstream_url":"/relative","protocol":"openai","key":"secret-key"}`, status: http.StatusBadRequest, code: "VALIDATION_FAILED"},
			{payload: `{"upstream_url":"https://api.example.com?token=query-secret","protocol":"openai","key":"secret-key"}`, status: http.StatusInternalServerError, code: "INTERNAL_SERVER_ERROR"},
		} {
			recorder := serveDiscoveryRequest(t, engine, authKey, test.payload)
			if recorder.Code != test.status || !strings.Contains(recorder.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
			}
			for _, secret := range []string{"secret-key", "query-secret", "api.example.com"} {
				if strings.Contains(recorder.Body.String(), secret) {
					t.Fatalf("response exposes %q: %s", secret, recorder.Body.String())
				}
			}
		}
	})

	t.Run("upstream failures map to localized bad gateway", func(t *testing.T) {
		service, engine := newServer(&recordingDiscoveryDialect{
			value: protocol.OpenAI,
			listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
				return nil, fmt.Errorf("raw upstream failure with secret-body")
			},
		})
		service.modelDiscoveryTimeout = 20 * time.Millisecond
		for _, test := range []struct {
			language string
			message  string
		}{
			{language: "en-US", message: "Upstream service error"},
			{language: "zh-CN", message: "上游服务错误"},
		} {
			recorder := serveDiscoveryRequestWithLanguage(t, engine, authKey,
				`{"upstream_url":"https://api.example.com?token=query-secret","protocol":"openai","key":"secret-key"}`,
				test.language,
			)
			if recorder.Code != http.StatusBadGateway ||
				!strings.Contains(recorder.Body.String(), `"code":"BAD_GATEWAY"`) ||
				!strings.Contains(recorder.Body.String(), test.message) {
				t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
			}
			for _, forbidden := range []string{"secret-key", "query-secret", "secret-body", "raw upstream failure"} {
				if strings.Contains(recorder.Body.String(), forbidden) {
					t.Fatalf("response exposes %q: %s", forbidden, recorder.Body.String())
				}
			}
		}
	})

	t.Run("timeout maps to bad gateway", func(t *testing.T) {
		service, engine := newServer(&recordingDiscoveryDialect{
			value: protocol.OpenAI,
			listFn: func(ctx context.Context, _ string, _ string, _ state.HeaderRules) ([]string, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		})
		service.modelDiscoveryTimeout = 20 * time.Millisecond
		recorder := serveDiscoveryRequest(t, engine, authKey,
			`{"upstream_url":"https://api.example.com","protocol":"openai","key":"sk-upstream"}`,
		)
		if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), `"code":"BAD_GATEWAY"`) {
			t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("broken upstream JSON maps to bad gateway", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"data":[`))
		}))
		defer upstream.Close()

		fixture := newServiceFixture(t)
		fixture.service.dialects = dialect.NewSet(dialect.NewOpenAI(upstream.Client()))
		engine := gin.New()
		NewServer(&config.Config{AuthKey: authKey}, fixture.service).RegisterRoutes(engine)
		recorder := serveDiscoveryRequest(t, engine, authKey,
			`{"upstream_url":"`+upstream.URL+`","protocol":"openai","key":"sk-upstream"}`,
		)
		if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), `"code":"BAD_GATEWAY"`) {
			t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), `{"data":[`) {
			t.Fatalf("response exposes upstream body: %s", recorder.Body.String())
		}
	})
}

func TestDiscoverModelsEndpointLogsOnlyMetadata(t *testing.T) {
	initControlI18n(t)
	const (
		authSecret  = "distinctive-auth-secret"
		keySecret   = "distinctive-upstream-key"
		querySecret = "distinctive-query-secret"
		bodySecret  = "distinctive-upstream-body"
	)
	fixture := newServiceFixture(t)
	fixture.service.dialects = dialect.NewSet(&recordingDiscoveryDialect{
		value: protocol.Anthropic,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			return nil, fmt.Errorf("provider error: %s", bodySecret)
		},
	})
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authSecret}, fixture.service).RegisterRoutes(engine)

	var logs bytes.Buffer
	previousOutput := logrus.StandardLogger().Out
	logrus.SetOutput(&logs)
	t.Cleanup(func() { logrus.SetOutput(previousOutput) })

	recorder := serveDiscoveryRequest(t, engine, authSecret,
		`{"upstream_url":"https://api.example.com?token=`+querySecret+`","protocol":"anthropic","key":"`+keySecret+`"}`,
	)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	logText := logs.String()
	for _, required := range []string{"operation=discover_models", "protocol=anthropic", "error_code=BAD_GATEWAY", "error_type="} {
		if !strings.Contains(logText, required) {
			t.Fatalf("logs missing %q: %s", required, logText)
		}
	}
	for _, forbidden := range []string{authSecret, keySecret, querySecret, bodySecret, "provider error", "Authorization"} {
		if strings.Contains(logText, forbidden) {
			t.Fatalf("logs expose %q: %s", forbidden, logText)
		}
	}
}

func serveDiscoveryRequest(t *testing.T, engine *gin.Engine, authKey, payload string) *httptest.ResponseRecorder {
	t.Helper()
	return serveDiscoveryRequestWithLanguage(t, engine, authKey, payload, "en-US")
}

func serveDiscoveryRequestWithLanguage(
	t *testing.T,
	engine *gin.Engine,
	authKey, payload, language string,
) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/models/discover", strings.NewReader(payload))
	if authKey != "" {
		request.Header.Set("Authorization", "Bearer "+authKey)
	}
	request.Header.Set("Accept-Language", language)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)
	return recorder
}
