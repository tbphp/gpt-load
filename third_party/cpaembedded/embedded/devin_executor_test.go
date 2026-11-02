package embedded

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	"google.golang.org/protobuf/encoding/protowire"
)

func devinTestReply(stop uint64, terminal bool) []byte {
	frame := devinTestBytes(3, []byte("Hello Devin"))
	frame = protowire.AppendVarint(protowire.AppendTag(frame, 5, protowire.VarintType), stop)
	usage := protowire.AppendVarint(protowire.AppendTag(nil, 2, protowire.VarintType), 10)
	usage = protowire.AppendVarint(protowire.AppendTag(usage, 3, protowire.VarintType), 4)
	frame = append(frame, devinTestBytes(7, usage)...)
	out := helps.WrapConnectEnvelope(frame)
	if terminal {
		out = append(out, helps.WrapConnectEnvelopeWithFlag(helps.ConnectFlagEndStream, []byte(`{}`))...)
	}
	return out
}

var devinTestRequests = map[string]string{
	"openai":          `{"model":"devin/swe-2","messages":[{"role":"user","content":"hello"}]}`,
	"openai-response": `{"model":"devin/swe-2","input":"hello"}`,
	"claude":          `{"model":"devin/swe-2","max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`,
	"gemini":          `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
}

func TestDevinExecutorFourFormatsAndStreamTermination(t *testing.T) {
	for format, payload := range devinTestRequests {
		for _, test := range []struct {
			name       string
			stop       uint64
			terminal   bool
			incomplete bool
			failure    bool
		}{{"complete", 2, true, false, false}, {"limited", 3, true, true, false}, {"truncated", 0, false, false, true}} {
			t.Run(format+"/"+test.name, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.URL.Path != helps.DevinChatPath || !strings.HasPrefix(r.Header.Get("Authorization"), "Basic devin-session-token$") || r.Header.Get("Content-Type") != "application/connect+proto" {
						t.Error("wrong upstream request")
					}
					if _, _, err := helps.ReadConnectFrame(r.Body); err != nil {
						t.Error(err)
					}
					w.Header().Set("Content-Type", "application/connect+proto")
					_, _ = w.Write(devinTestReply(test.stop, test.terminal))
				}))
				defer server.Close()
				executor := NewDevinHTTPExecutor().(*devinHTTPExecutor)
				executor.baseURL = server.URL
				credential := DevinCredential{Type: ProviderDevin, SessionToken: "devin-session-token$test-secret", UserID: "account-one"}
				request := ExecuteRequest{AttemptID: "attempt", Model: "devin/swe-2", Payload: []byte(payload), OriginalRequest: []byte(payload), Format: format}
				response, err := executor.ExecuteCanonical(context.Background(), "one", credential, request)
				if (err != nil) != test.failure {
					t.Fatalf("unary error=%v", err)
				}
				if !test.failure && !strings.Contains(string(response.Payload), "Hello Devin") {
					t.Fatalf("missing unary content: %s", response.Payload)
				}
				stream, err := executor.ExecuteStreamCanonical(context.Background(), "one", credential, request)
				if err != nil {
					t.Fatal(err)
				}
				var output bytes.Buffer
				incomplete, failed := false, false
				for chunk := range stream.Chunks {
					output.Write(chunk.Payload)
					incomplete = incomplete || chunk.Incomplete
					failed = failed || chunk.Err != nil
				}
				if failed != test.failure || incomplete != test.incomplete {
					t.Fatalf("stream failed=%v incomplete=%v; want %v/%v", failed, incomplete, test.failure, test.incomplete)
				}
				if !test.failure && !strings.Contains(output.String(), "Hello Devin") {
					t.Fatal("missing streamed content")
				}
				if calls != 2 {
					t.Fatal("CPA retried independently")
				}
			})
		}
	}
}

func TestDevinExecutorSessionIsolation(t *testing.T) {
	var sessions []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, raw, err := helps.ReadConnectFrame(r.Body)
		if err != nil {
			t.Error(err)
		}
		session, _, err := devinProtoField(raw, 15, protowire.BytesType)
		if err != nil {
			t.Error(err)
		}
		id, _, err := devinProtoField(session, 1, protowire.BytesType)
		if err != nil {
			t.Error(err)
		}
		sessions = append(sessions, string(id))
		_, _ = w.Write(devinTestReply(2, true))
	}))
	defer server.Close()
	executor := NewDevinHTTPExecutor().(*devinHTTPExecutor)
	executor.baseURL = server.URL
	credential := DevinCredential{Type: ProviderDevin, SessionToken: "secret", UserID: "one"}
	payload := []byte(`{"model":"devin/swe-2","session_id":"11111111-1111-1111-1111-111111111111","input":"hello"}`)
	request := ExecuteRequest{Model: "devin/swe-2", Format: "openai-response", Payload: payload, OriginalRequest: payload, ContinuityKey: "conversation"}
	for _, id := range []string{"one", "one", "two"} {
		if _, err := executor.ExecuteCanonical(context.Background(), id, credential, request); err != nil {
			t.Fatal(err)
		}
	}
	if len(sessions) != 3 || sessions[0] == "" || sessions[0] != sessions[1] || sessions[0] == sessions[2] || sessions[0] == "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("session isolation failed: %v", sessions)
	}
}

func TestDevinExecutorSetupErrorReturnsWithoutStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = io.WriteString(w, "secret-token")
	}))
	defer server.Close()
	executor := NewDevinHTTPExecutor().(*devinHTTPExecutor)
	executor.baseURL = server.URL
	response, err := executor.ExecuteStreamCanonical(context.Background(), "one", DevinCredential{Type: ProviderDevin, SessionToken: "secret", UserID: "one"}, ExecuteRequest{Model: "devin/swe-2", Format: "openai-response", Payload: []byte(`{"input":"hello"}`)})
	if err == nil || response == nil || response.Chunks != nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatal("invalid stream setup error")
	}
}

func TestDevinExecutorDecodesHTTPCompressionBeforeConnectFrames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		writer := gzip.NewWriter(w)
		_, _ = writer.Write(devinTestReply(3, true))
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	executor := NewDevinHTTPExecutor().(*devinHTTPExecutor)
	executor.baseURL = server.URL
	credential := DevinCredential{Type: ProviderDevin, SessionToken: "secret", UserID: "one"}
	request := ExecuteRequest{Model: "swe-2", Format: "openai-response", Payload: []byte(`{"input":"hello"}`), Headers: http.Header{"Accept-Encoding": []string{"gzip"}}, ConfiguredHeaders: []string{"Accept-Encoding"}}
	stream, err := executor.ExecuteStreamCanonical(t.Context(), "one", credential, request)
	if err != nil {
		t.Fatal(err)
	}
	incomplete := false
	for chunk := range stream.Chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		incomplete = incomplete || chunk.Incomplete
	}
	if !incomplete {
		t.Fatal("compressed response lost terminal state")
	}
}

func TestDevinExecutorCancellationClosesUpstreamBody(t *testing.T) {
	canceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/connect+proto")
		_, _ = w.Write(helps.WrapConnectEnvelope(devinTestBytes(3, []byte("hello"))))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(canceled)
	}))
	defer server.Close()
	executor := NewDevinHTTPExecutor().(*devinHTTPExecutor)
	executor.baseURL = server.URL
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := executor.ExecuteStreamCanonical(ctx, "one", DevinCredential{Type: ProviderDevin, SessionToken: "secret", UserID: "one"}, ExecuteRequest{Model: "swe-2", Format: "openai-response", Payload: []byte(`{"input":"hello"}`)})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-stream.Chunks:
	case <-time.After(3 * time.Second):
		t.Fatal("no initial chunk")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream body did not close")
	}
}

func TestDevinExecutorReportsAppliedReasoningFromWireModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, raw, err := helps.ReadConnectFrame(r.Body)
		if err != nil {
			t.Error(err)
		}
		model, _, err := devinProtoField(raw, 21, protowire.BytesType)
		if err != nil {
			t.Error(err)
		}
		if !strings.HasSuffix(strings.ToLower(string(model)), "high") {
			t.Errorf("reasoning did not reach upstream: %s", model)
		}
		_, _ = w.Write(devinTestReply(2, true))
	}))
	defer server.Close()
	executor := NewDevinHTTPExecutor().(*devinHTTPExecutor)
	executor.baseURL = server.URL
	payload := []byte(`{"input":"hello","reasoning":{"effort":"high"}}`)
	response, err := executor.ExecuteCanonical(t.Context(), "one", DevinCredential{Type: ProviderDevin, SessionToken: "secret", UserID: "one"}, ExecuteRequest{Model: "swe-2", Format: "openai-response", Payload: payload, OriginalRequest: payload})
	if err != nil {
		t.Fatal(err)
	}
	if response.AppliedReasoningEffort != "high" {
		t.Fatalf("applied reasoning = %q", response.AppliedReasoningEffort)
	}
}

func TestDevinExecutorToolCallsAndResultRoundTrip(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, body, err := helps.ReadConnectFrame(r.Body)
		if err != nil {
			t.Error(err)
		}
		if calls == 1 {
			if !bytes.Contains(body, []byte("lookup")) {
				t.Error("tool definition missing upstream")
			}
			tool := append(devinTestBytes(1, []byte("upstream/call+1")), devinTestBytes(2, []byte("lookup"))...)
			tool = append(tool, devinTestBytes(3, []byte(`{"city":"成都"}`))...)
			frame := devinTestBytes(6, tool)
			frame = protowire.AppendVarint(protowire.AppendTag(frame, 5, protowire.VarintType), 10)
			_, _ = w.Write(helps.WrapConnectEnvelope(frame))
			_, _ = w.Write(helps.WrapConnectEnvelopeWithFlag(helps.ConnectFlagEndStream, []byte(`{}`)))
		} else {
			if !bytes.Contains(body, []byte("upstream/call+1")) || !bytes.Contains(body, []byte("tool-result")) {
				t.Error("tool result lost original upstream call identity")
			}
			_, _ = w.Write(devinTestReply(2, true))
		}
	}))
	defer server.Close()
	executor := NewDevinHTTPExecutor().(*devinHTTPExecutor)
	executor.baseURL = server.URL
	credential := DevinCredential{Type: ProviderDevin, SessionToken: "secret", UserID: "one"}
	payload := []byte(`{"messages":[{"role":"user","content":"weather"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]}`)
	request := ExecuteRequest{Model: "swe-2", Format: "openai", Payload: payload, OriginalRequest: payload, ContinuityKey: "session"}
	response, err := executor.ExecuteCanonical(t.Context(), "one", credential, request)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Choices []struct {
			Message json.RawMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(response.Payload, &result); err != nil || len(result.Choices) != 1 {
		t.Fatal("invalid tool response")
	}
	var message struct {
		ToolCalls []struct {
			ID string `json:"id"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal(result.Choices[0].Message, &message); err != nil || len(message.ToolCalls) != 1 {
		t.Fatal("tool call missing")
	}
	next, err := json.Marshal(map[string]any{"messages": []any{map[string]string{"role": "user", "content": "weather"}, result.Choices[0].Message, map[string]string{"role": "tool", "tool_call_id": message.ToolCalls[0].ID, "content": "tool-result"}}})
	if err != nil {
		t.Fatal(err)
	}
	request.Payload, request.OriginalRequest = next, next
	if _, err := executor.ExecuteCanonical(t.Context(), "one", credential, request); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("unexpected number of upstream requests")
	}
}

func TestDevinTrailerQuotaRemainsCredentialScoped(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write(helps.WrapConnectEnvelopeWithFlag(helps.ConnectFlagEndStream, []byte(`{"error":{"code":"failed_precondition","message":"Your weekly usage quota has been exhausted."}}`)))
	}))
	defer server.Close()
	executor := NewDevinHTTPExecutor().(*devinHTTPExecutor)
	executor.baseURL = server.URL
	stream, err := executor.ExecuteStreamCanonical(t.Context(), "one", DevinCredential{Type: ProviderDevin, SessionToken: "secret", UserID: "one"}, ExecuteRequest{Model: "swe-2", Format: "openai-response", Payload: []byte(`{"input":"hello"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var quotaErr *DevinExecutionError
	for chunk := range stream.Chunks {
		if chunk.Err != nil {
			if !errors.As(chunk.Err, &quotaErr) {
				t.Fatalf("untyped quota error: %T", chunk.Err)
			}
		}
	}
	if quotaErr == nil || quotaErr.StatusCode() != 429 || !quotaErr.IsCredentialScoped() || calls != 1 {
		t.Fatal("quota error lost credential scope or retried in SDK")
	}
}

type devinUnsafeStatusError string

func (err devinUnsafeStatusError) Error() string { return string(err) }
func (devinUnsafeStatusError) StatusCode() int   { return 401 }
func TestDevinExecutionErrorNeverExportsArbitraryUpstreamCodes(t *testing.T) {
	err := normalizeDevinExecutionError(devinUnsafeStatusError(`{"error":{"code":"devin-session-token$secret","message":"secret"}}`))
	var typed *DevinExecutionError
	if !errors.As(err, &typed) || strings.Contains(typed.ErrorCode(), "secret") || strings.Contains(typed.Error(), "secret") {
		t.Fatal("upstream error exported a secret")
	}
}
