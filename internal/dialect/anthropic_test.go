package dialect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gpt-load/internal/health"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestAnthropicExtractModel(t *testing.T) {
	value := NewAnthropic(http.DefaultClient)
	tests := []struct {
		name       string
		request    *ParsedRequest
		wantModel  string
		wantStream bool
		wantErr    bool
	}{
		{name: "non-stream", request: &ParsedRequest{Body: []byte(`{"model":"claude-3-5-sonnet","stream":false}`)}, wantModel: "claude-3-5-sonnet"},
		{name: "stream", request: &ParsedRequest{Body: []byte(`{"model":"claude-3-5-sonnet","stream":true}`)}, wantModel: "claude-3-5-sonnet", wantStream: true},
		{name: "nil", wantErr: true},
		{name: "invalid JSON", request: &ParsedRequest{Body: []byte("{")}, wantErr: true},
		{name: "missing", request: &ParsedRequest{Body: []byte(`{}`)}, wantErr: true},
		{name: "blank", request: &ParsedRequest{Body: []byte(`{"model":"  "}`)}, wantErr: true},
		{name: "boundary whitespace", request: &ParsedRequest{Body: []byte(`{"model":" claude-3 "}`)}, wantErr: true},
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
			if err != nil {
				t.Fatalf("ExtractModel() error = %v", err)
			}
			if model != test.wantModel || stream != test.wantStream {
				t.Fatalf("ExtractModel() = (%q, %t), want (%q, %t)", model, stream, test.wantModel, test.wantStream)
			}
		})
	}
}

func TestAnthropicCredentialAndProtocol(t *testing.T) {
	value := NewAnthropic(http.DefaultClient)
	if got := value.Protocol(); got != protocol.Anthropic {
		t.Fatalf("Protocol() = %q, want %q", got, protocol.Anthropic)
	}

	for _, test := range []struct {
		name    string
		headers http.Header
		version string
	}{
		{name: "missing version", headers: make(http.Header), version: anthropicDefaultVersion},
		{name: "blank version", headers: http.Header{"Anthropic-Version": {"  "}}, version: anthropicDefaultVersion},
		{name: "existing version", headers: http.Header{"Anthropic-Version": {" 2024-01-01 "}}, version: " 2024-01-01 "},
	} {
		t.Run(test.name, func(t *testing.T) {
			value.InjectCredential(test.headers, "upstream-secret")
			if got := test.headers.Get("X-Api-Key"); got != "upstream-secret" {
				t.Fatalf("X-Api-Key = %q", got)
			}
			if got := test.headers.Get("Anthropic-Version"); got != test.version {
				t.Fatalf("Anthropic-Version = %q, want %q", got, test.version)
			}
		})
	}

	headers := make(http.Header)
	ApplyCredential(value, headers, "upstream-secret", state.HeaderRules{Set: map[string]string{
		"X-Api-Key":         "override-${API_KEY}",
		"Anthropic-Version": "2025-01-01",
	}})
	if got := headers.Get("X-Api-Key"); got != "override-upstream-secret" {
		t.Fatalf("overridden X-Api-Key = %q", got)
	}
	if got := headers.Get("Anthropic-Version"); got != "2025-01-01" {
		t.Fatalf("overridden Anthropic-Version = %q", got)
	}

	ApplyCredential(value, headers, "upstream-secret", state.HeaderRules{
		Set: map[string]string{
			"X-Api-Key":         "must-be-removed",
			"Anthropic-Version": "must-be-removed",
		},
		Remove: []string{"x-api-key", "anthropic-version"},
	})
	if headers.Get("X-Api-Key") != "" || headers.Get("Anthropic-Version") != "" {
		t.Fatalf("remove did not win: %#v", headers)
	}

	value.InjectCredential(nil, "upstream-secret")
	ApplyCredential(value, nil, "upstream-secret", state.HeaderRules{})
}

func TestAnthropicBuildUpstreamURL(t *testing.T) {
	value := NewAnthropic(http.DefaultClient)
	tests := []struct {
		name    string
		base    string
		request *ParsedRequest
		want    string
		wantErr bool
	}{
		{name: "root base", base: "https://api.example.com", request: &ParsedRequest{Path: "/v1/messages", RawQuery: "trace=true"}, want: "https://api.example.com/v1/messages?trace=true"},
		{name: "base path and queries", base: "https://api.example.com/compatible/?tenant=one", request: &ParsedRequest{Path: "/v1/messages", RawQuery: "trace=true"}, want: "https://api.example.com/compatible/v1/messages?tenant=one&trace=true"},
		{name: "nil request", base: "https://api.example.com", wantErr: true},
		{name: "relative request path", base: "https://api.example.com", request: &ParsedRequest{Path: "v1/messages"}, wantErr: true},
		{name: "relative base", base: "api.example.com", request: &ParsedRequest{Path: "/v1/messages"}, wantErr: true},
		{name: "unsupported base", base: "ftp://api.example.com", request: &ParsedRequest{Path: "/v1/messages"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := value.BuildUpstreamURL(test.base, test.request)
			if test.wantErr {
				if err == nil {
					t.Fatalf("BuildUpstreamURL() = %q, want error", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("BuildUpstreamURL() = %q, %v, want %q", got, err, test.want)
			}
		})
	}
}

func TestAnthropicClassifyStatus(t *testing.T) {
	value := NewAnthropic(http.DefaultClient)
	tests := []struct {
		name   string
		status int
		body   string
		want   health.ErrorClass
	}{
		{name: "success", status: http.StatusOK, body: `{"type":"authentication_error"}`, want: health.ErrorClassNonRetryable},
		{name: "unauthorized", status: http.StatusUnauthorized, want: health.ErrorClassRetryable},
		{name: "forbidden", status: http.StatusForbidden, want: health.ErrorClassRetryable},
		{name: "not found", status: http.StatusNotFound, want: health.ErrorClassRetryable},
		{name: "rate limited", status: http.StatusTooManyRequests, want: health.ErrorClassRetryable},
		{name: "server error", status: http.StatusInternalServerError, want: health.ErrorClassRetryable},
		{name: "authentication marker", status: http.StatusBadRequest, body: `{"type":"authentication_error"}`, want: health.ErrorClassRetryable},
		{name: "permission marker", status: http.StatusBadRequest, body: `{"type":"permission_error"}`, want: health.ErrorClassRetryable},
		{name: "rate marker", status: http.StatusBadRequest, body: `{"type":"rate_limit_error"}`, want: health.ErrorClassRetryable},
		{name: "overloaded marker", status: http.StatusBadRequest, body: `{"type":"overloaded_error"}`, want: health.ErrorClassRetryable},
		{name: "model not found code", status: http.StatusBadRequest, body: `{"type":"model_not_found"}`, want: health.ErrorClassRetryable},
		{name: "model not found message", status: http.StatusBadRequest, body: `{"message":"model not found"}`, want: health.ErrorClassRetryable},
		{name: "model not supported code", status: http.StatusBadRequest, body: `{"type":"model_not_supported"}`, want: health.ErrorClassRetryable},
		{name: "model not supported message", status: http.StatusBadRequest, body: `{"message":"model not supported"}`, want: health.ErrorClassRetryable},
		{name: "no model access", status: http.StatusBadRequest, body: `{"message":"no access to model"}`, want: health.ErrorClassRetryable},
		{name: "invalid request", status: http.StatusBadRequest, body: `{"type":"invalid_request_error"}`, want: health.ErrorClassNonRetryable},
		{name: "unsupported feature", status: http.StatusBadRequest, body: `{"message":"function calling is not supported with this model"}`, want: health.ErrorClassNonRetryable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := value.ClassifyStatus(test.status, []byte(test.body)); got != test.want {
				t.Fatalf("ClassifyStatus() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestAnthropicListModelsPaginatesAndPreservesInputs(t *testing.T) {
	var requestCount atomic.Int64
	const cursor = " cursor/+= ? "
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		pageNumber := requestCount.Add(1)
		if request.Method != http.MethodGet || request.URL.Path != "/prefix/v1/models" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if got := request.URL.Query()["limit"]; len(got) != 1 || got[0] != "1000" {
			t.Errorf("limit = %#v, want one 1000", got)
		}
		if got := request.URL.Query().Get("tenant"); got != "one" {
			t.Errorf("tenant = %q, want one", got)
		}
		if got := request.URL.Query().Get("fixed"); got != "preserved" {
			t.Errorf("fixed = %q, want preserved", got)
		}
		if got := request.Header.Get("X-Api-Key"); got != "upstream-secret" {
			t.Errorf("X-Api-Key = %q", got)
		}
		if got := request.Header.Get("Anthropic-Version"); got != anthropicDefaultVersion {
			t.Errorf("Anthropic-Version = %q", got)
		}
		if got := request.Header.Get("X-Discovery-Rule"); got != "applied-rule" {
			t.Errorf("X-Discovery-Rule = %q", got)
		}
		switch pageNumber {
		case 1:
			if _, exists := request.URL.Query()["after_id"]; exists {
				t.Errorf("page 1 retained stale after_id: %q", request.URL.Query().Get("after_id"))
			}
			_, _ = writer.Write([]byte(`{"data":[{"id":"claude-a"},{"id":"shared"},{"id":"claude-a"}],"has_more":true,"last_id":"` + cursor + `"}`))
		case 2:
			if got := request.URL.Query().Get("after_id"); got != cursor {
				t.Errorf("page 2 after_id = %q, want byte-for-byte %q", got, cursor)
			}
			_, _ = writer.Write([]byte(`{"data":[{"id":"shared"},{"id":"claude-b"}],"has_more":false}`))
		default:
			t.Errorf("unexpected page %d", pageNumber)
			writer.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	models, err := NewAnthropic(server.Client()).ListModels(
		context.Background(),
		server.URL+"/prefix?tenant=one&fixed=preserved&limit=1&limit=2&after_id=stale",
		"upstream-secret",
		state.HeaderRules{Set: map[string]string{"X-Discovery-Rule": "applied-rule"}},
	)
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if strings.Join(models, ",") != "claude-a,shared,claude-b" {
		t.Fatalf("models = %#v", models)
	}
	if got := requestCount.Load(); got != 2 {
		t.Fatalf("request count = %d, want 2", got)
	}
}

func TestAnthropicListModelsForcesIdentity(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.Header.Values("Accept-Encoding"); len(got) != 1 || got[0] != "identity" {
			t.Fatalf("Accept-Encoding = %#v, want one identity", got)
		}
		return anthropicTestResponse(http.StatusOK, `{"data":[]}`), nil
	})}
	models, err := NewAnthropic(client).ListModels(
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

func TestAnthropicListModelsRejectsBlankOrRepeatedCursor(t *testing.T) {
	t.Run("blank cursor", func(t *testing.T) {
		var requestCount atomic.Int64
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requestCount.Add(1)
			return anthropicTestResponse(http.StatusOK, `{"data":[{"id":"claude-a"}],"has_more":true,"last_id":" \t "}`), nil
		})}
		models, err := NewAnthropic(client).ListModels(context.Background(), "https://api.example.com", "secret", state.HeaderRules{})
		if err == nil || models != nil {
			t.Fatalf("ListModels() = %#v, %v, want nil models and error", models, err)
		}
		if got := requestCount.Load(); got != 1 {
			t.Fatalf("request count = %d, want 1", got)
		}
	})

	t.Run("repeated cursor", func(t *testing.T) {
		var requestCount atomic.Int64
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requestCount.Add(1)
			return anthropicTestResponse(http.StatusOK, `{"data":[{"id":"claude-a"}],"has_more":true,"last_id":"same-cursor"}`), nil
		})}
		models, err := NewAnthropic(client).ListModels(context.Background(), "https://api.example.com", "secret", state.HeaderRules{})
		if err == nil || models != nil {
			t.Fatalf("ListModels() = %#v, %v, want nil models and error", models, err)
		}
		if got := requestCount.Load(); got != 2 {
			t.Fatalf("request count = %d, want 2", got)
		}
	})
}

func TestAnthropicListModelsRejectsPageAndUniqueModelLimits(t *testing.T) {
	t.Run("page 100 may end", func(t *testing.T) {
		var requestCount atomic.Int64
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			pageNumber := requestCount.Add(1)
			return anthropicTestResponse(http.StatusOK, fmt.Sprintf(
				`{"data":[{"id":"model-%d"}],"has_more":%t,"last_id":"cursor-%d"}`,
				pageNumber, pageNumber < maxModelListPages, pageNumber,
			)), nil
		})}
		models, err := NewAnthropic(client).ListModels(context.Background(), "https://api.example.com", "secret", state.HeaderRules{})
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
			return anthropicTestResponse(http.StatusOK, fmt.Sprintf(
				`{"data":[{"id":"model-%d"}],"has_more":true,"last_id":"cursor-%d"}`,
				pageNumber, pageNumber,
			)), nil
		})}
		models, err := NewAnthropic(client).ListModels(context.Background(), "https://api.example.com", "secret", state.HeaderRules{})
		if err == nil || models != nil {
			t.Fatalf("ListModels() = %#v, %v, want nil models and error", models, err)
		}
		if got := requestCount.Load(); got != maxModelListPages {
			t.Fatalf("request count = %d, want %d", got, maxModelListPages)
		}
	})

	for _, test := range []struct {
		name       string
		modelCount int
		hasMore    bool
		wantErr    bool
	}{
		{name: "exact unique maximum may end", modelCount: maxUniqueModelListEntries},
		{name: "exact unique maximum may not continue", modelCount: maxUniqueModelListEntries, hasMore: true, wantErr: true},
		{name: "more than unique maximum is rejected", modelCount: maxUniqueModelListEntries + 1, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := mustAnthropicTestPayload(t, test.modelCount, test.hasMore, "next-cursor")
			var requestCount atomic.Int64
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				requestCount.Add(1)
				return anthropicTestResponse(http.StatusOK, body), nil
			})}
			models, err := NewAnthropic(client).ListModels(context.Background(), "https://api.example.com", "secret", state.HeaderRules{})
			if test.wantErr {
				if err == nil || models != nil {
					t.Fatalf("ListModels() = %#v, %v, want nil models and error", models, err)
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

func TestAnthropicListModelsReturnsNoPartialResultAfterLaterPageFailure(t *testing.T) {
	for _, test := range []struct {
		name       string
		secondPage func() (*http.Response, error)
	}{
		{name: "transport error", secondPage: func() (*http.Response, error) {
			return nil, errors.New("later transport failure")
		}},
		{name: "non-2xx", secondPage: func() (*http.Response, error) {
			return anthropicTestResponse(http.StatusBadGateway, `{"error":"later failure"}`), nil
		}},
		{name: "malformed JSON", secondPage: func() (*http.Response, error) {
			return anthropicTestResponse(http.StatusOK, `{"data":[`), nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requestCount atomic.Int64
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				if requestCount.Add(1) == 1 {
					return anthropicTestResponse(http.StatusOK, `{"data":[{"id":"first-page-model"}],"has_more":true,"last_id":"next"}`), nil
				}
				return test.secondPage()
			})}
			models, err := NewAnthropic(client).ListModels(context.Background(), "https://api.example.com", "secret", state.HeaderRules{})
			if err == nil || models != nil {
				t.Fatalf("ListModels() = %#v, %v, want nil models and later-page error", models, err)
			}
			if got := requestCount.Load(); got != 2 {
				t.Fatalf("request count = %d, want 2", got)
			}
		})
	}
}

func TestAnthropicListModelsClosesBodyBeforeRequestingNextPage(t *testing.T) {
	firstBody := &trackedReadCloser{Reader: strings.NewReader(`{"data":[{"id":"first"}],"has_more":true,"last_id":"next"}`)}
	secondBody := &trackedReadCloser{Reader: strings.NewReader(`{"data":[{"id":"second"}],"has_more":false}`)}
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
	models, err := NewAnthropic(client).ListModels(context.Background(), "https://api.example.com", "secret", state.HeaderRules{})
	if err != nil || strings.Join(models, ",") != "first,second" {
		t.Fatalf("ListModels() = %#v, %v, want first,second", models, err)
	}
	if !secondBody.closed.Load() {
		t.Fatal("last page body was not closed")
	}
}

func TestAnthropicListModelsRulesEmptyAndFailures(t *testing.T) {
	t.Run("rules override defaults", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if got := request.Header.Get("X-Api-Key"); got != "override-upstream-secret" {
				t.Errorf("X-Api-Key = %q", got)
			}
			if got := request.Header.Get("Anthropic-Version"); got != "2025-01-01" {
				t.Errorf("Anthropic-Version = %q", got)
			}
			_, _ = writer.Write([]byte(`{"data":[]}`))
		}))
		defer server.Close()
		models, err := NewAnthropic(server.Client()).ListModels(
			context.Background(), server.URL, "upstream-secret",
			state.HeaderRules{Set: map[string]string{
				"X-Api-Key":         "override-${API_KEY}",
				"Anthropic-Version": "2025-01-01",
			}},
		)
		if err != nil || models == nil || len(models) != 0 {
			t.Fatalf("ListModels() = %#v, %v, want non-nil empty", models, err)
		}
	})

	t.Run("rules remove defaults", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Header.Get("X-Api-Key") != "" || request.Header.Get("Anthropic-Version") != "" {
				t.Errorf("removed headers remain: %#v", request.Header)
			}
			_, _ = writer.Write([]byte(`{"data":[]}`))
		}))
		defer server.Close()
		_, err := NewAnthropic(server.Client()).ListModels(
			context.Background(), server.URL, "upstream-secret",
			state.HeaderRules{Remove: []string{"x-api-key", "anthropic-version"}},
		)
		if err != nil {
			t.Fatalf("ListModels() error = %v", err)
		}
	})

	t.Run("non-success does not expose body or key", func(t *testing.T) {
		const secret = "distinctive-upstream-secret"
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = writer.Write([]byte(`{"error":"` + secret + ` in body"}`))
		}))
		defer server.Close()
		_, err := NewAnthropic(server.Client()).ListModels(context.Background(), server.URL, secret, state.HeaderRules{})
		if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), server.URL) {
			t.Fatalf("ListModels() error = %v", err)
		}
	})

	t.Run("malformed JSON is contextual without URL or key", func(t *testing.T) {
		const secret = "distinctive-upstream-secret"
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte(`{"data":[`))
		}))
		defer server.Close()
		_, err := NewAnthropic(server.Client()).ListModels(
			context.Background(), server.URL+"?token=query-secret", secret, state.HeaderRules{},
		)
		if err == nil || !strings.Contains(err.Error(), "decode Anthropic model list") {
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
		_, err := NewAnthropic(server.Client()).ListModels(ctx, server.URL, "upstream-secret", state.HeaderRules{})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ListModels() error = %v, want deadline exceeded", err)
		}
	})

	t.Run("response body closes", func(t *testing.T) {
		body := &trackedReadCloser{Reader: strings.NewReader(`{"data":[]}`)}
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
		})}
		_, err := NewAnthropic(client).ListModels(context.Background(), "https://api.example.com", "secret", state.HeaderRules{})
		if err != nil {
			t.Fatalf("ListModels() error = %v", err)
		}
		if !body.closed.Load() {
			t.Fatal("response body was not closed")
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type trackedReadCloser struct {
	io.Reader
	closed atomic.Bool
}

func (body *trackedReadCloser) Close() error {
	body.closed.Store(true)
	return nil
}

func anthropicTestResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func mustAnthropicTestPayload(t *testing.T, modelCount int, hasMore bool, lastID string) string {
	t.Helper()
	type item struct {
		ID string `json:"id"`
	}
	payload := struct {
		Data    []item `json:"data"`
		HasMore bool   `json:"has_more"`
		LastID  string `json:"last_id"`
	}{
		Data:    make([]item, modelCount),
		HasMore: hasMore,
		LastID:  lastID,
	}
	for index := range payload.Data {
		payload.Data[index].ID = fmt.Sprintf("model-%06d", index)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal Anthropic test payload: %v", err)
	}
	return string(encoded)
}
