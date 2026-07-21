package dialect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		{name: "configured models prefix is not corrected", request: &ParsedRequest{Path: "/v1beta/models/models/custom:generateContent"}, wantErr: true},
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
		want   health.FailureCategory
	}{
		{name: "success", status: http.StatusOK, body: `{"status":"api_key_invalid"}`, want: health.FailureCategoryOK},
		{name: "unauthorized", status: http.StatusUnauthorized, want: health.FailureCategoryInvalidKey},
		{name: "forbidden", status: http.StatusForbidden, want: health.FailureCategoryInvalidKey},
		{name: "not found", status: http.StatusNotFound, want: health.FailureCategoryModelUnavailable},
		{name: "rate limited", status: http.StatusTooManyRequests, want: health.FailureCategoryRateLimited},
		{name: "server error", status: http.StatusServiceUnavailable, want: health.FailureCategoryUpstreamHostError},
		{name: "redirect is ambiguous", status: http.StatusTemporaryRedirect, want: health.FailureCategoryAmbiguous},
		{name: "zero status with marker is ambiguous", status: 0, body: `{"status":"api_key_invalid"}`, want: health.FailureCategoryAmbiguous},
		{name: "invalid key marker", status: http.StatusBadRequest, body: `{"status":"api_key_invalid"}`, want: health.FailureCategoryInvalidKey},
		{name: "permission marker", status: http.StatusBadRequest, body: `{"status":"permission_denied"}`, want: health.FailureCategoryInvalidKey},
		{name: "quota marker", status: http.StatusBadRequest, body: `{"status":"resource_exhausted"}`, want: health.FailureCategoryRateLimited},
		{name: "model marker", status: http.StatusBadRequest, body: `{"message":"model not supported"}`, want: health.FailureCategoryModelUnavailable},
		{name: "invalid argument", status: http.StatusBadRequest, body: `{"status":"invalid_argument"}`, want: health.FailureCategoryClientError},
		{name: "ordinary unavailable text", status: http.StatusBadRequest, body: `{"message":"tool is unavailable for this request"}`, want: health.FailureCategoryClientError},
		{name: "ordinary client error", status: http.StatusBadRequest, body: `{"error":"invalid input"}`, want: health.FailureCategoryClientError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := value.ClassifyStatus(test.status, []byte(test.body)); got != test.want {
				t.Fatalf("ClassifyStatus() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestGeminiListModelsPaginatesPreservesInputsAndParsesNames(t *testing.T) {
	var requestCount atomic.Int64
	const pageToken = " token/+= ? "
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		pageNumber := requestCount.Add(1)
		if request.Method != http.MethodGet || request.URL.Path != "/prefix/v1beta/models" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if got := request.URL.Query()["pageSize"]; len(got) != 1 || got[0] != "1000" {
			t.Errorf("pageSize = %#v, want one 1000", got)
		}
		if got := request.URL.Query().Get("tenant"); got != "one" {
			t.Errorf("tenant = %q, want one", got)
		}
		if got := request.URL.Query().Get("fixed"); got != "preserved" {
			t.Errorf("fixed = %q, want preserved", got)
		}
		if got := request.Header.Get("X-Goog-Api-Key"); got != "upstream-secret" {
			t.Errorf("X-Goog-Api-Key = %q", got)
		}
		if got := request.Header.Get("X-Discovery-Rule"); got != "applied-rule" {
			t.Errorf("X-Discovery-Rule = %q, want applied-rule", got)
		}
		switch pageNumber {
		case 1:
			if _, exists := request.URL.Query()["pageToken"]; exists {
				t.Errorf("page 1 retained stale pageToken: %q", request.URL.Query().Get("pageToken"))
			}
			_, _ = writer.Write([]byte(`{
				"models":[
					{"name":"models/gemini-versioned","baseModelId":"gemini-base"},
					{"name":"models/models/vendor-model"},
					{"name":"shared"},
					{"baseModelId":"base-only"}
				],
				"nextPageToken":"` + pageToken + `"
			}`))
		case 2:
			if got := request.URL.Query().Get("pageToken"); got != pageToken {
				t.Errorf("page 2 pageToken = %q, want byte-for-byte %q", got, pageToken)
			}
			_, _ = writer.Write([]byte(`{
				"models":[
					{"name":"models/shared"},
					{"name":"models/gemini-next"},
					{"name":"models/"}
				]
			}`))
		default:
			t.Errorf("unexpected page %d", pageNumber)
			writer.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	models, err := NewGemini(server.Client()).ListModels(
		context.Background(),
		server.URL+"/prefix?tenant=one&fixed=preserved&pageSize=1&pageSize=2&pageToken=stale",
		"upstream-secret",
		state.HeaderRules{Set: map[string]string{"X-Discovery-Rule": "applied-rule"}},
	)
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if strings.Join(models, ",") != "gemini-versioned,models/vendor-model,shared,gemini-next" {
		t.Fatalf("models = %#v", models)
	}
	if got := requestCount.Load(); got != 2 {
		t.Fatalf("request count = %d, want 2", got)
	}
}

func TestGeminiListModelsForcesIdentity(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.Header.Values("Accept-Encoding"); len(got) != 1 || got[0] != "identity" {
			t.Fatalf("Accept-Encoding = %#v, want one identity", got)
		}
		return geminiTestResponse(http.StatusOK, `{"models":[]}`), nil
	})}
	models, err := NewGemini(client).ListModels(
		context.Background(),
		"https://api.example.com",
		"secret",
		state.HeaderRules{
			Set:    map[string]string{"Accept-Encoding": "gzip"},
			Remove: []string{"Accept-Encoding"},
		},
	)
	if err != nil || models == nil {
		t.Fatalf("ListModels() = %#v, %v", models, err)
	}
}

func TestGeminiListModelsRejectsBlankOrRepeatedPageToken(t *testing.T) {
	t.Run("blank page token", func(t *testing.T) {
		var requestCount atomic.Int64
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requestCount.Add(1)
			return geminiTestResponse(http.StatusOK, `{"models":[{"name":"models/gemini-a"}],"nextPageToken":" \t "}`), nil
		})}
		models, err := NewGemini(client).ListModels(context.Background(), "https://api.example.com", "secret", state.HeaderRules{})
		if err == nil || models != nil {
			t.Fatalf("ListModels() = %#v, %v, want nil models and error", models, err)
		}
		if got := requestCount.Load(); got != 1 {
			t.Fatalf("request count = %d, want 1", got)
		}
	})

	t.Run("repeated page token", func(t *testing.T) {
		var requestCount atomic.Int64
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requestCount.Add(1)
			return geminiTestResponse(http.StatusOK, `{"models":[{"name":"models/gemini-a"}],"nextPageToken":"same-token"}`), nil
		})}
		models, err := NewGemini(client).ListModels(context.Background(), "https://api.example.com", "secret", state.HeaderRules{})
		if err == nil || models != nil {
			t.Fatalf("ListModels() = %#v, %v, want nil models and error", models, err)
		}
		if got := requestCount.Load(); got != 2 {
			t.Fatalf("request count = %d, want 2", got)
		}
	})
}

func TestGeminiListModelsRejectsPageAndUniqueModelLimits(t *testing.T) {
	t.Run("page 100 may end", func(t *testing.T) {
		var requestCount atomic.Int64
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			pageNumber := requestCount.Add(1)
			nextPageToken := ""
			if pageNumber < maxModelListPages {
				nextPageToken = fmt.Sprintf("token-%d", pageNumber)
			}
			return geminiTestResponse(http.StatusOK, fmt.Sprintf(
				`{"models":[{"name":"models/model-%d"}],"nextPageToken":%q}`,
				pageNumber, nextPageToken,
			)), nil
		})}
		models, err := NewGemini(client).ListModels(context.Background(), "https://api.example.com", "secret", state.HeaderRules{})
		if err != nil || len(models) != maxModelListPages {
			t.Fatalf("ListModels() = %d models, %v, want %d models", len(models), err, maxModelListPages)
		}
		if got := requestCount.Load(); got != maxModelListPages {
			t.Fatalf("request count = %d, want %d", got, maxModelListPages)
		}
	})

	t.Run("page 100 may not continue", func(t *testing.T) {
		var requestCount atomic.Int64
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			pageNumber := requestCount.Add(1)
			return geminiTestResponse(http.StatusOK, fmt.Sprintf(
				`{"models":[{"name":"models/model-%d"}],"nextPageToken":"token-%d"}`,
				pageNumber, pageNumber,
			)), nil
		})}
		models, err := NewGemini(client).ListModels(context.Background(), "https://api.example.com", "secret", state.HeaderRules{})
		if err == nil || models != nil {
			t.Fatalf("ListModels() = %#v, %v, want nil models and error", models, err)
		}
		if got := requestCount.Load(); got != maxModelListPages {
			t.Fatalf("request count = %d, want %d", got, maxModelListPages)
		}
	})

	for _, test := range []struct {
		name         string
		modelCount   int
		continuation bool
		wantErr      bool
	}{
		{name: "exact unique maximum may end", modelCount: maxUniqueModelListEntries},
		{name: "exact unique maximum may not continue", modelCount: maxUniqueModelListEntries, continuation: true, wantErr: true},
		{name: "more than unique maximum is rejected", modelCount: maxUniqueModelListEntries + 1, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := mustGeminiTestPayload(t, test.modelCount, test.continuation)
			var requestCount atomic.Int64
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				requestCount.Add(1)
				return geminiTestResponse(http.StatusOK, body), nil
			})}
			models, err := NewGemini(client).ListModels(context.Background(), "https://api.example.com", "secret", state.HeaderRules{})
			if test.wantErr {
				if err == nil || models != nil {
					t.Fatalf("ListModels() = %d models, %v, want nil models and error", len(models), err)
				}
			} else if err != nil || len(models) != test.modelCount {
				t.Fatalf("ListModels() = %d models, %v, want %d models", len(models), err, test.modelCount)
			}
			if got := requestCount.Load(); got != 1 {
				t.Fatalf("request count = %d, want 1", got)
			}
		})
	}
}

func TestGeminiListModelsReturnsNoPartialResultAfterLaterPageFailure(t *testing.T) {
	for _, test := range []struct {
		name       string
		secondPage func() (*http.Response, error)
	}{
		{name: "transport error", secondPage: func() (*http.Response, error) {
			return nil, errors.New("later transport failure")
		}},
		{name: "non-2xx", secondPage: func() (*http.Response, error) {
			return geminiTestResponse(http.StatusBadGateway, `{"error":"later failure"}`), nil
		}},
		{name: "malformed JSON", secondPage: func() (*http.Response, error) {
			return geminiTestResponse(http.StatusOK, `{"models":[`), nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requestCount atomic.Int64
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				if requestCount.Add(1) == 1 {
					return geminiTestResponse(http.StatusOK, `{"models":[{"name":"models/first-page-model"}],"nextPageToken":"next"}`), nil
				}
				return test.secondPage()
			})}
			models, err := NewGemini(client).ListModels(context.Background(), "https://api.example.com", "secret", state.HeaderRules{})
			if err == nil || models != nil {
				t.Fatalf("ListModels() = %#v, %v, want nil models and later-page error", models, err)
			}
			if got := requestCount.Load(); got != 2 {
				t.Fatalf("request count = %d, want 2", got)
			}
		})
	}
}

func TestGeminiListModelsClosesBodyBeforeRequestingNextPage(t *testing.T) {
	firstBody := &trackedReadCloser{Reader: strings.NewReader(`{"models":[{"name":"models/first"}],"nextPageToken":"next"}`)}
	secondBody := &trackedReadCloser{Reader: strings.NewReader(`{"models":[{"name":"models/second"}]}`)}
	var requestCount atomic.Int64
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		switch requestCount.Add(1) {
		case 1:
			return &http.Response{StatusCode: http.StatusOK, Body: firstBody, Header: make(http.Header)}, nil
		case 2:
			if !firstBody.closed.Load() {
				return nil, errors.New("first page body is still open")
			}
			return &http.Response{StatusCode: http.StatusOK, Body: secondBody, Header: make(http.Header)}, nil
		default:
			return nil, errors.New("unexpected extra request")
		}
	})}
	models, err := NewGemini(client).ListModels(context.Background(), "https://api.example.com", "secret", state.HeaderRules{})
	if err != nil || strings.Join(models, ",") != "first,second" {
		t.Fatalf("ListModels() = %#v, %v, want first,second", models, err)
	}
	if !secondBody.closed.Load() {
		t.Fatal("last page body was not closed")
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

func geminiTestResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func mustGeminiTestPayload(t *testing.T, modelCount int, continuation bool) string {
	t.Helper()
	type item struct {
		Name string `json:"name"`
	}
	payload := struct {
		Models        []item `json:"models"`
		NextPageToken string `json:"nextPageToken,omitempty"`
	}{
		Models: make([]item, modelCount),
	}
	if continuation {
		payload.NextPageToken = "next-token"
	}
	for index := range payload.Models {
		payload.Models[index].Name = fmt.Sprintf("models/model-%06d", index)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal Gemini test payload: %v", err)
	}
	return string(encoded)
}
