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
	// 上游签名内容里同时有模型抄回的密文和模型自己写的文字，其中可能与还原值相同。
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
			name: "claude thinking with the same model-written value", protocol: protocol.Anthropic,
			upstream: `{"type":"thinking","thinking":"user ` + token + `; model also wrote alice@example.invalid","signature":"SIG"}`,
			response: func(block string) string { return `{"content":[` + block + `]}` },
			client:   "content.0",
			request:  func(block string) string { return `{"messages":[{"role":"assistant","content":[` + block + `]}]}` },
			outbound: "messages.0.content.0",
		},
		{
			name: "gemini function call", protocol: protocol.Gemini,
			upstream: `{"functionCall":{"name":"send","args":{"name":"` + token + `","id":"` + token + `","cc":"bob@example.invalid"}},"thoughtSignature":"SIG"}`,
			response: func(part string) string {
				return `{"candidates":[{"content":{"role":"model","parts":[` + part + `]}}]}`
			},
			client:   "candidates.0.content.parts.0",
			request:  func(part string) string { return `{"contents":[{"role":"model","parts":[` + part + `]}]}` },
			outbound: "contents.0.parts.0",
		},
		{
			name: "chat reasoning_details", protocol: protocol.OpenAICompletions,
			upstream: `{"type":"reasoning.text","text":"user ` + token + `; test with bob@example.invalid","signature":"SIG","format":"anthropic-claude-v1"}`,
			response: func(item string) string {
				return `{"choices":[{"message":{"content":"ok","reasoning_details":[` + item + `]}}]}`
			},
			client: "choices.0.message.reasoning_details.0",
			request: func(item string) string {
				return `{"messages":[{"role":"assistant","content":"ok","reasoning_details":[` + item + `]}]}`
			},
			outbound: "messages.0.reasoning_details.0",
		},
		{
			name: "chat tool call with gemini signature", protocol: protocol.OpenAICompletions,
			upstream: `{"id":"call_1","type":"function","function":{"name":"send","arguments":` + jsonString(t, `{"to":"`+token+`","cc":"bob@example.invalid"}`) + `},"extra_content":{"google":{"thought_signature":"SIG"}}}`,
			response: func(call string) string {
				return `{"choices":[{"message":{"content":null,"tool_calls":[` + call + `]}}]}`
			},
			client: "choices.0.message.tool_calls.0",
			request: func(call string) string {
				return `{"messages":[{"role":"assistant","content":null,"tool_calls":[` + call + `]}]}`
			},
			outbound: "messages.0.tool_calls.0",
		},
		{
			name: "responses reasoning text", protocol: protocol.OpenAIResponses,
			upstream: `{"type":"reasoning","id":"rs_1","summary":[],"content":[{"type":"reasoning_text","text":"user ` + token + `; test with bob@example.invalid","signature":"SIG"}]}`,
			response: func(item string) string { return `{"object":"response","output":[` + item + `]}` },
			client:   "output.0",
			request:  func(item string) string { return `{"input":[` + item + `]}` },
			outbound: "input.0",
		},
	}
	for _, tc := range cases {
		restored, err := restoreUnaryBusinessFields([]byte(tc.response(tc.upstream)), tc.protocol, c.RestoreText, false, newRedactionSigner(c))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		client := gjson.GetBytes(restored, tc.client).Raw
		if strings.Contains(client, token) || !strings.Contains(client, "alice@example.invalid") || strings.Contains(client, `"SIG"`) {
			t.Fatalf("%s: client did not receive restored and signed content: %s", tc.name, client)
		}
		for mode, rules := range signedRoundTripRules(t) {
			outbound, err := rules.ApplyWithCipher([]byte(tc.request(client)), c)
			if err != nil || gjson.GetBytes(outbound, tc.outbound).Raw != tc.upstream {
				t.Errorf("%s/%s: signed content differs from upstream:\n got: %s / %v\nwant: %s", tc.name, mode, gjson.GetBytes(outbound, tc.outbound).Raw, err, tc.upstream)
			}
		}
	}
}

// 整条非流响应没有还原任何内容时，客户端不会拿到需要换回的明文，签名保持原样。
func TestRedactionUnaryKeepsSignaturesWithoutRestoration(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	body := []byte(`{"content":[{"type":"thinking","thinking":"test with bob@example.invalid","signature":"SIG"}]}`)
	got, err := restoreUnaryBusinessFields(body, protocol.Anthropic, c.RestoreText, false, newRedactionSigner(c))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("unrestored response changed: %s / %v", got, err)
	}
}

// 工具参数、业务 JSON 里的同名字段不是协议签名：不包装，也不影响其他字段的还原。
func TestRedactionSignaturesOnlyAtProtocolPositions(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, err := c.EncryptToken("alice@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		protocol protocol.Protocol
		body     string
		business []string
	}{
		{protocol.Anthropic,
			`{"content":[{"type":"tool_use","id":"t1","name":"sign","input":{"email":"` + token + `","signature":"customer-signature","note":{"signature":"` + token + `"}}}]}`,
			[]string{"content.0.input.signature", "content.0.input.note.signature"}},
		{protocol.Gemini,
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"sign","args":{"email":"` + token + `","thoughtSignature":"customer-signature"}}}]}}]}`,
			[]string{"candidates.0.content.parts.0.functionCall.args.thoughtSignature"}},
	}
	for _, tc := range cases {
		restored, err := restoreUnaryBusinessFields([]byte(tc.body), tc.protocol, c.RestoreText, false, newRedactionSigner(c))
		if err != nil || strings.Contains(string(restored), token) {
			t.Fatalf("%s: business fields were not restored: %s / %v", tc.protocol, restored, err)
		}
		for _, path := range tc.business {
			if value := gjson.GetBytes(restored, path).Str; value != "customer-signature" && value != "alice@example.invalid" {
				t.Errorf("%s: business field %s was treated as a signature: %q", tc.protocol, path, value)
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
		stream := newRedactionRestoreSSE(proto, c.RestoreText, false)
		stream.signer = newRedactionSigner(c)
		var out []byte
		for _, event := range events {
			got, err := stream.Push([]byte("data: " + event + "\n\n"))
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, got...)
		}
		tail, err := stream.Finish()
		if err != nil {
			t.Fatal(err)
		}
		return redactionContractPayloads(append(out, tail...))
	}
	outbound := func(t *testing.T, mode string, rules *requestredact.Compiled, request, path string) string {
		t.Helper()
		got, err := rules.ApplyWithCipher([]byte(request), c)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		return gjson.GetBytes(got, path).Raw
	}

	// Claude：思考分片里切开的密文，模型还写了相同的值，签名在最后单独下发。
	var thinking, signature string
	for _, payload := range push(t, protocol.Anthropic, []string{
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"user ` + token[:10] + `"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"` + token[10:] + `; model also wrote alice@example.invalid"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIG"}}`,
		`{"type":"content_block_stop","index":0}`,
	}) {
		thinking += gjson.GetBytes(payload, "delta.thinking").Str
		if value := gjson.GetBytes(payload, "delta.signature"); value.Exists() {
			signature = value.Str
		}
	}
	if strings.Count(thinking, "alice@example.invalid") != 2 || signature == "SIG" {
		t.Fatalf("claude stream was not restored or signed: %q / %q", thinking, signature)
	}
	claude := `{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":` + jsonString(t, thinking) + `,"signature":` + jsonString(t, signature) + `}]}]}`

	// Chat：reasoning_details 按 index 分片下发，签名在最后一片。
	var reasoning, detailSignature string
	for _, payload := range push(t, protocol.OpenAICompletions, []string{
		`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","index":0,"text":"user ` + token[:12] + `"}]}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","index":0,"text":"` + token[12:] + `; test with bob@example.invalid"}]}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","index":0,"signature":"SIG"}]}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
	}) {
		reasoning += gjson.GetBytes(payload, "choices.0.delta.reasoning_details.0.text").Str
		if value := gjson.GetBytes(payload, "choices.0.delta.reasoning_details.0.signature"); value.Exists() {
			detailSignature = value.Str
		}
	}
	if !strings.Contains(reasoning, "alice@example.invalid") || detailSignature == "SIG" {
		t.Fatalf("chat reasoning stream was not restored or signed: %q / %q", reasoning, detailSignature)
	}
	chat := `{"messages":[{"role":"assistant","content":"ok","reasoning_details":[{"type":"reasoning.text","text":` + jsonString(t, reasoning) + `,"signature":` + jsonString(t, detailSignature) + `}]}]}`

	// Gemini 兼容接口：工具调用连同 extra_content 签名一次下发。
	arguments := `{"to":"` + token + `","cc":"bob@example.invalid"}`
	payloads := push(t, protocol.OpenAICompletions, []string{
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"send","arguments":` + jsonString(t, arguments) + `},"extra_content":{"google":{"thought_signature":"SIG"}}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	})
	call := gjson.GetBytes(payloads[0], "choices.0.delta.tool_calls.0")
	if strings.Contains(call.Raw, token) || call.Get("extra_content.google.thought_signature").Str == "SIG" {
		t.Fatalf("chat tool call stream was not restored or signed: %s", call.Raw)
	}
	chatTool := `{"messages":[{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_1","type":"function","function":` + call.Get("function").Raw + `,"extra_content":` + call.Get("extra_content").Raw + `}]}]}`

	// Gemini：签名在最后一个分片，客户端保留各分片。
	var parts []string
	for _, payload := range push(t, protocol.Gemini, []string{
		`{"candidates":[{"index":0,"content":{"parts":[{"text":"user ` + token + `"}]}}]}`,
		`{"candidates":[{"index":0,"content":{"parts":[{"text":"; test with bob@example.invalid","thoughtSignature":"SIG"}]},"finishReason":"STOP"}]}`,
	}) {
		parts = append(parts, gjson.GetBytes(payload, "candidates.0.content.parts.0").Raw)
	}
	if len(parts) != 2 || !strings.Contains(parts[0], "alice@example.invalid") || strings.Contains(parts[1], `"SIG"`) {
		t.Fatalf("gemini stream was not restored or signed: %v", parts)
	}
	gemini := `{"contents":[{"role":"model","parts":[` + parts[0] + `,` + parts[1] + `]}]}`

	// Responses：推理项完成事件里带签名的推理文本。
	item := `{"type":"reasoning","id":"rs_1","summary":[],"content":[{"type":"reasoning_text","text":"user ` + token + `; test with bob@example.invalid","signature":"SIG"}]}`
	done := push(t, protocol.OpenAIResponses, []string{
		`{"type":"response.output_item.done","output_index":0,"item":` + item + `}`,
	})
	clientItem := gjson.GetBytes(done[0], "item").Raw
	if strings.Contains(clientItem, token) || strings.Contains(clientItem, `"SIG"`) {
		t.Fatalf("responses reasoning item was not restored or signed: %s", clientItem)
	}

	cases := []struct {
		name, request, path, upstream string
	}{
		{"claude", claude, "messages.0.content.0",
			`{"type":"thinking","thinking":"user ` + token + `; model also wrote alice@example.invalid","signature":"SIG"}`},
		{"chat reasoning", chat, "messages.0.reasoning_details.0",
			`{"type":"reasoning.text","text":"user ` + token + `; test with bob@example.invalid","signature":"SIG"}`},
		{"chat tool call", chatTool, "messages.0.tool_calls.0",
			`{"index":0,"id":"call_1","type":"function","function":{"name":"send","arguments":` + jsonString(t, arguments) + `},"extra_content":{"google":{"thought_signature":"SIG"}}}`},
		{"gemini signed part", gemini, "contents.0.parts.1",
			`{"text":"; test with bob@example.invalid","thoughtSignature":"SIG"}`},
		{"responses", `{"input":[` + clientItem + `]}`, "input.0", item},
	}
	for _, tc := range cases {
		for mode, rules := range signedRoundTripRules(t) {
			if got := outbound(t, mode, rules, tc.request, tc.path); got != tc.upstream {
				t.Errorf("%s/%s: streamed signed content differs:\n got: %s\nwant: %s", tc.name, mode, got, tc.upstream)
			}
		}
	}

	// 客户端把 Gemini 各分片合并进带签名的片段：记录对不上，按普通上游内容加密，不外泄明文。
	merged := `{"contents":[{"role":"model","parts":[{"text":` + jsonString(t, gjson.Get(parts[0], "text").Str+gjson.Get(parts[1], "text").Str) +
		`,"thoughtSignature":` + jsonString(t, gjson.Get(parts[1], "thoughtSignature").Str) + `}]}]}`
	for mode, rules := range signedRoundTripRules(t) {
		got := outbound(t, mode, rules, merged, "contents.0.parts.0")
		if strings.Contains(got, "alice@example.invalid") || gjson.Get(got, "thoughtSignature").Str != "SIG" {
			t.Errorf("%s: merged gemini part = %s", mode, got)
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
	wrapped := requestredact.WrapSignature("SIG", []requestredact.SignedString{{
		Path: []any{"thinking"}, Value: "user alice@example.invalid",
		Spans: []requestredact.RestoreSpan{{Offset: 5, Length: len("alice@example.invalid"), Original: token}},
	}})
	block := `{"type":"thinking","thinking":"user alice@example.invalid","signature":` + jsonString(t, wrapped) + `}`
	request := &dialect.ParsedRequest{Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"messages":[{"role":"assistant","content":[` + block + `]}]}`)}
	got, err := redactOutboundRequest(empty, protocol.Anthropic, request, c)
	want := `{"type":"thinking","thinking":"user ` + token + `","signature":"SIG"}`
	if err != nil || gjson.GetBytes(got.Body, "messages.0.content.0").Raw != want {
		t.Fatalf("signature record was not unwrapped: %s / %v", got.Body, err)
	}
}

// 签名通道累计超出上限时不再保存正文，记录必然核对失败，下一轮按普通上游内容加密。
func TestRedactionSignedChannelOverflowForcesEncryption(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, err := c.EncryptToken("alice@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	stream := newRedactionRestoreSSE(protocol.Anthropic, c.RestoreText, false)
	stream.signer = newRedactionSigner(c)
	stream.signedBytes = maxRedactionStreamPendingBytes
	var out []byte
	for _, event := range []string{
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"user ` + token + `; test with bob@example.invalid"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIG"}}`,
	} {
		got, err := stream.Push([]byte("data: " + event + "\n\n"))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, got...)
	}
	var thinking, signature string
	for _, payload := range redactionContractPayloads(out) {
		thinking += gjson.GetBytes(payload, "delta.thinking").Str
		if value := gjson.GetBytes(payload, "delta.signature"); value.Exists() {
			signature = value.Str
		}
	}
	request := `{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":` + jsonString(t, thinking) + `,"signature":` + jsonString(t, signature) + `}]}]}`
	outbound, err := signedRoundTripRules(t)["encrypt"].ApplyWithCipher([]byte(request), c)
	block := gjson.GetBytes(outbound, "messages.0.content.0")
	if err != nil || strings.Contains(block.Raw, "@example.invalid") || block.Get("signature").Str != "SIG" {
		t.Fatalf("overflowed record did not fall back to encryption: %s / %v", block.Raw, err)
	}
}

// 签名块里记录以外的内容（客户端改动的字段、签名之后才到的参数）按普通上游内容加密。
func TestRedactionSignedContentOutsideRecordIsEncrypted(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, err := c.EncryptToken("alice@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	encrypt := signedRoundTripRules(t)["encrypt"]
	assertEncrypted := func(t *testing.T, name, request string) {
		t.Helper()
		got, err := encrypt.ApplyWithCipher([]byte(request), c)
		if err != nil || strings.Contains(string(got), "@example.invalid") || strings.Contains(string(got), requestredactWrapperPrefix) {
			t.Errorf("%s: outbound = %s / %v", name, got, err)
		}
	}

	// 非流：客户端把未还原过的参数改成敏感文字。
	restored, err := restoreUnaryBusinessFields([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"send","args":{"to":"`+token+`","cc":"public"}},"thoughtSignature":"SIG"}]}}]}`),
		protocol.Gemini, c.RestoreText, false, newRedactionSigner(c))
	if err != nil {
		t.Fatal(err)
	}
	part := strings.Replace(gjson.GetBytes(restored, "candidates.0.content.parts.0").Raw, `"cc":"public"`, `"cc":"bob@example.invalid"`, 1)
	assertEncrypted(t, "changed argument", `{"contents":[{"role":"model","parts":[`+part+`]}]}`)

	push := func(t *testing.T, proto protocol.Protocol, events []string) [][]byte {
		t.Helper()
		stream := newRedactionRestoreSSE(proto, c.RestoreText, false)
		stream.signer = newRedactionSigner(c)
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
	// Gemini 流式：首包只有调用名和签名，参数在后续 partialArgs 里还原。
	gemini := push(t, protocol.Gemini, []string{
		`{"candidates":[{"index":0,"content":{"role":"model","parts":[{"functionCall":{"name":"send","willContinue":true},"thoughtSignature":"SIG"}]}}]}`,
		`{"candidates":[{"index":0,"content":{"role":"model","parts":[{"functionCall":{"partialArgs":[{"jsonPath":"$.to","stringValue":"` + token + `"}]}}]},"finishReason":"STOP"}]}`,
	})
	signature := gjson.GetBytes(gemini[0], "candidates.0.content.parts.0.thoughtSignature").Str
	value := gjson.GetBytes(gemini[1], "candidates.0.content.parts.0.functionCall.partialArgs.0.stringValue").Str
	if value != "alice@example.invalid" || signature == "SIG" {
		t.Fatalf("gemini stream = %q / %q", value, signature)
	}
	assertEncrypted(t, "gemini partial arguments", `{"contents":[{"role":"model","parts":[{"functionCall":{"name":"send","args":{"to":`+jsonString(t, value)+`}},"thoughtSignature":`+jsonString(t, signature)+`}]}]}`)

	// Gemini 兼容接口：extra_content 签名先到，参数在后续 delta。
	chat := push(t, protocol.OpenAICompletions, []string{
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"send","arguments":""},"extra_content":{"google":{"thought_signature":"SIG"}}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":` + jsonString(t, `{"to":"`+token+`"}`) + `}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	})
	toolSignature := gjson.GetBytes(chat[0], "choices.0.delta.tool_calls.0.extra_content.google.thought_signature").Str
	arguments := gjson.GetBytes(chat[1], "choices.0.delta.tool_calls.0.function.arguments").Str
	if !strings.Contains(arguments, "alice@example.invalid") || toolSignature == "SIG" {
		t.Fatalf("chat tool stream = %q / %q", arguments, toolSignature)
	}
	assertEncrypted(t, "chat tool arguments", `{"messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"send","arguments":`+jsonString(t, arguments)+`},"extra_content":{"google":{"thought_signature":`+jsonString(t, toolSignature)+`}}}]}]}`)
}

const requestredactWrapperPrefix = "R0xEU0lH"
