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
			if reason != "" || state.ExecutionPhase != ExecutionPhaseToolContinuation ||
				state.CurrentTask != "最新任务" || state.TaskFingerprint == "" ||
				len(state.ClientInstructions) != 1 || state.ClientInstructions[0].Text != "background" {
				t.Fatalf("task view = %#v, reason=%s", state, reason)
			}
			encoded, _ := json.Marshal(state)
			if !strings.Contains(string(encoded), "tool") || !strings.Contains(string(encoded), "context") {
				t.Fatal("necessary visible tool and previous-turn context was lost")
			}
		})
	}
}

func TestExtractDistinguishesUserTasksFromToolContinuations(t *testing.T) {
	for _, test := range []struct {
		name        string
		body        string
		wantPhase   string
		wantTask    string
		wantReason  string
		wantContext string
	}{
		{"user task", `{"input":[{"role":"user","content":[{"type":"input_text","text":"fix the parser"}]}]}`, ExecutionPhaseUserTask, "fix the parser", "", ""},
		{"custom tool continuation", `{"input":[{"role":"user","content":"fix the parser"},{"type":"custom_tool_call_output","call_id":"call-1","output":"tests failed"}]}`, ExecutionPhaseToolContinuation, "fix the parser", "", "tests failed"},
		{"pure tool continuation", `{"input":[{"type":"function_call_output","call_id":"call-1","output":"done"}]}`, ExecutionPhaseToolContinuation, "", "task_missing", ""},
		{"image task", `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}]}`, ExecutionPhaseUserTask, "", "non_text_task", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, reason := Extract(protocol.OpenAIResponses, []byte(test.body))
			if state.ExecutionPhase != test.wantPhase || state.CurrentTask != test.wantTask || reason != test.wantReason {
				t.Fatalf("state=%#v reason=%q", state, reason)
			}
			if test.wantContext != "" {
				encoded, _ := json.Marshal(state.RecentContext)
				if !strings.Contains(string(encoded), test.wantContext) {
					t.Fatalf("recent context = %s", encoded)
				}
			}
		})
	}
}

func TestExtractTaskFingerprintIgnoresLaterToolProgress(t *testing.T) {
	initial := `{"instructions":"keep compatibility","tools":[{"type":"function","name":"test"}],"input":[{"role":"user","content":"fix the parser"}]}`
	continued := `{"instructions":"keep compatibility","tools":[{"type":"function","name":"test"}],"input":[{"role":"user","content":"fix the parser"},{"type":"function_call","name":"test","arguments":"{}"},{"type":"function_call_output","call_id":"call-1","output":"first result"}]}`
	changed := `{"instructions":"keep compatibility","tools":[{"type":"function","name":"test"}],"input":[{"role":"user","content":"replace the parser"},{"type":"function_call_output","call_id":"call-1","output":"first result"}]}`
	first, firstReason := Extract(protocol.OpenAIResponses, []byte(initial))
	second, secondReason := Extract(protocol.OpenAIResponses, []byte(continued))
	third, thirdReason := Extract(protocol.OpenAIResponses, []byte(changed))
	if firstReason != "" || secondReason != "" || thirdReason != "" ||
		first.TaskFingerprint == "" || first.TaskFingerprint != second.TaskFingerprint ||
		first.TaskFingerprint == third.TaskFingerprint {
		t.Fatalf("fingerprints initial=%q continued=%q changed=%q reasons=%q/%q/%q", first.TaskFingerprint, second.TaskFingerprint, third.TaskFingerprint, firstReason, secondReason, thirdReason)
	}
}

func TestExtractBudgetDoesNotAlterOriginalRequest(t *testing.T) {
	current := "TASK START" + strings.Repeat("任\"\n", MaxStateBytes) + "TASK END"
	body, _ := json.Marshal(map[string]any{"messages": []any{
		map[string]string{"role": "system", "content": "keep constraints"},
		map[string]string{"role": "user", "content": "previous task"},
		map[string]string{"role": "user", "content": current},
		map[string]string{"role": "tool", "content": "latest tool result"},
	}})
	original := string(body)
	state, reason := Extract(protocol.OpenAICompletions, body)
	if reason != "" || !state.ContextTruncated || size(state) > MaxStateBytes ||
		!strings.HasPrefix(state.CurrentTask, "TASK START") || !strings.HasSuffix(state.CurrentTask, "TASK END") ||
		len(state.ClientInstructions) != 1 || len(state.RecentContext) != 2 {
		t.Fatalf("long task reason=%q bytes=%d instructions=%d context=%d", reason, size(state), len(state.ClientInstructions), len(state.RecentContext))
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

func TestExtractKeepsBoundedLongClientInstruction(t *testing.T) {
	instruction := strings.Repeat("quoted \"instruction\"\n", 200)
	body, _ := json.Marshal(map[string]any{
		"instructions": instruction,
		"input":        "implement the requested change",
	})
	state, reason := Extract(protocol.OpenAIResponses, body)
	if reason != "" || len(state.ClientInstructions) != 1 || state.ClientInstructions[0].Text == "" ||
		len(state.ClientInstructions[0].Text) >= len(instruction) || !state.ContextTruncated ||
		size(state.ClientInstructions) > maxClientInstructionBytes {
		t.Fatalf("instruction view=%#v reason=%q encoded=%d", state.ClientInstructions, reason, size(state.ClientInstructions))
	}
}

func TestExtractSharesInstructionBudgetAcrossSources(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"messages": []any{
		map[string]string{"role": "system", "content": strings.Repeat("system ", 600)},
		map[string]string{"role": "developer", "content": strings.Repeat("developer ", 600)},
		map[string]string{"role": "user", "content": "implement the task"},
	}})
	state, reason := Extract(protocol.OpenAICompletions, body)
	if reason != "" || len(state.ClientInstructions) != 2 ||
		state.ClientInstructions[0].Text == "" || state.ClientInstructions[1].Text == "" ||
		size(state.ClientInstructions) > maxClientInstructionBytes {
		t.Fatalf("instruction view=%#v reason=%q encoded=%d", state.ClientInstructions, reason, size(state.ClientInstructions))
	}
}
