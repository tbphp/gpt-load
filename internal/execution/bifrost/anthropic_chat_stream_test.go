package bifrost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/providers/anthropic"
	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

func TestAnthropicChatStreamPreservesContentAndRequest(t *testing.T) {
	for _, test := range []struct {
		name, body, blocks, stop, finish, text string
		tools                                  bool
	}{
		{name: "thinking and multiple tools", body: `"tools":[{"type":"function","function":{"name":"first","parameters":{"type":"object"}}},{"type":"function","function":{"name":"second","parameters":{"type":"object"}}}]`,
			blocks: strings.Join([]string{
				`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reasoning text"}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-test"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call1","name":"first","input":{}}}`,
				`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"a\":1}"}}`,
				`{"type":"content_block_stop","index":1}`,
				`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call2","name":"second","input":{}}}`,
				`{"type":"content_block_stop","index":2}`,
			}, "\n"), stop: "tool_use", finish: "tool_calls", tools: true},
		{name: "structured output", body: `"response_format":{"type":"json_schema","json_schema":{"name":"answer","schema":{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}}}`, blocks: strings.Join([]string{
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"{\"answer\":\"yes\"}"}}`,
			`{"type":"content_block_stop","index":0}`,
		}, "\n"), stop: "end_turn", finish: "stop", text: `{"answer":"yes"}`},
		{name: "length stop", body: `"max_tokens":100`, blocks: strings.Join([]string{
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
			`{"type":"content_block_stop","index":0}`,
		}, "\n"), stop: "max_tokens", finish: "length", text: "hello"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"model":"client-model","messages":[{"role":"system","content":"system text"},{"role":"user","content":"hello"}],` + test.body + `}`)
			requestBodies := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				requestBodies <- received
				blocks := test.blocks
				events := []string{`{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"served","content":[],"usage":{"input_tokens":1200,"output_tokens":0}}}`}
				events = append(events, strings.Split(blocks, "\n")...)
				events = append(events, `{"type":"message_delta","delta":{"stop_reason":"`+test.stop+`"},"usage":{"input_tokens":100,"cache_read_input_tokens":900,"output_tokens":10}}`, `{"type":"message_stop"}`)
				w.Header().Set("Content-Type", "text/event-stream")
				for _, event := range events {
					var root struct {
						Type string `json:"type"`
					}
					if err := json.Unmarshal([]byte(event), &root); err != nil {
						t.Error(err)
						return
					}
					_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", root.Type, event)
				}
			}))
			defer server.Close()
			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, anthropicBaseURL: server.URL})
			spec := convertedSpec(channel.Anthropic, protocol.OpenAICompletions, execution.OperationChatCompletion, "/v1/chat/completions", body)
			prepared, failure := runtime.prepare(spec, true)
			if failure != nil {
				t.Fatalf("prepare: %+v", failure)
			}
			expected, buildErr := anthropic.BuildAnthropicChatRequestBody(schemas.NewBifrostContext(t.Context(), time.Time{}), prepared.request, anthropic.AnthropicRequestBuildConfig{Provider: prepared.request.Provider, IsStreaming: true})
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			var data bytes.Buffer
			result := runtime.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
				if event.Kind == execution.StreamEventData {
					data.Write(event.Data)
				}
				return nil
			})
			if result.Error != nil {
				t.Fatalf("stream: %+v", result.Error)
			}
			if result.Usage == nil || result.Usage.Normalized.Tokens != (usage.Tokens{UncachedInput: 100, CacheRead: 900, Output: 10}) {
				t.Fatalf("usage: %+v", result.Usage)
			}
			var gotBody, wantBody any
			if err := json.Unmarshal(<-requestBodies, &gotBody); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(expected, &wantBody); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotBody, wantBody) {
				t.Fatalf("request changed: got %v want %s", gotBody, expected)
			}
			var content, thinking, signature, finish string
			args := map[int]string{}
			ids := map[int]string{}
			done := 0
			for _, line := range strings.Split(data.String(), "\n") {
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				payload := strings.TrimPrefix(line, "data: ")
				if payload == "[DONE]" {
					done++
					continue
				}
				var chunk struct {
					Choices []struct {
						Finish *string `json:"finish_reason"`
						Delta  struct {
							Content   string `json:"content"`
							Reasoning string `json:"reasoning"`
							Details   []struct {
								Signature string `json:"signature"`
							} `json:"reasoning_details"`
							Tools []struct {
								Index    int    `json:"index"`
								ID       string `json:"id"`
								Function struct {
									Arguments string `json:"arguments"`
								} `json:"function"`
							} `json:"tool_calls"`
						} `json:"delta"`
					} `json:"choices"`
				}
				if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
					t.Fatal(err)
				}
				for _, choice := range chunk.Choices {
					content += choice.Delta.Content
					thinking += choice.Delta.Reasoning
					for _, detail := range choice.Delta.Details {
						signature += detail.Signature
					}
					for _, tool := range choice.Delta.Tools {
						args[tool.Index] += tool.Function.Arguments
						if tool.ID != "" {
							ids[tool.Index] = tool.ID
						}
					}
					if choice.Finish != nil {
						finish = *choice.Finish
					}
				}
			}
			if done != 1 || finish != test.finish || content != test.text {
				t.Fatalf("stream content=%q finish=%q done=%d; wire=%s", content, finish, done, data.String())
			}
			if test.tools && (thinking != "reasoning text" || signature != "sig-test" || args[0] != `{"a":1}` || args[1] != "{}" || ids[0] != "call1" || ids[1] != "call2") {
				t.Fatalf("lost tool/thinking content: thinking=%q signature=%q args=%v ids=%v; wire=%s", thinking, signature, args, ids, data.String())
			}
			if !test.tools && len(args) != 0 {
				t.Fatalf("unexpected tools: %v", args)
			}
			assertNoPrivateLeak(t, data.String(), "raw_response", "raw_request", "system text", "client-secret")
		})
	}
}
