package container

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"gpt-load/internal/platform/httproute"
	"gpt-load/internal/platform/i18n"
)

func TestBuildContainerExposesUnifiedRouteCatalog(t *testing.T) {
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

	var routes []httproute.RouteInfo
	if err := dependencyContainer.Invoke(func(registry *httproute.Registry, db *gorm.DB) {
		routes = registry.Routes()
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			t.Cleanup(func() { _ = sqlDB.Close() })
		}
	}); err != nil {
		t.Fatalf("resolve unified route catalog: %v", err)
	}

	for _, expected := range []httproute.RouteInfo{
		{
			ModuleName: "system",
			RouteName:  "system.health",
			Owner:      httproute.OwnerSystem,
			Auth:       httproute.AuthNone,
			Methods:    []string{http.MethodGet},
			Path:       "/health",
		},
		{
			ModuleName: "control",
			RouteName:  "control.models.list",
			Owner:      httproute.OwnerControl,
			Auth:       httproute.AuthControl,
			Methods:    []string{http.MethodGet},
			Path:       "/api/models",
		},
		{
			ModuleName: "control",
			RouteName:  "control.model-prices.list",
			Owner:      httproute.OwnerControl,
			Auth:       httproute.AuthControl,
			Methods:    []string{http.MethodGet},
			Path:       "/api/model-prices",
		},
		{
			ModuleName: "control",
			RouteName:  "control.model-prices.update",
			Owner:      httproute.OwnerControl,
			Auth:       httproute.AuthControl,
			Methods:    []string{http.MethodPut},
			Path:       "/api/model-prices/:id",
		},
		{
			ModuleName: "control",
			RouteName:  "control.model-prices.reset",
			Owner:      httproute.OwnerControl,
			Auth:       httproute.AuthControl,
			Methods:    []string{http.MethodPost},
			Path:       "/api/model-prices/:id/reset",
		},
		{
			ModuleName: "control",
			RouteName:  "control.model-prices.delete",
			Owner:      httproute.OwnerControl,
			Auth:       httproute.AuthControl,
			Methods:    []string{http.MethodDelete},
			Path:       "/api/model-prices/:id",
		},
		{
			ModuleName: "control",
			RouteName:  "control.access-keys.reveal",
			Owner:      httproute.OwnerControl,
			Auth:       httproute.AuthControl,
			Methods:    []string{http.MethodPost},
			Path:       "/api/access-keys/:id/reveal",
		},
		{
			ModuleName: "control",
			RouteName:  "control.group-credentials.reveal",
			Owner:      httproute.OwnerControl,
			Auth:       httproute.AuthControl,
			Methods:    []string{http.MethodPost},
			Path:       "/api/groups/:group_id/credentials/:credential_id/reveal",
		},
		{
			ModuleName: "control",
			RouteName:  "control.group-credentials.refresh",
			Owner:      httproute.OwnerControl,
			Auth:       httproute.AuthControl,
			Methods:    []string{http.MethodPost},
			Path:       "/api/groups/:group_id/credentials/:credential_id/refresh",
		},
		{
			ModuleName: "control",
			RouteName:  "control.group-credentials.download",
			Owner:      httproute.OwnerControl,
			Auth:       httproute.AuthControl,
			Methods:    []string{http.MethodPost},
			Path:       "/api/groups/:group_id/credentials/:credential_id/download",
		},
		{
			ModuleName: "control",
			RouteName:  "control.groups.options",
			Owner:      httproute.OwnerControl,
			Auth:       httproute.AuthControl,
			Methods:    []string{http.MethodGet},
			Path:       "/api/groups/options",
		},
		{
			ModuleName: "data",
			RouteName:  "data.openai.responses.resource",
			Owner:      httproute.OwnerData,
			Auth:       httproute.AuthAccessKey,
			Methods: []string{
				http.MethodGet,
				http.MethodPost,
				http.MethodPut,
				http.MethodPatch,
				http.MethodHead,
				http.MethodOptions,
				http.MethodDelete,
				http.MethodConnect,
				http.MethodTrace,
			},
			Path: "/v1/responses/*resource_path",
		},
		{
			ModuleName: "web",
			RouteName:  "web.asset.favicon",
			Owner:      httproute.OwnerWeb,
			Auth:       httproute.AuthNone,
			Methods:    []string{http.MethodGet},
			Path:       "/favicon.svg",
		},
	} {
		assertRouteInfo(t, routes, expected)
	}
}

func TestHTTPRouteGroupOptionsOwnsStaticPathBeforeGroupID(t *testing.T) {
	engine := newRouteContractEngine(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/groups/options", nil)
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Accept-Language", "en-US")

	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf(
			"GET /api/groups/options = %d %s, want static handler database error",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	var envelope struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode options envelope: %v", err)
	}
	if envelope.Code != "DATABASE_ERROR" {
		t.Fatalf(
			"options envelope = %s, want static handler DATABASE_ERROR",
			recorder.Body.String(),
		)
	}
}

func TestHTTPRoutesReturnMethodNotAllowedBeforePlaneAuthentication(t *testing.T) {
	engine := newRouteContractEngine(t)

	var logs bytes.Buffer
	logger := logrus.StandardLogger()
	previousOutput := logger.Out
	previousFormatter := logger.Formatter
	logger.SetOutput(&logs)
	logger.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	t.Cleanup(func() {
		logger.SetOutput(previousOutput)
		logger.SetFormatter(previousFormatter)
	})

	tests := []struct {
		name        string
		method      string
		target      string
		wantMethods []string
	}{
		{
			name:        "system route",
			method:      http.MethodPost,
			target:      "/health",
			wantMethods: []string{http.MethodGet},
		},
		{
			name:        "control route",
			method:      http.MethodPost,
			target:      "/api/auth/session",
			wantMethods: []string{http.MethodGet},
		},
		{
			name:        "data route",
			method:      http.MethodGet,
			target:      "/v1/chat/completions",
			wantMethods: []string{http.MethodPost},
		},
		{
			name:        "web route",
			method:      http.MethodPost,
			target:      "/login",
			wantMethods: []string{http.MethodGet},
		},
		{
			name:        "catalog sync static path get",
			method:      http.MethodGet,
			target:      "/api/model-prices/sync",
			wantMethods: []string{http.MethodPost},
		},
		{
			name:        "catalog sync static path update",
			method:      http.MethodPut,
			target:      "/api/model-prices/sync",
			wantMethods: []string{http.MethodPost},
		},
		{
			name:        "catalog sync static path delete",
			method:      http.MethodDelete,
			target:      "/api/model-prices/sync",
			wantMethods: []string{http.MethodPost},
		},
		{
			name:        "retired model price collection update",
			method:      http.MethodPut,
			target:      "/api/model-prices",
			wantMethods: []string{http.MethodGet},
		},
		{
			name:        "retired model price collection delete",
			method:      http.MethodDelete,
			target:      "/api/model-prices",
			wantMethods: []string{http.MethodGet},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.target, nil)
			request.Header.Set("Authorization", "Bearer wrong-route-contract-key")

			engine.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusMethodNotAllowed {
				t.Fatalf(
					"%s %s = %d %s, want 405",
					test.method,
					test.target,
					recorder.Code,
					recorder.Body.String(),
				)
			}
			if got := allowMethods(recorder.Header().Get("Allow")); !equalStrings(got, test.wantMethods) {
				t.Fatalf("Allow = %q (%v), want %v", recorder.Header().Get("Allow"), got, test.wantMethods)
			}
		})
	}

	for _, event := range []string{
		"auth_failed",
		"auth_failed",
		"mutation",
		"route_not_found",
	} {
		if strings.Contains(logs.String(), event) {
			t.Fatalf("wrong-method requests emitted %q: %s", event, logs.String())
		}
	}
}

func TestHTTPRoutesValidateDynamicPathsBeforeDataAuthentication(t *testing.T) {
	engine := newRouteContractEngine(t)

	var logs bytes.Buffer
	logger := logrus.StandardLogger()
	previousOutput := logger.Out
	previousFormatter := logger.Formatter
	logger.SetOutput(&logs)
	logger.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	t.Cleanup(func() {
		logger.SetOutput(previousOutput)
		logger.SetFormatter(previousFormatter)
	})

	for _, target := range []string{
		"/v1beta/models/missing-action",
		"/v1beta/models/:generateContent",
	} {
		t.Run(target, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, target, nil)
			request.Header.Set("Authorization", "Bearer wrong-route-contract-key")

			engine.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusNotFound ||
				!strings.Contains(recorder.Body.String(), `"code":"protocol_endpoint_not_found"`) {
				t.Fatalf(
					"POST %s = %d %s, want data-plane route 404",
					target,
					recorder.Code,
					recorder.Body.String(),
				)
			}
		})
	}

	if strings.Contains(logs.String(), "auth_failed") {
		t.Fatalf("invalid dynamic paths emitted data auth failure: %s", logs.String())
	}
}

func TestHTTPRoutesPreserveResponsesAuthenticationBeforeLocalRejection(t *testing.T) {
	engine := newRouteContractEngine(t)

	var logs bytes.Buffer
	logger := logrus.StandardLogger()
	previousOutput := logger.Out
	previousFormatter := logger.Formatter
	logger.SetOutput(&logs)
	logger.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	t.Cleanup(func() {
		logger.SetOutput(previousOutput)
		logger.SetFormatter(previousFormatter)
	})

	for _, test := range []struct {
		method string
		target string
	}{
		{method: http.MethodOptions, target: "/v1/responses"},
		{method: http.MethodGet, target: "/v1/responses/%2e%2e/models"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.target, nil)
		request.Header.Set("Authorization", "Bearer wrong-route-contract-key")
		engine.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusUnauthorized ||
			!strings.Contains(recorder.Body.String(), `"code":"invalid_access_key"`) {
			t.Fatalf(
				"%s %s = %d %s, want authenticated data-plane 401",
				test.method,
				test.target,
				recorder.Code,
				recorder.Body.String(),
			)
		}
	}
	if !strings.Contains(logs.String(), "auth_failed") {
		t.Fatalf("Responses local rejections skipped authentication log: %s", logs.String())
	}
}

func TestHTTPControlFallbacksUseRequestLanguage(t *testing.T) {
	engine := newRouteContractEngine(t)

	for _, test := range []struct {
		name        string
		method      string
		target      string
		language    string
		wantCode    string
		wantMessage string
	}{
		{
			name: "Chinese not found", method: http.MethodGet,
			target: "/api/unknown", language: "zh-CN",
			wantCode: "ROUTE_NOT_FOUND", wantMessage: "路由不存在",
		},
		{
			name: "Japanese method not allowed", method: http.MethodPost,
			target: "/api/auth/session", language: "ja-JP",
			wantCode: "METHOD_NOT_ALLOWED", wantMessage: "許可されていないHTTPメソッドです",
		},
		{
			name: "English method not allowed", method: http.MethodPost,
			target: "/api/auth/session", language: "en-US",
			wantCode: "METHOD_NOT_ALLOWED", wantMessage: "Method not allowed",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.target, nil)
			request.Header.Set("Accept-Language", test.language)
			engine.ServeHTTP(recorder, request)

			var responseBody struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &responseBody); err != nil {
				t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
			}
			if responseBody.Code != test.wantCode ||
				responseBody.Message != test.wantMessage {
				t.Fatalf(
					"response = %#v, want code=%q message=%q",
					responseBody,
					test.wantCode,
					test.wantMessage,
				)
			}
		})
	}
}

func TestHTTPNamespacesDoNotFallThroughToSPA(t *testing.T) {
	engine := newRouteContractEngine(t)

	for _, test := range []struct {
		name     string
		target   string
		wantBody string
	}{
		{
			name: "control", target: "/api/unknown",
			wantBody: `"code":"ROUTE_NOT_FOUND"`,
		},
		{
			name: "OpenAI data", target: "/v1/unknown",
			wantBody: `"code":"protocol_endpoint_not_found"`,
		},
		{
			name: "Gemini data", target: "/v1beta/unknown",
			wantBody: `"code":"protocol_endpoint_not_found"`,
		},
		{name: "system", target: "/health/unknown"},
		{name: "assets", target: "/assets/missing.js"},
		{name: "favicon", target: "/favicon.svg/unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			request.Header.Set("Accept", "text/html,application/xhtml+xml")
			engine.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusNotFound {
				t.Fatalf(
					"GET %s = %d %s, want 404",
					test.target,
					recorder.Code,
					recorder.Body.String(),
				)
			}
			if strings.Contains(
				strings.ToLower(recorder.Body.String()),
				"<!doctype html",
			) {
				t.Fatalf("GET %s unexpectedly returned SPA index", test.target)
			}
			if test.wantBody != "" &&
				!strings.Contains(recorder.Body.String(), test.wantBody) {
				t.Fatalf(
					"GET %s body = %q, want containing %q",
					test.target,
					recorder.Body.String(),
					test.wantBody,
				)
			}
		})
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/unknown-page", nil)
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound ||
		!strings.Contains(
			strings.ToLower(recorder.Body.String()),
			"<!doctype html",
		) {
		t.Fatalf(
			"unknown browser page = %d %q, want 404 SPA index",
			recorder.Code,
			recorder.Body.String(),
		)
	}
}

func assertRouteInfo(
	t *testing.T,
	routes []httproute.RouteInfo,
	expected httproute.RouteInfo,
) {
	t.Helper()
	for _, route := range routes {
		if route.RouteName != expected.RouteName {
			continue
		}
		if route.ModuleName != expected.ModuleName ||
			route.Owner != expected.Owner ||
			route.Auth != expected.Auth ||
			route.Path != expected.Path ||
			!equalStrings(
				append([]string(nil), route.Methods...),
				append([]string(nil), expected.Methods...),
			) {
			t.Fatalf("route %q = %#v, want %#v", expected.RouteName, route, expected)
		}
		return
	}
	t.Fatalf("route %q is missing from unified catalog", expected.RouteName)
}

func newRouteContractEngine(t *testing.T) *gin.Engine {
	t.Helper()
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
	var engine *gin.Engine
	if err := dependencyContainer.Invoke(func(resolved *gin.Engine, db *gorm.DB) {
		engine = resolved
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			t.Cleanup(func() { _ = sqlDB.Close() })
		}
	}); err != nil {
		t.Fatalf("resolve route contract engine: %v", err)
	}
	return engine
}

func allowMethods(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	methods := strings.Split(value, ",")
	for index := range methods {
		methods[index] = strings.TrimSpace(methods[index])
	}
	sort.Strings(methods)
	return methods
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
