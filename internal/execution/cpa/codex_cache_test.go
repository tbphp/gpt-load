package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestCodexCacheIdentityAcrossProtocols(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			protocol      protocol.Protocol
			path, payload string
		}{
			{protocol.OpenAICompletions, "/v1/chat/completions", `{"model":"gpt-5.6-luna","messages":[{"role":"system","content":"stable"},{"role":"user","content":"hello"}]}`},
			{protocol.OpenAIResponses, "/v1/responses", `{"model":"gpt-5.6-luna","instructions":"stable","input":"hello"}`},
			{protocol.Anthropic, "/v1/messages", `{"model":"gpt-5.6-luna","max_tokens":64,"system":"stable","messages":[{"role":"user","content":"hello"}]}`},
			{protocol.Gemini, "/v1beta/models/gpt-5.6-luna:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`},
			{protocol.OpenAICompletions, "/v1/chat/completions", `{"model":"gpt-5.6-luna","prompt_cache_key":"caller-cache","messages":[{"role":"user","content":"hello"}]}`},
			{protocol.OpenAIResponses, "/v1/responses", `{"model":"gpt-5.6-luna","prompt_cache_key":"caller-cache","input":"hello"}`},
		} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.protocol, stream), func(t *testing.T) {
				canonical := credentialJSON("access", "refresh", time.Now().Add(time.Hour))
				adapter := NewAdapter(nil, channel.NewRegistry())
				adapter.credentials = &fakeCredentialPreparer{credential: subscriptionruntime.NewCredential(canonical, "cache-account", subscriptionruntime.Account{}, time.Time{}, false, nil)}
				var keys, sessions []string
				transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					body, err := io.ReadAll(req.Body)
					if err != nil {
						return nil, err
					}
					var obj map[string]any
					if err := json.Unmarshal(body, &obj); err != nil {
						return nil, err
					}
					key, _ := obj["prompt_cache_key"].(string)
					keys = append(keys, key)
					sessions = append(sessions, req.Header.Get("Session-Id"))
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(`data: {"type":"response.completed","response":{"id":"resp_cache","object":"response","status":"completed","model":"gpt-5.6-luna","output":[],"usage":{"input_tokens":8192,"output_tokens":4,"total_tokens":8196,"input_tokens_details":{"cached_tokens":4096}}}}` + "\n\n")), Request: req}, nil
				})
				ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
				for _, scope := range []string{"tenant-a-prefix", "tenant-a-prefix", "tenant-b-prefix"} {
					operation := execution.OperationChatCompletion
					mode := execution.RouteConverted
					if tc.protocol == protocol.OpenAIResponses {
						operation = execution.OperationResponsesCreate
						mode = execution.RouteNative
					}
					body := []byte(tc.payload)
					spec := execution.NewAttemptSpec(execution.AttemptSpec{RequestID: "cache-test", AttemptID: "cache-attempt", Sequence: 1, ChannelID: string(channel.Codex), RouteMode: mode, ClientProtocol: tc.protocol, Operation: operation, ClientModel: "gpt-5.6-luna", UpstreamModel: "gpt-5.6-luna", Method: http.MethodPost, Path: tc.path, Body: body, ContinuityKey: scope, Credential: execution.NewCredentialSnapshot(1, 1, 1, canonical)})
					var evidence *execution.ErrorEvidence
					if stream {
						evidence = adapter.ExecuteStream(ctx, spec, func(execution.StreamEvent) error { return nil }).Error
					} else {
						evidence = adapter.Execute(ctx, spec).Error
					}
					if evidence != nil {
						t.Fatalf("execution failed: %+v", evidence)
					}
					if !bytes.Equal(body, spec.Body) {
						t.Fatal("source body mutated")
					}
				}
				explicit := strings.Contains(tc.payload, "caller-cache")
				if len(keys) != 3 || keys[0] == "" || keys[0] != keys[1] || (!explicit && keys[0] == keys[2]) || (explicit && (keys[0] != "caller-cache" || keys[2] != "caller-cache")) {
					t.Fatalf("unstable or unisolated cache keys: %v", keys)
				}
				for i := range keys {
					if keys[i] != sessions[i] {
						t.Fatal("cache key and session disagree")
					}
				}
			})
		}
	}
}

func TestCodexCallerSessionIdentityPrecedesDerivedCacheGroup(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, claude := range []bool{false, true} {
			t.Run(fmt.Sprintf("claude=%t/stream=%t", claude, stream), func(t *testing.T) {
				adapter, _, _, service, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
				var sessions []string
				transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					sessions = append(sessions, req.Header.Get("Session-Id"))
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(`data: {"type":"response.completed","response":{"id":"resp_identity","object":"response","status":"completed","model":"gpt-5","output":[]}}` + "\n\n")), Request: req}, nil
				})
				ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
				for _, scope := range []string{"prefix-a", "prefix-b"} {
					spec := validSpec(t, row, service)
					spec.ContinuityKey = scope
					spec.Header = http.Header{"Session-Id": {"caller-session"}}
					if claude {
						spec.ClientProtocol = protocol.Anthropic
						spec.Operation = execution.OperationChatCompletion
						spec.RouteMode = execution.RouteConverted
						spec.Path = "/v1/messages"
						spec.Body = []byte(`{"model":"gpt-5","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`)
						spec.Header = http.Header{"X-Claude-Code-Session-Id": {"claude-session"}}
					}
					var evidence *execution.ErrorEvidence
					if stream {
						evidence = adapter.ExecuteStream(ctx, spec, func(execution.StreamEvent) error { return nil }).Error
					} else {
						evidence = adapter.Execute(ctx, spec).Error
					}
					if evidence != nil {
						t.Fatalf("execute: %+v", evidence)
					}
				}
				if len(sessions) != 2 || sessions[0] == "" || sessions[0] != sessions[1] {
					t.Fatal("caller identity overwritten by derived cache group")
				}
				if !claude && sessions[0] != "caller-session" {
					t.Fatal("caller session changed")
				}
			})
		}
	}
}
