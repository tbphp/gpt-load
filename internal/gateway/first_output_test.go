package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestFirstOutputSinkFramesSplitAndCoalescedSSE(t *testing.T) {
	calls := 0
	delivered := firstOutputSink(protocol.OpenAIResponses, func() {
		calls++
	})
	delivered([]byte(": keepalive\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hel"))
	if calls != 0 {
		t.Fatal("incomplete event counted")
	}
	delivered([]byte("lo\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\" world\"}\n\n"))
	if calls != 1 {
		t.Fatalf("coalesced delivery count=%d", calls)
	}
	delivered([]byte("data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"content\":[{\"text\":\"hello world\"}]}]}}\n\n"))
	if calls != 1 {
		t.Fatal("terminal snapshot changed timing")
	}
}

func TestHandlerRecordsUpstreamFirstResponseAcrossRetries(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(fmt.Sprintf("retry=%t", retry), func(t *testing.T) {
			attempts := 0
			started := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
			now := started
			sink := &recordingRequestLogSink{}
			executor := fakeExecutionExecutor{stream: func(_ context.Context, _ execution.AttemptSpec, emit execution.StreamSink) execution.StreamResult {
				attempts++
				if err := emit(execution.StreamEvent{Sequence: 1, Kind: execution.StreamEventReady, StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}}); err != nil {
					t.Fatal(err)
				}
				if retry && attempts == 1 {
					now = started.Add(50 * time.Millisecond)
					if err := emit(execution.StreamEvent{Sequence: 2, Kind: execution.StreamEventData, Data: []byte("data: {\"error\":{\"type\":\"rate_limit_error\",\"code\":\"rate_limit_exceeded\",\"message\":\"retry later\"}}\n\n")}); err != nil {
						t.Fatal(err)
					}
					return execution.StreamResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}}
				}
				for i, chunk := range []struct {
					ms   int
					data string
				}{
					{100, `{"choices":[{"delta":{"role":"assistant"}}]}`},
					{200, `{"choices":[{"delta":{"reasoning_content":"think"}}]}`},
					{700, `{"choices":[{"delta":{"content":"hello"}}]}`},
					{1500, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`},
					{2000, `{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":101,"total_tokens":102}}`},
					{2500, `[DONE]`},
				} {
					now = started.Add(time.Duration(chunk.ms) * time.Millisecond)
					if err := emit(execution.StreamEvent{Sequence: uint64(i + 2), Kind: execution.StreamEventData, Data: []byte("data: " + chunk.data + "\n\n")}); err != nil {
						t.Fatal(err)
					}
				}
				return execution.StreamResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}}
			}}
			engine, handler, _, _ := newRequestLogHandlerTestRuntime(t, NewExecutionForwarder(executor), &recordingAccessKeyRPMLimiter{}, sink, "sk-first", "sk-second")
			handler.requestNow = func() time.Time { return now }
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","stream":true}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			events := sink.snapshot()
			if len(events) != 1 {
				t.Fatalf("logs=%d", len(events))
			}
			event := events[0]
			wantFirst := int64(100)
			if retry {
				wantFirst = 50
			}
			if event.FirstResponseMs == nil || *event.FirstResponseMs != wantFirst || event.DurationMs != 2500 {
				t.Fatalf("first=%v duration=%d", event.FirstResponseMs, event.DurationMs)
			}

			if retry && attempts != 2 {
				t.Fatalf("attempts=%d", attempts)
			}
		})
	}
}

func TestFirstOutputRequiresSuccessfulFlushOfRestoredContent(t *testing.T) {
	cipher := websocketRedactionTestCipher(t)
	token, err := cipher.EncryptToken("restored text")
	if err != nil {
		t.Fatal(err)
	}
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("flush_failure=%t", fail), func(t *testing.T) {
			phase := 0
			var observed []int
			writer := &redactionFinishWriter{ResponseRecorder: httptest.NewRecorder(), fail: fail, err: errors.New("synthetic flush error")}
			executor := fakeExecutionExecutor{stream: func(_ context.Context, _ execution.AttemptSpec, sink execution.StreamSink) execution.StreamResult {
				if err := sink(execution.StreamEvent{Sequence: 1, Kind: execution.StreamEventReady, StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}}); err != nil {
					t.Fatal(err)
				}
				for i, body := range []string{
					`{"type":"response.created","response":{}}`,
					fmt.Sprintf(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":%q}`, token[:len(token)-5]),
					fmt.Sprintf(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":%q}`, token[len(token)-5:]),
					`{"type":"response.completed","response":{"status":"completed"}}`,
				} {
					phase = i + 1
					if err := sink(execution.StreamEvent{Sequence: uint64(i + 2), Kind: execution.StreamEventData, Data: []byte("data: " + body + "\n\n")}); err != nil {
						break
					}
				}
				return execution.StreamResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}}
			}}
			input := responsesExecutionForwardInput()
			input.RedactionCipher = cipher
			input.OnFirstOutput = func() {
				observed = append(observed, phase)
			}
			result := NewExecutionForwarder(executor).ForwardStream(context.Background(), input, writer)
			if fail {
				if result.Err == nil || len(observed) != 0 {
					t.Fatalf("failed delivery counted: %v, %v", observed, result.Err)
				}
			} else if result.Err != nil || len(observed) != 1 || observed[0] != 3 {
				t.Fatalf("restoration timing=%v, error=%v", observed, result.Err)
			}
		})
	}
}

func TestWebsocketRecordsFirstResponseBeforeUsageTail(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		for _, body := range [][]byte{
			[]byte(`{"type":"response.created","response":{"id":"resp_1","object":"response"}}`),
			[]byte(`{"type":"response.output_text.delta","response_id":"resp_1","delta":"hello"}`),
			[]byte(`{"type":"response.output_text.delta","response_id":"resp_1","delta":" world"}`),
			websocketCompleted("resp_1", ""),
		} {
			if conn.WriteMessage(websocket.TextMessage, body) != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer upstream.Close()
	handler, engine, _ := websocketTestHandler(t, upstream.URL, channel.OpenAI)
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatal(err)
		}
	}
	event := waitWebsocketLogs(t, sink, 1)[0]
	if event.FirstResponseMs == nil || event.DurationMs <= *event.FirstResponseMs {
		t.Fatalf("unexpected output timing: %+v", event)
	}
}

func TestHandlerFirstResponseIgnoresOutputIdentifiers(t *testing.T) {
	for _, coalesced := range []bool{false, true} {
		t.Run(fmt.Sprintf("coalesced=%t", coalesced), func(t *testing.T) {
			frames := []string{
				`{"type":"response.output_text.delta","output_index":0,"delta":"first"}`,
				fmt.Sprintf(`{"type":"response.output_text.delta","item_id":%q,"delta":"large identifier"}`, strings.Repeat("x", 4097)),
				`{"type":"response.output_text.delta","output_index":1,"delta":"last"}`,
				`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":3,"total_tokens":4}}}`,
			}
			chunks := make([]string, len(frames))
			for i, frame := range frames {
				chunks[i] = "data: " + frame + "\n\n"
			}
			want := strings.Join(chunks, "")
			if coalesced {
				chunks = []string{want}
			}
			executor := fakeExecutionExecutor{stream: func(_ context.Context, _ execution.AttemptSpec, emit execution.StreamSink) execution.StreamResult {
				if err := emit(execution.StreamEvent{Sequence: 1, Kind: execution.StreamEventReady, StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}}); err != nil {
					t.Fatal(err)
				}
				for i, chunk := range chunks {
					if err := emit(execution.StreamEvent{Sequence: uint64(i + 2), Kind: execution.StreamEventData, Data: []byte(chunk)}); err != nil {
						t.Fatal(err)
					}
				}
				return execution.StreamResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}}
			}}
			sink := &recordingRequestLogSink{}
			engine, handler, _, _ := newRequestLogHandlerTestRuntime(t, NewExecutionForwarder(executor), &recordingAccessKeyRPMLimiter{}, sink, "sk-first")
			handler.dialects = dialect.NewSet(dialect.NewOpenAIResponses())
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o","stream":true}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != want {
				t.Fatalf("delivery changed: status=%d body=%s", response.Code, response.Body.String())
			}
			events := sink.snapshot()
			if len(events) != 1 || events[0].Status != "success" || len(events[0].Attempts) != 1 {
				t.Fatalf("request outcome changed: %+v", events)
			}
			if events[0].FirstResponseMs == nil {
				t.Fatal("large output identifier removed first response timing")
			}
		})
	}
}

func TestWebsocketFirstResponseIgnoresOutputIdentifiers(t *testing.T) {
	frames := [][]byte{
		[]byte(`{"type":"response.created","response":{"id":"resp_1","object":"response"}}`),
		[]byte(`{"type":"response.output_text.delta","response_id":"resp_1","output_index":0,"delta":"first"}`),
		[]byte(fmt.Sprintf(`{"type":"response.output_text.delta","response_id":"resp_1","item_id":%q,"delta":"large identifier"}`, strings.Repeat("x", 4097))),
		[]byte(`{"type":"response.output_text.delta","response_id":"resp_1","output_index":1,"delta":"last"}`),
		websocketCompleted("resp_1", ""),
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		for _, frame := range frames {
			if conn.WriteMessage(websocket.TextMessage, frame) != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	handler, engine, _ := websocketTestHandler(t, upstream.URL, channel.OpenAI)
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	for i, want := range frames {
		_, got, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if i < len(frames)-1 && string(got) != string(want) {
			t.Fatalf("frame %d delivery changed: %s", i, got)
		}
	}
	event := waitWebsocketLogs(t, sink, 1)[0]
	if event.Status != "success" || len(event.Attempts) != 1 {
		t.Fatalf("request outcome changed: %+v", event)
	}
	if event.FirstResponseMs == nil {
		t.Fatal("large output identifier removed first response timing")
	}
}
