package bifrost

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestMistralNativeRoutesPreserveMethodPathAndCredential(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		operation execution.Operation
		method    string
		path      string
		body      string
		model     string
		wantPath  string
		wantBody  string
	}{
		{
			name: "ocr", operation: execution.OperationMistralOCR, method: http.MethodPost, path: "/v1/ocr",
			body:  `{"model":"public","document":{"type":"document_url","document_url":"https://example.com/a.pdf"}}`,
			model: "upstream-ocr", wantPath: "/v1/ocr", wantBody: `"model":"upstream-ocr"`,
		},
		{
			name: "batch job", operation: execution.OperationMistralBatch, method: http.MethodGet, path: "/mistral/v1/batch/jobs",
			wantPath: "/v1/batch/jobs",
		},
		{
			name: "file content", operation: execution.OperationMistralFiles, method: http.MethodGet, path: "/mistral/v1/files/file-1/content",
			wantPath: "/v1/files/file-1/content",
		},
		{
			name: "voices", operation: execution.OperationMistralVoices, method: http.MethodGet, path: "/mistral/v1/audio/voices",
			wantPath: "/v1/audio/voices",
		},
		{
			name: "voices v2", operation: execution.OperationMistralVoices, method: http.MethodGet, path: "/mistral/v2/audio/voices",
			wantPath: "/v2/audio/voices",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.Method != test.method || request.URL.Path != test.wantPath || request.URL.RawQuery != "tenant=a%2Bb" {
					t.Errorf("target = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
				}
				if request.Header.Get("Authorization") != "Bearer "+testAPIKey || request.Header.Get("X-Api-Key") != "" {
					t.Error("credential headers were not replaced")
				}
				payload, err := io.ReadAll(request.Body)
				if err != nil {
					t.Error(err)
					return
				}
				if test.wantBody != "" && !bytes.Contains(payload, []byte(test.wantBody)) {
					t.Errorf("body = %s", payload)
				}
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("X-Request-Id", "mistral-request")
				_, _ = io.WriteString(writer, `{"model":"upstream-ocr","ok":true}`)
			}))
			defer server.Close()

			spec := openAIEmbeddingsSpec(channel.Mistral, server.URL+"/v1", []byte(test.body))
			spec.ClientProtocol = protocol.Mistral
			spec.Operation = test.operation
			spec.Method = test.method
			spec.Path = test.path
			spec.RawQuery = "tenant=a%2Bb&api_key=injected"
			if test.model == "" {
				spec.ClientModel = ""
				spec.UpstreamModel = ""
			} else {
				spec.ClientModel = "public"
				spec.UpstreamModel = test.model
			}
			spec = freezeTestAttempt(spec)
			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
			result := runtime.Execute(context.Background(), spec)
			if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK || calls.Load() != 1 {
				t.Fatalf("calls=%d result=%+v error=%+v contract=%v body=%s", calls.Load(), result, result.Error, err, result.Body)
			}
			if result.UpstreamProtocol != protocol.Mistral || result.UpstreamRequestID != "mistral-request" {
				t.Fatalf("protocol/request id = %s / %s", result.UpstreamProtocol, result.UpstreamRequestID)
			}
		})
	}
}
