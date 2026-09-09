package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func rerankSpec(id channel.ID, baseURL string) execution.AttemptSpec {
	spec := openAIEmbeddingsSpec(id, baseURL, []byte(`{"model":"public","query":"q","documents":["a","b"],"stream":false,"api_key":"injected","future":1.2300}`))
	spec.ClientProtocol = protocol.Rerank
	spec.Operation = execution.OperationRerank
	spec.Path = "/v1/rerank"
	spec.ClientModel = "public"
	spec.RawQuery = "tenant=a%2Bb&api_key=injected"
	return freezeTestAttempt(spec)
}

func TestRerankRuntimePreservesNativeWireAcrossSupportedChannels(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		id           channel.ID
		prefix, path string
	}{
		{channel.OpenAICompatible, "/tenant/api/v4", "/tenant/api/v4/rerank"},
		{channel.OpenAICompatible, "/v2", "/v2/rerank"},
		{channel.NewAPI, "/team", "/team/v1/rerank"},
		{channel.GPTLoad, "/team", "/team/v1/rerank"},
	} {
		t.Run(string(test.id)+test.prefix, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != test.path || r.URL.RawQuery != "tenant=a%2Bb" {
					t.Errorf("target: %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Authorization") != "Bearer "+testAPIKey || r.Header.Get("X-Api-Key") != "" {
					t.Error("credential headers were not replaced")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				var object map[string]json.RawMessage
				if json.Unmarshal(body, &object) != nil || string(object["model"]) != `"provider-model"` || string(object["query"]) != `"q"` || string(object["future"]) != "1.2300" {
					t.Errorf("body=%s", body)
				}
				for _, field := range []string{"stream", "api_key", "fallbacks"} {
					if _, exists := object[field]; exists {
						t.Errorf("control field %s retained", field)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-Id", "rerank-request")
				_, _ = io.WriteString(w, `{"model":"provider-model","results":[{"index":1,"relevance_score":0.1234567890123456789,"document":{"text":"a"}}],"future":1.2300,"usage":{"total_tokens":8}}`)
			}))
			defer server.Close()
			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
			result := runtime.Execute(context.Background(), rerankSpec(test.id, server.URL+test.prefix))
			if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != 200 || calls.Load() != 1 || result.UpstreamProtocol != protocol.Rerank || result.UpstreamRequestID != "rerank-request" {
				t.Fatalf("calls=%d result=%+v error=%+v contract=%v", calls.Load(), result, result.Error, err)
			}
			for _, value := range []string{`"model":"public"`, `"relevance_score":0.1234567890123456789`, `"future":1.2300`} {
				if !bytes.Contains(result.Body, []byte(value)) {
					t.Fatalf("missing %s: %s", value, result.Body)
				}
			}
			if runtime.keyPoolCalls() != 0 {
				t.Fatal("SDK key pool was used")
			}
		})
	}
}

func TestRerankProbeUsesMinimalBodyAndRejectsInvalidSuccess(t *testing.T) {
	for _, response := range []string{`{"results":[{"index":0,"relevance_score":0.8}]}`, `{"results":[]}`, `{"choices":[]}`} {
		t.Run(response, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				var object map[string]json.RawMessage
				if json.Unmarshal(body, &object) != nil || len(object) != 4 || string(object["query"]) != `"ping"` || string(object["documents"]) != `["ping"]` || string(object["top_n"]) != "1" {
					t.Errorf("probe=%s", body)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, response)
			}))
			defer server.Close()
			spec := rerankSpec(channel.OpenAICompatible, server.URL+"/v1")
			spec.Operation = execution.OperationProbe
			spec.Method, spec.Path, spec.RawQuery = "", "", ""
			spec.Body = nil
			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
			result := runtime.Execute(context.Background(), freezeTestAttempt(spec))
			wantSuccess := strings.Contains(response, "relevance_score")
			if err := result.Validate(); err != nil || (result.Error == nil) != wantSuccess {
				t.Fatalf("result=%+v error=%+v contract=%v", result, result.Error, err)
			}
		})
	}
}

func TestRerankRuntimeEnforcesBodyLimitAndCancellation(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("wait") == "true" {
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[],"extra":"`+strings.Repeat("x", 2048)+`"}`)
	}))
	defer server.Close()
	defer close(release)
	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, maxUnaryResponseBodyBytes: 1024})
	spec := rerankSpec(channel.OpenAICompatible, server.URL+"/v1")
	result := runtime.Execute(context.Background(), spec)
	if result.Error == nil {
		t.Fatal("oversized response accepted")
	}
	spec.RawQuery = "wait=true"
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	result = runtime.Execute(ctx, freezeTestAttempt(spec))
	if result.Error == nil || time.Since(started) > time.Second {
		t.Fatalf("cancellation=%+v", result)
	}
}
