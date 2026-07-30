package webui

import (
	"bytes"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/httproute"
)

func TestServerServesSameIndexForExplicitPageRoutes(t *testing.T) {
	const wantCSP = "default-src 'self'; script-src 'self'; style-src 'self'; " +
		"style-src-elem 'self'; style-src-attr 'unsafe-inline'; " +
		"img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; " +
		"base-uri 'self'; frame-ancestors 'none'; form-action 'self'"

	server := newServer(fstest.MapFS{
		"dist/index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>test index</title>")},
	}, "dist")
	engine := testEngine(server)

	var firstBody string
	for _, target := range []string{
		"/", "/login", "/import", "/groups/42", "/access-keys", "/monitor?tab=logs", "/settings",
		"/settings/model-prices",
	} {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", target, recorder.Code)
		}
		if firstBody == "" {
			firstBody = recorder.Body.String()
		}
		if recorder.Body.String() != firstBody {
			t.Fatalf("GET %s body differs from index", target)
		}
		if got := recorder.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("GET %s Cache-Control = %q, want no-cache", target, got)
		}
		if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Fatalf("GET %s Content-Type = %q, want HTML", target, got)
		}
		if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("GET %s X-Content-Type-Options = %q", target, got)
		}
		if got := recorder.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Fatalf("GET %s X-Frame-Options = %q", target, got)
		}
		got := recorder.Header().Get("Content-Security-Policy")
		if strings.Contains(got, "style-src 'self' 'unsafe-inline'") {
			t.Fatalf("GET %s CSP allows broad inline styles: %q", target, got)
		}
		if got != wantCSP {
			t.Fatalf("GET %s CSP = %q, want %q", target, got, wantCSP)
		}
	}
}

func TestServerServesModelPricesDeepLinkWithoutCatchingUnknownSettingsPaths(t *testing.T) {
	engine := testEngine(newServer(fstest.MapFS{
		"dist/index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>prices</title>")},
	}, "dist"))

	modelPrices := httptest.NewRecorder()
	engine.ServeHTTP(
		modelPrices,
		httptest.NewRequest(http.MethodGet, "/settings/model-prices", nil),
	)
	if modelPrices.Code != http.StatusOK || !strings.Contains(modelPrices.Body.String(), "<title>prices</title>") {
		t.Fatalf("GET /settings/model-prices = %d %q, want embedded index", modelPrices.Code, modelPrices.Body.String())
	}

	unknown := httptest.NewRecorder()
	engine.ServeHTTP(
		unknown,
		httptest.NewRequest(http.MethodGet, "/settings/model-prices/unknown", nil),
	)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("GET unknown nested settings path = %d, want 404", unknown.Code)
	}
}

func TestServerFallbackReturnsNotFoundForUnknownRequests(t *testing.T) {
	server := newServer(fstest.MapFS{
		"dist/index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>fallback</title>")},
	}, "dist")
	engine := testEngine(server)

	browserPage := httptest.NewRecorder()
	browserPageRequest := httptest.NewRequest(http.MethodGet, "/phase-2-unknown-route", nil)
	browserPageRequest.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9")
	engine.ServeHTTP(browserPage, browserPageRequest)
	if browserPage.Code != http.StatusNotFound ||
		!strings.Contains(browserPage.Body.String(), "<title>fallback</title>") {
		t.Fatalf(
			"unknown browser page = %d %q, want 404 SPA index",
			browserPage.Code,
			browserPage.Body.String(),
		)
	}

	for _, testCase := range []struct {
		name   string
		method string
		target string
		accept string
	}{
		{name: "non HTML client", method: http.MethodGet, target: "/unknown"},
		{
			name:   "HTML explicitly unacceptable",
			method: http.MethodGet,
			target: "/unknown",
			accept: "text/html;q=0, application/json",
		},
		{name: "non GET browser request", method: http.MethodPost, target: "/unknown", accept: "text/html"},
		{name: "control namespace", method: http.MethodGet, target: "/api/unknown", accept: "text/html"},
		{name: "OpenAI namespace", method: http.MethodGet, target: "/v1/unknown", accept: "text/html"},
		{name: "Gemini namespace", method: http.MethodGet, target: "/v1beta/unknown", accept: "text/html"},
		{name: "health namespace", method: http.MethodGet, target: "/health/unknown", accept: "text/html"},
		{
			name:   "Chrome DevTools workspace probe",
			method: http.MethodGet,
			target: "/.well-known/appspecific/com.chrome.devtools.json",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(testCase.method, testCase.target, nil)
			if testCase.accept != "" {
				request.Header.Set("Accept", testCase.accept)
			}
			engine.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusNotFound {
				t.Fatalf(
					"%s %s = %d %q, want 404",
					testCase.method,
					testCase.target,
					recorder.Code,
					recorder.Body.String(),
				)
			}
		})
	}
}

func TestServerUsesCompileFallbackWhenIndexIsMissing(t *testing.T) {
	server := newServer(fstest.MapFS{
		"dist/assets/embed-placeholder.txt": &fstest.MapFile{Data: []byte("marker")},
	}, "dist")
	recorder := httptest.NewRecorder()

	testEngine(server).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "前端资源尚未构建") {
		t.Fatalf("fallback response = %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestServerServesAssetsWithImmutableCaching(t *testing.T) {
	server := newServer(fstest.MapFS{
		"dist/assets/app.js":  &fstest.MapFile{Data: []byte("export default true")},
		"dist/assets/app.css": &fstest.MapFile{Data: []byte("body { color: black; }")},
		"dist/assets/app":     &fstest.MapFile{Data: []byte{0x00, 0x01}},
	}, "dist")
	engine := testEngine(server)

	for _, testCase := range []struct {
		name           string
		target         string
		wantMediaTypes []string
	}{
		{name: "javascript", target: "/assets/app.js", wantMediaTypes: []string{"text/javascript", "application/javascript"}},
		{name: "stylesheet", target: "/assets/app.css", wantMediaTypes: []string{"text/css"}},
		{name: "no extension", target: "/assets/app", wantMediaTypes: []string{"application/octet-stream"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, testCase.target, nil))

			if recorder.Code != http.StatusOK {
				t.Fatalf("asset status = %d, want 200", recorder.Code)
			}
			contentType := recorder.Header().Get("Content-Type")
			mediaType, _, err := mime.ParseMediaType(contentType)
			if err != nil {
				t.Fatalf("parse Content-Type %q: %v", contentType, err)
			}
			matches := false
			for _, want := range testCase.wantMediaTypes {
				if mediaType == want {
					matches = true
					break
				}
			}
			if !matches {
				t.Fatalf("Content-Type media type = %q, want one of %v", mediaType, testCase.wantMediaTypes)
			}
			if got := recorder.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
				t.Fatalf("Cache-Control = %q", got)
			}
			if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("X-Content-Type-Options = %q", got)
			}
		})
	}
}

func TestServerServesThemeBootstrapAsExplicitRootAsset(t *testing.T) {
	server := newServer(fstest.MapFS{
		"dist/index.html":         &fstest.MapFile{Data: []byte("<!doctype html>")},
		"dist/theme-bootstrap.js": &fstest.MapFile{Data: []byte("document.documentElement.dataset.theme = 'dark'")},
	}, "dist")
	recorder := httptest.NewRecorder()

	testEngine(server).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/theme-bootstrap.js", nil),
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("theme bootstrap status = %d, want 200", recorder.Code)
	}
	mediaType, _, err := mime.ParseMediaType(recorder.Header().Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse theme bootstrap Content-Type: %v", err)
	}
	if mediaType != "text/javascript" && mediaType != "application/javascript" {
		t.Fatalf("theme bootstrap Content-Type = %q, want JavaScript", mediaType)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("theme bootstrap Cache-Control = %q, want no-cache", got)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("theme bootstrap X-Content-Type-Options = %q, want nosniff", got)
	}
	if !strings.Contains(recorder.Body.String(), "dataset.theme") {
		t.Fatalf("theme bootstrap body = %q", recorder.Body.String())
	}
}

func TestServerServesFaviconAsExplicitRootAsset(t *testing.T) {
	want := []byte{0x00, 0x00, 0x01, 0x00}
	server := newServer(fstest.MapFS{
		"dist/index.html":  &fstest.MapFile{Data: []byte("<!doctype html>")},
		"dist/favicon.ico": &fstest.MapFile{Data: want},
	}, "dist")
	recorder := httptest.NewRecorder()

	testEngine(server).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/favicon.ico", nil),
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("favicon status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "image/vnd.microsoft.icon" {
		t.Fatalf("favicon Content-Type = %q, want image/vnd.microsoft.icon", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("favicon Cache-Control = %q, want no-cache", got)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("favicon X-Content-Type-Options = %q, want nosniff", got)
	}
	if !bytes.Equal(recorder.Body.Bytes(), want) {
		t.Fatalf("favicon body = %v, want %v", recorder.Body.Bytes(), want)
	}
}

func TestServerDoesNotExposeFilesOutsideAssets(t *testing.T) {
	const (
		indexSecret = "outer-index-secret"
		fileSecret  = "outer-file-secret"
	)
	engine := testEngine(newServer(fstest.MapFS{
		"dist/index.html": &fstest.MapFile{Data: []byte("<!doctype html>" + indexSecret)},
		"dist/secret":     &fstest.MapFile{Data: []byte(fileSecret)},
	}, "dist"))

	for _, testCase := range []struct {
		name   string
		target string
	}{
		{name: "parent traversal", target: "/assets/../index.html"},
		{name: "encoded parent traversal", target: "/assets/%2e%2e/secret"},
		{name: "double slash absolute shape", target: "/assets//secret"},
		{name: "encoded absolute shape", target: "/assets/%2Fsecret"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, testCase.target, nil))

			if recorder.Code == http.StatusOK {
				t.Fatalf("GET %s status = 200, want rejection or redirect", testCase.target)
			}
			if body := recorder.Body.String(); strings.Contains(body, indexSecret) || strings.Contains(body, fileSecret) {
				t.Fatalf("GET %s leaked content outside assets: %q", testCase.target, body)
			}
		})
	}
}

func TestServerDoesNotHandleBackendOrUnknownRoutes(t *testing.T) {
	engine := testEngine(newServer(fstest.MapFS{
		"dist/index.html": &fstest.MapFile{Data: []byte("<!doctype html>")},
	}, "dist"))

	for _, target := range []string{
		"/api/unknown", "/v1/models", "/unknown", "/settings/unknown", "/assets/missing.js",
	} {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404", target, recorder.Code)
		}
		if strings.Contains(strings.ToLower(recorder.Body.String()), "<!doctype html") {
			t.Fatalf("GET %s unexpectedly returned the UI index", target)
		}
	}
}

func testEngine(server *Server) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.RedirectTrailingSlash = false
	registry, err := httproute.NewRegistry(server.HTTPModule())
	if err != nil {
		panic(err)
	}
	if err := registry.Bind(engine); err != nil {
		panic(err)
	}
	return engine
}
