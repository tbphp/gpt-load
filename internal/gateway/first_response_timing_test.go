package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"gpt-load/internal/automodel"
	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/protocol"
)

func TestFirstResponseSamplesDataLineBeforeFrameAndRestoration(t *testing.T) {
	phase, observed := 0, 0
	input := responsesExecutionForwardInput()
	input.RedactionCipher = websocketRedactionTestCipher(t)
	input.OnFirstResponse = func() {
		if observed == 0 {
			observed = phase
		}
	}
	executor := fakeExecutionExecutor{stream: func(_ context.Context, _ execution.AttemptSpec, emit execution.StreamSink) execution.StreamResult {
		if err := emit(execution.StreamEvent{Sequence: 1, Kind: execution.StreamEventReady, StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}}); err != nil {
			t.Fatal(err)
		}
		for i, data := range []string{
			": ping\n\nevent: response.created\ndata: \n",
			"data: {\"type\":\"response.created\",\"response\":{}}\n",
			"\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n",
		} {
			phase = i + 1
			if err := emit(execution.StreamEvent{Sequence: uint64(i + 2), Kind: execution.StreamEventData, Data: []byte(data)}); err != nil {
				t.Fatal(err)
			}
		}
		return execution.StreamResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, StatusCode: http.StatusOK}
	}}
	NewExecutionForwarder(executor).ForwardStream(t.Context(), input, httptest.NewRecorder())
	if observed != 2 {
		t.Fatalf("first response phase = %d, want 2 before the SSE frame is complete", observed)
	}
}

func TestAutoModelFirstOutputRemainsIndependentOfLogFirstResponse(t *testing.T) {
	start := time.Unix(1000, 0)
	now := start
	recorder := newRequestRecorder(nil, fixedRequestID, start, 1, protocol.OpenAIResponses, func() time.Time { return now })
	recorder.autoDecision = &automodel.Decision{}
	recorder.setStream(true)
	recorder.beforeForward()
	now = start.Add(50 * time.Millisecond)
	recorder.recordFirstResponse()
	now = start.Add(200 * time.Millisecond)
	recorder.recordFirstOutput()
	now = start.Add(700 * time.Millisecond)
	recorder.recordFirstOutput()
	if recorder.firstResponseMs == nil || *recorder.firstResponseMs != 50 || recorder.autoDecision.AnswerFirstResponseMs == nil || *recorder.autoDecision.AnswerFirstResponseMs != 200 {
		t.Fatalf("independent observation lost: first=%v auto=%+v", recorder.firstResponseMs, recorder.autoDecision)
	}
}

func TestWebsocketTimingExcludesConnectionIdle(t *testing.T) {
	var clockMS atomic.Int64
	ack := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for turn, id := range []string{"resp_1", "resp_2"} {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			base := int64(turn) * 10_000
			clockMS.Store(base + 50)
			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.created","response":{"id":"`+id+`","object":"response"}}`)); err != nil {
				return
			}
			select {
			case <-ack:
			case <-r.Context().Done():
				return
			case <-time.After(2 * time.Second):
				t.Error("client did not acknowledge first response")
				return
			}
			clockMS.Store(base + 400)
			if err := conn.WriteMessage(websocket.TextMessage, websocketCompleted(id, "")); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	handler, engine, _ := websocketTestHandler(t, upstream.URL, channel.OpenAI)
	handler.requestNow = func() time.Time { return time.Unix(1000, 0).Add(time.Duration(clockMS.Load()) * time.Millisecond) }
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	for turn := range 2 {
		clockMS.Store(int64(turn) * 10_000)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"hello"}`)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatal(err)
		}
		ack <- struct{}{}
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatal(err)
		}
		event := waitWebsocketLogs(t, sink, turn+1)[turn]
		if event.DurationMs != 400 || event.FirstResponseMs == nil || *event.FirstResponseMs != 50 {
			t.Fatalf("turn %d timing: duration=%d first=%v", turn, event.DurationMs, event.FirstResponseMs)
		}
	}
}

func TestHTTPLogTimingExcludesAdmissionAndIncludesRetries(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{true: "stream", false: "unary"}[stream], func(t *testing.T) {
			start := time.Unix(1000, 200_000_000)
			now, attempts := start, 0
			limiter := &recordingAccessKeyRPMLimiter{onAllow: func(rpmLimiterCall) { now = start.Add(2 * time.Second) }}
			sink := &recordingRequestLogSink{}
			executor := fakeExecutionExecutor{}
			executor.unary = func(_ context.Context, _ execution.AttemptSpec) execution.AttemptResult {
				attempts++
				now = start.Add(9 * time.Second)
				return execution.AttemptResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":100,"total_tokens":101}}`)}
			}
			executor.stream = func(_ context.Context, _ execution.AttemptSpec, emit execution.StreamSink) execution.StreamResult {
				attempts++
				status := 200
				body := "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n"
				now = start.Add(7300 * time.Millisecond)
				if attempts == 1 {
					now = start.Add(7 * time.Second)
					return execution.StreamResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, StatusCode: 401, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, StatusCode: 401, Hint: execution.FailureHintInvalidCredential, Summary: "invalid credential"}}
				}
				for _, event := range []execution.StreamEvent{
					{Sequence: 1, Kind: execution.StreamEventReady, StatusCode: status, Header: http.Header{"Content-Type": {"text/event-stream"}}},
					{Sequence: 2, Kind: execution.StreamEventData, Data: []byte(body)},
				} {
					if err := emit(event); err != nil {
						t.Fatal(err)
					}
				}
				if attempts > 1 {
					now = start.Add(9 * time.Second)
					if err := emit(execution.StreamEvent{Sequence: 3, Kind: execution.StreamEventData, Data: []byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":100,\"total_tokens\":101}}\n\ndata: [DONE]\n\n")}); err != nil {
						t.Fatal(err)
					}
				}
				return execution.StreamResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, StatusCode: status, Header: http.Header{"Content-Type": {"text/event-stream"}}}
			}
			engine, handler, _, _ := newRequestLogHandlerTestRuntime(t, NewExecutionForwarder(executor), limiter, sink, "sk-first", "sk-second")
			handler.requestNow = func() time.Time { return now }
			body := `{"model":"gpt-4o","stream":false}`
			if stream {
				body = `{"model":"gpt-4o","stream":true}`
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer gl-client")
			engine.ServeHTTP(httptest.NewRecorder(), request)
			events := sink.snapshot()
			if len(events) != 1 {
				t.Fatalf("logs = %d", len(events))
			}
			event := events[0]
			if event.DurationMs != 7000 {
				t.Fatalf("duration = %d, want 7000; event=%+v", event.DurationMs, event)
			}
			if stream && (attempts != 2 || event.FirstResponseMs == nil || *event.FirstResponseMs != 5300) {
				t.Fatalf("attempts=%d first=%v", attempts, event.FirstResponseMs)
			}
			if !stream && event.FirstResponseMs != nil {
				t.Fatal("unary response has streaming timing")
			}
		})
	}
}

func TestRequestDurationStopsBeforeLogAndQuotaCleanup(t *testing.T) {
	start := time.Unix(1000, 900_000_000)
	now := start.Add(200 * time.Millisecond)
	sink := &recordingRequestLogSink{}
	recorder := newRequestRecorder(sink, fixedRequestID, start, 1, protocol.OpenAIResponses, func() time.Time { return now })
	recorder.completeResponse(UpstreamResult{StatusCode: http.StatusOK}, health.Decision{}, "model", -1)
	now = now.Add(3 * time.Second)
	recorder.emit()
	if got := sink.snapshot()[0].DurationMs; got != 200 {
		t.Fatalf("duration = %d, want 200 ms including the fractional second boundary", got)
	}
}
