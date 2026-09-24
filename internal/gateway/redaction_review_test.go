package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/protocol"
	"gpt-load/internal/requestredact"
)

func redactionReviewEvent(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return append(append([]byte("data: "), b...), '\n', '\n')
}
func redactionReviewResponseDelta(t *testing.T, index int, text string) []byte {
	return redactionReviewEvent(t, map[string]any{"type": "response.function_call_arguments.delta", "output_index": index, "delta": text})
}

func TestRedactionReviewStructuredNoToken(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	for _, text := range []string{"", `{"x":"truncated`, "I cannot help with that."} {
		v, _ := json.Marshal(text)
		body := []byte(`{"id":"\u0041","choices":[{"message":{"content":` + string(v) + `}}]}`)
		got, err := restoreUnaryBusinessFields(body, protocol.OpenAICompletions, c.RestoreText, true)
		if err != nil || !bytes.Equal(got, body) {
			t.Errorf("token-free structured content length=%d was rejected: %v", len(text), err)
		}
	}
}

func TestRedactionReviewCloseExactChannel(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, err := c.EncryptToken("synthetic-email@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	s := newRedactionRestoreSSE(protocol.OpenAIResponses, c.RestoreText, false)
	if _, err = s.Push(redactionReviewResponseDelta(t, 1, `{"x":"ok"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Push(redactionReviewResponseDelta(t, 10, `{"x":"`+token[:len(token)/2])); err != nil {
		t.Fatal(err)
	}
	_, err = s.Push(redactionReviewEvent(t, map[string]any{"type": "response.function_call_arguments.done", "output_index": 1, "arguments": `{"x":"ok"}`}))
	if err != nil {
		t.Errorf("closing tool 1 attempted to close unfinished tool 10: %v", err)
	}
}

func TestRedactionReviewIncompleteSnapshot(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, err := c.EncryptToken("synthetic-email@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	s := newRedactionRestoreSSE(protocol.OpenAIResponses, c.RestoreText, false)
	first, err := s.Push(redactionReviewEvent(t, map[string]any{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "delta": token}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(first, []byte("synthetic-email@example.invalid")) {
		t.Fatal("setup delta not restored")
	}
	snapshot := map[string]any{"type": "response.incomplete", "response": map[string]any{"status": "incomplete", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": token}}}}}}
	got, err := s.Push(redactionReviewEvent(t, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte(token)) {
		t.Error("incomplete snapshot still contains ciphertext after its delta was restored")
	}
}

func TestRedactionReviewGeminiSignedBoundary(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	for _, tail := range []string{" reply", ""} {
		s := newRedactionRestoreSSE(protocol.Gemini, c.RestoreText, false)
		first := redactionReviewEvent(t, map[string]any{"candidates": []any{map[string]any{"index": 0, "content": map[string]any{"parts": []any{map[string]any{"text": "running"}}}}}})
		if _, err := s.Push(first); err != nil {
			t.Fatal(err)
		}
		last := redactionReviewEvent(t, map[string]any{"candidates": []any{map[string]any{"index": 0, "content": map[string]any{"parts": []any{map[string]any{"text": tail, "thoughtSignature": "synthetic-signature"}}}, "finishReason": "STOP"}}})
		if _, err := s.Push(last); err != nil {
			t.Errorf("ordinary signed tail length=%d was rejected after preceding g: %v", len(tail), err)
		}
	}
}

func TestRedactionReviewCustomToolsRoundTrip(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	rules, err := requestredact.Compile([]requestredact.Rule{{Pattern: `alice@example\.invalid`, Mode: requestredact.ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"model":"test","input":[{"type":"custom_tool_call","name":"apply_patch","call_id":"ct1","input":"+alice@example.invalid"}]}`)
	upstream, err := rules.ApplyWithCipher(request, c)
	if err != nil {
		t.Fatal(err)
	}
	encrypted := gjson.GetBytes(upstream, "input.0.input").Str
	if encrypted == "+alice@example.invalid" || !strings.Contains(encrypted, "gld1_") {
		t.Fatal("setup custom input was not encrypted")
	}
	item := map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "ct2", "input": encrypted}
	body, _ := json.Marshal(map[string]any{"output": []any{item}})
	got, err := restoreUnaryBusinessFields(body, protocol.OpenAIResponses, c.RestoreText, false)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(got, "output.0.input").Str != "+alice@example.invalid" {
		t.Error("unary custom_tool_call.input was not restored")
	}
	s := newRedactionRestoreSSE(protocol.OpenAIResponses, c.RestoreText, false)
	stream, err := s.Push(redactionReviewEvent(t, map[string]any{"type": "response.custom_tool_call_input.delta", "output_index": 0, "delta": encrypted}))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stream, []byte("gld1_")) {
		t.Error("custom tool delta passed ciphertext to the client")
	}
}

type redactionReviewCountingCipher struct {
	encryption.RedactionCipher
	calls, valid int
}

func (c *redactionReviewCountingCipher) ValidTokenAt(s string, p int) (int, bool) {
	c.calls++
	end, ok := c.RedactionCipher.ValidTokenAt(s, p)
	if ok {
		c.valid++
	}
	return end, ok
}
func TestRedactionReviewAuthenticationBudget(t *testing.T) {
	inner := websocketRedactionTestCipher(t)
	rules, err := requestredact.Compile([]requestredact.Rule{{Pattern: "never-match-synthetic", Replacement: "masked"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range []int{128, 512} {
		declared := count * 64
		input := strings.Repeat(fmt.Sprintf("gld1_%d_", declared), count) + strings.Repeat("A", declared)
		c := &redactionReviewCountingCipher{RedactionCipher: inner}
		start := time.Now()
		_, err := rules.TextWithCipher(input, c)
		processed := c.calls * declared
		t.Logf("input=%d bytes candidates=%d valid=%d candidate_bytes=%d amplification=%.1fx duration=%s rejected=%v", len(input), c.calls, c.valid, processed, float64(processed)/float64(len(input)), time.Since(start), err != nil)
		if err == nil || processed > len(input) {
			t.Error("no work budget stopped repeated authentication over overlapping invalid candidates")
		}
	}
}

func TestRedactionReviewWebsocketClientError(t *testing.T) {
	h, engine, _ := websocketTestHandler(t, "http://unused.invalid/v1", channel.OpenAI)
	cipher, err := h.encryption.NewRedactionCipher(1)
	if err != nil {
		t.Fatal(err)
	}
	token, err := cipher.EncryptToken("synthetic")
	if err != nil {
		t.Fatal(err)
	}
	damaged := token[:len(token)-1] + "!"
	h.forwarder = websocketScriptForwarder{AttemptForwarder: h.forwarder, open: func(_ context.Context, _ ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
		session := &websocketScriptSession{done: make(chan struct{})}
		session.turn = func(ctx context.Context, _ []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
			if err := emit(ctx, websocketRedactionEvent(t, "response.output_text.delta", "", "delta", damaged)); err != nil {
				return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindInternal}}
			}
			return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
		}
		return session, execution.WebsocketResult{}
	}}
	h.requestLogSink = &recordingRequestLogSink{}
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "hello", "store": false}); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, body, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	code := gjson.GetBytes(body, "error.code").Str
	if code != "response_redaction_failed" {
		t.Errorf("client error code=%q, want response_redaction_failed", code)
	}
}

func TestRedactionReviewToolProgressWithoutTokens(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	stream := newRedactionRestoreSSE(protocol.OpenAIResponses, c.RestoreText, false)
	for _, fragment := range []string{`{"text":"ordinary`, ` content`, `"}`} {
		event := redactionReviewResponseDelta(t, 0, fragment)
		got, err := stream.Push(event)
		if err != nil || !bytes.Equal(got, event) {
			t.Fatalf("token-free delta was held or changed: output=%d error=%v", len(got), err)
		}
		ping := []byte(": ping\n\n")
		got, err = stream.Push(ping)
		if err != nil || !bytes.Equal(got, ping) {
			t.Fatalf("ping blocked: %v", err)
		}
	}
}

func TestRedactionReviewJSONStreamsEverySplit(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	token, err := cipher.EncryptToken("a\"b\n\\c😀")
	if err != nil {
		t.Fatal(err)
	}
	var escaped strings.Builder
	for _, ch := range token {
		fmt.Fprintf(&escaped, `\u%04x`, ch)
	}
	for _, representation := range []string{token, escaped.String()} {
		doc := `{"` + token + `":"keep-key","v":"prefix ` + representation + ` suffix","emoji":"\ud83d\ude00","n":9007199254740993}`
		restoredValue, err := json.Marshal("prefix a\"b\n\\c😀 suffix")
		if err != nil {
			t.Fatal(err)
		}
		want := `{"` + token + `":"keep-key","v":` + string(restoredValue) + `,"emoji":"\ud83d\ude00","n":9007199254740993}`
		for split := 0; split <= len(doc); split++ {
			stream := newRedactionRestoreSSE(protocol.OpenAIResponses, cipher.RestoreText, false)
			var output []byte
			for _, part := range []string{doc[:split], doc[split:]} {
				got, err := stream.Push(redactionReviewResponseDelta(t, 0, part))
				if err != nil {
					t.Fatalf("split %d: %v", split, err)
				}
				output = append(output, got...)
			}
			got, err := stream.Push(redactionReviewEvent(t, map[string]any{"type": "response.function_call_arguments.done", "output_index": 0, "arguments": doc}))
			if err != nil {
				t.Fatalf("split %d done: %v", split, err)
			}
			output = append(output, got...)
			var joined strings.Builder
			for _, event := range bytes.Split(output, []byte("\n\n")) {
				payload, _, ok := redactionSSEData(append(bytes.Clone(event), '\n', '\n'))
				if ok && gjson.GetBytes(payload, "type").Str == "response.function_call_arguments.delta" {
					joined.WriteString(gjson.GetBytes(payload, "delta").Str)
				}
			}
			if joined.String() != want {
				t.Fatalf("split %d changed JSON value, key, precision or escapes", split)
			}
		}
	}
}

func TestRedactionReviewLongUnprotectedTool(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	stream := newRedactionRestoreSSE(protocol.OpenAIResponses, c.RestoreText, false)
	fragments := []string{`{"text":"`}
	for i := 0; i < 12; i++ {
		fragments = append(fragments, strings.Repeat("x", 1<<20))
	}
	fragments = append(fragments, `"}`)
	for _, part := range fragments {
		event := redactionReviewResponseDelta(t, 0, part)
		got, err := stream.Push(event)
		if err != nil || !bytes.Equal(got, event) {
			t.Fatalf("large unprotected document did not stream: %v", err)
		}
	}
}

func TestRedactionReviewTruncatedJSONWithoutTokens(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	for _, document := range []string{"", `{"text":"truncated`, `{"text":"\u4e`, `{"text":"hello g`, `{"text":"\ud83d`, "I cannot comply."} {
		stream := newRedactionRestoreSSE(protocol.OpenAIResponses, cipher.RestoreText, false)
		var events []byte
		for i := range len(document) {
			got, err := stream.Push(redactionReviewResponseDelta(t, 0, document[i:i+1]))
			if err != nil {
				t.Fatal(err)
			}
			events = append(events, got...)
		}
		got, err := stream.Push(redactionReviewEvent(t, map[string]any{"type": "response.function_call_arguments.done", "output_index": 0, "arguments": document}))
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, got...)
		var text strings.Builder
		for _, event := range bytes.Split(events, []byte("\n\n")) {
			payload, _, ok := redactionSSEData(append(bytes.Clone(event), '\n', '\n'))
			if ok && gjson.GetBytes(payload, "type").Str == "response.function_call_arguments.delta" {
				text.WriteString(gjson.GetBytes(payload, "delta").Str)
			}
		}
		if text.String() != document {
			t.Fatal("truncated unprotected JSON was changed")
		}
	}
}

func TestRedactionReviewCustomToolSnapshots(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	token, err := cipher.EncryptToken("alice@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	item := map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "ct_review", "input": "+" + token}
	for _, test := range []struct {
		kind string
		body map[string]any
		path string
	}{
		{"input.done", map[string]any{"type": "response.custom_tool_call_input.done", "input": "+" + token}, "input"},
		{"item.added", map[string]any{"type": "response.output_item.added", "item": item}, "item.input"},
		{"item.done", map[string]any{"type": "response.output_item.done", "item": item}, "item.input"},
		{"completed", map[string]any{"type": "response.completed", "response": map[string]any{"output": []any{item}}}, "response.output.0.input"},
		{"incomplete", map[string]any{"type": "response.incomplete", "response": map[string]any{"output": []any{item}}}, "response.output.0.input"},
	} {
		t.Run(test.kind, func(t *testing.T) {
			// JSON 输出模式不应改变自定义工具的纯文本输入语义。
			ws := newWebsocketRedactionOutput(cipher.RestoreText, true)
			body, err := json.Marshal(test.body)
			if err != nil {
				t.Fatal(err)
			}
			frames, err := ws.Push(body)
			if err != nil || len(frames) != 1 || gjson.GetBytes(frames[0], test.path).Str != "+alice@example.invalid" {
				t.Fatalf("custom snapshot not restored: %v", err)
			}
		})
	}
	list, err := json.Marshal(map[string]any{"object": "list", "data": []any{item}})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := restoreUnaryBusinessFields(list, protocol.OpenAIResponses, cipher.RestoreText, true)
	if err != nil || gjson.GetBytes(restored, "data.0.input").Str != "+alice@example.invalid" {
		t.Fatalf("stored custom input not restored: %v", err)
	}
}

func TestRedactionReviewRestoresEscapedTokenInTruncatedStructuredText(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	token, err := cipher.EncryptToken("private")
	if err != nil {
		t.Fatal(err)
	}
	text := `{"value":"\u0067` + token[1:] + `"` // 缺少对象结束符，但存在完整密文。
	body, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": text}}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := restoreUnaryBusinessFields(body, protocol.OpenAICompletions, cipher.RestoreText, true)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(got, "choices.0.message.content").Str != `{"value":"private"` {
		t.Fatal("complete escaped token was not restored while preserving truncation")
	}

}
