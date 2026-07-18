package dialect

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gpt-load/internal/health"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestGeminiExtractModel(t *testing.T) {
	value := NewGemini(http.DefaultClient)
	tests := []struct {
		name       string
		request    *ParsedRequest
		wantModel  string
		wantStream bool
		wantErr    bool
	}{
		{name: "generate", request: &ParsedRequest{Path: "/v1beta/models/gemini-2.5-pro:generateContent", Body: []byte("{")}, wantModel: "gemini-2.5-pro"},
		{name: "stream", request: &ParsedRequest{Path: "/v1beta/models/gemini-2.5-pro:streamGenerateContent"}, wantModel: "gemini-2.5-pro", wantStream: true},
		{name: "nil", wantErr: true},
		{name: "wrong prefix", request: &ParsedRequest{Path: "/models/gemini:generateContent"}, wantErr: true},
		{name: "wrong suffix", request: &ParsedRequest{Path: "/v1beta/models/gemini:embedContent"}, wantErr: true},
		{name: "empty model", request: &ParsedRequest{Path: "/v1beta/models/:generateContent"}, wantErr: true},
		{name: "nested slash", request: &ParsedRequest{Path: "/v1beta/models/vendor/gemini:generateContent"}, wantErr: true},
		{name: "boundary whitespace", request: &ParsedRequest{Path: "/v1beta/models/ gemini :generateContent"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model, stream, err := value.ExtractModel(test.request)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ExtractModel() = (%q, %t, nil), want error", model, stream)
				}
				return
			}
			if err != nil || model != test.wantModel || stream != test.wantStream {
				t.Fatalf("ExtractModel() = (%q, %t, %v), want (%q, %t)", model, stream, err, test.wantModel, test.wantStream)
			}
		})
	}
}

func TestGeminiBuildUpstreamURL(t *testing.T) {
	value := NewGemini(http.DefaultClient)

	streamURL, err := value.BuildUpstreamURL(
		"https://api.example.com/proxy/?alt=json&alt=proto&tenant=base",
		&ParsedRequest{
			Path:     "/v1beta/models/gemini-2.5-pro:streamGenerateContent",
			RawQuery: "alt=xml&trace=true",
		},
	)
	if err != nil {
		t.Fatalf("stream BuildUpstreamURL() error = %v", err)
	}
	assertGeminiURL(t, streamURL, "/proxy/v1beta/models/gemini-2.5-pro:streamGenerateContent", url.Values{
		"alt":    {"sse"},
		"tenant": {"base"},
		"trace":  {"true"},
	})

	nonStreamURL, err := value.BuildUpstreamURL(
		"https://api.example.com?alt=json&tenant=base",
		&ParsedRequest{Path: "/v1beta/models/gemini-2.5-flash:generateContent", RawQuery: "alt=xml&trace=true"},
	)
	if err != nil {
		t.Fatalf("non-stream BuildUpstreamURL() error = %v", err)
	}
	assertGeminiURL(t, nonStreamURL, "/v1beta/models/gemini-2.5-flash:generateContent", url.Values{
		"tenant": {"base"},
		"trace":  {"true"},
	})

	for _, request := range []*ParsedRequest{
		nil,
		{Path: "/v1beta/models/gemini:embedContent"},
		{Path: "/v1beta/models/:generateContent"},
	} {
		if got, err := value.BuildUpstreamURL("https://api.example.com", request); err == nil {
			t.Fatalf("BuildUpstreamURL(%#v) = %q, want error", request, got)
		}
	}
	if got, err := value.BuildUpstreamURL("ftp://api.example.com", &ParsedRequest{Path: "/v1beta/models/gemini:generateContent"}); err == nil {
		t.Fatalf("BuildUpstreamURL() = %q, want invalid base error", got)
	}
}

func assertGeminiURL(t *testing.T, rawURL, wantPath string, wantQuery url.Values) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL %q: %v", rawURL, err)
	}
	if parsed.Path != wantPath {
		t.Fatalf("path = %q, want %q", parsed.Path, wantPath)
	}
	if parsed.Query().Encode() != wantQuery.Encode() {
		t.Fatalf("query = %q, want %q", parsed.Query().Encode(), wantQuery.Encode())
	}
	if parsed.Query().Get("key") != "" {
		t.Fatalf("unexpected key query: %q", parsed.RawQuery)
	}
}

func TestGeminiCredentialAndProtocol(t *testing.T) {
	value := NewGemini(http.DefaultClient)
	if got := value.Protocol(); got != protocol.Gemini {
		t.Fatalf("Protocol() = %q, want %q", got, protocol.Gemini)
	}
	headers := make(http.Header)
	value.InjectCredential(headers, "upstream-secret")
	if got := headers.Get("X-Goog-Api-Key"); got != "upstream-secret" || len(headers) != 1 {
		t.Fatalf("headers = %#v", headers)
	}

	ApplyCredential(value, headers, "upstream-secret", state.HeaderRules{
		Set: map[string]string{"X-Goog-Api-Key": "override-${API_KEY}"},
	})
	if got := headers.Get("X-Goog-Api-Key"); got != "override-upstream-secret" {
		t.Fatalf("overridden X-Goog-Api-Key = %q", got)
	}
	ApplyCredential(value, headers, "upstream-secret", state.HeaderRules{
		Set:    map[string]string{"X-Goog-Api-Key": "must-be-removed"},
		Remove: []string{"x-goog-api-key"},
	})
	if got := headers.Get("X-Goog-Api-Key"); got != "" {
		t.Fatalf("removed X-Goog-Api-Key = %q", got)
	}
	value.InjectCredential(nil, "upstream-secret")
}

func TestGeminiClassifyStatus(t *testing.T) {
	value := NewGemini(http.DefaultClient)
	tests := []struct {
		name   string
		status int
		body   string
		want   health.ErrorClass
	}{
		{name: "success", status: http.StatusOK, body: `{"status":"api_key_invalid"}`, want: health.ErrorClassNonRetryable},
		{name: "unauthorized", status: http.StatusUnauthorized, want: health.ErrorClassRetryable},
		{name: "forbidden", status: http.StatusForbidden, want: health.ErrorClassRetryable},
		{name: "not found", status: http.StatusNotFound, want: health.ErrorClassRetryable},
		{name: "rate limited", status: http.StatusTooManyRequests, want: health.ErrorClassRetryable},
		{name: "server error", status: http.StatusServiceUnavailable, want: health.ErrorClassRetryable},
		{name: "invalid key marker", status: http.StatusBadRequest, body: `{"status":"api_key_invalid"}`, want: health.ErrorClassRetryable},
		{name: "permission marker", status: http.StatusBadRequest, body: `{"status":"permission_denied"}`, want: health.ErrorClassRetryable},
		{name: "quota marker", status: http.StatusBadRequest, body: `{"status":"resource_exhausted"}`, want: health.ErrorClassRetryable},
		{name: "model marker", status: http.StatusBadRequest, body: `{"message":"model not supported"}`, want: health.ErrorClassRetryable},
		{name: "invalid argument", status: http.StatusBadRequest, body: `{"status":"invalid_argument"}`, want: health.ErrorClassNonRetryable},
		{name: "ordinary unavailable text", status: http.StatusBadRequest, body: `{"message":"tool is unavailable for this request"}`, want: health.ErrorClassNonRetryable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := value.ClassifyStatus(test.status, []byte(test.body)); got != test.want {
				t.Fatalf("ClassifyStatus() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestGeminiListModelsUsesOneMaximumPageAndParsesNames(t *testing.T) {
	var requestCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestCount.Add(1)
		if request.Method != http.MethodGet || request.URL.Path != "/prefix/v1beta/models" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if got := request.URL.Query()["pageSize"]; len(got) != 1 || got[0] != "1000" {
			t.Errorf("pageSize = %#v, want one 1000", got)
		}
		if got := request.URL.Query().Get("tenant"); got != "one" {
			t.Errorf("tenant = %q", got)
		}
		if got := request.Header.Get("X-Goog-Api-Key"); got != "upstream-secret" {
			t.Errorf("X-Goog-Api-Key = %q", got)
		}
		_, _ = writer.Write([]byte(`{
			"models":[
				{"name":"models/gemini-2.5-pro","supportedGenerationMethods":[]},
				{"name":"compatible-bare-name"},
				{"name":"models/models/vendor-model"},
				{"name":"models/"}
			],
			"nextPageToken":"ignored-token"
		}`))
	}))
	defer server.Close()

	models, err := NewGemini(server.Client()).ListModels(
		context.Background(),
		server.URL+"/prefix?tenant=one&pageSize=1&pageSize=2",
		"upstream-secret",
		state.HeaderRules{},
	)
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if strings.Join(models, ",") != "gemini-2.5-pro,compatible-bare-name,models/vendor-model" {
		t.Fatalf("models = %#v", models)
	}
	if got := requestCount.Load(); got != 1 {
		t.Fatalf("request count = %d, want 1", got)
	}
}

func TestGeminiListModelsRulesAndFailures(t *testing.T) {
	t.Run("rules override and remove", func(t *testing.T) {
		requests := atomic.Int64{}
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			call := requests.Add(1)
			switch call {
			case 1:
				if got := request.Header.Get("X-Goog-Api-Key"); got != "override-upstream-secret" {
					t.Errorf("overridden X-Goog-Api-Key = %q", got)
				}
			case 2:
				if got := request.Header.Get("X-Goog-Api-Key"); got != "" {
					t.Errorf("removed X-Goog-Api-Key = %q", got)
				}
			}
			_, _ = writer.Write([]byte(`{"models":[]}`))
		}))
		defer server.Close()
		models, err := NewGemini(server.Client()).ListModels(
			context.Background(), server.URL, "upstream-secret",
			state.HeaderRules{Set: map[string]string{"X-Goog-Api-Key": "override-${API_KEY}"}},
		)
		if err != nil || models == nil || len(models) != 0 {
			t.Fatalf("override ListModels() = %#v, %v", models, err)
		}
		_, err = NewGemini(server.Client()).ListModels(
			context.Background(), server.URL, "upstream-secret",
			state.HeaderRules{Remove: []string{"x-goog-api-key"}},
		)
		if err != nil {
			t.Fatalf("remove ListModels() error = %v", err)
		}
	})

	t.Run("non-success does not expose body or key", func(t *testing.T) {
		const secret = "distinctive-gemini-secret"
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusForbidden)
			_, _ = writer.Write([]byte(`{"error":"` + secret + ` in body"}`))
		}))
		defer server.Close()
		_, err := NewGemini(server.Client()).ListModels(context.Background(), server.URL, secret, state.HeaderRules{})
		if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), server.URL) {
			t.Fatalf("ListModels() error = %v", err)
		}
	})

	t.Run("malformed JSON is contextual and key free", func(t *testing.T) {
		const secret = "distinctive-gemini-secret"
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte(`{"models":[`))
		}))
		defer server.Close()
		_, err := NewGemini(server.Client()).ListModels(
			context.Background(), server.URL+"?token=query-secret", secret, state.HeaderRules{},
		)
		if err == nil || !strings.Contains(err.Error(), "decode Gemini model list") {
			t.Fatalf("ListModels() error = %v", err)
		}
		for _, forbidden := range []string{secret, "query-secret", server.URL} {
			if strings.Contains(err.Error(), forbidden) {
				t.Fatalf("error exposes %q: %v", forbidden, err)
			}
		}
	})

	t.Run("caller context stops request", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			<-request.Context().Done()
		}))
		defer server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		_, err := NewGemini(server.Client()).ListModels(ctx, server.URL, "secret", state.HeaderRules{})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ListModels() error = %v, want deadline exceeded", err)
		}
	})
}
