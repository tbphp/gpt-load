package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
)

func TestDownstreamHeadersMiddlewarePreservesDisabledPreflightBehavior(t *testing.T) {
	handler := newDownstreamHeadersTestHandler(t, nil)
	engine := gin.New()
	engine.Use(handler.DownstreamHeadersMiddleware())
	reached := false
	engine.Any("/v1/responses", func(context *gin.Context) {
		reached = true
		context.JSON(http.StatusUnauthorized, gin.H{"code": "invalid_access_key"})
	})

	request := newPreflightRequest("/v1/responses", "app://obsidian.md", "POST", "authorization")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if !reached || response.Code != http.StatusUnauthorized {
		t.Fatalf("disabled preflight = reached:%t status:%d, want downstream 401", reached, response.Code)
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disabled preflight exposed CORS headers: %#v", response.Header())
	}
}

func TestDownstreamHeadersMiddlewareAnswersAllowedPreflightBeforeAuthentication(t *testing.T) {
	handler := newDownstreamHeadersTestHandler(t, configuredBrowserAccessSettings())
	engine := gin.New()
	engine.Use(handler.DownstreamHeadersMiddleware())
	reached := false
	engine.Any("/v1/responses", func(context *gin.Context) {
		reached = true
		context.Status(http.StatusUnauthorized)
	})

	request := newPreflightRequest(
		"/v1/responses",
		"app://obsidian.md",
		"POST",
		"authorization, content-type",
	)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if reached || response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf(
			"preflight = reached:%t status:%d body:%q, want local 204",
			reached,
			response.Code,
			response.Body.String(),
		)
	}
	for name, want := range map[string]string{
		"Access-Control-Allow-Origin":      "app://obsidian.md",
		"Access-Control-Allow-Methods":     "POST, GET",
		"Access-Control-Allow-Headers":     "Authorization, Content-Type",
		"Access-Control-Expose-Headers":    "X-Request-Id",
		"Access-Control-Allow-Credentials": "true",
		"Access-Control-Max-Age":           "900",
		"X-Browser-Client":                 "enabled",
	} {
		if got := response.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	for _, token := range []string{"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"} {
		if !headerListContains(response.Header().Values("Vary"), token) {
			t.Errorf("Vary = %#v, want %q", response.Header().Values("Vary"), token)
		}
	}
	if response.Header().Get(debugHeaderAttempts) != "" || response.Header().Get(debugHeaderGroup) != "" {
		t.Fatalf("preflight initialized data-plane debug headers: %#v", response.Header())
	}
}

func TestDownstreamHeadersMiddlewareAppliesConfiguredRulesToActualResponses(t *testing.T) {
	handler := newDownstreamHeadersTestHandler(t, configuredBrowserAccessSettings())
	engine := gin.New()
	engine.Use(handler.DownstreamHeadersMiddleware())
	engine.POST("/v1/responses", func(context *gin.Context) {
		context.Header("X-Browser-Client", "upstream")
		context.Header("X-Upstream-Marker", "remove-me")
		context.Header("Vary", "Accept-Encoding")
		context.JSON(http.StatusUnauthorized, gin.H{"code": "invalid_access_key"})
	})

	request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	request.Header.Set("Origin", "app://obsidian.md")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("response status = %d, want 401", response.Code)
	}
	if got := response.Header().Values("X-Browser-Client"); len(got) != 1 || got[0] != "enabled" {
		t.Fatalf("X-Browser-Client = %#v, want configured override", got)
	}
	if got := response.Header().Get("X-Upstream-Marker"); got != "" {
		t.Fatalf("X-Upstream-Marker = %q, want removed", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "app://obsidian.md" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	for _, token := range []string{"Accept-Encoding", "Origin"} {
		if !headerListContains(response.Header().Values("Vary"), token) {
			t.Errorf("Vary = %#v, want %q", response.Header().Values("Vary"), token)
		}
	}
}

func TestDownstreamHeadersMiddlewareVariesExplicitOriginPolicyOnOriginMisses(t *testing.T) {
	handler := newDownstreamHeadersTestHandler(t, config.Settings{
		state.SettingCORS: map[string]any{
			"enabled":         true,
			"allowed_origins": []any{"app://obsidian.md"},
		},
	})
	engine := gin.New()
	engine.Use(handler.DownstreamHeadersMiddleware())
	engine.POST("/v1/responses", func(context *gin.Context) {
		context.Status(http.StatusUnauthorized)
	})

	for _, origin := range []string{"", "https://untrusted.example"} {
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)

		if response.Code != http.StatusUnauthorized {
			t.Fatalf("origin %q status = %d, want 401", origin, response.Code)
		}
		if !headerListContains(response.Header().Values("Vary"), "Origin") {
			t.Errorf("origin %q Vary = %#v, want Origin", origin, response.Header().Values("Vary"))
		}
		if response.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("origin %q exposed CORS headers: %#v", origin, response.Header())
		}
	}
}

func TestDownstreamHeadersMiddlewareRejectsDisallowedPreflightWithoutAffectingControlPlane(t *testing.T) {
	handler := newDownstreamHeadersTestHandler(t, configuredBrowserAccessSettings())
	engine := gin.New()
	engine.Use(handler.DownstreamHeadersMiddleware())
	engine.Any("/v1/responses", func(context *gin.Context) {
		context.Status(http.StatusUnauthorized)
	})
	engine.Any("/api/settings", func(context *gin.Context) {
		context.Status(http.StatusTeapot)
	})

	for _, request := range []*http.Request{
		newPreflightRequest("/v1/responses", "https://untrusted.example", "POST", "authorization"),
		newPreflightRequest("/v1/responses", "app://obsidian.md", "DELETE", "authorization"),
		newPreflightRequest("/v1/responses", "app://obsidian.md", "POST", "x-not-allowed"),
	} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized || response.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("disallowed preflight = %d headers=%#v, want downstream 401 without CORS", response.Code, response.Header())
		}
	}

	controlResponse := httptest.NewRecorder()
	engine.ServeHTTP(
		controlResponse,
		newPreflightRequest("/api/settings", "app://obsidian.md", "PUT", "authorization"),
	)
	if controlResponse.Code != http.StatusTeapot || controlResponse.Header().Get("X-Browser-Client") != "" {
		t.Fatalf("control preflight = %d headers=%#v, want untouched", controlResponse.Code, controlResponse.Header())
	}
}

func TestDownstreamHeadersMiddlewareExpandsWildcardAllowedHeaders(t *testing.T) {
	handler := newDownstreamHeadersTestHandler(t, config.Settings{
		state.SettingCORS: map[string]any{
			"enabled":         true,
			"allowed_origins": []any{"*"},
			"allowed_methods": []any{"POST"},
			"allowed_headers": []any{"*"},
		},
	})
	engine := gin.New()
	engine.Use(handler.DownstreamHeadersMiddleware())
	engine.Any("/v1/chat/completions", func(context *gin.Context) {
		context.Status(http.StatusUnauthorized)
	})

	response := httptest.NewRecorder()
	engine.ServeHTTP(
		response,
		newPreflightRequest(
			"/v1/chat/completions",
			"https://client.example",
			"POST",
			"authorization, x-client-version",
		),
	)

	if response.Code != http.StatusNoContent ||
		response.Header().Get("Access-Control-Allow-Origin") != "*" ||
		response.Header().Get("Access-Control-Allow-Headers") != "Authorization, X-Client-Version" {
		t.Fatalf("wildcard preflight = %d headers=%#v", response.Code, response.Header())
	}
}

func newDownstreamHeadersTestHandler(t *testing.T, settings config.Settings) *Handler {
	t.Helper()
	manager := state.NewManager()
	if _, err := manager.Publish(state.CompileInput{
		SystemSettings:  settings,
		ChannelRegistry: channel.NewRegistry(),
	}); err != nil {
		t.Fatalf("publish test settings: %v", err)
	}
	return &Handler{manager: manager}
}

func configuredBrowserAccessSettings() config.Settings {
	return config.Settings{
		state.SettingCORS: map[string]any{
			"enabled":           true,
			"allowed_origins":   []any{"app://obsidian.md"},
			"allowed_methods":   []any{"POST", "GET"},
			"allowed_headers":   []any{"Authorization", "Content-Type"},
			"exposed_headers":   []any{"X-Request-Id"},
			"allow_credentials": true,
			"max_age":           900,
		},
		state.SettingResponseHeaderRules: map[string]any{
			"set":    map[string]any{"X-Browser-Client": "enabled"},
			"remove": []any{"X-Upstream-Marker"},
		},
	}
}

func newPreflightRequest(path, origin, method, headers string) *http.Request {
	request := httptest.NewRequest(http.MethodOptions, path, nil)
	request.Header.Set("Origin", origin)
	request.Header.Set("Access-Control-Request-Method", method)
	if headers != "" {
		request.Header.Set("Access-Control-Request-Headers", headers)
	}
	return request
}

func headerListContains(values []string, target string) bool {
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), target) {
				return true
			}
		}
	}
	return false
}
