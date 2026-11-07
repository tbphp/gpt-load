package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
)

const (
	codexReasoningStatusHistoryItems = 500
	codexReasoningStatusBody         = `{"model":"gpt-5","store":false,"input":[{"type":"reasoning","id":"item_9d700c62-046b-426a-8431-27497dcd0a0a_0","status":"completed","encrypted_content":"","summary":[{"type":"summary_text","text":"Devin summary"}]},{"type":"reasoning","id":"rs_native","status":null,"encrypted_content":"native opaque bytes","summary":[]},{"type":"message","id":"msg_keep","status":"completed","role":"assistant","content":[{"type":"output_text","text":"history"}]},{"type":"function_call","id":"fc_keep","call_id":"call_keep","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_keep","output":"result"}],"metadata":{"n":9007199254740993}}`
)

func TestPrepareCodexExecutionRequestStripsReasoningStatusOnly(t *testing.T) {
	source := []byte(codexReasoningStatusBody)
	original := bytes.Clone(source)
	originalRequest := bytes.Clone(source)
	request := ExecuteRequest{
		Format: "openai-response", Payload: source, OriginalRequest: originalRequest,
		Headers: http.Header{"User-Agent": {"custom-client"}},
	}
	prepared := prepareCodexExecutionRequest(request)
	if !bytes.Equal(source, original) || !bytes.Equal(originalRequest, original) {
		t.Fatal("source payload mutated")
	}
	assertPreparedCodexReasoningStatus(t, prepared.Payload)
	assertPreparedCodexReasoningStatus(t, prepared.OriginalRequest)
	if second := prepareCodexExecutionRequest(prepared); !bytes.Equal(second.Payload, prepared.Payload) {
		t.Fatalf("cleanup is not idempotent: %s", second.Payload)
	}

	objectBody := []byte(`{"input":{"type":"reasoning","status":"completed","id":"keep","summary":[{"type":"summary_text","text":"keep"}]}}`)
	objectOriginal := bytes.Clone(objectBody)
	object := prepareCodexExecutionRequest(ExecuteRequest{Format: "openai-response", Payload: objectBody})
	if !bytes.Equal(objectBody, objectOriginal) {
		t.Fatal("object source payload mutated")
	}
	if gjson.GetBytes(object.Payload, "input.status").Exists() || gjson.GetBytes(object.Payload, "input.id").String() != "keep" {
		t.Fatalf("object input changed beyond reasoning.status: %s", object.Payload)
	}

	for _, format := range []string{"openai", "openai-image"} {
		untouched := prepareCodexExecutionRequest(ExecuteRequest{Format: format, Payload: bytes.Clone(source)})
		if !bytes.Equal(untouched.Payload, source) {
			t.Fatalf("%s payload changed: %s", format, untouched.Payload)
		}
	}
	for _, body := range []string{
		`not json`, `{}`, `{"input":"text"}`,
		`{"input":[{"type":"reasoning","encrypted_content":"keep"},{"type":"message","status":"completed"},null,"text"]}`,
		`{"input":[{"type":"item_reference","id":"rs_keep"}]}`,
		`{ "input" : [ { "type" : "reasoning" , "encrypted_content" : "keep" } , { "type" : "message" , "status" : "completed" } ] }`,
	} {
		sourceBody := []byte(body)
		got := prepareCodexExecutionRequest(ExecuteRequest{Format: "openai-response", Payload: sourceBody})
		if !bytes.Equal(got.Payload, []byte(body)) || !bytes.Equal(sourceBody, []byte(body)) {
			t.Fatalf("unexpected cleanup for %s: %s", body, got.Payload)
		}
	}
}

func TestPrepareCodexExecutionRequestStripsLongHistory(t *testing.T) {
	source := validCodexReasoningStatusHistory(t, codexReasoningStatusHistoryItems)
	t.Logf("input_bytes=%d reasoning_items=%d", len(source), codexReasoningStatusHistoryItems)
	original := bytes.Clone(source)
	originalRequest := bytes.Clone(source)
	prepared := prepareCodexExecutionRequest(ExecuteRequest{
		Format: "openai-response", Payload: source, OriginalRequest: originalRequest,
	})
	if !bytes.Equal(source, original) || !bytes.Equal(originalRequest, original) {
		t.Fatal("source payload mutated")
	}
	assertCodexReasoningStatusHistory(t, prepared.Payload, codexReasoningStatusHistoryItems)
	assertCodexReasoningStatusHistory(t, prepared.OriginalRequest, codexReasoningStatusHistoryItems)
}

func TestCodexHTTPExecutorStripsReasoningStatusOnResponses(t *testing.T) {
	credential := CodexCredential{Type: ProviderCodex, AccessToken: "access", RefreshToken: "refresh", AccountID: "account-1"}
	for _, test := range []struct {
		name   string
		stream bool
	}{
		{name: "canonical"},
		{name: "stream", stream: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			captured := make(chan []byte, 1)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				captured <- body
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: "+string(wsCompleted("resp_status"))+"\n\n")
			}))
			defer server.Close()
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", server.Client().Transport)
			request := ExecuteRequest{
				Model: "gpt-5", Format: "openai-response", BaseURL: server.URL,
				Payload: []byte(codexReasoningStatusBody), OriginalRequest: []byte(codexReasoningStatusBody),
			}
			if test.stream {
				response, err := NewCodexHTTPExecutor().ExecuteStreamCanonical(ctx, "credential-1", credential, request)
				if err != nil {
					t.Fatalf("ExecuteStreamCanonical() error = %v", err)
				}
				for range response.Chunks {
				}
			} else if _, err := NewCodexHTTPExecutor().ExecuteCanonical(ctx, "credential-1", credential, request); err != nil {
				t.Fatalf("ExecuteCanonical() error = %v", err)
			}
			assertOutboundCodexReasoningStatus(t, <-captured)
		})
	}
}

func TestCodexWSSessionStripsReasoningStatus(t *testing.T) {
	captured := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, body, err := conn.ReadMessage()
		if err != nil {
			t.Error(err)
			return
		}
		captured <- body
		_ = conn.WriteMessage(websocket.TextMessage, wsCompleted("resp_status"))
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	if _, err := wsTestSession(t, server.URL).ExecuteTurn(t.Context(), json.RawMessage(codexReasoningStatusBody), nil); err != nil {
		t.Fatalf("ExecuteTurn() error = %v", err)
	}
	assertOutboundCodexReasoningStatus(t, <-captured)
}

func BenchmarkStripCodexReasoningStatus(b *testing.B) {
	body := validCodexReasoningStatusHistory(b, codexReasoningStatusHistoryItems)
	b.SetBytes(int64(len(body)))
	b.ReportMetric(float64(len(body)), "input_bytes")
	b.ReportMetric(float64(codexReasoningStatusHistoryItems), "reasoning_items")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		got := stripCodexReasoningStatus(append([]byte(nil), body...))
		if len(got) == 0 {
			b.Fatal("strip returned empty")
		}
	}
}

func BenchmarkPrepareCodexExecutionRequest(b *testing.B) {
	body := validCodexReasoningStatusHistory(b, codexReasoningStatusHistoryItems)
	b.SetBytes(int64(len(body) * 2))
	b.ReportMetric(float64(len(body)), "input_bytes")
	b.ReportMetric(float64(codexReasoningStatusHistoryItems), "reasoning_items")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		got := prepareCodexExecutionRequest(ExecuteRequest{
			Format: "openai-response", Payload: append([]byte(nil), body...), OriginalRequest: append([]byte(nil), body...),
		})
		if len(got.Payload) == 0 || len(got.OriginalRequest) == 0 {
			b.Fatal("prepare returned empty")
		}
	}
}

func assertPreparedCodexReasoningStatus(t *testing.T, raw []byte) {
	t.Helper()
	assertOutboundCodexReasoningStatus(t, raw)
	if gjson.GetBytes(raw, "input.0.id").String() != "item_9d700c62-046b-426a-8431-27497dcd0a0a_0" ||
		gjson.GetBytes(raw, "input.0.encrypted_content").String() != "" ||
		gjson.GetBytes(raw, "input.1.id").String() != "rs_native" ||
		gjson.GetBytes(raw, "input.1.encrypted_content").String() != "native opaque bytes" ||
		gjson.GetBytes(raw, "input.2.id").String() != "msg_keep" {
		t.Fatalf("fields other than reasoning.status changed: %s", raw)
	}
}

func assertOutboundCodexReasoningStatus(t *testing.T, raw []byte) {
	t.Helper()
	if !bytes.Contains(raw, []byte("9007199254740993")) {
		t.Fatalf("unrelated integer rewritten: %s", raw)
	}
	input := gjson.GetBytes(raw, "input")
	if !input.IsArray() {
		t.Fatalf("input missing: %s", raw)
	}
	var reasoning, messages, tools int
	input.ForEach(func(_, item gjson.Result) bool {
		switch item.Get("type").String() {
		case "reasoning":
			reasoning++
			if item.Get("status").Exists() {
				t.Fatalf("reasoning.status still present: %s", raw)
			}
		case "message":
			messages++
			if item.Get("status").String() != "completed" {
				t.Fatalf("message.status changed: %s", raw)
			}
		case "function_call", "function_call_output":
			tools++
			if item.Get("call_id").String() != "call_keep" {
				t.Fatalf("tool pair changed: %s", raw)
			}
		}
		return true
	})
	if reasoning == 0 || messages != 1 || tools != 2 {
		t.Fatalf("input items dropped or rewritten: %s", raw)
	}
}

func assertCodexReasoningStatusHistory(t *testing.T, raw []byte, want int) {
	t.Helper()
	if !bytes.Contains(raw, []byte(`"metadata":{"n":9007199254740993}`)) {
		t.Fatalf("root metadata.n literal rewritten: %s", raw)
	}
	input := gjson.GetBytes(raw, "input")
	if !input.IsArray() {
		t.Fatalf("input missing: %s", raw)
	}
	var reasoning, messages int
	input.ForEach(func(_, item gjson.Result) bool {
		switch item.Get("type").String() {
		case "reasoning":
			reasoning++
			if item.Get("status").Exists() {
				t.Fatalf("reasoning.status still present")
			}
			if !item.Get("id").Exists() || item.Get("summary.0.text").String() != "keep" {
				t.Fatalf("reasoning fields other than status changed: %s", item.Raw)
			}
		case "message":
			messages++
			if item.Get("status").String() != "completed" || item.Get("id").String() != "msg_keep" {
				t.Fatalf("message fields changed: %s", item.Raw)
			}
		}
		return true
	})
	if reasoning != want || messages != 1 {
		t.Fatalf("history items dropped or rewritten: reasoning=%d messages=%d", reasoning, messages)
	}
}

func validCodexReasoningStatusHistory(tb testing.TB, n int) []byte {
	tb.Helper()
	body := codexReasoningStatusHistory(n)
	if !json.Valid(body) {
		tb.Fatalf("history JSON is invalid (%d bytes)", len(body))
	}
	if !bytes.Contains(body, []byte(`"metadata":{"n":9007199254740993}`)) {
		tb.Fatalf("root metadata.n literal missing (%d bytes)", len(body))
	}
	return body
}

func codexReasoningStatusHistory(n int) []byte {
	var b strings.Builder
	b.WriteString(`{"model":"gpt-5","store":false,"input":[`)
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"type":"reasoning","id":"rs_`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`","status":`)
		if i%2 == 1 {
			b.WriteString("null")
		} else {
			b.WriteString(`"completed"`)
		}
		b.WriteString(`,"summary":[{"type":"summary_text","text":"keep"}]}`)
	}
	if n > 0 {
		b.WriteByte(',')
	}
	b.WriteString(`{"type":"message","id":"msg_keep","status":"completed","role":"assistant","content":[{"type":"output_text","text":"history"}]}`)
	b.WriteString(`],"metadata":{"n":9007199254740993}}`)
	return []byte(b.String())
}
