package dialect

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/health"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/testutil/fakeupstream"
)

func TestParsedRequestCarriesForwardingInputs(t *testing.T) {
	request := ParsedRequest{
		Method:   http.MethodPost,
		Path:     "/v1/chat/completions",
		RawQuery: "trace=true",
		Header:   http.Header{"X-Test": {"value"}},
		Body:     []byte(`{"model":"gpt-4o"}`),
	}

	if request.Method != http.MethodPost ||
		request.Path != "/v1/chat/completions" ||
		request.RawQuery != "trace=true" ||
		request.Header.Get("X-Test") != "value" ||
		string(request.Body) != `{"model":"gpt-4o"}` {
		t.Fatalf("ParsedRequest lost forwarding input: %#v", request)
	}
}

func TestOpenAIProtocol(t *testing.T) {
	dialect := NewOpenAI(http.DefaultClient)

	if got := dialect.Protocol(); got != protocol.OpenAI {
		t.Fatalf("OpenAI.Protocol() = %q, want %q", got, protocol.OpenAI)
	}
}

func TestOpenAIInjectCredential(t *testing.T) {
	dialect := NewOpenAI(http.DefaultClient)
	headers := make(http.Header)

	dialect.InjectCredential(headers, "sk-upstream")

	if got := headers.Get("Authorization"); got != "Bearer sk-upstream" {
		t.Fatalf("Authorization = %q, want Bearer credential", got)
	}
}

func TestOpenAIBuildUpstreamURL(t *testing.T) {
	dialect := NewOpenAI(http.DefaultClient)
	tests := []struct {
		name    string
		base    string
		request *ParsedRequest
		want    string
		wantErr bool
	}{
		{
			name: "root base",
			base: "https://api.example.com",
			request: &ParsedRequest{
				Path:     "/v1/chat/completions",
				RawQuery: "trace=true",
			},
			want: "https://api.example.com/v1/chat/completions?trace=true",
		},
		{
			name: "base path prefix",
			base: "https://api.example.com/compatible/",
			request: &ParsedRequest{
				Path: "/v1/chat/completions",
			},
			want: "https://api.example.com/compatible/v1/chat/completions",
		},
		{
			name:    "nil request",
			base:    "https://api.example.com",
			request: nil,
			wantErr: true,
		},
		{
			name: "relative request path",
			base: "https://api.example.com",
			request: &ParsedRequest{
				Path: "v1/chat/completions",
			},
			wantErr: true,
		},
		{
			name: "relative base",
			base: "api.example.com",
			request: &ParsedRequest{
				Path: "/v1/chat/completions",
			},
			wantErr: true,
		},
		{
			name: "unsupported base scheme",
			base: "ftp://api.example.com",
			request: &ParsedRequest{
				Path: "/v1/chat/completions",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := dialect.BuildUpstreamURL(tt.base, tt.request)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("BuildUpstreamURL() = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildUpstreamURL() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("BuildUpstreamURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOpenAIExtractModel(t *testing.T) {
	dialect := NewOpenAI(http.DefaultClient)
	tests := []struct {
		name       string
		request    *ParsedRequest
		wantModel  string
		wantStream bool
		wantErr    bool
	}{
		{
			name:       "non-stream",
			request:    &ParsedRequest{Body: []byte(`{"model":"gpt-4o","messages":[]}`)},
			wantModel:  "gpt-4o",
			wantStream: false,
		},
		{
			name:       "stream true",
			request:    &ParsedRequest{Body: []byte(`{"model":"gpt-4o-mini","stream":true}`)},
			wantModel:  "gpt-4o-mini",
			wantStream: true,
		},
		{
			name:    "nil request",
			request: nil,
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			request: &ParsedRequest{Body: []byte(`{"model":`)},
			wantErr: true,
		},
		{
			name:    "missing model",
			request: &ParsedRequest{Body: []byte(`{"stream":true}`)},
			wantErr: true,
		},
		{
			name:    "blank model",
			request: &ParsedRequest{Body: []byte(`{"model":"  "}`)},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, stream, err := dialect.ExtractModel(tt.request)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ExtractModel() = (%q, %t, nil), want error", model, stream)
				}
				return
			}
			if err != nil {
				t.Fatalf("ExtractModel() error = %v", err)
			}
			if model != tt.wantModel || stream != tt.wantStream {
				t.Fatalf(
					"ExtractModel() = (%q, %t), want (%q, %t)",
					model,
					stream,
					tt.wantModel,
					tt.wantStream,
				)
			}
		})
	}
}

func TestOpenAIClassifyStatus(t *testing.T) {
	dialect := NewOpenAI(http.DefaultClient)
	tests := []struct {
		name   string
		status int
		body   string
		want   health.ErrorClass
	}{
		{name: "success is not retryable", status: http.StatusOK, want: health.ErrorClassNonRetryable},
		{
			name:   "success body marker is not retryable",
			status: http.StatusOK,
			body:   `{"choices":[{"message":{"content":"rate limit"}}]}`,
			want:   health.ErrorClassNonRetryable,
		},
		{name: "generic bad request", status: http.StatusBadRequest, body: `{"error":"invalid input"}`, want: health.ErrorClassNonRetryable},
		{name: "payload too large", status: http.StatusRequestEntityTooLarge, want: health.ErrorClassNonRetryable},
		{name: "unprocessable entity", status: http.StatusUnprocessableEntity, want: health.ErrorClassNonRetryable},
		{name: "unauthorized", status: http.StatusUnauthorized, want: health.ErrorClassRetryable},
		{name: "forbidden", status: http.StatusForbidden, want: health.ErrorClassRetryable},
		{name: "model not found", status: http.StatusNotFound, want: health.ErrorClassRetryable},
		{name: "rate limited", status: http.StatusTooManyRequests, want: health.ErrorClassRetryable},
		{name: "upstream server error", status: http.StatusInternalServerError, want: health.ErrorClassRetryable},
		{
			name:   "rate keyword overrides 400",
			status: http.StatusBadRequest,
			body:   `{"error":{"code":"rate_limit_exceeded"}}`,
			want:   health.ErrorClassRetryable,
		},
		{
			name:   "invalid key keyword overrides 400",
			status: http.StatusBadRequest,
			body:   `{"error":{"code":"invalid_api_key"}}`,
			want:   health.ErrorClassRetryable,
		},
		{
			name:   "model keyword overrides 400",
			status: http.StatusBadRequest,
			body:   `{"error":{"message":"model not supported"}}`,
			want:   health.ErrorClassRetryable,
		},
		{
			name:   "unsupported parameter remains non retryable",
			status: http.StatusBadRequest,
			body:   `{"error":{"message":"parameter response_format is not supported"}}`,
			want:   health.ErrorClassNonRetryable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dialect.ClassifyStatus(tt.status, []byte(tt.body)); got != tt.want {
				t.Fatalf("ClassifyStatus(%d) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestOpenAIListModelsUsesDefaultCredentialWithEmptyRules(t *testing.T) {
	server := fakeupstream.New(fakeupstream.Step{
		Status:  http.StatusOK,
		Fixture: "openai/models.json",
	})
	defer server.Close()

	dialect := NewOpenAI(server.Client())
	models, err := dialect.ListModels(
		context.Background(),
		server.URL,
		"sk-default-models",
		state.HeaderRules{},
	)
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	want := []string{"gpt-4o", "gpt-4o-mini"}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("ListModels() = %#v, want %#v", models, want)
	}

	requests := server.Requests()
	if len(requests) != 1 {
		t.Fatalf("upstream requests = %d, want 1", len(requests))
	}
	if requests[0].Method != http.MethodGet ||
		requests[0].Path != "/v1/models" {
		t.Fatalf("upstream request = %#v, want GET /v1/models", requests[0])
	}
	if got := requests[0].Headers.Get("Authorization"); got != "Bearer sk-default-models" {
		t.Fatalf("Authorization = %q, want default Bearer credential", got)
	}
}

func TestOpenAIListModelsAppliesHeaderRuleOverrides(t *testing.T) {
	server := fakeupstream.New(fakeupstream.Step{
		Status:  http.StatusOK,
		Fixture: "openai/models.json",
	})
	defer server.Close()

	dialect := NewOpenAI(server.Client())
	_, err := dialect.ListModels(
		context.Background(),
		server.URL,
		"sk-custom-models",
		state.HeaderRules{
			Set: map[string]string{
				"Authorization": "Token ${API_KEY}",
				"X-Custom-Key":  "${API_KEY}",
			},
		},
	)
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}

	request := server.Requests()[0]
	if got := request.Headers.Get("Authorization"); got != "Token sk-custom-models" {
		t.Fatalf("Authorization = %q, want custom override", got)
	}
	if got := request.Headers.Get("X-Custom-Key"); got != "sk-custom-models" {
		t.Fatalf("X-Custom-Key = %q, want expanded key", got)
	}
}

func TestOpenAIListModelsHonorsContextTimeout(t *testing.T) {
	server := fakeupstream.New(fakeupstream.Step{
		Status:  http.StatusOK,
		Fixture: "openai/models.json",
		Delay:   200 * time.Millisecond,
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	dialect := NewOpenAI(server.Client())
	_, err := dialect.ListModels(
		ctx,
		server.URL,
		"sk-timeout",
		state.HeaderRules{},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ListModels() error = %v, want context deadline exceeded", err)
	}
}

func TestOpenAIListModelsRejectsNonSuccessWithoutLeakingKey(t *testing.T) {
	server := fakeupstream.New(fakeupstream.Step{
		Status:  http.StatusUnauthorized,
		Fixture: "openai/401.json",
	})
	defer server.Close()

	const apiKey = "sk-must-not-leak"
	dialect := NewOpenAI(server.Client())
	_, err := dialect.ListModels(
		context.Background(),
		server.URL,
		apiKey,
		state.HeaderRules{},
	)
	if err == nil {
		t.Fatal("ListModels() error = nil, want upstream status error")
	}
	if !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("ListModels() error = %v, want status 401", err)
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Fatalf("ListModels() error leaked API key: %v", err)
	}
}
