package webui

import (
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

func TestServerServesSameIndexForExplicitPageRoutes(t *testing.T) {
	const wantCSP = "default-src 'self'; script-src 'self'; style-src 'self'; " +
		"img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; " +
		"base-uri 'self'; frame-ancestors 'none'; form-action 'self'"

	server := newServer(fstest.MapFS{
		"dist/index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>test index</title>")},
	}, "dist")
	engine := testEngine(server)

	var firstBody string
	for _, target := range []string{
		"/", "/login", "/import", "/groups/42", "/access-keys", "/monitor?tab=requests", "/settings",
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
		if got := recorder.Header().Get("Content-Security-Policy"); got != wantCSP {
			t.Fatalf("GET %s CSP = %q, want %q", target, got, wantCSP)
		}
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

	for _, target := range []string{"/api/unknown", "/v1/models", "/unknown", "/assets/missing.js"} {
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
	server.RegisterRoutes(engine)
	return engine
}
