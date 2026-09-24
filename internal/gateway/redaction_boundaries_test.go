package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
	"gpt-load/internal/requestredact"
)

func redactionBoundaryJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func redactionBoundaryChat(t *testing.T, delta any, finish any) []byte {
	return redactionReviewEvent(t, map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
}
func redactionBoundaryTool(fragment string) any {
	return map[string]any{"tool_calls": []any{map[string]any{"index": 0, "type": "function", "function": map[string]any{"name": "write", "arguments": fragment}}}}
}

func TestRedactionBoundaryStoredStructuredOutput(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, e := c.EncryptToken("quote\" newline\n slash\\")
	if e != nil {
		t.Fatal(e)
	}
	doc := `{"value":"` + token + `"}`
	body := redactionBoundaryJSON(t, map[string]any{"id": "resp_test", "object": "response", "text": map[string]any{"format": map[string]any{"type": "json_object"}}, "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": doc}}}}})
	in := ForwardInput{ClientProtocol: protocol.OpenAIResponses, Dialect: dialect.NewOpenAIResponses(), Operation: execution.OperationResponsesCreate, RedactionCipher: c, Request: &dialect.ParsedRequest{Body: []byte(`{"text":{"format":{"type":"json_object"}}}`)}}
	p := &responseProcessor{redactor: redact.New()}
	generated, e := p.prepareSuccessRepresentation(in, http.StatusOK, http.Header{}, body, nil)
	if e != nil {
		t.Fatal(e)
	}
	if !json.Valid([]byte(gjson.GetBytes(generated.downstream, "output.0.content.0.text").Str)) {
		t.Fatal("setup create JSON invalid")
	}
	in.Operation = execution.OperationResponsesRetrieve
	in.Request = &dialect.ParsedRequest{}
	fetched, e := p.prepareSuccessRepresentation(in, http.StatusOK, http.Header{}, body, nil)
	if e != nil {
		t.Fatal(e)
	}
	if !json.Valid([]byte(gjson.GetBytes(fetched.downstream, "output.0.content.0.text").Str)) {
		t.Error("GET restored the same value as raw text and broke inner JSON")
	}
}

func TestRedactionBoundaryCustomToolOutputHistory(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	rules, e := requestredact.Compile([]requestredact.Rule{{Pattern: "synthetic-secret", Mode: requestredact.ModeEncrypt}})
	if e != nil {
		t.Fatal(e)
	}
	req := []byte(`{"input":[{"type":"custom_tool_call_output","call_id":"ct1","output":"synthetic-secret"}]}`)
	up, e := rules.ApplyWithCipher(req, c)
	if e != nil {
		t.Fatal(e)
	}
	token := gjson.GetBytes(up, "input.0.output").Str
	if !strings.HasPrefix(token, "gld1_") {
		t.Fatal("setup output not encrypted")
	}
	body := []byte(`{"object":"list","data":[` + gjson.GetBytes(up, "input.0").Raw + `]}`)
	got, e := restoreUnaryBusinessFields(body, protocol.OpenAIResponses, c.RestoreText, false)
	if e != nil {
		t.Fatal(e)
	}
	if gjson.GetBytes(got, "data.0.output").Str != "synthetic-secret" {
		t.Error("stored custom tool result remained ciphertext")
	}
}

func TestRedactionBoundaryFailedErrorAfterCommit(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, e := c.EncryptToken("synthetic-secret")
	if e != nil {
		t.Fatal(e)
	}
	data := [][]byte{
		redactionReviewEvent(t, map[string]any{"type": "response.output_text.delta", "output_index": 0, "delta": "Hello"}),
		redactionReviewEvent(t, map[string]any{"type": "response.failed", "response": map[string]any{"id": "resp_test", "object": "response", "status": "failed", "error": map[string]any{"code": "server_error", "message": "echo " + token}}}),
	}
	x := fakeExecutionExecutor{stream: func(_ context.Context, _ execution.AttemptSpec, sink execution.StreamSink) execution.StreamResult {
		h := http.Header{"Content-Type": {"text/event-stream"}}
		if e := sink(execution.StreamEvent{Sequence: 1, Kind: execution.StreamEventReady, StatusCode: 200, Header: h}); e != nil {
			t.Fatal(e)
		}
		for i, b := range data {
			if e := sink(execution.StreamEvent{Sequence: uint64(i + 2), Kind: execution.StreamEventData, Data: b}); e != nil {
				t.Fatal(e)
			}
		}
		return execution.StreamResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, StatusCode: 200, Header: h}
	}}
	in := executionForwardInput()
	in.ClientProtocol = protocol.OpenAIResponses
	in.Dialect = dialect.NewOpenAIResponses()
	in.Operation = execution.OperationResponsesCreate
	in.RedactionCipher = c
	w := httptest.NewRecorder()
	res := NewExecutionForwarder(x).ForwardStream(context.Background(), in, w)
	t.Logf("committed=%v reason=%v", res.Committed, res.Stream.EndReason)
	if !res.Committed {
		t.Fatal("setup did not commit")
	}
	if bytes.Contains(w.Body.Bytes(), []byte(token)) {
		t.Error("committed response.failed leaked complete ciphertext")
	}
}

func TestRedactionBoundaryAmbiguousTextTailKeepsEventOrder(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	for _, content := range []string{"Checking the file", "Checking the log"} {
		stream := newRedactionRestoreSSE(protocol.OpenAICompletions, cipher.RestoreText, false)
		var expected, actual []byte
		send := func(event []byte) int {
			expected = append(expected, event...)
			out, err := stream.Push(event)
			if err != nil {
				t.Fatal(err)
			}
			actual = append(actual, out...)
			return len(out)
		}
		send(redactionBoundaryChat(t, map[string]any{"content": content}, nil))
		released := 0
		for i := 0; i < 200; i++ {
			fragment := "x"
			if i == 0 {
				fragment = `{"text":"`
			}
			if i == 199 {
				fragment = `"}`
			}
			if send(redactionBoundaryChat(t, redactionBoundaryTool(fragment), nil)) > 0 {
				released++
			}
		}
		want := 200
		if strings.HasSuffix(content, "g") {
			want = 0
		}
		if released != want {
			t.Fatalf("ordinary tail policy: released=%d want=%d", released, want)
		}
		send(redactionBoundaryChat(t, map[string]any{}, "tool_calls"))
		if !bytes.Equal(actual, expected) {
			t.Fatal("field completion changed content or event order")
		}
	}
}

func TestRedactionBoundaryTruncatedParameterAfterCompleteToken(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, e := c.EncryptToken("synthetic-secret")
	if e != nil {
		t.Fatal(e)
	}
	args := `{"value":"` + token + `","later":"partial`
	unary := redactionBoundaryJSON(t, map[string]any{"choices": []any{map[string]any{"message": redactionBoundaryTool(args), "finish_reason": "length"}}})
	out, e := restoreUnaryBusinessFields(unary, protocol.OpenAICompletions, c.RestoreText, false)
	t.Logf("unary success=%v plaintext=%v", e == nil, bytes.Contains(out, []byte("synthetic-secret")))
	s := newRedactionRestoreSSE(protocol.OpenAICompletions, c.RestoreText, false)
	out, e = s.Push(redactionBoundaryChat(t, redactionBoundaryTool(args), nil))
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("stream already emitted plaintext=%v", bytes.Contains(out, []byte("synthetic-secret")))
	_, e = s.Push(redactionBoundaryChat(t, map[string]any{}, "length"))
	if e != nil {
		t.Errorf("complete restored token followed by ordinary truncation was reclassified: %v", e)
	}
}

func TestRedactionBoundaryChatCustomTool(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, e := c.EncryptToken("synthetic-secret")
	if e != nil {
		t.Fatal(e)
	}
	call := map[string]any{"index": 0, "id": "ct1", "type": "custom", "custom": map[string]any{"name": "apply_patch", "input": "+" + token}}
	message := map[string]any{"tool_calls": []any{call}}
	body := redactionBoundaryJSON(t, map[string]any{"choices": []any{map[string]any{"message": message}}})
	got, e := restoreUnaryBusinessFields(body, protocol.OpenAICompletions, c.RestoreText, false)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(got, []byte(token)) {
		t.Error("Chat custom input not restored in unary output")
	}
	s := newRedactionRestoreSSE(protocol.OpenAICompletions, c.RestoreText, false)
	got, e = s.Push(redactionBoundaryChat(t, message, "tool_calls"))
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(got, []byte(token)) {
		t.Error("Chat custom input not restored in stream")
	}
}

func TestRedactionBoundaryUnquotedTokenInStructuredOutput(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, e := c.EncryptToken("synthetic-secret")
	if e != nil {
		t.Fatal(e)
	}
	doc := `{"value":` + token + `}`
	unary := redactionBoundaryJSON(t, map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": doc}}}})
	_, e = restoreUnaryBusinessFields(unary, protocol.OpenAICompletions, c.RestoreText, true)
	t.Logf("unary rejected=%v", e != nil)
	s := newRedactionRestoreSSE(protocol.OpenAICompletions, c.RestoreText, true)
	got, e := s.Push(redactionBoundaryChat(t, map[string]any{"content": doc}, "stop"))
	if e == nil && bytes.Contains(got, []byte(token)) {
		t.Error("structured SSE passed unquoted ciphertext and a successful end")
	}
}

func TestRedactionPendingCandidateAllocations(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	chunk := strings.Repeat("A", 64)
	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			doc := redactionStreamDocument{}
			if _, err := doc.push(`{"x":"gld1_1000000_`, cipher.RestoreText); err != nil {
				b.Fatal(err)
			}
			for j := 0; j < 1024; j++ {
				if _, err := doc.push(chunk, cipher.RestoreText); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
	t.Logf("64 KiB pending candidate allocated %d bytes", result.AllocedBytesPerOp())
	if result.AllocedBytesPerOp() > 8<<20 {
		t.Fatalf("64 KiB pending candidate allocated %d bytes", result.AllocedBytesPerOp())
	}
}

func TestRedactionBoundaryInterleavedRealCandidateStaysProtected(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, err := c.EncryptToken("synthetic-secret")
	if err != nil {
		t.Fatal(err)
	}
	stream := newRedactionRestoreSSE(protocol.OpenAICompletions, c.RestoreText, false)
	first := redactionBoundaryChat(t, map[string]any{"content": token[:len(token)-4]}, nil)
	tools := redactionBoundaryChat(t, redactionBoundaryTool(`{"x":1}`), nil)
	for _, frame := range [][]byte{first, tools} {
		got, err := stream.Push(frame)
		if err != nil || len(got) != 0 {
			t.Fatalf("real candidate released at tool transition: %v", err)
		}
	}
	got, err := stream.Push(redactionBoundaryChat(t, map[string]any{"content": token[len(token)-4:]}, "tool_calls"))
	if err != nil || bytes.Contains(got, []byte("gld1_")) || bytes.Count(got, []byte("data: ")) != 3 {
		t.Fatalf("interleaved candidate did not restore in order: %v", err)
	}
	if !bytes.Contains(got, []byte("synthetic-secret")) {
		t.Fatal("restored value missing")
	}
}

func TestRedactionBoundaryTruncatedCipherStillFails(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, err := c.EncryptToken("synthetic-secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`{"value":"` + token[:len(token)-3], `{"value":` + token[:len(token)-3]} {
		stream := newRedactionRestoreSSE(protocol.OpenAICompletions, c.RestoreText, false)
		if got, err := stream.Push(redactionBoundaryChat(t, redactionBoundaryTool(fragment), nil)); err != nil || len(got) != 0 {
			t.Fatalf("truncated candidate escaped: %v", err)
		}
		if _, err := stream.Push(redactionBoundaryChat(t, map[string]any{}, "length")); err == nil {
			t.Fatal("length finish accepted truncated ciphertext")
		}
	}
}

func TestRedactionBoundaryChatCustomInputAcrossEverySplit(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, err := c.EncryptToken("a\"b\n")
	if err != nil {
		t.Fatal(err)
	}
	for split := 0; split <= len(token); split++ {
		stream := newRedactionRestoreSSE(protocol.OpenAICompletions, c.RestoreText, false)
		var output []byte
		for _, part := range []string{token[:split], token[split:]} {
			delta := map[string]any{"tool_calls": []any{map[string]any{"index": 0, "type": "custom", "custom": map[string]any{"name": "apply_patch", "input": part}}}}
			got, err := stream.Push(redactionBoundaryChat(t, delta, nil))
			if err != nil {
				t.Fatal(err)
			}
			output = append(output, got...)
		}
		got, err := stream.Push(redactionBoundaryChat(t, map[string]any{}, "tool_calls"))
		if err != nil {
			t.Fatal(err)
		}
		output = append(output, got...)
		var joined strings.Builder
		for _, frame := range bytes.Split(output, []byte("\n\n")) {
			payload, _, ok := redactionSSEData(append(bytes.Clone(frame), '\n', '\n'))
			if ok {
				joined.WriteString(gjson.GetBytes(payload, "choices.0.delta.tool_calls.0.custom.input").Str)
			}
		}
		if joined.String() != "a\"b\n" {
			t.Fatalf("custom tool split %d did not restore", split)
		}
	}
}

func TestRedactionOrdinaryPrefixAllocations(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	chunk := strings.Repeat("g", 64)
	value := `{"x":"` + strings.Repeat(chunk, 1024) + `"}`
	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			doc := redactionStreamDocument{}
			var all strings.Builder
			first, err := doc.push(`{"x":"`, cipher.RestoreText)
			if err != nil {
				b.Fatal(err)
			}
			all.WriteString(first)
			for j := 0; j < 1024; j++ {
				got, err := doc.push(chunk, cipher.RestoreText)
				if err != nil {
					b.Fatal(err)
				}
				all.WriteString(got)
			}
			last, err := doc.push(`"}`, cipher.RestoreText)
			if err != nil {
				b.Fatal(err)
			}
			all.WriteString(last)
			if all.String() != value || doc.blocked() {
				b.Fatal("ordinary text changed or buffered")
			}
		}
	})
	if result.AllocedBytesPerOp() > 2<<20 {
		t.Fatalf("64 KiB ordinary text allocated %d bytes", result.AllocedBytesPerOp())
	}
}
