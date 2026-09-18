package automodel

import (
	"encoding/json"
	"strings"
	"testing"

	"gpt-load/internal/protocol"
)

func TestExtractLatestTaskAcrossProtocols(t *testing.T) {
	for _, test := range []struct {
		protocol protocol.Protocol
		body     string
	}{
		{protocol.OpenAICompletions, `{"messages":[{"role":"system","content":"background"},{"role":"user","content":"old"},{"role":"assistant","content":"context"},{"role":"user","content":[{"type":"text","text":"最新任务"}]},{"role":"tool","content":"tool data"}]}`},
		{protocol.OpenAIResponses, `{"instructions":"background","input":[{"role":"user","content":"old"},{"role":"assistant","content":"context"},{"role":"user","content":[{"type":"input_text","text":"最新任务"}]},{"type":"function_call_output","output":"tool data"}]}`},
		{protocol.Anthropic, `{"system":[{"type":"text","text":"background"}],"messages":[{"role":"user","content":"old"},{"role":"assistant","content":"context"},{"role":"user","content":[{"type":"text","text":"最新任务"}]},{"role":"user","content":[{"type":"tool_result","content":"tool data"}]}]}`},
		{protocol.Gemini, `{"systemInstruction":{"parts":[{"text":"background"}]},"contents":[{"role":"user","parts":[{"text":"old"}]},{"role":"model","parts":[{"text":"context"}]},{"role":"user","parts":[{"text":"最新任务"}]},{"role":"user","parts":[{"functionResponse":{"response":{"text":"tool data"}}}]}]}`},
	} {
		t.Run(string(test.protocol), func(t *testing.T) {
			state, reason := Extract(test.protocol, []byte(test.body))
			if reason != "" || state.CurrentTask != "最新任务" || len(state.ClientInstructions) != 1 || state.ClientInstructions[0].Text != "background" {
				t.Fatalf("task view = %#v, reason=%s", state, reason)
			}
			encoded, _ := json.Marshal(state)
			if !strings.Contains(string(encoded), "tool") || !strings.Contains(string(encoded), "context") {
				t.Fatal("necessary visible tool and previous-turn context was lost")
			}
		})
	}
}

func TestExtractBudgetDoesNotAlterOriginalRequest(t *testing.T) {
	current := strings.Repeat("任", MaxStateBytes)
	body, _ := json.Marshal(map[string]any{"messages": []any{map[string]string{"role": "user", "content": current}}})
	original := string(body)
	if _, reason := Extract(protocol.OpenAICompletions, body); reason != "task_too_large" {
		t.Fatalf("large current task reason = %q", reason)
	}
	if string(body) != original {
		t.Fatal("extractor changed answer request")
	}
}

func TestExtractKeepsNewestToolContextWithinBudget(t *testing.T) {
	messages := []any{map[string]string{"role": "user", "content": "最新任务"}}
	for range 1000 {
		messages = append(messages, map[string]string{"role": "tool", "content": strings.Repeat("上下文", 100)})
	}
	messages = append(messages, map[string]string{"role": "tool", "content": "newest result"})
	body, _ := json.Marshal(map[string]any{"messages": messages})
	state, reason := Extract(protocol.OpenAICompletions, body)
	encoded, _ := json.Marshal(state)
	if reason != "" || !state.ContextTruncated || len(encoded) > MaxStateBytes || len(state.RecentContext) == 0 || state.RecentContext[len(state.RecentContext)-1].Text != "newest result" {
		t.Fatalf("bounded recent view: reason=%s bytes=%d truncated=%v", reason, len(encoded), state.ContextTruncated)
	}
}
