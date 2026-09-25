package dialect

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

func TestGeminiEmbeddingsInspectRequestAcceptsBothEndpoints(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		path string
		body string
	}{
		{
			name: "embed content",
			path: "/v1beta/models/gemini-embedding-001:embedContent",
			body: `{"content":{"parts":[{"text":"hello"}]},"taskType":"RETRIEVAL_QUERY","outputDimensionality":768}`,
		},
		{
			name: "batch embed contents",
			path: "/v1beta/models/gemini-embedding-001:batchEmbedContents",
			body: `{"requests":[{"model":"models/gemini-embedding-001","content":{"parts":[{"text":"a"}]}},{"content":{"parts":[{"text":"b"}]}}]}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			metadata, err := NewGeminiEmbeddings().InspectRequest(&ParsedRequest{
				Method: http.MethodPost,
				Path:   test.path,
				Body:   []byte(test.body),
			})
			if err != nil {
				t.Fatalf("InspectRequest() error = %v", err)
			}
			if metadata.Model == nil || *metadata.Model != "gemini-embedding-001" || metadata.Stream ||
				metadata.Operation != execution.OperationEmbeddingsCreate ||
				metadata.RouteRequirement != execution.RouteRequirementNative || !metadata.ObserveUsage {
				t.Fatalf("metadata = %#v", metadata)
			}
		})
	}
}

func TestGeminiEmbeddingsInspectRequestRejectsInvalidContract(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "wrong method", method: http.MethodGet, path: "/v1beta/models/m:embedContent", body: `{"content":{}}`},
		{name: "generation action", method: http.MethodPost, path: "/v1beta/models/m:generateContent", body: `{"content":{}}`},
		{name: "empty model", method: http.MethodPost, path: "/v1beta/models/:embedContent", body: `{"content":{}}`},
		{name: "nested model", method: http.MethodPost, path: "/v1beta/models/a/b:embedContent", body: `{"content":{}}`},
		{name: "boundary whitespace model", method: http.MethodPost, path: "/v1beta/models/ m:embedContent", body: `{"content":{}}`},
		{name: "non object body", method: http.MethodPost, path: "/v1beta/models/m:embedContent", body: `[]`},
		{name: "trailing data", method: http.MethodPost, path: "/v1beta/models/m:embedContent", body: `{"content":{}} {}`},
		{name: "missing content", method: http.MethodPost, path: "/v1beta/models/m:embedContent", body: `{"taskType":"RETRIEVAL_QUERY"}`},
		{name: "content is not object", method: http.MethodPost, path: "/v1beta/models/m:embedContent", body: `{"content":"hello"}`},
		{name: "missing requests", method: http.MethodPost, path: "/v1beta/models/m:batchEmbedContents", body: `{"content":{}}`},
		{name: "empty requests", method: http.MethodPost, path: "/v1beta/models/m:batchEmbedContents", body: `{"requests":[]}`},
		{name: "requests is not array", method: http.MethodPost, path: "/v1beta/models/m:batchEmbedContents", body: `{"requests":{}}`},
		{name: "request item is not object", method: http.MethodPost, path: "/v1beta/models/m:batchEmbedContents", body: `{"requests":["hello"]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewGeminiEmbeddings().InspectRequest(&ParsedRequest{
				Method: test.method,
				Path:   test.path,
				Body:   []byte(test.body),
			}); err == nil {
				t.Fatal("InspectRequest() error = nil")
			}
		})
	}
}

func TestGeminiEmbeddingsSanitizeRequestForAttemptRewritesEveryModel(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		path     string
		body     string
		wantPath string
		wantBody string
	}{
		{
			name:     "embed content rewrites present model",
			path:     "/v1beta/models/public-embedding:embedContent",
			body:     `{"model":"models/public-embedding","content":{"parts":[{"text":"hello"}]},"x-goog-api-key":"secret"}`,
			wantPath: "/v1beta/models/gemini-embedding-001:embedContent",
			wantBody: `{"model":"models/gemini-embedding-001","content":{"parts":[{"text":"hello"}]}}`,
		},
		{
			name:     "embed content keeps omitted model omitted",
			path:     "/v1beta/models/public-embedding:embedContent",
			body:     `{"content":{"parts":[{"text":"hello"}]}}`,
			wantPath: "/v1beta/models/gemini-embedding-001:embedContent",
			wantBody: `{"content":{"parts":[{"text":"hello"}]}}`,
		},
		{
			name:     "batch rewrites and fills every request model",
			path:     "/v1beta/models/public-embedding:batchEmbedContents",
			body:     `{"requests":[{"model":"models/other-model","content":{"parts":[{"text":"a"}]}},{"content":{"parts":[{"text":"b"}]},"outputDimensionality":8}],"provider":"x"}`,
			wantPath: "/v1beta/models/gemini-embedding-001:batchEmbedContents",
			wantBody: `{"requests":[{"model":"models/gemini-embedding-001","content":{"parts":[{"text":"a"}]}},{"model":"models/gemini-embedding-001","content":{"parts":[{"text":"b"}]},"outputDimensionality":8}]}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sanitized, err := NewGeminiEmbeddings().SanitizeRequestForAttempt(&ParsedRequest{
				Method: http.MethodPost,
				Path:   test.path,
				Body:   []byte(test.body),
			}, "gemini-embedding-001")
			if err != nil {
				t.Fatalf("SanitizeRequestForAttempt() error = %v", err)
			}
			if sanitized.Path != test.wantPath {
				t.Fatalf("path = %q, want %q", sanitized.Path, test.wantPath)
			}
			assertEquivalentJSON(t, sanitized.Body, test.wantBody)
			if sanitized.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("Content-Type = %q", sanitized.Header.Get("Content-Type"))
			}
		})
	}
}

func TestGeminiEmbeddingsSanitizeRequestForAttemptRejectsInvalidTarget(t *testing.T) {
	t.Parallel()

	request := &ParsedRequest{
		Method: http.MethodPost,
		Path:   "/v1beta/models/m:embedContent",
		Body:   []byte(`{"content":{}}`),
	}
	for _, model := range []string{"", " m", "a/b"} {
		if _, err := NewGeminiEmbeddings().SanitizeRequestForAttempt(request, model); err == nil {
			t.Fatalf("SanitizeRequestForAttempt(%q) error = nil", model)
		}
	}
}

func TestGeminiEmbeddingsRewriteResponseModelKeepsBody(t *testing.T) {
	t.Parallel()

	body := []byte(`{"embeddings":[{"values":[0.1,-0.2]}]}`)
	rewritten, err := NewGeminiEmbeddings().RewriteResponseModel(body, "public-embedding")
	if err != nil {
		t.Fatalf("RewriteResponseModel() error = %v", err)
	}
	if string(rewritten) != string(body) {
		t.Fatalf("RewriteResponseModel() = %s", rewritten)
	}
}

func TestGeminiEmbeddingsExtractUsage(t *testing.T) {
	t.Parallel()

	result, err := NewGeminiEmbeddings().ExtractUsage([]byte(
		`{"embeddings":[{"values":[0.1]},{"values":[0.2]}],"usageMetadata":{"promptTokenCount":7,"totalTokenCount":7}}`,
	))
	if err != nil {
		t.Fatalf("ExtractUsage() error = %v", err)
	}
	if result.State != usage.StateComplete || result.Tokens.UncachedInput != 7 || result.Tokens.Output != 0 {
		t.Fatalf("usage = %#v", result)
	}

	missing, err := NewGeminiEmbeddings().ExtractUsage([]byte(`{"embedding":{"values":[0.1]}}`))
	if err != nil {
		t.Fatalf("ExtractUsage() missing error = %v", err)
	}
	if missing.State != usage.StateMissing {
		t.Fatalf("missing usage = %#v", missing)
	}
	if _, err := NewGeminiEmbeddings().ExtractUsage([]byte(`[]`)); err == nil {
		t.Fatal("ExtractUsage() non-object error = nil")
	}

	stream := NewGeminiEmbeddings().NewUsageStreamExtractor()
	if err := stream.Observe([]byte(`{}`)); err == nil {
		t.Fatal("stream Observe() error = nil")
	}
	if result, finalized := stream.Finalize(); !finalized || result.State != usage.StateNotApplicable {
		t.Fatalf("stream Finalize() = %#v, %t", result, finalized)
	}
}

func TestGeminiEmbeddingsStandardRequestUsesEmbedContent(t *testing.T) {
	t.Parallel()

	metadata, err := InspectStandardRequest(protocol.GeminiEmbeddings, "gemini-embedding-001")
	if err != nil {
		t.Fatalf("InspectStandardRequest() error = %v", err)
	}
	if metadata.Model == nil || *metadata.Model != "gemini-embedding-001" ||
		metadata.Operation != execution.OperationEmbeddingsCreate ||
		metadata.RouteRequirement != execution.RouteRequirementNative {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func assertEquivalentJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode got JSON %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode want JSON: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}
