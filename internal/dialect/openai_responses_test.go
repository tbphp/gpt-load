package dialect

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gpt-load/internal/health"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestOpenAIResponsesProtocolAndRequestMetadata(t *testing.T) {
	t.Parallel()

	selected := NewOpenAIResponses(http.DefaultClient)
	if got := selected.Protocol(); got != protocol.OpenAIResponses {
		t.Fatalf("Protocol() = %q, want %q", got, protocol.OpenAIResponses)
	}

	tests := []struct {
		name        string
		request     *ParsedRequest
		wantModel   *string
		wantStream  bool
		wantObserve bool
		wantError   bool
	}{
		{
			name:        "empty create body",
			request:     &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses"},
			wantObserve: true,
		},
		{
			name: "create with model and stream",
			request: &ParsedRequest{
				Method: http.MethodPost,
				Path:   "/v1/responses",
				Body:   []byte(`{"model":"gpt-5","stream":true,"input":"ping"}`),
			},
			wantModel:   stringPointerForTest("gpt-5"),
			wantStream:  true,
			wantObserve: true,
		},
		{
			name: "compact observes usage",
			request: &ParsedRequest{
				Method: http.MethodPost,
				Path:   "/v1/responses/compact",
				Body:   []byte(`{"model":"gpt-5","input":"ping"}`),
			},
			wantModel:   stringPointerForTest("gpt-5"),
			wantObserve: true,
		},
		{
			name: "input tokens does not observe usage",
			request: &ParsedRequest{
				Method: http.MethodPost,
				Path:   "/v1/responses/input_tokens",
				Body:   []byte(`{"model":"gpt-5","input":"ping"}`),
			},
			wantModel: stringPointerForTest("gpt-5"),
		},
		{
			name: "retrieve stream query true",
			request: &ParsedRequest{
				Method:   http.MethodGet,
				Path:     "/v1/responses/resp_123",
				RawQuery: "include=output&stream=true",
			},
			wantStream: true,
		},
		{
			name: "retrieve stream query false",
			request: &ParsedRequest{
				Method:   http.MethodGet,
				Path:     "/v1/responses/resp_123",
				RawQuery: "stream=false",
			},
		},
		{
			name: "retrieve percent encoded stream query",
			request: &ParsedRequest{
				Method:   http.MethodGet,
				Path:     "/v1/responses/resp_123",
				RawQuery: "%73tream=%74rue",
			},
			wantStream: true,
		},
		{
			name:      "nil request",
			request:   nil,
			wantError: true,
		},
		{
			name: "non object",
			request: &ParsedRequest{
				Method: http.MethodPost,
				Path:   "/v1/responses",
				Body:   []byte(`[]`),
			},
			wantError: true,
		},
		{
			name: "multiple values",
			request: &ParsedRequest{
				Method: http.MethodPost,
				Path:   "/v1/responses",
				Body:   []byte(`{} {}`),
			},
			wantError: true,
		},
		{
			name: "invalid utf8",
			request: &ParsedRequest{
				Method: http.MethodPost,
				Path:   "/v1/responses",
				Body:   []byte{0xff},
			},
			wantError: true,
		},
		{
			name: "blank model",
			request: &ParsedRequest{
				Method: http.MethodPost,
				Path:   "/v1/responses",
				Body:   []byte(`{"model":""}`),
			},
			wantError: true,
		},
		{
			name: "model boundary whitespace",
			request: &ParsedRequest{
				Method: http.MethodPost,
				Path:   "/v1/responses",
				Body:   []byte(`{"model":" gpt-5"}`),
			},
			wantError: true,
		},
		{
			name: "model case alias",
			request: &ParsedRequest{
				Method: http.MethodPost,
				Path:   "/v1/responses",
				Body:   []byte(`{"Model":"gpt-5"}`),
			},
			wantError: true,
		},
		{
			name: "duplicate model",
			request: &ParsedRequest{
				Method: http.MethodPost,
				Path:   "/v1/responses",
				Body:   []byte(`{"model":"gpt-5","model":"gpt-5-mini"}`),
			},
			wantError: true,
		},
		{
			name: "stream must be bool",
			request: &ParsedRequest{
				Method: http.MethodPost,
				Path:   "/v1/responses",
				Body:   []byte(`{"stream":"true"}`),
			},
			wantError: true,
		},
		{
			name: "duplicate stream query",
			request: &ParsedRequest{
				Method:   http.MethodGet,
				Path:     "/v1/responses/resp_123",
				RawQuery: "stream=true&stream=true",
			},
			wantError: true,
		},
		{
			name: "mixed encoded duplicate stream query",
			request: &ParsedRequest{
				Method:   http.MethodGet,
				Path:     "/v1/responses/resp_123",
				RawQuery: "stream=true&%73tream=false",
			},
			wantError: true,
		},
		{
			name: "invalid stream query value",
			request: &ParsedRequest{
				Method:   http.MethodGet,
				Path:     "/v1/responses/resp_123",
				RawQuery: "stream=1",
			},
			wantError: true,
		},
		{
			name: "malformed stream query encoding",
			request: &ParsedRequest{
				Method:   http.MethodGet,
				Path:     "/v1/responses/resp_123",
				RawQuery: "stream=%ZZ",
			},
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := selected.InspectRequest(test.request)
			if test.wantError {
				if err == nil {
					t.Fatalf("InspectRequest() = %#v, nil; want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("InspectRequest() error = %v", err)
			}
			if !sameOptionalString(got.Model, test.wantModel) ||
				got.Stream != test.wantStream ||
				got.ObserveUsage != test.wantObserve {
				t.Fatalf(
					"InspectRequest() = %#v, want model=%v stream=%t observe=%t",
					got,
					test.wantModel,
					test.wantStream,
					test.wantObserve,
				)
			}
		})
	}
}

func TestOpenAIResponsesResource404DoesNotBlameKey(t *testing.T) {
	t.Parallel()

	selected := NewOpenAIResponses(http.DefaultClient)
	resourceBody := []byte(
		`{"error":{"type":"invalid_request_error","message":"No response found with id resp_123"}}`,
	)
	if got := selected.ClassifyStatus(http.StatusNotFound, resourceBody); got != health.FailureCategoryClientError {
		t.Fatalf("resource 404 category = %v, want client error", got)
	}
	decision := health.Judge(selected, health.Attempt{
		StatusCode: http.StatusNotFound,
		Body:       resourceBody,
		Now:        time.Unix(1, 0),
	})
	if decision.Category != health.FailureCategoryClientError ||
		decision.Action != health.ActionTerminate {
		t.Fatalf("resource 404 decision = %#v", decision)
	}

	modelBody := []byte(`{"error":{"code":"model_not_found"}}`)
	if got := selected.ClassifyStatus(http.StatusNotFound, modelBody); got != health.FailureCategoryModelUnavailable {
		t.Fatalf("model 404 category = %v, want model unavailable", got)
	}
}

func TestOpenAIResponsesProbeUsesMinimumValidRequest(t *testing.T) {
	t.Parallel()

	var received map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.Method != http.MethodPost ||
			request.URL.Path != "/v1/responses" ||
			request.Header.Get("Authorization") != "Bearer sk-responses" {
			t.Fatalf(
				"request method/path/auth = %s/%s/%q",
				request.Method,
				request.URL.Path,
				request.Header.Get("Authorization"),
			)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if err := json.Unmarshal(body, &received); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	err := NewOpenAIResponses(upstream.Client()).Probe(
		context.Background(),
		upstream.URL,
		"sk-responses",
		state.HeaderRules{},
		"gpt-5",
	)
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	want := map[string]any{
		"model":             "gpt-5",
		"input":             "ping",
		"max_output_tokens": float64(16),
		"store":             false,
	}
	if len(received) != len(want) {
		t.Fatalf("probe body = %#v, want %#v", received, want)
	}
	for name, value := range want {
		if received[name] != value {
			t.Fatalf("probe body[%q] = %#v, want %#v", name, received[name], value)
		}
	}
}

func TestOpenAIResponsesUsesOpenAIURLAndCredentialRules(t *testing.T) {
	t.Parallel()

	selected := NewOpenAIResponses(http.DefaultClient)
	headers := make(http.Header)
	selected.InjectCredential(headers, "sk-provider")
	if got := headers.Get("Authorization"); got != "Bearer sk-provider" {
		t.Fatalf("Authorization = %q, want Bearer provider key", got)
	}

	got, err := selected.BuildUpstreamURL(
		"https://api.example.com/compatible?api-version=2026-01-01",
		&ParsedRequest{
			Path:     "/v1/responses/resp_123/input_items",
			RawQuery: "limit=20&after=item_1",
		},
	)
	if err != nil {
		t.Fatalf("BuildUpstreamURL() error = %v", err)
	}
	const want = "https://api.example.com/compatible/v1/responses/resp_123/input_items?api-version=2026-01-01&limit=20&after=item_1"
	if got != want {
		t.Fatalf("BuildUpstreamURL() = %q, want %q", got, want)
	}
}

func stringPointerForTest(value string) *string {
	return &value
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
