package bifrost

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestConvertedMidConversationInstructionsAreNotWeakened(t *testing.T) {
	t.Parallel()
	for _, clientProtocol := range []protocol.Protocol{protocol.OpenAICompletions, protocol.OpenAIResponses} {
		for _, test := range []struct {
			name      string
			channel   channel.ID
			model     string
			roles     []string
			wantError bool
		}{
			{"unsupported model", channel.Anthropic, "claude-sonnet-4-6", []string{"user", "system", "assistant"}, true},
			{"unsupported position", channel.Anthropic, "claude-opus-4-8", []string{"user", "assistant", "system", "user"}, true},
			{"supported position", channel.Anthropic, "claude-opus-4-8", []string{"user", "system", "assistant"}, false},
			{"ordinary user reminder", channel.Anthropic, "claude-sonnet-4-6", []string{"user", "assistant", "user"}, false},
			{"Gemini system hoist", channel.Gemini, "gemini-2.5-pro", []string{"user", "assistant", "system", "user"}, true},
		} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%t", clientProtocol, test.name, stream), func(t *testing.T) {
					var calls atomic.Int64
					server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
						calls.Add(1)
						var received struct {
							Messages []struct{ Role string } `json:"messages"`
						}
						if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
							t.Error(err)
						}
						if !test.wantError && test.name == "supported position" &&
							(len(received.Messages) < 2 || received.Messages[1].Role != "system") {
							t.Error("supported system instruction did not keep its position")
						}
						if stream {
							writer.Header().Set("Content-Type", "text/event-stream")
							_, _ = io.WriteString(writer, anthropicResponsesStreamFixture)
						} else {
							writer.Header().Set("Content-Type", "application/json")
							_, _ = io.WriteString(writer, anthropicResponsesConvertedFixture)
						}
					}))
					defer server.Close()
					runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true,
						anthropicBaseURL: server.URL, geminiBaseURL: server.URL + "/v1beta"})
					operation, path, field := execution.OperationChatCompletion, "/v1/chat/completions", "messages"
					if clientProtocol == protocol.OpenAIResponses {
						operation, path, field = execution.OperationResponsesCreate, "/v1/responses", "input"
					}
					messages := make([]map[string]any, 0, len(test.roles))
					for _, role := range test.roles {
						messages = append(messages, map[string]any{"role": role, "content": "<system-reminder>instruction</system-reminder>"})
					}
					body, err := json.Marshal(map[string]any{"model": "client-model", field: messages, "max_tokens": 32, "stream": stream})
					if err != nil {
						t.Fatal(err)
					}
					spec := convertedSpec(test.channel, clientProtocol, operation, path, body)
					spec.UpstreamModel = test.model
					var evidence *execution.ErrorEvidence
					if stream {
						result := runtime.ExecuteStream(t.Context(), spec, func(execution.StreamEvent) error { return nil })
						evidence = result.Error
					} else {
						evidence = runtime.Execute(t.Context(), spec).Error
					}
					if test.wantError {
						if calls.Load() != 0 || evidence == nil || evidence.Kind != execution.ErrorKindConversionUnsupported ||
							evidence.Code != execution.ErrorCodeCriticalSemanticLoss {
							t.Fatalf("lossy conversion: calls=%d error=%+v", calls.Load(), evidence)
						}
					} else if calls.Load() != 1 || evidence != nil {
						t.Fatalf("supported conversion: calls=%d error=%+v", calls.Load(), evidence)
					}
				})
			}
		}
	}
}

func TestConvertedInstructionsAfterRolelessResponsesHistory(t *testing.T) {
	t.Parallel()
	const search = `{"type":"web_search_call","id":"ws_history","status":"completed","action":{"type":"search","query":"synthetic"}}`
	const custom = `{"type":"custom_tool_call","id":"ct_history","call_id":"call_history","name":"synthetic_tool","input":"synthetic input"}`
	for _, test := range []struct {
		name      string
		history   string
		role      string
		wantError bool
	}{
		{"search then system", search, "system", true},
		{"search then developer", search, "developer", true},
		{"custom then system", custom, "system", true},
		{"custom then developer", custom, "developer", true},
		{"ordinary user reminder", search, "user", false},
		{"leading global instructions", `{"role":"system","content":"global instruction"}`, "developer", false},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", test.name, stream), func(t *testing.T) {
				var calls atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					calls.Add(1)
					body, err := io.ReadAll(request.Body)
					if err != nil {
						t.Error(err)
					}
					if test.wantError {
						t.Errorf("lossy instruction reached upstream: %s", body)
					} else if !strings.Contains(string(body), "caller reminder") {
						t.Errorf("preserved instruction or user text is missing: %s", body)
					}
					writer.Header().Set("Content-Type", "application/json")
					response := anthropicResponsesConvertedFixture
					if stream {
						writer.Header().Set("Content-Type", "text/event-stream")
						response = anthropicResponsesStreamFixture
					}
					_, _ = io.WriteString(writer, response)
				}))
				defer server.Close()
				runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, anthropicBaseURL: server.URL})
				body := []byte(fmt.Sprintf(`{"model":"client-model","store":false,"input":[%s,{"role":"%s","content":"<system-reminder>caller reminder</system-reminder>"},{"role":"user","content":"continue"}]}`, test.history, test.role))
				spec := convertedSpec(channel.Anthropic, protocol.OpenAIResponses, execution.OperationResponsesCreate, "/v1/responses", body)
				spec.UpstreamModel = "claude-sonnet-4-6"
				var evidence *execution.ErrorEvidence
				var dispatch execution.DispatchState
				if stream {
					result := runtime.ExecuteStream(t.Context(), spec, func(execution.StreamEvent) error { return nil })
					evidence, dispatch = result.Error, result.DispatchState
				} else {
					result := runtime.Execute(t.Context(), spec)
					evidence, dispatch = result.Error, result.DispatchState
				}
				if test.wantError {
					if calls.Load() != 0 || dispatch != execution.DispatchNotSent || evidence == nil ||
						evidence.Kind != execution.ErrorKindConversionUnsupported || evidence.Code != execution.ErrorCodeCriticalSemanticLoss {
						t.Fatalf("lossy conversion: calls=%d dispatch=%s error=%+v", calls.Load(), dispatch, evidence)
					}
				} else if calls.Load() != 1 || evidence != nil {
					t.Fatalf("preservable input was rejected: calls=%d error=%+v", calls.Load(), evidence)
				}
			})
		}
	}
}

func TestChatFallbackDoesNotDropRequestedTools(t *testing.T) {
	t.Parallel()
	for _, channelID := range []channel.ID{channel.OpenAICompatible, channel.Groq} {
		for _, test := range []struct {
			name      string
			tools     string
			choice    string
			wantError bool
		}{
			{"no tools automatic", `[]`, `"auto"`, false},
			{"no tools disabled", `[]`, `"none"`, false},
			{"no tools required", `[]`, `"required"`, true},
			{"web search", `[{"type":"web_search_preview"}]`, `"auto"`, true},
			{"function", `[{"type":"function","name":"lookup","parameters":{"type":"object"}}]`, `"required"`, false},
			{"allowed tools", `[{"type":"function","name":"lookup","parameters":{"type":"object"}}]`, `{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"lookup"}]}`, true},
		} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%t", channelID, test.name, stream), func(t *testing.T) {
					var calls atomic.Int64
					server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
						calls.Add(1)
						_, body, events := nativeFidelityFixture(protocol.OpenAICompletions)
						if stream {
							writer.Header().Set("Content-Type", "text/event-stream")
							_, _ = io.WriteString(writer, events)
						} else {
							writer.Header().Set("Content-Type", "application/json")
							_, _ = io.WriteString(writer, body)
						}
					}))
					defer server.Close()
					runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
					runtime.baseURLs[channelID] = server.URL
					body := []byte(fmt.Sprintf(`{"model":"client-model","input":"hello","store":false,"tools":%s,"tool_choice":%s}`, test.tools, test.choice))
					spec := convertedSpec(channelID, protocol.OpenAIResponses, execution.OperationResponsesCreate, "/v1/responses", body)
					var evidence *execution.ErrorEvidence
					if stream {
						evidence = runtime.ExecuteStream(t.Context(), spec, func(execution.StreamEvent) error { return nil }).Error
					} else {
						evidence = runtime.Execute(t.Context(), spec).Error
					}
					if test.wantError {
						if calls.Load() != 0 || evidence == nil || evidence.Kind != execution.ErrorKindConversionUnsupported || evidence.Code != execution.ErrorCodeCriticalSemanticLoss {
							t.Fatalf("tool requirement was lost: calls=%d error=%+v", calls.Load(), evidence)
						}
					} else if calls.Load() != 1 || evidence != nil {
						t.Fatalf("function tool was rejected: calls=%d error=%+v", calls.Load(), evidence)
					}
				})
			}
		}
	}
}

func TestDeepSeekConversionDoesNotDisableExplicitThinking(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		thinking  string
		mode      string
		history   string
		wantError bool
	}{
		{"forced tool with thinking", `{"thinkingBudget":4096}`, "ANY", "", true},
		{"automatic tools with thinking", `{"thinkingLevel":"high"}`, "AUTO", "", false},
		{"forced tool without explicit thinking", `{}`, "ANY", "", false},
		{"missing replay with thinking", `{"thinkingLevel":"high"}`, "AUTO", `,{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{"result":"ok"}}}]}`, true},
		{"thinking disabled", `{"thinkingBudget":0}`, "ANY", "", false},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", test.name, stream), func(t *testing.T) {
				var calls atomic.Int64
				var wireBody []byte
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					wireBody, _ = io.ReadAll(request.Body)
					calls.Add(1)
					_, body, events := nativeFidelityFixture(protocol.OpenAICompletions)
					if stream {
						writer.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(writer, events)
					} else {
						writer.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(writer, body)
					}
				}))
				defer server.Close()
				runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
				runtime.baseURLs[channel.DeepSeek] = server.URL
				body := []byte(fmt.Sprintf(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}%s],"generationConfig":{"thinkingConfig":%s},"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object"}}]}],"toolConfig":{"functionCallingConfig":{"mode":"%s"}}}`, test.history, test.thinking, test.mode))
				spec := convertedSpec(channel.DeepSeek, protocol.Gemini, execution.OperationChatCompletion, "/v1beta/models/client-model:generateContent", body)
				if stream {
					spec.Path = "/v1beta/models/client-model:streamGenerateContent"
				}
				var evidence *execution.ErrorEvidence
				if stream {
					evidence = runtime.ExecuteStream(t.Context(), spec, func(execution.StreamEvent) error { return nil }).Error
				} else {
					evidence = runtime.Execute(t.Context(), spec).Error
				}
				if test.wantError {
					if calls.Load() != 0 || evidence == nil || evidence.Kind != execution.ErrorKindConversionUnsupported || evidence.Code != execution.ErrorCodeCriticalSemanticLoss {
						t.Fatalf("thinking was silently disabled: calls=%d error=%+v wire=%s", calls.Load(), evidence, wireBody)
					}
				} else if calls.Load() != 1 || evidence != nil {
					t.Fatalf("valid thinking/tool combination was rejected: calls=%d error=%+v", calls.Load(), evidence)
				} else if test.name == "automatic tools with thinking" {
					wire := nativeFidelityObject(t, wireBody)
					thinking, _ := wire["thinking"].(map[string]any)
					if wire["tool_choice"] != "auto" || thinking["type"] == "disabled" {
						t.Fatalf("automatic tools changed thinking or forced a tool: %s", wireBody)
					}
				}
			})
		}
	}
}

func TestCloudConversionKeepsSupportedSystemRoles(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		channel    channel.ID
		model      string
		config     string
		credential string
		wantError  bool
	}{
		{channel.GoogleVertex, "claude-opus-4-8", `{"location":"us-central1"}`, `{"service_account_json":"{\"type\":\"service_account\",\"project_id\":\"test-project\",\"client_email\":\"svc@example.com\",\"private_key\":\"synthetic\"}"}`, true},
		{channel.GoogleVertex, "gemini-2.5-pro", `{"location":"us-central1"}`, `{"service_account_json":"{\"type\":\"service_account\",\"project_id\":\"test-project\",\"client_email\":\"svc@example.com\",\"private_key\":\"synthetic\"}"}`, true},
		{channel.GoogleVertex, "meta/llama-4", `{"location":"us-central1"}`, `{"service_account_json":"{\"type\":\"service_account\",\"project_id\":\"test-project\",\"client_email\":\"svc@example.com\",\"private_key\":\"synthetic\"}"}`, false},
		{channel.AWSBedrock, "anthropic.claude-opus-4-8", `{"region":"us-east-1"}`, `{"api_key":"synthetic"}`, true},
		{channel.AWSBedrock, "amazon.nova-pro-v1:0", `{"region":"us-east-1"}`, `{"api_key":"synthetic"}`, true},
	} {
		for _, clientProtocol := range []protocol.Protocol{protocol.OpenAICompletions, protocol.OpenAIResponses} {
			t.Run(fmt.Sprintf("%s/%s/%s", test.channel, test.model, clientProtocol), func(t *testing.T) {
				field, path, operation := "messages", "/v1/chat/completions", execution.OperationChatCompletion
				if clientProtocol == protocol.OpenAIResponses {
					field, path, operation = "input", "/v1/responses", execution.OperationResponsesCreate
				}
				body := []byte(fmt.Sprintf(`{"model":"client-model","%s":[{"role":"user","content":"hello"},{"role":"system","content":"instruction"},{"role":"assistant","content":"ok"},{"role":"user","content":"next"}]}`, field))
				spec := convertedSpec(test.channel, clientProtocol, operation, path, body)
				spec.TargetConfig = json.RawMessage(test.config)
				spec.UpstreamModel = test.model
				spec.Credential = execution.NewCredentialSnapshot(1, 1, 1, []byte(test.credential))
				spec = freezeTestAttempt(spec)
				runtime := newTestRuntime(t)
				for _, stream := range []bool{false, true} {
					_, failure := runtime.prepare(spec, stream)
					if test.wantError {
						if failure == nil || failure.Error == nil || failure.Error.Code != execution.ErrorCodeCriticalSemanticLoss || failure.DispatchState != execution.DispatchNotSent {
							t.Fatalf("lossy cloud conversion was not rejected before dispatch: %+v", failure)
						}
					} else if failure != nil {
						t.Fatalf("OpenAI wire can preserve system roles: %+v", failure.Error)
					}
				}
			})
		}
	}
}

func TestConvertedResponsesKeepsAllGlobalInstructions(t *testing.T) {
	t.Parallel()
	for _, channelID := range []channel.ID{channel.Anthropic, channel.Gemini, channel.OpenAICompatible} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", channelID, stream), func(t *testing.T) {
				var calls atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					calls.Add(1)
					body, _ := io.ReadAll(request.Body)
					wire := string(body)
					first, second := strings.Index(wire, "FIRST_GLOBAL"), strings.Index(wire, "SECOND_GLOBAL")
					if first < 0 || second < 0 || first > second || strings.Count(wire, "FIRST_GLOBAL") != 1 || strings.Count(wire, "SECOND_GLOBAL") != 1 {
						t.Errorf("global instructions were dropped, duplicated or reordered: %s", body)
					}
					_, response, events := nativeFidelityFixture(protocol.OpenAICompletions)
					if channelID == channel.Anthropic {
						response, events = anthropicResponsesConvertedFixture, anthropicResponsesStreamFixture
					} else if channelID == channel.Gemini {
						response, events = geminiResponsesConvertedFixture, geminiResponsesStreamFixture
					}
					writer.Header().Set("Content-Type", "application/json")
					if stream {
						writer.Header().Set("Content-Type", "text/event-stream")
						response = events
					}
					_, _ = io.WriteString(writer, response)
				}))
				defer server.Close()
				runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, anthropicBaseURL: server.URL, geminiBaseURL: server.URL + "/v1beta"})
				spec := convertedSpec(channelID, protocol.OpenAIResponses, execution.OperationResponsesCreate, "/v1/responses", []byte(`{"model":"client-model","instructions":"FIRST_GLOBAL","input":[{"role":"system","content":"SECOND_GLOBAL"},{"role":"user","content":"hello"}],"store":false}`))
				if channelID == channel.OpenAICompatible {
					spec.TargetConfig = json.RawMessage(`{"base_url":"` + server.URL + `/v1"}`)
					spec = freezeTestAttempt(spec)
				}
				var evidence *execution.ErrorEvidence
				if stream {
					evidence = runtime.ExecuteStream(t.Context(), spec, func(execution.StreamEvent) error { return nil }).Error
				} else {
					evidence = runtime.Execute(t.Context(), spec).Error
				}
				if calls.Load() != 1 || evidence != nil {
					t.Fatalf("valid global instructions were rejected: calls=%d error=%+v", calls.Load(), evidence)
				}
			})
		}
	}
}
