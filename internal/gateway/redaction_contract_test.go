package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"gpt-load/internal/dialect"
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
	// 切在 g、gl、gld、gld1 之后的片段在转入工具调用时放行，避免普通文本尾巴扣住工具调用；
	// 已开始的密文（gld1_ 之后）仍跨工具事件完整还原。
	t.Logf("unrestored split positions=%v", failed)
	if want := []int{1, 2, 3, 4}; !slices.Equal(failed, want) {
		t.Errorf("unrestored split positions=%v, want %v", failed, want)
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

// signedRoundTripRules 覆盖两种规则模式：带签名的内容都应逐字节还给上游。
func signedRoundTripRules(t *testing.T) map[string]*requestredact.Compiled {
	t.Helper()
	rules := map[string]*requestredact.Compiled{}
	for name, rule := range map[string]requestredact.Rule{
		"encrypt": {Pattern: `[a-z]+@example\.invalid`, Mode: requestredact.ModeEncrypt},
		"replace": {Pattern: `[a-z]+@example\.invalid`, Replacement: "[EMAIL]"},
	} {
		compiled, err := requestredact.Compile([]requestredact.Rule{rule})
		if err != nil {
			t.Fatal(err)
		}
		rules[name] = compiled
	}
	return rules
}

func jsonString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestRedactionContractSignedContentRoundTrip(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, err := c.EncryptToken("alice@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	// 上游签名内容里同时有模型抄回的密文和模型自己写的邮箱。
	cases := []struct {
		name     string
		protocol protocol.Protocol
		upstream string
		response func(string) string
		client   string
		request  func(string) string
		outbound string
	}{
		{
			name: "claude thinking", protocol: protocol.Anthropic,
			upstream: `{"type":"thinking","thinking":"user ` + token + `; test with bob@example.invalid","signature":"SIG"}`,
			response: func(block string) string { return `{"content":[` + block + `]}` },
			client:   "content.0",
			request:  func(block string) string { return `{"messages":[{"role":"assistant","content":[` + block + `]}]}` },
			outbound: "messages.0.content.0",
		},
		{
			name: "gemini function call", protocol: protocol.Gemini,
			upstream: `{"functionCall":{"name":"send","args":{"to":"` + token + `","cc":"bob@example.invalid"}},"thoughtSignature":"SIG"}`,
			response: func(part string) string {
				return `{"candidates":[{"content":{"role":"model","parts":[` + part + `]}}]}`
			},
			client:   "candidates.0.content.parts.0",
			request:  func(part string) string { return `{"contents":[{"role":"model","parts":[` + part + `]}]}` },
			outbound: "contents.0.parts.0",
		},
		{
			name: "chat reasoning_details", protocol: protocol.OpenAICompletions,
			upstream: `{"type":"reasoning.text","text":"user ` + token + `; test with bob@example.invalid","signature":"SIG"}`,
			response: func(item string) string {
				return `{"choices":[{"message":{"content":"ok","reasoning_details":[` + item + `]}}]}`
			},
			client: "choices.0.message.reasoning_details.0",
			request: func(item string) string {
				return `{"messages":[{"role":"assistant","content":"ok","reasoning_details":[` + item + `]}]}`
			},
			outbound: "messages.0.reasoning_details.0",
		},
	}
	for _, tc := range cases {
		session := newRedactionRestoreSession(c, c.RestoreText)
		restored, err := restoreUnaryBusinessFields([]byte(tc.response(tc.upstream)), tc.protocol, session.restore, false, session.sign)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		client := gjson.GetBytes(restored, tc.client).Raw
		if strings.Contains(client, token) || !strings.Contains(client, "alice@example.invalid") {
			t.Fatalf("%s: client did not receive restored content: %s", tc.name, client)
		}
		for mode, rules := range signedRoundTripRules(t) {
			outbound, err := rules.ApplyWithCipher([]byte(tc.request(client)), c)
			if err != nil || gjson.GetBytes(outbound, tc.outbound).Raw != tc.upstream {
				t.Errorf("%s/%s: signed content differs from upstream:\n got: %s / %v\nwant: %s", tc.name, mode, gjson.GetBytes(outbound, tc.outbound).Raw, err, tc.upstream)
			}
		}
	}
}

func TestRedactionContractStreamedSignedContentRoundTrip(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, err := c.EncryptToken("alice@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	push := func(t *testing.T, proto protocol.Protocol, events []string) [][]byte {
		t.Helper()
		session := newRedactionRestoreSession(c, c.RestoreText)
		stream := newRedactionRestoreSSE(proto, session.restore, false)
		stream.sign = session.sign
		var out []byte
		for _, event := range events {
			got, err := stream.Push([]byte("data: " + event + "\n\n"))
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, got...)
		}
		return redactionContractPayloads(out)
	}
	// Claude：思考分片里切开的密文，签名在最后单独下发。
	var thinking, signature string
	for _, payload := range push(t, protocol.Anthropic, []string{
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"user ` + token[:10] + `"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"` + token[10:] + `; test with bob@example.invalid"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIG"}}`,
		`{"type":"content_block_stop","index":0}`,
	}) {
		thinking += gjson.GetBytes(payload, "delta.thinking").Str
		if value := gjson.GetBytes(payload, "delta.signature"); value.Exists() {
			signature = value.Str
		}
	}
	if !strings.Contains(thinking, "alice@example.invalid") || signature == "SIG" {
		t.Fatalf("claude stream was not restored or signed: %q / %q", thinking, signature)
	}
	claude := `{"type":"thinking","thinking":` + jsonString(t, thinking) + `,"signature":` + jsonString(t, signature) + `}`
	// Gemini：签名在最后一个分片；客户端把各分片文本合并进带签名的片段。
	var text, thought string
	for _, payload := range push(t, protocol.Gemini, []string{
		`{"candidates":[{"index":0,"content":{"parts":[{"text":"user ` + token + `"}]}}]}`,
		`{"candidates":[{"index":0,"content":{"parts":[{"text":"; test with bob@example.invalid","thoughtSignature":"SIG"}]},"finishReason":"STOP"}]}`,
	}) {
		text += gjson.GetBytes(payload, "candidates.0.content.parts.0.text").Str
		if value := gjson.GetBytes(payload, "candidates.0.content.parts.0.thoughtSignature"); value.Exists() {
			thought = value.Str
		}
	}
	if !strings.Contains(text, "alice@example.invalid") || thought == "SIG" {
		t.Fatalf("gemini stream was not restored or signed: %q / %q", text, thought)
	}
	gemini := `{"text":` + jsonString(t, text) + `,"thoughtSignature":` + jsonString(t, thought) + `}`
	cases := []struct {
		name, request, outbound, upstream string
	}{
		{"claude", `{"messages":[{"role":"assistant","content":[` + claude + `]}]}`, "messages.0.content.0",
			`{"type":"thinking","thinking":"user ` + token + `; test with bob@example.invalid","signature":"SIG"}`},
		{"gemini", `{"contents":[{"role":"model","parts":[` + gemini + `]}]}`, "contents.0.parts.0",
			`{"text":"user ` + token + `; test with bob@example.invalid","thoughtSignature":"SIG"}`},
	}
	for _, tc := range cases {
		for mode, rules := range signedRoundTripRules(t) {
			outbound, err := rules.ApplyWithCipher([]byte(tc.request), c)
			if err != nil || gjson.GetBytes(outbound, tc.outbound).Raw != tc.upstream {
				t.Errorf("%s/%s: streamed signed content differs:\n got: %s / %v\nwant: %s", tc.name, mode, gjson.GetBytes(outbound, tc.outbound).Raw, err, tc.upstream)
			}
		}
	}
}

func TestRedactOutboundRequestUnwrapsSignaturesWithoutRules(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, err := c.EncryptToken("alice@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := requestredact.Compile(nil)
	if err != nil {
		t.Fatal(err)
	}
	block := `{"type":"thinking","thinking":"user alice@example.invalid","signature":` + jsonString(t, requestredact.WrapSignature("SIG", []string{token})) + `}`
	request := &dialect.ParsedRequest{Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"messages":[{"role":"assistant","content":[` + block + `]}]}`)}
	got, err := redactOutboundRequest(empty, protocol.Anthropic, request, c)
	want := `{"type":"thinking","thinking":"user ` + token + `","signature":"SIG"}`
	if err != nil || gjson.GetBytes(got.Body, "messages.0.content.0").Raw != want {
		t.Fatalf("signature record was not unwrapped: %s / %v", got.Body, err)
	}
}
