package gateway

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"gpt-load/internal/protocol"
	"gpt-load/internal/requestredact"
)

func TestRedactionToolResultJSONEscaping(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	original := "-----BEGIN PRIVATE KEY-----\nsynthetic\"\\value\n-----END PRIVATE KEY-----"
	inner := string(redactionBoundaryJSON(t, map[string]any{"key": original, "count": 42, "ok": true, "empty": nil}))
	rules, err := requestredact.Compile([]requestredact.Rule{{Pattern: `-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----[\s\S]*?-----END (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`, Mode: requestredact.ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		item map[string]any
		path string
	}{
		{"Responses", map[string]any{"type": "function_call_output", "output": inner}, "output"},
		{"Responses custom", map[string]any{"type": "custom_tool_call_output", "output": inner}, "output"},
		{"Responses text block", map[string]any{"type": "function_call_output", "output": []any{map[string]any{"type": "input_text", "text": inner}}}, "output.0.text"},
		{"Chat", map[string]any{"role": "tool", "content": inner}, "content"},
		{"legacy Chat", map[string]any{"role": "function", "content": inner}, "content"},
		{"Anthropic", map[string]any{"type": "tool_result", "content": inner}, "content"},
		{"Anthropic text block", map[string]any{"type": "tool_result", "content": []any{map[string]any{"type": "text", "text": inner}}}, "content.0.text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := redactionBoundaryJSON(t, map[string]any{"input": []any{tc.item}})
			encrypted, err := rules.ApplyWithCipher(request, cipher)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(tc.name, "Responses") {
				history := []byte(`{"object":"list","data":` + gjson.GetBytes(encrypted, "input").Raw + `}`)
				restoredHistory, err := restoreUnaryBusinessFields(history, protocol.OpenAIResponses, cipher.RestoreText, false)
				if err != nil || gjson.GetBytes(restoredHistory, "data.0."+tc.path).Str != inner {
					t.Fatalf("historical tool result no longer matches original JSON: %v", err)
				}
			}
			token := gjson.Get(gjson.GetBytes(encrypted, "input.0."+tc.path).Str, "key").Str
			response := redactionBoundaryJSON(t, map[string]any{"output": []any{map[string]any{"type": "function_call", "arguments": string(redactionBoundaryJSON(t, map[string]any{"key": token}))}}})
			restored, err := restoreUnaryBusinessFields(response, protocol.OpenAIResponses, cipher.RestoreText, false)
			if err != nil {
				t.Fatal(err)
			}
			if gjson.Get(gjson.GetBytes(restored, "output.0.arguments").Str, "key").Str != original {
				t.Fatal("tool result escape syntax was encrypted as part of the original value")
			}
		})
	}
}
