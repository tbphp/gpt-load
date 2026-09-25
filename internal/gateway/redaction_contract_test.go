package gateway

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"gpt-load/internal/protocol"
	"gpt-load/internal/requestredact"
)

func redactionContractPayloads(events []byte) [][]byte {
	var out [][]byte
	for _, part := range bytes.Split(events, []byte("\n\n")) {
		p, _, ok := redactionSSEData(append(bytes.Clone(part), '\n', '\n'))
		if ok {
			out = append(out, p)
		}
	}
	return out
}

func TestRedactionContractInterleavedHeaderEverySplit(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, err := c.EncryptToken("synthetic-secret")
	if err != nil {
		t.Fatal(err)
	}
	var failed []int
	for split := 1; split < len(token); split++ {
		s := newRedactionRestoreSSE(protocol.OpenAICompletions, c.RestoreText, false)
		var out []byte
		for _, event := range [][]byte{
			redactionBoundaryChat(t, map[string]any{"content": token[:split]}, nil),
			redactionBoundaryChat(t, redactionBoundaryTool(`{"x":1}`), nil),
			redactionBoundaryChat(t, map[string]any{"content": token[split:]}, "tool_calls"),
		} {
			got, err := s.Push(event)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, got...)
		}
		var text strings.Builder
		for _, p := range redactionContractPayloads(out) {
			text.WriteString(gjson.GetBytes(p, "choices.0.delta.content").Str)
		}
		if text.String() != "synthetic-secret" {
			failed = append(failed, split)
		}
	}
	t.Logf("unrestored split positions=%v", failed)
	if len(failed) > 0 {
		t.Error("tool event discarded a possible ciphertext header")
	}
}

func TestRedactionContractTruncatedArgumentsSnapshots(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, err := c.EncryptToken("a\"b\n\\c")
	if err != nil {
		t.Fatal(err)
	}
	args := `{"value":"` + token + `","later":"partial`
	s := newRedactionRestoreSSE(protocol.OpenAIResponses, c.RestoreText, false)
	delta, err := s.Push(redactionReviewResponseDelta(t, 0, args))
	if err != nil {
		t.Fatal(err)
	}
	parts := redactionContractPayloads(delta)
	if len(parts) != 1 {
		t.Fatal("missing delta")
	}
	want := gjson.GetBytes(parts[0], "delta").Str
	done, err := s.Push(redactionReviewEvent(t, map[string]any{"type": "response.function_call_arguments.done", "output_index": 0, "arguments": args}))
	if err != nil {
		t.Fatal(err)
	}
	parts = redactionContractPayloads(done)
	if len(parts) != 1 {
		t.Fatal("missing done")
	}
	got := gjson.GetBytes(parts[0], "arguments").Str
	t.Logf("delta_equals_done=%v complete_field_preserved=%v", got == want, gjson.Get(got, "value").Str == "a\"b\n\\c")
	if got != want {
		t.Error("truncated arguments.done uses unescaped plaintext")
	}
	snapshot := redactionBoundaryJSON(t, map[string]any{"output": []any{map[string]any{"type": "function_call", "arguments": args}}})
	gotBody, err := restoreUnaryBusinessFields(snapshot, protocol.OpenAIResponses, c.RestoreText, false)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(gotBody, "output.0.arguments").Str != want {
		t.Error("final arguments snapshot differs from delta")
	}
}

func TestRedactionContractStructuredTruncationConsistency(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, err := c.EncryptToken("a\"b\n\\c")
	if err != nil {
		t.Fatal(err)
	}
	value := `{"value":"` + token + `","later":"partial`
	chat := redactionBoundaryJSON(t, map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": value}, "finish_reason": "length"}}})
	_, err = restoreUnaryBusinessFields(chat, protocol.OpenAICompletions, c.RestoreText, true)
	t.Logf("Chat unary rejected=%v", err != nil)
	if err != nil {
		t.Error("Chat unary rejects complete tokens in truncated structured JSON")
	}
	s := newRedactionRestoreSSE(protocol.OpenAICompletions, c.RestoreText, true)
	if _, err := s.Push(redactionBoundaryChat(t, map[string]any{"content": value}, "length")); err != nil {
		t.Errorf("Chat stream: %v", err)
	}
	for _, kind := range []string{"response.output_text.done", "response.incomplete"} {
		s := newRedactionRestoreSSE(protocol.OpenAIResponses, c.RestoreText, true)
		if got, err := s.Push(redactionReviewEvent(t, map[string]any{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "delta": value})); err != nil || len(got) == 0 {
			t.Fatal("setup delta failed")
		}
		event := map[string]any{"type": kind, "output_index": 0, "content_index": 0, "text": value}
		if kind == "response.incomplete" {
			event = map[string]any{"type": kind, "response": map[string]any{"status": "incomplete", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": value}}}}}}
		}
		if _, err := s.Push(redactionReviewEvent(t, event)); err != nil {
			t.Errorf("%s rejects a partial structured snapshot: %v", kind, err)
		}
	}
}

func TestRedactionContractPlainToolResultsKeepTextSemantics(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	original := "a\"b\n\\c"
	token, err := cipher.EncryptToken(original)
	if err != nil {
		t.Fatal(err)
	}
	text := "tool returned: " + token + " (not JSON)"
	for _, kind := range []string{"function_call_output", "custom_tool_call_output"} {
		body := redactionBoundaryJSON(t, map[string]any{"object": "list", "data": []any{map[string]any{"type": kind, "output": text}}})
		got, err := restoreUnaryBusinessFields(body, protocol.OpenAIResponses, cipher.RestoreText, false)
		if err != nil || gjson.GetBytes(got, "data.0.output").Str != "tool returned: "+original+" (not JSON)" {
			t.Fatalf("plain %s changed semantics: %v", kind, err)
		}
	}
}

func TestRedactionContractPartialSnapshotsKeepDamagedCiphertext(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	token, err := cipher.EncryptToken("synthetic-secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, broken := range []string{token[:len(token)-1], token[:len(token)-1] + "!"} {
		partial := `{"value":"` + broken
		tool := redactionBoundaryJSON(t, map[string]any{"output": []any{map[string]any{"type": "function_call", "arguments": partial}}})
		if got, err := restoreUnaryBusinessFields(tool, protocol.OpenAIResponses, cipher.RestoreText, false); err != nil || !bytes.Equal(got, tool) {
			t.Fatalf("tool snapshot changed damaged ciphertext: %s / %v", got, err)
		}
		text := redactionBoundaryJSON(t, map[string]any{"output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": partial}}}}})
		if got, err := restoreUnaryBusinessFields(text, protocol.OpenAIResponses, cipher.RestoreText, true); err != nil || !bytes.Equal(got, text) {
			t.Fatalf("structured snapshot changed damaged ciphertext: %s / %v", got, err)
		}
	}
}

func TestRedactionContractGeminiSignedPartRoundTrip(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	rules, err := requestredact.Compile([]requestredact.Rule{{Pattern: `alice@example\.invalid`, Mode: requestredact.ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	token, err := c.EncryptToken("alice@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	// 第 1 轮：上游返回带签名的工具调用，参数是密文，客户端拿到明文。
	upstreamPart := `{"functionCall":{"name":"send","args":{"to":"` + token + `"}},"thoughtSignature":"synthetic-signature"}`
	response := []byte(`{"candidates":[{"content":{"role":"model","parts":[` + upstreamPart + `]}}]}`)
	restored, err := restoreUnaryBusinessFields(response, protocol.Gemini, c.RestoreText, false)
	if err != nil || gjson.GetBytes(restored, "candidates.0.content.parts.0.functionCall.args.to").Str != "alice@example.invalid" {
		t.Fatalf("signed part was not restored: %s / %v", restored, err)
	}
	// 第 2 轮：客户端把这段放回历史，网关重新加密后应与上游签名时的原文逐字节一致。
	clientPart := gjson.GetBytes(restored, "candidates.0.content.parts.0").Raw
	request := []byte(`{"contents":[{"role":"model","parts":[` + clientPart + `]}]}`)
	outbound, err := rules.ApplyWithCipher(request, c)
	if err != nil || gjson.GetBytes(outbound, "contents.0.parts.0").Raw != upstreamPart {
		t.Fatalf("re-encrypted signed part differs from upstream output: %s / %v", outbound, err)
	}
}

func TestRedactionContractClaudeThinkingRoundTrip(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	rules, err := requestredact.Compile([]requestredact.Rule{{Pattern: `alice@example\.invalid`, Mode: requestredact.ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	token, err := c.EncryptToken("alice@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	// 第 1 轮：上游思考里是密文，客户端看到明文。
	upstreamBlock := `{"type":"thinking","thinking":"用户邮箱是 ` + token + `","signature":"synthetic-signature"}`
	response := []byte(`{"content":[` + upstreamBlock + `]}`)
	restored, err := restoreUnaryBusinessFields(response, protocol.Anthropic, c.RestoreText, false)
	if err != nil || gjson.GetBytes(restored, "content.0.thinking").Str != "用户邮箱是 alice@example.invalid" {
		t.Fatalf("thinking was not restored: %s / %v", restored, err)
	}
	// 第 2 轮：客户端原样回传，网关重新加密后与上游签名时的原文逐字节一致。
	request := []byte(`{"messages":[{"role":"assistant","content":[` + gjson.GetBytes(restored, "content.0").Raw + `]}]}`)
	outbound, err := rules.ApplyWithCipher(request, c)
	if err != nil || gjson.GetBytes(outbound, "messages.0.content.0").Raw != upstreamBlock {
		t.Fatalf("re-encrypted thinking differs from upstream output: %s / %v", outbound, err)
	}
}
