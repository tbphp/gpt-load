package bifrost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

func TestNativeMessagesPreserveConversationAndExtensions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		channel  channel.ID
		protocol protocol.Protocol
		prefix   string
		path     string
	}{
		{channel.DeepSeek, protocol.Anthropic, "/tenant", "/anthropic/v1/messages"},
		{channel.DeepSeek, protocol.OpenAICompletions, "/tenant", "/chat/completions"},
		{channel.DeepSeek, protocol.OpenAIResponses, "/tenant", "/responses"},
		{channel.OpenRouter, protocol.OpenAICompletions, "/api", "/v1/chat/completions"},
		{channel.OpenRouter, protocol.OpenAIResponses, "/api", "/v1/responses"},
		{channel.Groq, protocol.OpenAICompletions, "/openai", "/v1/chat/completions"},
		{channel.XAI, protocol.OpenAICompletions, "", "/v1/chat/completions"},
		{channel.XAI, protocol.OpenAIResponses, "", "/v1/responses"},
		{channel.OpenAICompatible, protocol.OpenAICompletions, "/custom/api/v4", "/chat/completions"},
		{channel.ZhipuAI, protocol.OpenAICompletions, "/api/paas/v4", "/chat/completions"},
		{channel.Volcengine, protocol.OpenAICompletions, "/api/v3", "/chat/completions"},
	}
	for _, test := range tests {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%s/stream=%t", test.channel, test.protocol, stream), func(t *testing.T) {
				body, responseBody, responseStream := nativeFidelityFixture(test.protocol)
				var original map[string]json.RawMessage
				if err := json.Unmarshal([]byte(body), &original); err != nil {
					t.Fatal(err)
				}
				original["stream"] = json.RawMessage(fmt.Sprint(stream))
				requestBody, err := json.Marshal(original)
				if err != nil {
					t.Fatal(err)
				}
				expected := nativeFidelityObject(t, requestBody)
				expected["model"] = "upstream-model"
				delete(expected, "provider")
				expectedBody, err := json.Marshal(expected)
				if err != nil {
					t.Fatal(err)
				}
				var calls atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					calls.Add(1)
					if request.Method != http.MethodPost || request.URL.Path != test.prefix+test.path {
						t.Errorf("target = %s %s, want POST %s", request.Method, request.URL.Path, test.prefix+test.path)
					}
					if test.protocol == protocol.Anthropic {
						if request.Header.Get("X-Api-Key") != "native-key" || request.Header.Get("Authorization") != "" {
							t.Error("Anthropic credential was not selected correctly")
						}
					} else if request.Header.Get("Authorization") != "Bearer native-key" {
						t.Error("OpenAI credential was not selected correctly")
					}
					actualBody, readErr := io.ReadAll(request.Body)
					if readErr != nil {
						t.Error(readErr)
						return
					}
					if actual := nativeFidelityObject(t, actualBody); !reflect.DeepEqual(actual, expected) {
						t.Errorf("native request changed conversation or extensions:\ngot  %s\nwant %s", actualBody, expectedBody)
					}
					if stream {
						writer.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(writer, responseStream)
					} else {
						writer.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(writer, responseBody)
					}
				}))
				defer server.Close()
				runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
				baseURL := server.URL + test.prefix
				var spec execution.AttemptSpec
				switch test.protocol {
				case protocol.Anthropic:
					spec = deepSeekAnthropicAttempt(t, baseURL)
				case protocol.OpenAIResponses:
					spec = openAIResponsesAttempt(t, test.channel, baseURL)
				default:
					spec = openAIChatAttempt(t, test.channel, baseURL, "native-key")
				}
				spec.ClientModel = spec.UpstreamModel
				spec.Body = requestBody
				if stream {
					var events []execution.StreamEvent
					result := runtime.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
						events = append(events, event.Clone())
						return nil
					})
					assertRawStream(t, result, events, responseStream, usage.Tokens{UncachedInput: 100, CacheRead: 900, Output: 3})
				} else {
					result := runtime.Execute(t.Context(), spec)
					if result.Error != nil || result.StatusCode != http.StatusOK || result.UpstreamProtocol != test.protocol {
						t.Fatalf("native result = %+v", result)
					}
					if !bytes.Equal(result.Body, []byte(responseBody)) {
						t.Errorf("native response changed: %s", result.Body)
					}
					assertUsage(t, result.Usage, usage.Tokens{UncachedInput: 100, CacheRead: 900, Output: 3})
				}
				if calls.Load() != 1 {
					t.Errorf("upstream calls = %d, want 1", calls.Load())
				}
			})
		}
	}
}

func nativeFidelityObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var result map[string]any
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestNativeDeepSeekAnthropicReplayKeepsExistingPrefix(t *testing.T) {
	t.Parallel()
	wireBodies := make(chan []byte, 2)
	const response = `{"id":"msg_replay","type":"message","role":"assistant","model":"upstream-model","content":[{"type":"thinking","thinking":"replay reasoning","signature":"synthetic-native-signature"},{"type":"tool_use","id":"call_replay","name":"lookup","input":{"n":9007199254740993}}],"stop_reason":"tool_use","usage":{"input_tokens":4,"output_tokens":2}}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		wireBodies <- body
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, response)
	}))
	defer server.Close()
	runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
	spec := deepSeekAnthropicAttempt(t, server.URL)
	initial, _, _ := nativeFidelityFixture(protocol.Anthropic)
	spec.Body = []byte(initial)
	first := runtime.Execute(t.Context(), spec)
	if first.Error != nil {
		t.Fatalf("first request: %+v", first.Error)
	}
	var returned struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(first.Body, &returned); err != nil {
		t.Fatal(err)
	}
	root := nativeFidelityObject(t, spec.Body)
	history := root["messages"].([]any)
	assistant := map[string]any{"role": returned.Role, "content": returned.Content}
	root["messages"] = append(history, assistant,
		map[string]any{"role": "user", "content": json.RawMessage(`[{"type":"tool_result","tool_use_id":"call_replay","content":"result"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aW1hZ2U="}}]`)},
		map[string]any{"role": "system", "content": "new round instruction"},
		map[string]any{"role": "user", "content": "continue"})
	var err error
	spec.Body, err = json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	second := runtime.Execute(t.Context(), spec)
	if second.Error != nil {
		t.Fatalf("replayed request: %+v", second.Error)
	}
	before, after := nativeFidelityObject(t, <-wireBodies), nativeFidelityObject(t, <-wireBodies)
	oldMessages, newMessages := before["messages"].([]any), after["messages"].([]any)
	if !reflect.DeepEqual(oldMessages, newMessages[:len(oldMessages)]) {
		t.Fatal("appending tool results and system instructions changed the existing message prefix")
	}
	expected := nativeFidelityObject(t, spec.Body)
	if !reflect.DeepEqual(after["messages"], expected["messages"]) {
		t.Fatal("response replay changed thinking, signature, tool arguments, media or associations")
	}
	for _, field := range []string{"system", "tools", "thinking", "output_config"} {
		if !reflect.DeepEqual(before[field], after[field]) {
			t.Errorf("appending a round changed %s", field)
		}
	}
}

func nativeFidelityFixture(clientProtocol protocol.Protocol) (string, string, string) {
	switch clientProtocol {
	case protocol.Anthropic:
		return `{"model":"client-model","provider":"must-strip","max_tokens":8192,"system":[{"type":"text","text":"stable\ninstruction","cache_control":{"type":"ephemeral"}}],"thinking":{"type":"adaptive"},"output_config":{"effort":"high"},"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{"q":{"type":"string"}}},"vendor_tool":{"precise":1.2300}}],"messages":[{"role":"user","content":"start"},{"role":"assistant","content":[{"type":"thinking","thinking":"reasoning history","signature":""},{"type":"tool_use","id":"call_1","name":"lookup","input":{"q":"one","n":9007199254740993}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"result"}]},{"role":"system","content":[{"type":"text","text":"new instruction"}]},{"role":"assistant","content":[{"type":"text","text":"continued","vendor_block":true}]},{"role":"user","content":"next"}],"vendor_extension":{"precise":1.2300}}`,
			`{"id":"msg_native","type":"message","role":"assistant","model":"upstream-model","content":[{"type":"text","text":"ok","vendor":true}],"stop_reason":"end_turn","usage":{"input_tokens":100,"cache_read_input_tokens":900,"output_tokens":3},"vendor_response":true}`,
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_native\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"upstream-model\",\"content\":[],\"usage\":{\"input_tokens\":100,\"cache_read_input_tokens\":900,\"output_tokens\":0}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"},\"vendor\":true}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	case protocol.OpenAIResponses:
		return `{"model":"client-model","provider":"must-strip","instructions":"stable\ninstruction","input":[{"role":"user","content":[{"type":"input_text","text":"start","vendor_block":true}]},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{\"n\":9007199254740993}"},{"type":"function_call_output","call_id":"call_1","output":"result"},{"role":"system","content":"new instruction"},{"role":"user","content":"next"}],"reasoning":{"effort":"high"},"vendor_extension":{"precise":1.2300}}`,
			`{"id":"resp_native","object":"response","status":"completed","model":"upstream-model","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1000,"input_tokens_details":{"cached_tokens":900},"output_tokens":3,"total_tokens":1003},"vendor_response":true}`,
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\",\"vendor\":true}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_native\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"upstream-model\",\"output\":[],\"usage\":{\"input_tokens\":1000,\"input_tokens_details\":{\"cached_tokens\":900},\"output_tokens\":3,\"total_tokens\":1003}}}\n\n"
	default:
		return `{"model":"client-model","provider":"must-strip","messages":[{"role":"system","content":"stable\ninstruction"},{"role":"user","content":"start"},{"role":"assistant","content":null,"reasoning_content":"reasoning history","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"n\":9007199254740993}"}}],"vendor_message":true},{"role":"tool","tool_call_id":"call_1","content":"result"},{"role":"system","content":"new instruction"},{"role":"user","content":"next"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}},"vendor_tool":true}],"tool_choice":"required","thinking":{"type":"enabled"},"reasoning_effort":"high","vendor_extension":{"precise":1.2300}}`,
			`{"id":"chat_native","object":"chat.completion","model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok","vendor":true},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000,"prompt_tokens_details":{"cached_tokens":900},"completion_tokens":3,"total_tokens":1003},"vendor_response":true}`,
			"data: {\"id\":\"chat_native\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\",\"vendor\":true},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chat_native\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1000,\"prompt_tokens_details\":{\"cached_tokens\":900},\"completion_tokens\":3,\"total_tokens\":1003}}\n\ndata: [DONE]\n\n"
	}
}
