package embedded

import (
	"bytes"
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

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	internalexecutor "github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	"github.com/tidwall/gjson"
)

func TestGrokExecutorConvertsFourProtocolsUnaryAndStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer access-secret" {
			t.Fatalf("request = %s %s, auth %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.completed\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":0,"status":"completed","model":"grok-4.3","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}` + "\n\n"))
	}))
	defer server.Close()

	cfg := &internalconfig.Config{}
	executor := &grokHTTPExecutor{cfg: cfg, inner: internalexecutor.NewXAIExecutor(cfg), baseURL: server.URL}
	credential := testGrokExecutionCredential()
	for _, test := range []struct {
		name    string
		format  string
		payload string
		usage   string
	}{
		{name: "responses", format: "openai-response", payload: `{"model":"grok-4.3","input":"hi"}`, usage: "usage.input_tokens"},
		{name: "chat", format: "openai", payload: `{"model":"grok-4.3","messages":[{"role":"user","content":"hi"}]}`, usage: "usage.prompt_tokens"},
		{name: "anthropic", format: "claude", payload: `{"model":"grok-4.3","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`, usage: "usage.input_tokens"},
		{name: "gemini", format: "gemini", payload: `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`, usage: "usageMetadata.promptTokenCount"},
	} {
		t.Run(test.name+" unary", func(t *testing.T) {
			response, err := executor.ExecuteCanonical(t.Context(), "credential-1", credential, ExecuteRequest{
				AttemptID: "attempt-1", Model: "grok-4.3", Format: test.format,
				Payload: []byte(test.payload), OriginalRequest: []byte(test.payload), ContinuityKey: "tenant-scope",
			})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(response.Payload, []byte("ok")) || gjson.GetBytes(response.Payload, test.usage).Int() != 2 {
				t.Fatalf("response = %s", response.Payload)
			}
		})
		t.Run(test.name+" stream", func(t *testing.T) {
			response, err := executor.ExecuteStreamCanonical(t.Context(), "credential-1", credential, ExecuteRequest{
				AttemptID: "attempt-1", Model: "grok-4.3", Format: test.format,
				Payload: []byte(test.payload), OriginalRequest: []byte(test.payload), ContinuityKey: "tenant-scope",
			})
			if err != nil {
				t.Fatal(err)
			}
			var output []byte
			for chunk := range response.Chunks {
				if chunk.Err != nil {
					t.Fatal(chunk.Err)
				}
				output = append(output, chunk.Payload...)
			}
			if len(output) == 0 {
				t.Fatal("stream returned no translated chunks")
			}
		})
	}
}

func TestGrokExecutorUsesHeaderSafeConversationIDForOpaqueContinuity(t *testing.T) {
	var conversationID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conversationID = r.Header.Get("x-grok-conv-id")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.completed\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"grok-4.6","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}` + "\n\n"))
	}))
	defer server.Close()

	payload := []byte(`{"model":"grok-4.6","input":"hi"}`)
	_, err := testGrokHTTPExecutor(server.URL).ExecuteCanonical(
		t.Context(), "credential-1", testGrokExecutionCredential(), ExecuteRequest{
			AttemptID: "attempt-1", Model: "grok-4.6", Format: "openai-response",
			Payload: payload, OriginalRequest: payload,
			ContinuityKey: "tenant\x00credential-1\x00grok-4.6",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversationID) != 36 || strings.ContainsAny(conversationID, "\r\n\x00") {
		t.Fatalf("conversation ID = %q", conversationID)
	}
}

func TestGrokExecutorCountsTokensLocally(t *testing.T) {
	executor := NewGrokHTTPExecutor()
	for _, test := range []struct {
		format  string
		payload string
		path    string
	}{
		{format: "openai-response", payload: `{"model":"grok-4.3","input":"hello world"}`, path: "input_tokens"},
		{format: "claude", payload: `{"model":"grok-4.3","messages":[{"role":"user","content":"hello world"}]}`, path: "input_tokens"},
		{format: "gemini", payload: `{"contents":[{"role":"user","parts":[{"text":"hello world"}]}]}`, path: "totalTokens"},
	} {
		response, err := executor.CountTokensCanonical(context.Background(), ExecuteRequest{
			Model: "grok-4.3", Format: test.format, Payload: []byte(test.payload), OriginalRequest: []byte(test.payload),
		})
		if err != nil {
			t.Fatal(err)
		}
		if gjson.GetBytes(response.Payload, test.path).Int() <= 0 || strings.Contains(string(response.Payload), "access-secret") {
			t.Fatalf("count response = %s", response.Payload)
		}
		if test.format == "openai-response" && gjson.GetBytes(response.Payload, "object").String() != "response.input_tokens" {
			t.Fatalf("Responses token count = %s", response.Payload)
		}
	}
}

func TestGrokExecutorPreservesRichChatRequestAndToolResponse(t *testing.T) {
	var upstreamBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		upstreamBody = append([]byte(nil), body...)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.completed\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_tool","object":"response","status":"completed","model":"grok-4.3","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"considering"}]},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{\"q\":\"x\"}","status":"completed"}],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}` + "\n\n"))
	}))
	defer server.Close()

	payload := []byte(`{
		"model":"grok-4.3","reasoning_effort":"high",
		"messages":[
			{"role":"system","content":"follow the rule"},
			{"role":"user","content":[
				{"type":"text","text":"inspect"},
				{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}
			]}
		],
		"tools":[{"type":"function","function":{"name":"lookup","description":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}}],
		"tool_choice":"auto"
	}`)
	response, err := testGrokHTTPExecutor(server.URL).ExecuteCanonical(
		t.Context(), "credential-1", testGrokExecutionCredential(), ExecuteRequest{
			AttemptID: "attempt-1", Model: "grok-4.3", Format: "openai",
			Payload: payload, OriginalRequest: payload, ContinuityKey: "tenant-scope",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(upstreamBody), "follow the rule") ||
		gjson.GetBytes(upstreamBody, "tools.0.name").String() != "lookup" ||
		gjson.GetBytes(upstreamBody, "reasoning.effort").String() != "high" ||
		!strings.Contains(string(upstreamBody), "data:image/png;base64,AAAA") {
		t.Fatalf("rich upstream request = %s", upstreamBody)
	}
	if gjson.GetBytes(response.Payload, "choices.0.message.tool_calls.0.function.name").String() != "lookup" ||
		gjson.GetBytes(response.Payload, "choices.0.finish_reason").String() != "tool_calls" ||
		gjson.GetBytes(response.Payload, "usage.prompt_tokens").Int() != 5 ||
		gjson.GetBytes(response.Payload, "usage.completion_tokens").Int() != 3 ||
		response.AppliedReasoningEffort != "high" {
		t.Fatalf("rich downstream response = %s, applied=%q", response.Payload, response.AppliedReasoningEffort)
	}
}

func TestGrokExecutionOnlyBridgeUsesOneDispatchAndRejectsRedirects(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", "https://example.invalid/redirected")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	executor := testGrokHTTPExecutor(server.URL)
	_, err := executor.ExecuteCanonical(t.Context(), "credential-1", testGrokExecutionCredential(), testGrokRequest())
	if !errors.Is(err, ErrRedirectNotAllowed) || calls.Load() != 1 {
		t.Fatalf("redirect result = %v, calls = %d", err, calls.Load())
	}
}

func TestGrokExecutionOnlyBridgeDoesNotRetryOrRefreshPreparedCredential(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":"subscription:free-usage-exhausted","error":"exhausted"}`))
	}))
	defer server.Close()

	credential := testGrokExecutionCredential()
	credential.Expire = time.Now().UTC().Add(time.Second).Format(time.RFC3339)
	_, err := testGrokHTTPExecutor(server.URL).ExecuteCanonical(t.Context(), "credential-1", credential, testGrokRequest())
	var upstream *GrokExecutionError
	if !errors.As(err, &upstream) || upstream.StatusCode() != http.StatusTooManyRequests ||
		upstream.ErrorCode() != "subscription:free-usage-exhausted" || calls.Load() != 1 {
		t.Fatalf("execution result = %#v, calls = %d", err, calls.Load())
	}
	if retry := upstream.RetryAfter(); retry == nil || *retry != 24*time.Hour {
		t.Fatalf("RetryAfter = %v", retry)
	}
}

func TestGrokExecutionOnlyBridgeMapsBadCredentialsToUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"unauthenticated:bad-credentials","error":"invalid OAuth access token"}`))
	}))
	defer server.Close()

	_, err := testGrokHTTPExecutor(server.URL).ExecuteCanonical(
		t.Context(), "credential-1", testGrokExecutionCredential(), testGrokRequest(),
	)
	var upstream *GrokExecutionError
	if !errors.As(err, &upstream) || upstream.StatusCode() != http.StatusUnauthorized ||
		upstream.ErrorCode() != "unauthenticated:bad-credentials" {
		t.Fatalf("bad credentials error = %#v", err)
	}
}

func TestGrokExecutionRequestRemovesCallerContinuity(t *testing.T) {
	prepared := prepareGrokExecutionRequest(ExecuteRequest{
		AttemptID: "attempt-1", ContinuityKey: "private-scope",
		Payload:         []byte(`{"input":"keep","session_id":"caller","prompt_cache_key":"caller-cache","metadata":{"sessionId":"nested"}}`),
		OriginalRequest: []byte(`{"input":"keep","sessionId":"caller","prompt_cache_key":"caller-cache","metadata":{"session_id":"nested"}}`),
		Headers:         http.Header{"Session-Id": {"caller"}, "X-Grok-Conv-Id": {"caller-conversation"}},
	})
	for _, raw := range [][]byte{prepared.Payload, prepared.OriginalRequest} {
		if !json.Valid(raw) || strings.Contains(string(raw), "caller") || !strings.Contains(string(raw), "keep") {
			t.Fatalf("prepared payload = %s", raw)
		}
	}
	if prepared.Headers.Get("Session-Id") != "" || prepared.Headers.Get("X-Grok-Conv-Id") != "" ||
		prepared.ContinuityKey != "private-scope" {
		t.Fatalf("prepared request = %#v", prepared)
	}
}

func TestGrokExecutionOnlyBridgePropagatesCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := testGrokHTTPExecutor(server.URL).ExecuteCanonical(ctx, "credential-1", testGrokExecutionCredential(), testGrokRequest())
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled Grok request did not return")
	}
}

type grokWrappedStatusError struct {
	status int
	body   string
}

func (err grokWrappedStatusError) Error() string   { return err.body }
func (err grokWrappedStatusError) StatusCode() int { return err.status }
func (err grokWrappedStatusError) RetryAfter() *time.Duration {
	value := 3 * time.Second
	return &value
}

func TestNormalizeGrokExecutionErrorPreservesWrappedMetadata(t *testing.T) {
	err := normalizeGrokExecutionError(fmt.Errorf("execute Grok request: %w", grokWrappedStatusError{
		status: http.StatusTooManyRequests,
		body:   `{"code":"subscription:free-usage-exhausted"}`,
	}))
	var normalized *GrokExecutionError
	if !errors.As(err, &normalized) || normalized.StatusCode() != http.StatusTooManyRequests ||
		normalized.ErrorCode() != "subscription:free-usage-exhausted" || normalized.RetryAfter() == nil ||
		*normalized.RetryAfter() != 3*time.Second {
		t.Fatalf("normalized error = %#v", err)
	}
}

func testGrokHTTPExecutor(baseURL string) *grokHTTPExecutor {
	cfg := &internalconfig.Config{}
	return &grokHTTPExecutor{cfg: cfg, inner: internalexecutor.NewXAIExecutor(cfg), baseURL: baseURL}
}

func testGrokRequest() ExecuteRequest {
	payload := []byte(`{"model":"grok-4.3","input":"hello"}`)
	return ExecuteRequest{
		AttemptID: "attempt-1", Model: "grok-4.3", Format: "openai-response",
		Payload: payload, OriginalRequest: append([]byte(nil), payload...), ContinuityKey: "tenant-scope",
	}
}

func testGrokExecutionCredential() GrokCredential {
	return GrokCredential{
		Type: ProviderGrok, AccessToken: "access-secret", RefreshToken: "refresh-secret",
		TokenType: "Bearer", AccountID: "account-1", Email: "owner@example.com",
		TokenEndpoint: "https://auth.x.ai/oauth/token",
		Expire:        time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}
}
