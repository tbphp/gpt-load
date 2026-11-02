package embedded

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func testMuseExecutionCredential() MuseCredential {
	return MuseCredential{Type: ProviderMuse, AccessToken: "minted-secret", DCAToken: "dca:never-send-upstream", Email: "muse@example.com"}
}

const museCompleted = `{"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"muse-spark-1.3","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`

func writeMuseTestStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range []string{
		`{"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress","model":"muse-spark-1.3","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"ok"}`,
		`{"type":"response.output_text.done","item_id":"msg_1","output_index":0,"content_index":0,"text":"ok"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}`,
		museCompleted,
		`{"type":"response.subscription_usage","subscription":{"tier":"private-tier","weekly":{"used_percent":20}}}`,
	} {
		_, _ = io.WriteString(w, "event: "+gjson.Get(event, "type").String()+"\ndata: "+event+"\n\n")
	}
}

func TestMuseExecutorConvertsProtocolsAndPreservesUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer minted-secret" {
			t.Error("incorrect Muse upstream request")
		}
		if r.Header.Get("X-Tbh-Contributor-Mode") != "0" {
			t.Error("contributor mode enabled without opt-in")
		}
		if strings.Contains(string(body), "never-send-upstream") || strings.Contains(r.Header.Get("Authorization"), "dca:") {
			t.Error("DCA token leaked to execution")
		}
		if gjson.GetBytes(body, "model").String() != "muse-spark-1.3" {
			t.Error("incorrect upstream model")
		}
		writeMuseTestStream(w)
	}))
	defer server.Close()
	executor := NewMuseHTTPExecutor().(*museHTTPExecutor)
	executor.baseURL = server.URL
	for _, test := range []struct{ format, payload, usage string }{
		{"openai-response", `{"model":"muse-spark-1.3","input":"hi"}`, "usage.input_tokens"},
		{"openai", `{"model":"muse-spark-1.3","messages":[{"role":"user","content":"hi"}]}`, "usage.prompt_tokens"},
		{"claude", `{"model":"muse-spark-1.3","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`, "usage.input_tokens"},
		{"gemini", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`, "usageMetadata.promptTokenCount"},
	} {
		t.Run(test.format, func(t *testing.T) {
			request := ExecuteRequest{AttemptID: "attempt", Model: "muse-spark-1.3", Format: test.format, Payload: []byte(test.payload), OriginalRequest: []byte(test.payload), ContinuityKey: "tenant-account-model", Headers: http.Header{"X-Tbh-Contributor-Mode": {"1"}}}
			response, err := executor.ExecuteCanonical(t.Context(), "account", testMuseExecutionCredential(), request)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(response.Payload), "ok") || gjson.GetBytes(response.Payload, test.usage).Int() != 2 {
				t.Fatalf("response: %s", response.Payload)
			}
			stream, err := executor.ExecuteStreamCanonical(t.Context(), "account", testMuseExecutionCredential(), request)
			if err != nil {
				t.Fatal(err)
			}
			var output strings.Builder
			for chunk := range stream.Chunks {
				if chunk.Err != nil {
					t.Fatal(chunk.Err)
				}
				output.Write(chunk.Payload)
			}
			if !strings.Contains(output.String(), "ok") || strings.Contains(output.String(), "private-tier") {
				t.Fatalf("stream: %s", output.String())
			}
		})
	}
}

func TestMuseExecutorCountsTokensWithoutCredentials(t *testing.T) {
	executor := NewMuseHTTPExecutor()
	for _, test := range []struct{ format, payload, path string }{
		{"openai-response", `{"model":"muse-spark-1.3","input":"hello world"}`, "input_tokens"},
		{"claude", `{"model":"muse-spark-1.3","messages":[{"role":"user","content":"hello world"}]}`, "input_tokens"},
		{"gemini", `{"contents":[{"role":"user","parts":[{"text":"hello world"}]}]}`, "totalTokens"},
	} {
		response, err := executor.CountTokensCanonical(t.Context(), ExecuteRequest{Model: "muse-spark-1.3", Format: test.format, Payload: []byte(test.payload)})
		if err != nil || gjson.GetBytes(response.Payload, test.path).Int() <= 0 {
			t.Fatalf("%s token count: %s, %v", test.format, response.Payload, err)
		}
	}
}

func TestMuseExecutionQuotaRetainsScopeAndSafeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":{"code":"rate_limit_exceeded","message":"Subscription quota exhausted. dca:private-secret","resets_at":4102444800}}`))
	}))
	defer server.Close()
	executor := NewMuseHTTPExecutor().(*museHTTPExecutor)
	executor.baseURL = server.URL
	_, err := executor.ExecuteCanonical(t.Context(), "account", testMuseExecutionCredential(), ExecuteRequest{Model: "muse-spark-1.3", Format: "openai-response", Payload: []byte(`{"input":"hi"}`)})
	var failure *MuseExecutionError
	if !errors.As(err, &failure) || !failure.IsCredentialScoped() || failure.StatusCode() != 429 || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("quota error: %v", err)
	}
}

func TestMuseExecutorNeverReceivesRenewalCredentials(t *testing.T) {
	auth := NewMuseAuth("account", testMuseExecutionCredential(), "https://api.meta.ai/v1")
	if auth.Storage != nil || auth.Metadata != nil || auth.Attributes["dca_token"] != "" {
		t.Fatal("execution can access renewable credentials")
	}
	a := museAuthForRequest("account-a", testMuseExecutionCredential(), "https://api.meta.ai/v1", ExecuteRequest{ContinuityKey: "same"})
	b := museAuthForRequest("account-b", testMuseExecutionCredential(), "https://api.meta.ai/v1", ExecuteRequest{ContinuityKey: "same"})
	if a.Attributes["header:X-Tbh-Session-Id"] == b.Attributes["header:X-Tbh-Session-Id"] {
		t.Fatal("different accounts share a session")
	}
}

func TestMuseExecutorAppliesConfiguredHeaders(t *testing.T) {
	var operator, cache, authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		operator, cache, authorization = r.Header.Get("X-Operator"), r.Header.Get("Cache-Control"), r.Header.Get("Authorization")
		writeMuseTestStream(w)
	}))
	defer server.Close()
	executor := NewMuseHTTPExecutor().(*museHTTPExecutor)
	executor.baseURL = server.URL
	_, err := executor.ExecuteCanonical(t.Context(), "account", testMuseExecutionCredential(), ExecuteRequest{
		Model: "muse-spark-1.3", Format: "openai-response", Payload: []byte(`{"input":"hi"}`),
		Headers:           http.Header{"X-Operator": {"configured"}, "Authorization": {"downstream-secret"}},
		ConfiguredHeaders: []string{"X-Operator", "Cache-Control", "Authorization"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if operator != "configured" || cache != "" || authorization != "Bearer minted-secret" {
		t.Fatalf("operator=%q cache=%q authorization preserved=%t", operator, cache, authorization == "Bearer minted-secret")
	}
}
