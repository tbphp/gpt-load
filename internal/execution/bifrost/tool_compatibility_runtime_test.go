package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tidwall/gjson"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
)

func TestOpenAICompatibleAllowedToolsTwoTurnFunctionFlow(t *testing.T) {
	t.Parallel()
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				call := calls.Add(1)
				body, _ := io.ReadAll(request.Body)
				if request.URL.Path != "/tenant/v1/chat/completions" ||
					gjson.GetBytes(body, "tools.#").Int() != 1 ||
					gjson.GetBytes(body, "tools.0.function.name").String() != "lookup" ||
					gjson.GetBytes(body, "tool_choice").String() != "required" ||
					gjson.GetBytes(body, "parallel_tool_calls").Bool() {
					t.Errorf("converted request lost tool constraints: %s", body)
				}
				if call == 2 {
					assertChatFunctionHistory(t, body)
				}
				if stream {
					writer.Header().Set("Content-Type", "text/event-stream")
					if call == 1 {
						_, _ = io.WriteString(writer, openAIChatToolStream)
					} else {
						_, _ = io.WriteString(writer, openAIChatFinalStream)
					}
					return
				}
				writer.Header().Set("Content-Type", "application/json")
				if call == 1 {
					_, _ = io.WriteString(writer, openAIChatToolResponse)
				} else {
					_, _ = io.WriteString(writer, openAIChatFinalResponse)
				}
			}))
			defer server.Close()

			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
			target, _ := json.Marshal(map[string]string{"base_url": server.URL + "/tenant/v1"})
			first := executeConvertedToolTurn(t, runtime, target, stream,
				`[{"role":"user","content":"start"}]`)
			if !strings.Contains(first, "call_1") || !strings.Contains(first, "lookup") || !strings.Contains(first, `\"query\"`) {
				t.Fatalf("tool call was not returned as Responses output: %s", first)
			}

			second := executeConvertedToolTurn(t, runtime, target, stream,
				`[{"role":"user","content":"start"},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{\"query\":\"x\"}"},{"type":"function_call_output","call_id":"call_1","output":""},{"role":"user","content":"continue"}]`)
			if !strings.Contains(second, "done") {
				t.Fatalf("final response was not returned: %s", second)
			}
			if calls.Load() != 2 {
				t.Fatalf("upstream calls = %d, want 2", calls.Load())
			}
		})
	}
}

func executeConvertedToolTurn(
	t *testing.T,
	runtime *testRuntime,
	target json.RawMessage,
	stream bool,
	input string,
) string {
	t.Helper()
	spec := openAIResponsesSpec(execution.OperationResponsesCreate, http.MethodPost, "/v1/responses")
	spec.ChannelID = string(channel.OpenAICompatible)
	spec.TargetConfig = target
	spec.ClientModel = "client-model"
	spec.UpstreamModel = "upstream-model"
	spec.Body = []byte(fmt.Sprintf(`{"model":"client-model","stream":%t,"input":%s,"store":false,"parallel_tool_calls":false,"tools":%s,"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"lookup"}]}}`, stream, input, convertedAllowedTools))
	spec = freezeTestAttempt(spec)
	if !stream {
		result := runtime.Execute(context.Background(), spec)
		if err := result.Validate(); err != nil || result.Error != nil {
			t.Fatalf("execute converted tool turn: result=%+v err=%v body=%s", result, err, result.Body)
		}
		return string(result.Body)
	}
	var data bytes.Buffer
	result := runtime.ExecuteStream(context.Background(), spec, func(event execution.StreamEvent) error {
		if event.Kind == execution.StreamEventData {
			data.Write(event.Data)
		}
		return nil
	})
	if err := result.Validate(); err != nil || result.Error != nil {
		t.Fatalf("stream converted tool turn: result=%+v err=%v data=%s", result, err, data.String())
	}
	return data.String()
}

func assertChatFunctionHistory(t *testing.T, body []byte) {
	t.Helper()
	assistant := gjson.GetBytes(body, `messages.#(role=="assistant")`)
	tool := gjson.GetBytes(body, `messages.#(role=="tool")`)
	if assistant.Get("tool_calls.0.id").String() != "call_1" ||
		assistant.Get("tool_calls.0.function.name").String() != "lookup" ||
		assistant.Get("tool_calls.0.function.arguments").String() != `{"query":"x"}` ||
		tool.Get("tool_call_id").String() != "call_1" || tool.Get("content").String() != "" {
		t.Errorf("function call history was not preserved: %s", body)
	}
}

const openAIChatToolResponse = `{"id":"chat_tool_1","object":"chat.completion","created":1,"model":"served","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"query\":\"x\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`

const openAIChatFinalResponse = `{"id":"chat_final_1","object":"chat.completion","created":2,"model":"served","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":1,"total_tokens":9}}`

const openAIChatToolStream = "data: {\"id\":\"chat_tool_1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"served\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"query\\\":\\\"x\\\"}\"}}]},\"finish_reason\":null}]}\n\n" +
	"data: {\"id\":\"chat_tool_1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"served\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2,\"total_tokens\":7}}\n\n" +
	"data: [DONE]\n\n"

const openAIChatFinalStream = "data: {\"id\":\"chat_final_1\",\"object\":\"chat.completion.chunk\",\"created\":2,\"model\":\"served\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"done\"},\"finish_reason\":null}]}\n\n" +
	"data: {\"id\":\"chat_final_1\",\"object\":\"chat.completion.chunk\",\"created\":2,\"model\":\"served\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":1,\"total_tokens\":9}}\n\n" +
	"data: [DONE]\n\n"
