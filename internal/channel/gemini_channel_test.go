package channel

import (
	"context"
	"gpt-load/internal/models"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestGeminiBuildUpstreamURLPreservesDeveloperAPIPath(t *testing.T) {
	ch := newTestGeminiChannel(t, "https://generativelanguage.googleapis.com")
	originalURL := mustParseURL(t, "http://localhost:3001/proxy/gemini/v1beta/models/gemini-2.5-pro:generateContent?key=proxy-key")

	got, err := ch.BuildUpstreamURL(originalURL, "gemini")
	if err != nil {
		t.Fatalf("BuildUpstreamURL returned error: %v", err)
	}

	want := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-pro:generateContent?key=proxy-key"
	if got != want {
		t.Fatalf("BuildUpstreamURL() = %q, want %q", got, want)
	}
}

func TestGeminiBuildUpstreamURLConvertsNativePathForVertexPublisherBase(t *testing.T) {
	ch := newTestGeminiChannel(t, "https://aiplatform.googleapis.com/v1/publishers/google")
	originalURL := mustParseURL(t, "http://localhost:3001/proxy/gemini/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse&key=proxy-key")

	got, err := ch.BuildUpstreamURL(originalURL, "gemini")
	if err != nil {
		t.Fatalf("BuildUpstreamURL returned error: %v", err)
	}

	want := "https://aiplatform.googleapis.com/v1/publishers/google/models/gemini-2.5-pro:streamGenerateContent?alt=sse&key=proxy-key"
	if got != want {
		t.Fatalf("BuildUpstreamURL() = %q, want %q", got, want)
	}
}

func TestGeminiBuildUpstreamURLConvertsNativePathForBareAiplatformBase(t *testing.T) {
	ch := newTestGeminiChannel(t, "https://aiplatform.googleapis.com/")
	originalURL := mustParseURL(t, "http://localhost:3001/proxy/gemini/v1beta/models/gemini-2.5-pro:generateContent?key=proxy-key")

	got, err := ch.BuildUpstreamURL(originalURL, "gemini")
	if err != nil {
		t.Fatalf("BuildUpstreamURL returned error: %v", err)
	}

	want := "https://aiplatform.googleapis.com/v1/publishers/google/models/gemini-2.5-pro:generateContent?key=proxy-key"
	if got != want {
		t.Fatalf("BuildUpstreamURL() = %q, want %q", got, want)
	}
}

func TestGeminiBuildUpstreamURLDoesNotDuplicateVertexPublisherPath(t *testing.T) {
	ch := newTestGeminiChannel(t, "https://aiplatform.googleapis.com/v1/publishers/google")
	originalURL := mustParseURL(t, "http://localhost:3001/proxy/gemini/v1/publishers/google/models/gemini-2.5-flash:generateContent")

	got, err := ch.BuildUpstreamURL(originalURL, "gemini")
	if err != nil {
		t.Fatalf("BuildUpstreamURL returned error: %v", err)
	}

	want := "https://aiplatform.googleapis.com/v1/publishers/google/models/gemini-2.5-flash:generateContent"
	if got != want {
		t.Fatalf("BuildUpstreamURL() = %q, want %q", got, want)
	}
}

func TestGeminiApplyModelRedirectWorksWithVertexPublisherPath(t *testing.T) {
	ch := newTestGeminiChannel(t, "https://aiplatform.googleapis.com/v1/publishers/google")
	req := &http.Request{
		URL: mustParseURL(t, "https://aiplatform.googleapis.com/v1/publishers/google/models/source-model:generateContent"),
	}
	group := &models.Group{
		Name:             "gemini",
		ModelRedirectMap: map[string]string{"source-model": "target-model"},
	}
	body := []byte(`{"contents":[]}`)

	gotBody, err := ch.ApplyModelRedirect(req, body, group)
	if err != nil {
		t.Fatalf("ApplyModelRedirect returned error: %v", err)
	}
	if string(gotBody) != string(body) {
		t.Fatalf("ApplyModelRedirect body = %s, want %s", gotBody, body)
	}

	wantPath := "/v1/publishers/google/models/target-model:generateContent"
	if req.URL.Path != wantPath {
		t.Fatalf("redirected path = %q, want %q", req.URL.Path, wantPath)
	}
}

func TestGeminiValidateKeyUsesVertexPublisherPath(t *testing.T) {
	var gotPath string
	var gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.URL.Query().Get("key")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	ch := newTestGeminiChannel(t, server.URL+"/v1/publishers/google")
	ch.HTTPClient = server.Client()
	ch.TestModel = "gemini-test"

	ok, err := ch.ValidateKey(context.Background(), &models.APIKey{KeyValue: "secret-key"}, &models.Group{Name: "gemini"})
	if err != nil {
		t.Fatalf("ValidateKey returned error: %v", err)
	}
	if !ok {
		t.Fatal("ValidateKey returned false, want true")
	}

	wantPath := "/v1/publishers/google/models/gemini-test:generateContent"
	if gotPath != wantPath {
		t.Fatalf("validation path = %q, want %q", gotPath, wantPath)
	}
	if gotKey != "secret-key" {
		t.Fatalf("validation key = %q, want %q", gotKey, "secret-key")
	}
}

func newTestGeminiChannel(t *testing.T, upstream string) *GeminiChannel {
	t.Helper()

	upstreamURL := mustParseURL(t, upstream)
	return &GeminiChannel{
		BaseChannel: &BaseChannel{
			Name:       "gemini",
			Upstreams:  []UpstreamInfo{{URL: upstreamURL, Weight: 1}},
			HTTPClient: http.DefaultClient,
			TestModel:  "gemini-2.0-flash-lite",
		},
	}
}

func mustParseURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("failed to parse url %q: %v", rawURL, err)
	}
	return parsed
}
