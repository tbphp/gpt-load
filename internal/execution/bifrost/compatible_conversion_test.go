package bifrost

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/tidwall/gjson"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

type compatibleHistoryCase struct{ name, body string }

func compatibleHistoryCases() []compatibleHistoryCase {
	const call = `{"type":"function_call","call_id":"call_1","name":"weather","arguments":"{}"}`
	const output = `{"type":"function_call_output","call_id":"call_1","output":"done"}`
	return []compatibleHistoryCase{
		{"Responses summary", `{"model":"client-model","reasoning":{"effort":"high"},"input":[{"role":"user","content":"hi"},{"type":"reasoning","summary":[{"type":"summary_text","text":"Need tools."}]},` + call + `,` + output + `]}`},
		{"Responses full and narration", `{"model":"client-model","reasoning":{"effort":"high"},"input":[{"role":"user","content":"hi"},{"type":"reasoning","content":[{"type":"reasoning_text","text":"Need tools."}]},{"role":"assistant","content":"Checking."},` + call + `,` + output + `]}`},
		{"Responses full overrides summary", `{"model":"client-model","input":[{"role":"user","content":"hi"},{"type":"reasoning","content":[{"type":"reasoning_text","text":"Need tools."}]},{"role":"assistant","content":"Checking."},{"type":"reasoning","summary":[{"type":"summary_text","text":"Short."}]},` + call + `,` + output + `]}`},
		{"Responses full control", `{"model":"client-model","input":[{"role":"user","content":"hi"},{"type":"reasoning","content":[{"type":"reasoning_text","text":"Need tools."}]},` + call + `,` + output + `]}`},
		{"Anthropic full and narration", `{"model":"client-model","max_tokens":64,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"thinking","thinking":"Need tools."},{"type":"text","text":"Checking."},{"type":"tool_use","id":"call_1","name":"weather","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"done"}]}]}`},
		{"Gemini full and narration", `{"contents":[{"role":"user","parts":[{"text":"hi"}]},{"role":"model","parts":[{"thought":true,"text":"Need tools."},{"text":"Checking."},{"functionCall":{"id":"call_1","name":"weather","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"weather","response":{"result":"done"}}}]}],"generationConfig":{"maxOutputTokens":64}}`},
	}
}

func compatibleTestSpec(t *testing.T, upstream, body string, stream bool) execution.AttemptSpec {
	t.Helper()
	client, operation, path := protocol.OpenAIResponses, execution.OperationResponsesCreate, "/v1/responses"
	if gjson.Get(body, "messages").Exists() {
		client, operation, path = protocol.Anthropic, execution.OperationChatCompletion, "/v1/messages"
	}
	if gjson.Get(body, "contents").Exists() {
		client, operation, path = protocol.Gemini, execution.OperationChatCompletion, "/v1beta/models/client-model:generateContent"
		if stream {
			path = "/v1beta/models/client-model:streamGenerateContent"
		}
	}
	spec := convertedSpec(channel.OpenAICompatible, client, operation, path, []byte(body))
	var err error
	spec.TargetConfig, err = json.Marshal(map[string]string{"base_url": upstream + "/tenant/v1"})
	if err != nil {
		t.Fatal(err)
	}
	spec.UpstreamModel = "deepseek-flash"
	return freezeTestAttempt(spec)
}

func compatibleReasoningServer(t *testing.T, stream bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		if r.URL.Path != "/tenant/v1/chat/completions" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Error("selected credential lost")
		}
		messages := gjson.GetBytes(body, "messages").Array()
		if len(messages) != 3 || messages[0].Get("role").String() != "user" ||
			messages[1].Get("role").String() != "assistant" || messages[2].Get("role").String() != "tool" {
			t.Errorf("assistant turn was split: %s", gjson.GetBytes(body, "messages").Raw)
		}
		found := false
		for _, message := range gjson.GetBytes(body, "messages").Array() {
			if len(message.Get("tool_calls").Array()) == 0 {
				continue
			}
			found = true
			if message.Get("reasoning_content").String() != "Need tools." {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(400)
				_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"missing tool reasoning_content"}}`)
				return
			}
		}
		if !found {
			t.Error("tool history missing")
		}
		if gjson.GetBytes(body, "reasoning_effort").Exists() {
			t.Error("#730 reasoning effort filter regressed")
		}
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, openAIChatFinalStream)
		} else {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, openAIChatFinalResponse)
		}
	}))
}

func TestCompatibleUnaryToolTurnReasoning(t *testing.T) {
	for _, tc := range compatibleHistoryCases() {
		t.Run(tc.name, func(t *testing.T) {
			server := compatibleReasoningServer(t, false)
			defer server.Close()
			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
			spec := compatibleTestSpec(t, server.URL, tc.body, false)
			before := spec.Clone()
			result := runtime.Execute(t.Context(), spec)
			if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != 200 {
				t.Fatalf("result=%+v validation=%v", result, err)
			}
			if !bytes.Contains(result.Body, []byte("done")) {
				t.Fatalf("missing final answer: %s", result.Body)
			}
			if !reflect.DeepEqual(spec, before) {
				t.Fatal("execution changed original request")
			}
		})
	}
}

func TestCompatibleRequestKeepsSDKToolPreparation(t *testing.T) {
	const function = `{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"value":{"type":"string","pattern":"^a\\0(?=b)b$"}}}}`
	for _, tc := range []struct{ name, model, tools, pattern string }{
		{"flat schema", "deepseek-flash", function, `^a\x00b$`},
		{"other model unchanged", "other-model", function, `^a\0(?=b)b$`},
		// 锁定 SDK 先处理 schema 再解包；嵌套 schema 保持其原有行为。
		{"default namespace", "deepseek-flash", `{"type":"namespace","name":"functions","tools":[` + function + `]}`, `^a\0(?=b)b$`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				if gjson.GetBytes(body, "tools.#").Int() != 1 || gjson.GetBytes(body, "tools.0.function.name").String() != "lookup" ||
					gjson.GetBytes(body, "tools.0.function.parameters.properties.value.pattern").String() != tc.pattern {
					t.Errorf("SDK tool preparation changed: %s", body)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, openAIChatFinalResponse)
			}))
			defer server.Close()
			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
			body := `{"model":"client-model","input":"hi","tools":[` + tc.tools + `]}`
			spec := compatibleTestSpec(t, server.URL, body, false)
			spec.UpstreamModel = tc.model
			before := spec.Clone()
			result := runtime.Execute(t.Context(), spec)
			if result.Error != nil {
				t.Fatal(result.Error)
			}
			if !reflect.DeepEqual(spec, before) {
				t.Fatal("tool preparation changed original request")
			}
		})
	}
}

func TestCompatibleToolReasoningBoundaries(t *testing.T) {
	const call = `{"type":"function_call","call_id":"call_1","name":"weather","arguments":"{}"}`
	for _, tc := range []struct {
		name, input string
		want        *string
	}{
		{"encrypted plus summary", `[{"type":"reasoning","encrypted_content":"opaque","summary":[{"type":"summary_text","text":"Short."}]},` + call + `]`, nil},
		{"user boundary", `[{"type":"reasoning","summary":[{"type":"summary_text","text":"Old."}]},{"role":"assistant","content":"Old answer."},{"role":"user","content":"New question."},` + call + `]`, nil},
		{"tool result boundary", `[{"type":"reasoning","summary":[{"type":"summary_text","text":"Old."}]},{"type":"function_call","call_id":"old","name":"weather","arguments":"{}"},{"type":"function_call_output","call_id":"old","output":"done"},` + call + `]`, nil},
		{"new tool round reasoning", `[{"type":"reasoning","content":[{"type":"reasoning_text","text":"Old full."}]},{"type":"function_call","call_id":"old","name":"weather","arguments":"{}"},{"type":"function_call_output","call_id":"old","output":"done"},{"type":"reasoning","summary":[{"type":"summary_text","text":"New."}]},` + call + `]`, schemas.Ptr("New.")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var input []schemas.ResponsesMessage
			if e := json.Unmarshal([]byte(tc.input), &input); e != nil {
				t.Fatal(e)
			}
			before, _ := json.Marshal(input)
			request := &schemas.BifrostResponsesRequest{Input: input}
			chat := request.ToChatRequest()
			preserveCompatibleToolTurnReasoning(chat.Input)
			got := chat.Input[len(chat.Input)-1].ChatAssistantMessage.Reasoning
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("reasoning got=%v want=%v", got, tc.want)
			}
			after, _ := json.Marshal(input)
			if string(before) != string(after) {
				t.Fatal("input was mutated")
			}
		})
	}
}
