package bifrost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"gpt-load/internal/execution"
)

func TestCompatibleStreamToolTurnReasoning(t *testing.T) {
	for _, tc := range compatibleHistoryCases() {
		t.Run(tc.name, func(t *testing.T) {
			server := compatibleReasoningServer(t, true)
			defer server.Close()
			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
			var data bytes.Buffer
			result := runtime.ExecuteStream(t.Context(), compatibleTestSpec(t, server.URL, tc.body, true), func(event execution.StreamEvent) error {
				data.Write(event.Data)
				return nil
			})
			if err := result.Validate(); err != nil || result.Error != nil || !strings.Contains(data.String(), "done") {
				t.Fatalf("result=%+v validation=%v data=%s", result, err, data.String())
			}
		})
	}
}

func TestCompatibleStreamParallelToolsAndTerminal(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		finishWithTools, usage, fail, zero bool
	}{
		{"trailing usage", false, true, false, false},
		{"finish with tools", true, true, false, false},
		{"explicit zero usage", true, true, false, true},
		{"missing usage", false, false, false, false},
		{"error after finish", true, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				stream := compatibleToolStream(tc.finishWithTools, tc.usage, tc.fail)
				if tc.zero {
					stream = strings.ReplaceAll(stream, `"prompt_tokens":7,"completion_tokens":2,"total_tokens":9`, `"prompt_tokens":0,"completion_tokens":0,"total_tokens":0`)
				}
				_, _ = io.WriteString(w, stream)
			}))
			defer server.Close()
			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
			var data bytes.Buffer
			result := runtime.ExecuteStream(t.Context(), compatibleTestSpec(t, server.URL, `{"model":"client-model","input":"hi","stream":true}`, true), func(event execution.StreamEvent) error {
				data.Write(event.Data)
				return nil
			})
			var final gjson.Result
			count := 0
			lastSequence := int64(-1)
			for _, line := range strings.Split(data.String(), "\n") {
				if strings.HasPrefix(line, "data: ") {
					event := gjson.Parse(strings.TrimPrefix(line, "data: "))
					if sequence := event.Get("sequence_number").Int(); sequence != lastSequence+1 {
						t.Fatalf("noncontiguous event sequence after %d: %s", lastSequence, event.Raw)
					} else {
						lastSequence = sequence
					}
					if event.Get("type").String() == "response.completed" {
						count++
						final = event.Get("response")
					}
				}
			}
			if tc.fail {
				if result.Error == nil || count != 0 {
					t.Fatalf("late error reported success: %+v completed=%d", result, count)
				}
				return
			}
			if result.Error != nil || count != 1 || len(final.Get(`output.#(type=="function_call")#`).Array()) != 2 {
				t.Fatalf("incomplete tools: result=%+v data=%s", result, data.String())
			}
			if tc.usage {
				want := int64(9)
				if tc.zero {
					want = 0
				}
				if !final.Get("usage.total_tokens").Exists() || final.Get("usage.total_tokens").Int() != want || result.Usage == nil {
					t.Fatalf("trailing usage lost: %+v %s", result.Usage, final.Raw)
				}
			} else if (final.Get("usage").Exists() && final.Get("usage").Type != gjson.Null) || result.Usage != nil {
				t.Fatalf("missing usage became zero: %+v %s", result.Usage, final.Raw)
			}
			if strings.Contains(data.String(), "upstream-model") {
				t.Fatal("upstream model alias leaked")
			}
		})
	}
}

func TestCompatibleStreamRejectsUnknownToolIndex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":9,\"function\":{\"arguments\":\"{}\"}}]},\"finish_reason\":null}]}\n\ndata: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	var data bytes.Buffer
	result := runtime.ExecuteStream(t.Context(), compatibleTestSpec(t, server.URL, `{"model":"client-model","input":"hi"}`, true), func(event execution.StreamEvent) error {
		data.Write(event.Data)
		return nil
	})
	if result.Error == nil || strings.Contains(data.String(), "response.completed") {
		t.Fatalf("unknown index silently dropped: %+v data=%s", result, data.String())
	}
}

func TestCompatibleGeminiParallelToolOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, compatibleToolStream(false, true, false))
	}))
	defer server.Close()
	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	spec := compatibleTestSpec(t, server.URL, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`, true)
	// SDK 的工具结束事件来自 map，重复执行可覆盖两种遍历顺序。
	for attempt := 0; attempt < 64; attempt++ {
		var calls []string
		result := runtime.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
			for _, line := range strings.Split(string(event.Data), "\n") {
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				for _, part := range gjson.Parse(strings.TrimPrefix(line, "data: ")).Get("candidates.0.content.parts").Array() {
					if id := part.Get("functionCall.id").String(); id != "" {
						calls = append(calls, id)
					}
				}
			}
			return nil
		})
		if result.Error != nil || len(calls) != 2 || calls[0] != "call_0" || calls[1] != "call_1" {
			t.Fatalf("tool order changed: calls=%v error=%+v", calls, result.Error)
		}
	}
}

func compatibleToolStream(withFinish, withUsage, withError bool) string {
	calls := []any{}
	for i, city := range []string{"Beijing", "Shanghai"} {
		args, _ := json.Marshal(map[string]string{"city": city})
		calls = append(calls, map[string]any{"index": i, "id": fmt.Sprintf("call_%d", i), "type": "function", "function": map[string]any{"name": "weather", "arguments": string(args)}})
	}
	var finish any
	if withFinish {
		finish = "tool_calls"
	}
	first := map[string]any{"id": "chat_1", "model": "upstream-model", "object": "chat.completion.chunk", "created": 1, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": "Checking.", "reasoning_content": "Need weather.", "tool_calls": calls}, "finish_reason": finish}}}
	b, _ := json.Marshal(first)
	out := "data: " + string(b) + "\n\n"
	if !withFinish {
		out += "data: {\"id\":\"chat_1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"
	}
	if withUsage {
		out += "data: {\"id\":\"chat_1\",\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":2,\"total_tokens\":9}}\n\n"
	}
	if withError {
		out += "data: {\"error\":{\"type\":\"server_error\",\"message\":\"stream broke\"}}\n\n"
	}
	return out + "data: [DONE]\n\n"
}
