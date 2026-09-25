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
	"gpt-load/internal/requestredact"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
)

func websocketRedactionTestCipher(t *testing.T) encryption.RedactionCipher {
	t.Helper()
	service, err := encryption.NewService("synthetic-websocket-redaction-master-key-2026-09-23")
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := service.NewRedactionCipher(12)
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func websocketRedactionEvent(t *testing.T, kind, lane, field, value string) []byte {
	t.Helper()
	fields := map[string]any{"type": kind, "output_index": 0, "content_index": 0, field: value}
	if lane != "" {
		fields["stream_id"] = lane
	}
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestWebsocketRedactionRestoresTextDeltasAndCompleteSnapshots(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	token, err := cipher.EncryptToken("alice@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	output := newWebsocketRedactionOutput(cipher.RestoreText, false)
	first := websocketRedactionEvent(t, "response.output_text.delta", "lane-a", "delta", token[:len(token)-5])
	second := websocketRedactionEvent(t, "response.output_text.delta", "lane-a", "delta", token[len(token)-5:])
	if got, err := output.Push(first); err != nil || len(got) != 0 {
		t.Fatalf("partial token escaped: %#v / %v", got, err)
	}
	got, err := output.Push(second)
	if err != nil || len(got) != 2 ||
		gjson.GetBytes(got[0], "delta").Str+gjson.GetBytes(got[1], "delta").Str != "alice@example.invalid" {
		t.Fatalf("restored deltas = %#v / %v", got, err)
	}
	for _, frame := range got {
		if gjson.GetBytes(frame, "stream_id").Str != "lane-a" || bytes.Contains(frame, []byte(token)) {
			t.Fatalf("lane changed or ciphertext escaped: %s", frame)
		}
	}
	done := websocketRedactionEvent(t, "response.output_text.done", "lane-a", "text", token)
	got, err = output.Push(done)
	if err != nil || len(got) != 1 || gjson.GetBytes(got[0], "text").Str != "alice@example.invalid" {
		t.Fatalf("restored text snapshot = %#v / %v", got, err)
	}
	terminal := []byte(fmt.Sprintf(`{"type":"response.done","stream_id":"lane-a","response":{"id":"resp_1","object":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":%q}]}]}}`, token))
	got, err = output.Push(terminal)
	if err != nil || len(got) != 1 ||
		gjson.GetBytes(got[0], "type").Str != "response.done" ||
		gjson.GetBytes(got[0], "response.output.0.content.0.text").Str != "alice@example.invalid" ||
		gjson.GetBytes(got[0], "response.id").Str != "resp_1" {
		t.Fatalf("restored response.done snapshot = %#v / %v", got, err)
	}
	if tail, err := output.Finish(); err != nil || len(tail) != 0 {
		t.Fatalf("Finish() = %#v / %v", tail, err)
	}
}

func TestWebsocketRedactionRestoresToolArgumentsAndKeepsFrameOrder(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	token, err := cipher.EncryptToken("secret-value")
	if err != nil {
		t.Fatal(err)
	}
	output := newWebsocketRedactionOutput(cipher.RestoreText, false)
	first := websocketRedactionEvent(t, "response.function_call_arguments.delta", "lane-b", "delta", `{"password":"`+token[:len(token)-4])
	second := websocketRedactionEvent(t, "response.function_call_arguments.delta", "lane-b", "delta", token[len(token)-4:]+`","n":9007199254740993}`)
	got, err := output.Push(first)
	if err != nil || len(got) != 0 {
		t.Fatalf("incomplete token escaped: %v", err)
	}
	got, err = output.Push(second)
	if err != nil || len(got) != 2 {
		t.Fatalf("completed token did not stream: %v", err)
	}
	done := websocketRedactionEvent(t, "response.function_call_arguments.done", "lane-b", "arguments", `{"password":"`+token+`","n":9007199254740993}`)
	tail, err := output.Push(done)
	got = append(got, tail...)
	if err != nil || len(got) != 3 {
		t.Fatalf("tool output frames = %#v / %v", got, err)
	}
	if gjson.GetBytes(got[0], "type").Str != "response.function_call_arguments.delta" ||
		gjson.GetBytes(got[1], "type").Str != "response.function_call_arguments.delta" ||
		gjson.GetBytes(got[2], "type").Str != "response.function_call_arguments.done" {
		t.Fatalf("tool frame order changed: %#v", got)
	}
	var fragments strings.Builder
	for _, frame := range got[:2] {
		fragments.WriteString(gjson.GetBytes(frame, "delta").Str)
	}
	if fragments.String() != `{"password":"secret-value","n":9007199254740993}` ||
		gjson.GetBytes(got[2], "arguments").Str != fragments.String() {
		t.Fatalf("restored tool arguments = %q / %s", fragments.String(), got[2])
	}
	terminal := []byte(fmt.Sprintf(`{"type":"response.done","stream_id":"lane-b","response":{"id":"resp_tool","object":"response","status":"completed","output":[{"type":"function_call","name":"send","arguments":%q}]}}`, `{"password":"`+token+`","n":9007199254740993}`))
	got, err = output.Push(terminal)
	if err != nil || len(got) != 1 ||
		gjson.GetBytes(got[0], "type").Str != "response.done" ||
		gjson.GetBytes(got[0], "response.output.0.arguments").Str != fragments.String() {
		t.Fatalf("restored tool snapshot = %#v / %v", got, err)
	}
	if _, err := output.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestWebsocketRedactionDoneStatusRestoresIncompleteAndMasksFailedSnapshots(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	token, err := cipher.EncryptToken("private")
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"incomplete", "failed"} {
		t.Run(status, func(t *testing.T) {
			output := newWebsocketRedactionOutput(cipher.RestoreText, false)
			body := []byte(fmt.Sprintf(`{"type":"response.done","response":{"id":"resp_1","object":"response","status":%q,"output":[{"type":"message","content":[{"type":"output_text","text":%q}]}]}}`, status, token))
			got, err := output.Push(body)
			want := body
			if status == "incomplete" {
				want = bytes.ReplaceAll(body, []byte(token), []byte("private"))
			} else {
				want = bytes.ReplaceAll(body, []byte(token), []byte("[REDACTED]"))
			}
			if err != nil || len(got) != 1 || !bytes.Equal(got[0], want) {
				t.Fatalf("%s response.done differs: %#v / %v", status, got, err)
			}
		})
	}
}

func TestWebsocketRedactionKeepsIndependentLanesAndUnchangedFrames(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	token, err := cipher.EncryptToken("private")
	if err != nil {
		t.Fatal(err)
	}
	first := newWebsocketRedactionOutput(cipher.RestoreText, false)
	second := newWebsocketRedactionOutput(cipher.RestoreText, false)
	if got, err := first.Push(websocketRedactionEvent(t, "response.output_text.delta", "first", "delta", token[:len(token)-2])); err != nil || len(got) != 0 {
		t.Fatalf("first lane pending output = %#v / %v", got, err)
	}
	plain := []byte(" {\n \"type\": \"response.output_text.delta\", \"stream_id\": \"second\", \"output_index\": 0, \"content_index\": 0, \"delta\": \"public\" }")
	if got, err := second.Push(plain); err != nil || len(got) != 1 || !bytes.Equal(got[0], plain) {
		t.Fatalf("second lane waited or changed: %#v / %v", got, err)
	}
	got, err := first.Push(websocketRedactionEvent(t, "response.output_text.delta", "first", "delta", token[len(token)-2:]))
	if err != nil || len(got) != 2 ||
		gjson.GetBytes(got[0], "delta").Str+gjson.GetBytes(got[1], "delta").Str != "private" {
		t.Fatalf("first lane restoration = %#v / %v", got, err)
	}
}

func TestWebsocketRedactionKeepsDamagedAndTruncatedTokens(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	token, err := cipher.EncryptToken("private")
	if err != nil {
		t.Fatal(err)
	}
	last := "A"
	if strings.HasSuffix(token, last) {
		last = "B"
	}
	damaged := token[:len(token)-1] + last
	output := newWebsocketRedactionOutput(cipher.RestoreText, false)
	if got, err := output.Push(websocketRedactionEvent(t, "response.output_text.delta", "lane", "delta", damaged)); err != nil ||
		len(got) != 1 || gjson.GetBytes(got[0], "delta").Str != damaged {
		t.Fatalf("damaged token was not forwarded unchanged: %#v / %v", got, err)
	}
	output = newWebsocketRedactionOutput(cipher.RestoreText, false)
	if got, err := output.Push(websocketRedactionEvent(t, "response.output_text.delta", "lane", "delta", token[:len(token)-1])); err != nil || len(got) != 0 {
		t.Fatalf("partial token forwarded: %#v / %v", got, err)
	}
	if got, err := output.Finish(); err != nil || len(got) != 1 || gjson.GetBytes(got[0], "delta").Str != token[:len(token)-1] {
		t.Fatalf("truncated token was not kept at end: %#v / %v", got, err)
	}
}

func TestWebsocketRedactionAcceptsExistingMaximumFrameSize(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	token, err := cipher.EncryptToken("p")
	if err != nil {
		t.Fatal(err)
	}
	prefix := `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"` + token
	suffix := `"}`
	body := []byte(prefix + strings.Repeat("x", maxRedactionStreamDocument-len(prefix)-len(suffix)) + suffix)
	if len(body) != maxRedactionStreamDocument || !json.Valid(body) {
		t.Fatal("invalid maximum-size WebSocket fixture")
	}
	output := newWebsocketRedactionOutput(cipher.RestoreText, false)
	frames, err := output.Push(body)
	if err != nil || len(frames) != 1 || len(frames[0]) > maxRedactionStreamDocument ||
		!strings.HasPrefix(gjson.GetBytes(frames[0], "delta").Str, "p") {
		t.Fatalf("maximum-size WebSocket frame rejected or not restored: frames=%d error=%v", len(frames), err)
	}
	donePrefix := `{"type":"response.done","response":{"id":"resp_1","object":"response","status":"incomplete"},"padding":"`
	done := []byte(donePrefix + strings.Repeat("x", maxRedactionStreamDocument-len(donePrefix)-len(suffix)) + suffix)
	output = newWebsocketRedactionOutput(cipher.RestoreText, false)
	frames, err = output.Push(done)
	if err != nil || len(frames) != 1 || !bytes.Equal(frames[0], done) {
		t.Fatalf("maximum-size response.done frame changed or rejected: frames=%d error=%v", len(frames), err)
	}
}

func TestWebsocketRedactionRestoresOnlyClientVisibleFrames(t *testing.T) {
	h, engine, settings := websocketTestHandler(t, "http://unused.invalid/v1", channel.OpenAI)
	settings.RequestRedaction = []requestredact.Rule{{Pattern: `alice@example\.invalid`, Mode: requestredact.ModeEncrypt}}
	if _, err := h.manager.Publish(settings); err != nil {
		t.Fatal(err)
	}
	cipher, err := h.encryption.NewRedactionCipher(1)
	if err != nil {
		t.Fatal(err)
	}
	token, err := cipher.EncryptToken("alice@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	first := websocketRedactionEvent(t, "response.output_text.delta", "", "delta", token[:len(token)-4])
	second := websocketRedactionEvent(t, "response.output_text.delta", "", "delta", token[len(token)-4:])
	terminal := []byte(fmt.Sprintf(`{"type":"response.done","response":{"id":"resp_1","object":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":%q}]}]}}`, token))
	upstreamFrames := [][]byte{first, second, terminal}
	originalFrames := [][]byte{bytes.Clone(first), bytes.Clone(second), bytes.Clone(terminal)}
	seenUpstream := make(chan []byte, 1)
	emitFailures := make(chan error, 1)
	h.forwarder = websocketScriptForwarder{AttemptForwarder: h.forwarder, open: func(_ context.Context, input ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
		if input.RedactionCipher == nil {
			t.Error("WebSocket attempt has no redaction cipher")
		}
		session := &websocketScriptSession{done: make(chan struct{})}
		session.turn = func(ctx context.Context, body []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
			seenUpstream <- bytes.Clone(body)
			for _, frame := range upstreamFrames {
				if err := emit(ctx, frame); err != nil {
					emitFailures <- err
					return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindInternal}}
				}
			}
			return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
		}
		return session, execution.WebsocketResult{}
	}}
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "alice@example.invalid", "store": false}); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var deltas strings.Builder
	for index := range 3 {
		_, frame, err := conn.ReadMessage()
		if err != nil {
			select {
			case emitErr := <-emitFailures:
				t.Fatalf("read frame %d: %v; upstream emit error=%v; logs=%+v", index, err, emitErr, sink.snapshot())
			default:
				t.Fatalf("read frame %d: %v; logs=%+v", index, err, sink.snapshot())
			}
		}
		if gjson.GetBytes(frame, "type").Str == "response.output_text.delta" {
			deltas.WriteString(gjson.GetBytes(frame, "delta").Str)
		} else if gjson.GetBytes(frame, "type").Str == "response.done" &&
			gjson.GetBytes(frame, "response.output.0.content.0.text").Str != "alice@example.invalid" {
			t.Fatalf("response.done snapshot not restored: %s", frame)
		}
	}
	if deltas.String() != "alice@example.invalid" {
		t.Fatalf("client deltas = %q, want plaintext", deltas.String())
	}
	upstream := <-seenUpstream
	if bytes.Contains(upstream, []byte("alice@example.invalid")) || !bytes.Contains(upstream, []byte(token)) {
		t.Fatalf("upstream request not encrypted: %s", upstream)
	}
	for index, frame := range upstreamFrames {
		if !bytes.Equal(frame, originalFrames[index]) {
			t.Fatalf("upstream response mutated: %s", frame)
		}
	}
	logs, err := json.Marshal(waitWebsocketLogs(t, sink, 1))
	if err != nil || bytes.Contains(logs, []byte("alice@example.invalid")) {
		t.Fatalf("observability contains restored plaintext: %s / %v", logs, err)
	}
}

func TestWebsocketRedactionRejectsCredentialConflict(t *testing.T) {
	h, engine, _ := websocketTestHandler(t, "http://unused.invalid/v1", channel.OpenAI)
	cipher, err := h.encryption.NewRedactionCipher(1)
	if err != nil {
		t.Fatal(err)
	}
	// 请求历史里带着密文时才启用还原。
	history, err := cipher.EncryptToken("history")
	if err != nil {
		t.Fatal(err)
	}
	h.forwarder = websocketScriptForwarder{AttemptForwarder: h.forwarder, open: func(_ context.Context, input ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
		token, err := cipher.EncryptToken(input.APIKey)
		if err != nil || input.APIKey == "" {
			t.Error("missing synthetic credential")
			return nil, execution.WebsocketResult{}
		}
		session := &websocketScriptSession{done: make(chan struct{})}
		session.turn = func(ctx context.Context, _ []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
			if err := emit(ctx, websocketRedactionEvent(t, "response.output_text.delta", "", "delta", token)); err != nil {
				return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindInternal}}
			}
			return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
		}
		return session, execution.WebsocketResult{}
	}}
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "hello " + history, "store": false}); err != nil {
		t.Fatal(err)
	}
	logs := waitWebsocketLogs(t, sink, 1)
	if len(logs[0].Attempts) != 1 || logs[0].Attempts[0].ErrorCode != "response_redaction_failed" ||
		logs[0].Attempts[0].FailureOrigin != execution.ErrorOriginInternal ||
		logs[0].Attempts[0].Effect != telemetry.EffectNone {
		t.Fatalf("restoration failure attributed to upstream: %+v", logs[0].Attempts)
	}
	registry := h.registry.(*state.CredentialRegistry)
	if until, _ := registry.CredentialCooldownUntil(1); !until.IsZero() {
		t.Fatalf("restoration failure cooled credential until %s", until)
	}
	if until := registry.ModelCooldowns(1, time.Now())["upstream"]; !until.IsZero() {
		t.Fatalf("restoration failure cooled model until %s", until)
	}
}
