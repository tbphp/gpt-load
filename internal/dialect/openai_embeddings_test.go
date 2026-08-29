package dialect

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestOpenAIEmbeddingsInspectRequestAcceptsOfficialInputShapes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		input string
	}{
		{name: "string", input: `"hello"`},
		{name: "strings", input: `["hello",""]`},
		{name: "token IDs", input: `[1,-2,123456789012345678901234567890]`},
		{name: "token ID arrays", input: `[[1,-2],[]]`},
		{name: "empty array without provider length validation", input: `[]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := []byte(`{"model":"Qwen/Qwen3-Embedding-8B","input":` + test.input + `,"dimensions":-1,"encoding_format":"base64","user":"tenant","future":{"kept":true}}`)
			metadata, err := NewOpenAIEmbeddings().InspectRequest(&ParsedRequest{
				Method: http.MethodPost,
				Path:   "/v1/embeddings",
				Header: http.Header{"Content-Type": {"application/json; charset=utf-8"}},
				Body:   body,
			})
			if err != nil {
				t.Fatalf("InspectRequest() error = %v", err)
			}
			if metadata.Model == nil || *metadata.Model != "Qwen/Qwen3-Embedding-8B" ||
				metadata.Stream || metadata.Operation != execution.OperationEmbeddingsCreate ||
				metadata.RouteRequirement != execution.RouteRequirementNative || !metadata.ObserveUsage {
				t.Fatalf("metadata = %#v", metadata)
			}
		})
	}
}

func TestOpenAIEmbeddingsInspectRequestRejectsInvalidContract(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		method      string
		path        string
		contentType string
		body        []byte
	}{
		{name: "wrong method", method: http.MethodGet, path: "/v1/embeddings", body: []byte(`{"model":"m","input":"x"}`)},
		{name: "wrong path", method: http.MethodPost, path: "/v1/embedding", body: []byte(`{"model":"m","input":"x"}`)},
		{name: "wrong content type", method: http.MethodPost, path: "/v1/embeddings", contentType: "text/plain", body: []byte(`{"model":"m","input":"x"}`)},
		{name: "malformed content type", method: http.MethodPost, path: "/v1/embeddings", contentType: `application/json; charset="`, body: []byte(`{"model":"m","input":"x"}`)},
		{name: "missing model", method: http.MethodPost, path: "/v1/embeddings", body: []byte(`{"input":"x"}`)},
		{name: "missing input", method: http.MethodPost, path: "/v1/embeddings", body: []byte(`{"model":"m"}`)},
		{name: "null input", method: http.MethodPost, path: "/v1/embeddings", body: []byte(`{"model":"m","input":null}`)},
		{name: "mixed input", method: http.MethodPost, path: "/v1/embeddings", body: []byte(`{"model":"m","input":["x",1]}`)},
		{name: "object input", method: http.MethodPost, path: "/v1/embeddings", body: []byte(`{"model":"m","input":{"text":"x"}}`)},
		{name: "duplicate input", method: http.MethodPost, path: "/v1/embeddings", body: []byte(`{"model":"m","input":"x","input":"y"}`)},
		{name: "input case alias", method: http.MethodPost, path: "/v1/embeddings", body: []byte(`{"model":"m","Input":"x"}`)},
		{name: "stream true", method: http.MethodPost, path: "/v1/embeddings", body: []byte(`{"model":"m","input":"x","stream":true}`)},
		{name: "stream non-boolean", method: http.MethodPost, path: "/v1/embeddings", body: []byte(`{"model":"m","input":"x","stream":"false"}`)},
		{name: "duplicate stream", method: http.MethodPost, path: "/v1/embeddings", body: []byte(`{"model":"m","input":"x","stream":false,"stream":false}`)},
		{name: "stream case alias", method: http.MethodPost, path: "/v1/embeddings", body: []byte(`{"model":"m","input":"x","Stream":false}`)},
		{name: "trailing JSON", method: http.MethodPost, path: "/v1/embeddings", body: []byte(`{"model":"m","input":"x"}{}`)},
		{name: "invalid UTF-8", method: http.MethodPost, path: "/v1/embeddings", body: []byte{'{', '"', 'm', 'o', 'd', 'e', 'l', '"', ':', '"', 0xff, '"', '}'}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			header := make(http.Header)
			if test.contentType != "" {
				header.Set("Content-Type", test.contentType)
			}
			metadata, err := NewOpenAIEmbeddings().InspectRequest(&ParsedRequest{
				Method: test.method,
				Path:   test.path,
				Header: header,
				Body:   test.body,
			})
			if err == nil {
				t.Fatalf("InspectRequest() = %#v, nil error", metadata)
			}
		})
	}
}

func TestOpenAIEmbeddingsSanitizeRequestForAttempt(t *testing.T) {
	t.Parallel()

	originalBody := []byte(`{"model":"public-model","input":[[1,2],[3,4]],"stream":false,"dimensions":256,"encoding_format":"base64","user":"tenant","api_key":"client-secret","fallbacks":["other"],"future":{"kept":true}}`)
	original := &ParsedRequest{
		Method: http.MethodPost,
		Path:   "/v1/embeddings",
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   bytes.Clone(originalBody),
	}
	sanitized, err := NewOpenAIEmbeddings().SanitizeRequestForAttempt(original, "upstream-model")
	if err != nil {
		t.Fatalf("SanitizeRequestForAttempt() error = %v", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(sanitized.Body, &object); err != nil {
		t.Fatalf("decode sanitized body: %v", err)
	}
	var model string
	if err := json.Unmarshal(object["model"], &model); err != nil || model != "upstream-model" {
		t.Fatalf("model = %q, %v", model, err)
	}
	for _, removed := range []string{"stream", "api_key", "fallbacks"} {
		if _, exists := object[removed]; exists {
			t.Fatalf("control field %q remains in %s", removed, sanitized.Body)
		}
	}
	for field, want := range map[string]string{
		"input":           `[[1,2],[3,4]]`,
		"dimensions":      `256`,
		"encoding_format": `"base64"`,
		"user":            `"tenant"`,
		"future":          `{"kept":true}`,
	} {
		if got := string(object[field]); got != want {
			t.Fatalf("%s = %s, want %s", field, got, want)
		}
	}
	if !bytes.Equal(original.Body, originalBody) {
		t.Fatalf("original body mutated: %s", original.Body)
	}
}

func TestOpenAIEmbeddingsRewriteResponseModelOnlyChangesTopLevel(t *testing.T) {
	t.Parallel()

	body := []byte(`{"object":"list","model":"provider-model","data":[{"object":"embedding","index":0,"embedding":"provider-model"}],"future":{"model":"provider-model"}}`)
	rewritten, err := NewOpenAIEmbeddings().RewriteResponseModel(body, "public-model")
	if err != nil {
		t.Fatalf("RewriteResponseModel() error = %v", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(rewritten, &object); err != nil {
		t.Fatalf("decode rewritten body: %v", err)
	}
	if string(object["model"]) != `"public-model"` ||
		!bytes.Contains(object["data"], []byte(`"embedding":"provider-model"`)) ||
		!bytes.Contains(object["future"], []byte(`"model":"provider-model"`)) {
		t.Fatalf("rewritten body = %s", rewritten)
	}
}

func TestOpenAIEmbeddingsProtocol(t *testing.T) {
	t.Parallel()

	if got := NewOpenAIEmbeddings().Protocol(); got != protocol.OpenAIEmbeddings {
		t.Fatalf("Protocol() = %q", got)
	}
}

func TestOpenAIEmbeddingsStandardRequestUsesCreate(t *testing.T) {
	t.Parallel()

	metadata, err := InspectStandardRequest(protocol.OpenAIEmbeddings, "text-embedding-3-small")
	if err != nil {
		t.Fatalf("InspectStandardRequest() error = %v", err)
	}
	if metadata.Operation != execution.OperationEmbeddingsCreate || metadata.Model == nil ||
		*metadata.Model != "text-embedding-3-small" || metadata.Stream ||
		metadata.RouteRequirement != execution.RouteRequirementNative || !metadata.ObserveUsage {
		t.Fatalf("metadata = %#v", metadata)
	}
}
