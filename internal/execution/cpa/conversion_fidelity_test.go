package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/subscription/providers/grok"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestAdapterRejectsOnlyLossyMidConversationInstructions(t *testing.T) {
	t.Parallel()
	for _, channelID := range []channel.ID{channel.Codex, channel.Grok, channel.Claude, channel.Antigravity} {
		for _, clientProtocol := range []protocol.Protocol{protocol.OpenAICompletions, protocol.OpenAIResponses, protocol.Anthropic} {
			for _, role := range []string{"system", "developer", "user"} {
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/stream=%t", channelID, clientProtocol, role, stream), func(t *testing.T) {
						registry := channel.NewRegistry()
						target, err := registry.Resolve(channelID, nil)
						if err != nil {
							t.Fatal(err)
						}
						operation, path, field := execution.OperationChatCompletion, "/v1/chat/completions", "messages"
						if clientProtocol == protocol.OpenAIResponses {
							operation, path, field = execution.OperationResponsesCreate, "/v1/responses", "input"
						} else if clientProtocol == protocol.Anthropic {
							path = "/v1/messages"
						}
						mode, exists := target.Mode(clientProtocol, operation)
						if !exists {
							t.Fatal("expected subscription route is missing")
						}
						messages := []map[string]any{
							{"role": "user", "content": "start"},
							{"role": "assistant", "content": "reply"},
							{"role": role, "content": "<system-reminder>new instruction</system-reminder>"},
							{"role": "user", "content": "next"},
						}
						if clientProtocol == protocol.OpenAIResponses {
							messages = append([]map[string]any{{
								"type": "web_search_call", "id": "ws_history", "status": "completed",
								"action": map[string]any{"type": "search", "query": "synthetic"},
							}}, messages[2:]...)
						}
						body, err := json.Marshal(map[string]any{"model": "client-model", "max_tokens": 32, field: messages})
						if err != nil {
							t.Fatal(err)
						}
						preparer := &fakeCredentialPreparer{evidence: &execution.ErrorEvidence{
							Kind: execution.ErrorKindInternal, Code: "after_request_validation", Summary: "test boundary reached",
						}}
						adapter := NewAdapter(nil, registry)
						adapter.credentials = preparer
						spec := execution.NewAttemptSpec(execution.AttemptSpec{
							RequestID: "fidelity-request", AttemptID: "fidelity-attempt", Sequence: 1,
							ChannelID: string(channelID), TargetConfig: target.TargetConfig, RouteMode: mode,
							ClientProtocol: clientProtocol, Operation: operation, Method: http.MethodPost, Path: path,
							ClientModel: "client-model", UpstreamModel: "upstream-model", Body: body,
							Credential: execution.NewCredentialSnapshot(1, 1, 1, []byte(`{}`)),
						})
						var evidence *execution.ErrorEvidence
						if stream {
							evidence = adapter.ExecuteStream(t.Context(), spec, func(execution.StreamEvent) error { return nil }).Error
						} else {
							evidence = adapter.Execute(t.Context(), spec).Error
						}
						wantRejected := role != "user" && mode == execution.RouteConverted &&
							(channelID == channel.Claude || channelID == channel.Antigravity)
						if wantRejected {
							if preparer.calls != 0 || evidence == nil || evidence.Kind != execution.ErrorKindConversionUnsupported ||
								evidence.Code != execution.ErrorCodeCriticalSemanticLoss {
								t.Fatalf("lossy conversion reached credentials: calls=%d error=%+v", preparer.calls, evidence)
							}
						} else if preparer.calls != 1 || evidence == nil || evidence.Code != "after_request_validation" {
							t.Fatalf("preservable input was rejected: calls=%d error=%+v", preparer.calls, evidence)
						}
					})
				}
			}
		}
	}
}

func TestAdapterPreservesAnthropicInstructionsOnResponsesTargets(t *testing.T) {
	for _, channelID := range []channel.ID{channel.Codex, channel.Grok} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", channelID, stream), func(t *testing.T) {
				model := "gpt-5.6-luna"
				canonical := credentialJSON("access", "refresh", time.Now().Add(time.Hour))
				if channelID == channel.Grok {
					model = "grok-4.3"
					var err error
					canonical, err = grok.MarshalCredential(grok.Credential{
						Type: grok.Provider, AccessToken: "access", RefreshToken: "refresh", AccountID: "xai-account",
						Email: "grok@example.com", Expire: "2030-01-01T00:00:00Z", TokenEndpoint: "https://auth.x.ai/oauth/token",
					})
					if err != nil {
						t.Fatal(err)
					}
				}
				adapter := NewAdapter(nil, channel.NewRegistry())
				adapter.credentials = &fakeCredentialPreparer{credential: subscriptionruntime.NewCredential(
					canonical, "instruction-account", subscriptionruntime.Account{}, time.Time{}, false, nil,
				)}
				body := []byte(fmt.Sprintf(`{
					"model":%q,"max_tokens":64,"stream":%t,
					"system":[{"type":"text","text":"GLOBAL"}],
					"thinking":{"type":"adaptive"},"output_config":{"effort":"high"},
					"messages":[
						{"role":"system","content":"PREFIX"},
						{"role":"user","content":"start"},
						{"role":"assistant","content":"first"},
						{"role":"system","content":[{"type":"text","text":"MID_ONE"},{"type":"text","text":"MID_TWO"}]},
						{"role":"developer","content":"EXISTING_DEVELOPER"},
						{"role":"user","content":"<system-reminder>ordinary reminder</system-reminder>"},
						{"role":"assistant","content":[{"type":"tool_use","id":"call_lookup","name":"lookup","input":{"query":"hello"}}]},
						{"role":"system","content":"DURING_TOOL"},
						{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_lookup","content":"tool result"}]},
						{"role":"user","content":"next"}
					],
					"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{"query":{"type":"string"}}}}]
				}`, model, stream))
				spec := execution.NewAttemptSpec(execution.AttemptSpec{
					RequestID: "instruction-request", AttemptID: "instruction-attempt", Sequence: 1,
					ChannelID: string(channelID), RouteMode: execution.RouteConverted,
					ClientProtocol: protocol.Anthropic, Operation: execution.OperationChatCompletion,
					ClientModel: model, UpstreamModel: model, Method: http.MethodPost, Path: "/v1/messages",
					Body: body, Credential: execution.NewCredentialSnapshot(1, 1, 1, canonical),
				})
				wireBodies := make(chan []byte, 1)
				transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
					wire, err := io.ReadAll(request.Body)
					if err != nil {
						return nil, err
					}
					select {
					case wireBodies <- wire:
					default:
						return nil, fmt.Errorf("unexpected repeated upstream request")
					}
					return &http.Response{
						StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
						Body:    io.NopCloser(strings.NewReader(`data: {"type":"response.completed","response":{"id":"resp_instructions","object":"response","status":"completed","model":"` + model + `","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}` + "\n\n")),
						Request: request,
					}, nil
				})
				ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
				var evidence *execution.ErrorEvidence
				if stream {
					evidence = adapter.ExecuteStream(ctx, spec, func(execution.StreamEvent) error { return nil }).Error
				} else {
					evidence = adapter.Execute(ctx, spec).Error
				}
				if evidence != nil {
					t.Fatalf("instruction conversion failed: %+v", evidence)
				}
				if !bytes.Equal(spec.Body, body) {
					t.Fatal("conversion changed the original request used by other attempts")
				}
				var wire []byte
				select {
				case wire = <-wireBodies:
				default:
					t.Fatal("request did not reach the upstream transport")
				}
				var order []string
				for _, item := range gjson.GetBytes(wire, "input").Array() {
					role := item.Get("role").String()
					if role == "" {
						role = item.Get("type").String()
					}
					order = append(order, role)
				}
				wantOrder := []string{"developer", "developer", "user", "assistant", "developer", "developer", "user", "function_call", "developer", "function_call_output", "user"}
				if !reflect.DeepEqual(order, wantOrder) {
					t.Fatalf("upstream message order = %v, want %v", order, wantOrder)
				}
				for path, want := range map[string]string{
					"input.0.content.0.text": "GLOBAL", "input.1.content.0.text": "PREFIX",
					"input.4.content.0.text": "MID_ONE", "input.4.content.1.text": "MID_TWO",
					"input.5.content.0.text": "EXISTING_DEVELOPER",
					"input.6.content.0.text": "<system-reminder>ordinary reminder</system-reminder>",
					"input.7.call_id":        "call_lookup", "input.7.name": "lookup",
					"input.8.content.0.text": "DURING_TOOL", "input.9.call_id": "call_lookup",
					"input.9.output":          "tool result",
					"input.10.content.0.text": "next", "reasoning.effort": "high",
				} {
					if got := gjson.GetBytes(wire, path).String(); got != want {
						t.Errorf("upstream %s = %q, want %q", path, got, want)
					}
				}
			})
		}
	}
}

func TestAdapterTokenCountPreservesAnthropicInstructions(t *testing.T) {
	for _, target := range []struct {
		channel channel.ID
		model   string
	}{
		{channel.Codex, "gpt-5.6-luna"},
		{channel.Grok, "grok-4.3"},
	} {
		for _, input := range []struct {
			name    string
			content string
		}{
			{"string", `"Always answer in Chinese"`},
			{"blocks", `[{"type":"text","text":"Use Chinese"},{"type":"text","text":"Keep the answer concise"}]`},
		} {
			t.Run(fmt.Sprintf("%s/%s", target.channel, input.name), func(t *testing.T) {
				adapter := NewAdapter(nil, channel.NewRegistry())
				preparer := &fakeCredentialPreparer{evidence: &execution.ErrorEvidence{
					Kind: execution.ErrorKindInternal, Summary: "local token count must not prepare credentials",
				}}
				adapter.credentials = preparer
				count := func(role string) int64 {
					t.Helper()
					body := []byte(fmt.Sprintf(`{
						"model":%q,"system":"GLOBAL",
						"messages":[
							{"role":%q,"content":"PREFIX"},
							{"role":"user","content":"hello"},
							{"role":"assistant","content":"ok"},
							{"role":%q,"content":%s},
							{"role":"user","content":"<system-reminder>ordinary reminder</system-reminder>"}
						]
					}`, target.model, role, role, input.content))
					spec := execution.NewAttemptSpec(execution.AttemptSpec{
						RequestID: "count-instructions", AttemptID: "count-instructions-1", Sequence: 1,
						ChannelID: string(target.channel), RouteMode: execution.RouteConverted,
						ClientProtocol: protocol.Anthropic, Operation: execution.OperationCountTokens,
						ClientModel: target.model, UpstreamModel: target.model,
						Method: http.MethodPost, Path: "/v1/messages/count_tokens", Body: body,
						Credential: execution.NewCredentialSnapshot(1, 1, 1, []byte(`{}`)),
					})
					result := adapter.Execute(t.Context(), spec)
					if result.Error != nil {
						t.Fatalf("count instructions: %+v", result.Error)
					}
					if preparer.calls != 0 || result.Header.Get(localTokenCountHeader) != "local-estimate" {
						t.Fatal("token count did not use the local estimator")
					}
					if !bytes.Equal(spec.Body, body) {
						t.Fatal("token count changed the original request")
					}
					tokens := gjson.GetBytes(result.Body, "input_tokens").Int()
					if tokens <= 0 {
						t.Fatalf("invalid token count: %s", result.Body)
					}
					return tokens
				}
				// 执行请求以 developer 原位发送指令，计数必须采用同一个提示。
				want := count("developer")
				if got := count("system"); got != want {
					t.Fatalf("system token count = %d, executed developer prompt count = %d", got, want)
				}
			})
		}
	}
}
