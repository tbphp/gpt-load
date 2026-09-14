package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
)

type websocketTestLogBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func TestWebsocketUnavailableBindingPreventsDispatch(t *testing.T) {
	h, _, _ := websocketTestHandler(t, "http://127.0.0.1:1", channel.CLIProxyAPI)
	ctx, cancel := context.WithCancel(context.Background())
	s := &websocketConnection{handler: h, ctx: ctx, cancel: cancel, accepting: true,
		registered: 1, binding: &websocketBinding{}, bind: make(chan struct{}, 1),
		bindingStopped: make(chan struct{}, 1)}
	s.bind <- struct{}{}
	defer func() { cancel(); s.workers.Wait() }()
	// 通知仍未消费时，已经登记的排队请求也不得派发。
	s.markBindingUnavailable(s.binding)
	s.bindingStopped <- struct{}{}
	if s.dispatchTurn(websocketTurn{}, make(chan websocketFinished, 1)) {
		t.Fatal("dispatched a queued turn after binding became unavailable")
	}
}

func TestWebsocketFrozenReaderPreservesFinalization(t *testing.T) {
	for _, mode := range []string{"binding stopped", "reconnect reserved", "client disconnected", "binding stopped binary", "reconnect reserved binary"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ready := make(chan *websocketConnection, 1)
			done := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				s := &websocketConnection{handler: &Handler{writeTimeout: time.Second}, conn: conn,
					ctx: ctx, cancel: cancel, accepting: true, registered: 1, binding: &websocketBinding{}}
				s.workers.Add(1)
				ready <- s
				s.readMessages(make(chan websocketTurn))
				close(done)
				<-ctx.Done()
			}))
			defer server.Close()
			defer cancel()
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			s := <-ready
			switch mode {
			case "binding stopped", "binding stopped binary":
				s.markBindingUnavailable(s.binding)
			case "reconnect reserved", "reconnect reserved binary":
				if !s.reserveReconnect() {
					t.Fatal("could not reserve reconnect")
				}
			case "client disconnected":
				conn.Close()
			}
			if mode != "client disconnected" {
				kind := websocket.TextMessage
				if strings.HasSuffix(mode, " binary") {
					kind = websocket.BinaryMessage
				}
				if err := conn.WriteMessage(kind, []byte(`{}`)); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("reader did not exit")
			}
			if mode == "client disconnected" {
				if ctx.Err() == nil {
					t.Fatal("real disconnect did not cancel")
				}
				return
			}
			if ctx.Err() != nil {
				t.Fatal("frozen reader canceled the finalizing turn")
			}
			s.closeWith(websocket.CloseTryAgainLater, "Reconnect to retry the request.")
			conn.SetReadDeadline(time.Now().Add(time.Second))
			if _, _, err := conn.ReadMessage(); !websocket.IsCloseError(err, websocket.CloseTryAgainLater) {
				t.Fatalf("close = %v, want 1013", err)
			}
		})
	}
}

func TestWebsocketReadFailuresStayRegisteredUntilCanceled(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      []byte
		closeCode int
		binary    bool
	}{
		{name: "binary message", body: []byte(`{}`), closeCode: websocket.CloseUnsupportedData, binary: true},
		{name: "invalid JSON", body: []byte(`{`), closeCode: websocket.CloseInvalidFramePayloadData},
		{name: "invalid stream ID", body: []byte(`{"stream_id":"invalid/id"}`), closeCode: websocket.ClosePolicyViolation},
		{name: "input limit", body: bytes.Repeat([]byte(" "), (32<<10)+1), closeCode: websocket.CloseTryAgainLater},
		{name: "read failure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stopping, release := make(chan struct{}), make(chan struct{})
			resume := sync.OnceFunc(func() { close(release) })
			ready, done := make(chan *websocketConnection, 1), make(chan struct{})
			h := &Handler{websocketLimits: defaultWebsocketLimits(), writeTimeout: time.Second, requestNow: time.Now}
			h.websocketLimits.input = 32 << 10
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				s := &websocketConnection{handler: h, conn: conn, ctx: ctx, accepting: true, registered: 1}
				// 保留一个活动请求，把读错误收尾停在取消前，检验并发重连门禁。
				s.cancel = sync.OnceFunc(func() { close(stopping); <-release; cancel() })
				s.workers.Add(1)
				ready <- s
				s.readMessages(make(chan websocketTurn))
				close(done)
			}))
			defer server.Close()
			defer resume()
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			s := <-ready
			if test.closeCode == 0 {
				// 声明两个字节的掩码文本帧，只发送一个字节，触发正文读取失败。
				if _, err := conn.UnderlyingConn().Write([]byte{0x81, 0x82, 1, 2, 3, 4, '{' ^ 1}); err != nil {
					t.Fatal(err)
				}
				conn.Close()
			} else {
				kind := websocket.TextMessage
				if test.binary {
					kind = websocket.BinaryMessage
				}
				if err := conn.WriteMessage(kind, test.body); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-stopping:
			case <-time.After(2 * time.Second):
				t.Fatal("read failure did not reach cancellation")
			}
			if s.reserveReconnect() {
				t.Error("allowed reconnect before the rejected turn finished closing/canceling")
			}
			resume()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("reader did not finish")
			}
			if s.registered != 1 || s.inputBytes != 0 || h.websocketBudget.input != 0 || ctx.Err() == nil {
				t.Fatalf("cleanup: registered=%d input=%d global_input=%d ctx=%v", s.registered, s.inputBytes, h.websocketBudget.input, ctx.Err())
			}
			if test.closeCode != 0 {
				conn.SetReadDeadline(time.Now().Add(time.Second))
				if _, _, err := conn.ReadMessage(); !websocket.IsCloseError(err, test.closeCode) {
					t.Fatalf("close=%v, want %d", err, test.closeCode)
				}
			}
		})
	}
}

func (buffer *websocketTestLogBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.Buffer.Write(value)
}

func (buffer *websocketTestLogBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.Buffer.String()
}

func waitWebsocketProcessLog(t *testing.T, buffer *websocketTestLogBuffer, fragment string) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		value := buffer.String()
		if strings.Contains(value, fragment) {
			return value
		}
		if time.Now().After(deadline) {
			t.Fatalf("process log missing %s: %s", fragment, value)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestWebsocketFirstRejectionRetry(t *testing.T) {
	for _, tc := range []struct {
		name                                   string
		retries, credentials, wantAttempts     int
		partial, unknown, exhausted, multiplex bool
		host, invalid                          bool
	}{
		{name: "quota switches credential", retries: 2, credentials: 2, wantAttempts: 2},
		{name: "host error skips group", retries: 2, credentials: 2, wantAttempts: 2, host: true},
		{name: "explicit request rejection stops", retries: 2, credentials: 2, wantAttempts: 1, invalid: true},
		{name: "zero budget preserves error", credentials: 2, wantAttempts: 1},
		{name: "no candidate preserves error", retries: 2, credentials: 1, wantAttempts: 1},
		{name: "exhausted budget preserves last error", retries: 1, credentials: 2, wantAttempts: 2, exhausted: true},
		{name: "output already committed", retries: 2, credentials: 2, wantAttempts: 1, partial: true},
		{name: "unknown replay safety", retries: 2, credentials: 2, wantAttempts: 1, unknown: true},
		{name: "multiplex stays bound", retries: 2, credentials: 2, wantAttempts: 1, multiplex: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := channel.CLIProxyAPI
			if tc.multiplex {
				id = channel.OpenAI
			}
			h, engine, input := websocketTestHandler(t, "http://127.0.0.1:1", id)
			input.SystemSettings = config.Settings{state.SettingRetryCount: tc.retries}
			registry := h.registry.(*state.CredentialRegistry)
			entries := []state.CredentialEntry{testCredentialEntry(t, h.encryption, 1, 1, "first-key")}
			if tc.credentials == 2 {
				input.Credentials = append(input.Credentials, testCredentialConfig(2, 1))
				entries = append(entries, testCredentialEntry(t, h.encryption, 2, 1, "second-key"))
			}
			if tc.host {
				backup := input.Groups[0]
				backup.ID, backup.Name = 2, "backup"
				input.Groups = append(input.Groups, backup)
				input.Credentials = append(input.Credentials, testCredentialConfig(3, 2))
				entries = append(entries, testCredentialEntry(t, h.encryption, 3, 2, "third-key"))
			}
			if _, err := h.manager.Publish(input); err != nil {
				t.Fatal(err)
			}
			if err := registry.ReplaceCredentials(entries); err != nil {
				t.Fatal(err)
			}
			sink := &recordingRequestLogSink{}
			h.requestLogSink = sink
			var attempted []uint
			h.forwarder = websocketScriptForwarder{AttemptForwarder: h.forwarder, open: func(_ context.Context, in ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
				session := &websocketScriptSession{done: make(chan struct{})}
				turns := 0
				session.turn = func(ctx context.Context, _ []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
					turns++
					attempted = append(attempted, in.Credential.ID)
					if in.Credential.ID != 1 && !tc.exhausted {
						_ = emit(ctx, websocketCompleted("resp_recovered", ""))
						return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
					}
					if tc.partial {
						_ = emit(ctx, []byte(`{"type":"response.created","response":{"id":"resp_partial","object":"response","status":"in_progress","model":"upstream"}}`))
					}
					status, errorType := 429, "usage_limit_reached"
					if tc.host {
						status, errorType = 503, "server_error"
					}
					if tc.invalid {
						status, errorType = 400, "invalid_parameter"
					}
					payload := []byte(fmt.Sprintf(`{"type":"error","status":%d,"error":{"type":%q,"message":"quota-%d %s","resets_in_seconds":60}}`, status, errorType, in.Credential.ID, in.APIKey))
					if err := emit(ctx, payload); err != nil {
						return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled}}
					}
					safety := execution.ReplaySafety("")
					if tc.unknown {
						safety = execution.ReplaySafetyUnknown
					}
					return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, StatusCode: status, Code: "upstream_error", OriginHint: execution.ErrorOriginUpstream, ReplaySafety: safety, Summary: "upstream rejected request"}}
				}
				return session, execution.WebsocketResult{DispatchState: execution.DispatchNotSent}
			}}
			server := httptest.NewServer(engine)
			defer server.Close()
			conn := dialGatewayWebsocket(t, server.URL)
			defer conn.Close()
			request := map[string]any{"type": "response.create", "model": "public", "input": "hello", "store": false}
			if err := conn.WriteJSON(request); err != nil {
				t.Fatal(err)
			}
			_, body, err := conn.ReadMessage()
			if err != nil {
				t.Fatal(err)
			}
			if tc.partial {
				if !strings.Contains(string(body), "response.created") {
					t.Fatalf("missing prior output: %s", body)
				}
				_, body, err = conn.ReadMessage()
				if err != nil {
					t.Fatal(err)
				}
			}
			events := waitWebsocketLogs(t, sink, 1)
			event := events[0]
			if len(attempted) != tc.wantAttempts || len(event.Attempts) != tc.wantAttempts {
				t.Fatalf("attempted=%v logs=%+v response=%s", attempted, event.Attempts, body)
			}
			if registry.ModelCooldowns(1, time.Now())["upstream"].After(time.Now()) != (!tc.host && !tc.invalid) {
				t.Fatal("unexpected model cooldown")
			}
			if tc.host && attempted[1] != 3 {
				t.Fatalf("failed group was not skipped: %v", attempted)
			}
			if until, _ := registry.CredentialCooldownUntil(1); !until.IsZero() {
				t.Fatal("entire credential cooled")
			}
			var response map[string]any
			if err := json.Unmarshal(body, &response); err != nil {
				t.Fatal(err)
			}
			recovered := tc.wantAttempts == 2 && !tc.exhausted
			if recovered {
				if response["type"] != "response.completed" || !strings.Contains(string(body), "resp_recovered") {
					t.Fatalf("error leaked instead of recovery: %s", body)
				}
				if !event.Attempts[0].WillRetry || event.Attempts[0].Committed || attempted[0] == attempted[1] {
					t.Fatalf("retry accounting=%+v", event.Attempts)
				}
			} else {
				if response["type"] != "error" || !strings.Contains(string(body), fmt.Sprintf("quota-%d", attempted[len(attempted)-1])) || strings.Contains(string(body), "first-key") {
					t.Fatalf("original sanitized error lost: %s", body)
				}
			}
		})
	}
}

func TestWebsocketBoundRetryableErrorRequestsClientReconnect(t *testing.T) {
	for _, test := range []struct {
		name       string
		payload    []byte
		evidence   execution.ErrorEvidence
		wantEffect string
	}{
		{name: "error event", payload: []byte(`{"type":"error","status":429,"error":{"type":"usage_limit_reached","message":"quota exhausted"}}`),
			evidence: execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, StatusCode: http.StatusTooManyRequests, Code: "upstream_error", OriginHint: execution.ErrorOriginUpstream}, wantEffect: "cooldown_model"},
		{name: "failed response", payload: []byte(`{"type":"response.failed","response":{"id":"resp_failed","object":"response","status":"failed","model":"upstream","error":{"type":"usage_limit_reached","code":"usage_limit_reached","message":"quota exhausted"}}}`),
			evidence: execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, StatusCode: http.StatusTooManyRequests, Code: "upstream_error", OriginHint: execution.ErrorOriginUpstream}, wantEffect: "cooldown_model"},
		{name: "unknown provider error", payload: []byte(`{"type":"error","error":{"type":"new_provider_error","message":"temporary upstream error"}}`),
			evidence: execution.ErrorEvidence{Kind: execution.ErrorKindProvider, Code: "new_provider_error", OriginHint: execution.ErrorOriginUpstream}, wantEffect: "none"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testWebsocketBoundRetryableErrorRequestsClientReconnect(t, test.payload, test.evidence, test.wantEffect)
		})
	}
}

func testWebsocketBoundRetryableErrorRequestsClientReconnect(t *testing.T, payload []byte, evidence execution.ErrorEvidence, wantEffect string) {
	h, engine, input := websocketTestHandler(t, "http://127.0.0.1:1", channel.CLIProxyAPI)
	input.SystemSettings = config.Settings{state.SettingRetryCount: 0}
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	var processLogs websocketTestLogBuffer
	h.logger = logrus.New()
	h.logger.SetOutput(&processLogs)
	h.logger.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	var turns int
	open := func(_ context.Context, _ ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
		session := &websocketScriptSession{done: make(chan struct{})}
		session.turn = func(ctx context.Context, _ []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
			turns++
			if turns == 1 {
				if err := emit(ctx, websocketCompleted("resp_prior", "")); err != nil {
					return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent,
						Error: &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled}}
				}
			}
			if err := emit(ctx, payload); err != nil {
				return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled}}
			}
			_ = session.Close()
			copy := evidence.Clone()
			copy.Summary = "upstream rejected request"
			return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &copy}
		}
		return session, execution.WebsocketResult{DispatchState: execution.DispatchNotSent}
	}
	h.forwarder = websocketScriptForwarder{AttemptForwarder: h.forwarder, open: open}
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "one", "store": false}); err != nil {
		t.Fatal(err)
	}
	if _, body, err := conn.ReadMessage(); err != nil || !strings.Contains(string(body), "resp_prior") {
		t.Fatalf("first turn body=%s err=%v", body, err)
	}
	waitWebsocketLogs(t, sink, 1)
	if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "two", "previous_response_id": "resp_prior", "store": false}); err != nil {
		t.Fatal(err)
	}
	if _, body, err := conn.ReadMessage(); !websocket.IsCloseError(err, websocket.CloseTryAgainLater) || !strings.Contains(err.Error(), "Reconnect to retry the request.") {
		t.Fatalf("second turn body=%s err=%v, want close 1013", body, err)
	}
	events := sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("request log was not submitted before close: events=%d", len(events))
	}
	attempts := events[1].Attempts
	if len(attempts) != 1 || attempts[0].RetryDirective != "next_candidate" ||
		string(attempts[0].Effect) != wantEffect || attempts[0].WillRetry || attempts[0].Committed {
		t.Fatalf("second turn attempts=%+v", attempts)
	}
	logged := waitWebsocketProcessLog(t, &processLogs, `"event":"ws_reconnect_requested"`)
	for _, field := range []string{`"event":"ws_reconnect_requested"`, `"close_code":1013`,
		`"retry_directive":"next_candidate"`, `"write_result":"sent"`} {
		if !strings.Contains(logged, field) {
			t.Fatalf("reconnect log missing %s: %s", field, logged)
		}
	}
}

func TestWebsocketBoundErrorKeepsConnectionWhenAnotherTurnIsRegistered(t *testing.T) {
	h, engine, _ := websocketTestHandler(t, "http://127.0.0.1:1", channel.CLIProxyAPI)
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	var processLogs websocketTestLogBuffer
	h.logger = logrus.New()
	h.logger.SetOutput(&processLogs)
	h.logger.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	secondStarted := make(chan struct{})
	releaseSecond := make(chan struct{})
	var turns int
	open := func(_ context.Context, _ ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
		session := &websocketScriptSession{done: make(chan struct{})}
		session.turn = func(ctx context.Context, _ []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
			turns++
			if turns == 1 {
				_ = emit(ctx, websocketCompleted("resp_prior", ""))
				return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
			}
			if turns == 2 {
				close(secondStarted)
				<-releaseSecond
				payload := []byte(`{"type":"error","status":429,"error":{"type":"usage_limit_reached","message":"quota exhausted"}}`)
				if err := emit(ctx, payload); err != nil {
					return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled}}
				}
				return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &execution.ErrorEvidence{
					Kind: execution.ErrorKindHTTP, StatusCode: http.StatusTooManyRequests,
					Code: "upstream_error", OriginHint: execution.ErrorOriginUpstream,
				}}
			}
			_ = emit(ctx, websocketCompleted("resp_after_error", ""))
			return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
		}
		return session, execution.WebsocketResult{DispatchState: execution.DispatchNotSent}
	}
	h.forwarder = websocketScriptForwarder{AttemptForwarder: h.forwarder, open: open}
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	first := []byte(`{"type":"response.create","model":"public","input":"one","store":false}`)
	second := []byte(`{"type":"response.create","model":"public","input":"two","previous_response_id":"resp_prior","store":false}`)
	third := []byte(`{"type":"response.create","model":"public","input":"three","previous_response_id":"resp_prior","store":false}`)
	if err := conn.WriteMessage(websocket.TextMessage, first); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	waitWebsocketLogs(t, sink, 1)
	if err := conn.WriteMessage(websocket.TextMessage, second); err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("second turn did not start")
	}
	if err := conn.WriteMessage(websocket.TextMessage, third); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		h.websocketBudget.mu.Lock()
		inputBytes := h.websocketBudget.input
		h.websocketBudget.mu.Unlock()
		if inputBytes >= len(second)+len(third) {
			break
		}
		if time.Now().After(deadline) {
			close(releaseSecond)
			t.Fatalf("third turn was not registered: input_bytes=%d", inputBytes)
		}
		time.Sleep(time.Millisecond)
	}
	close(releaseSecond)
	if _, body, err := conn.ReadMessage(); err != nil || !strings.Contains(string(body), "quota exhausted") {
		t.Fatalf("bound error body=%s err=%v", body, err)
	}
	if strings.Contains(processLogs.String(), `"event":"ws_reconnect_requested"`) {
		t.Fatalf("reconnect requested with another registered turn: %s", processLogs.String())
	}
}

type websocketStreamLimitLogSink struct {
	recordingRequestLogSink
	blocked chan struct{}
	release chan struct{}
}

func (sink *websocketStreamLimitLogSink) Emit(event telemetry.RequestEvent) {
	if event.ErrorCode == "websocket_stream_limit_reached" {
		close(sink.blocked)
		<-sink.release
	}
	sink.recordingRequestLogSink.Emit(event)
}

func TestWebsocketStreamLimitRejectionStaysRegisteredUntilFinalized(t *testing.T) {
	h, engine, _ := websocketTestHandler(t, "http://127.0.0.1:1", channel.OpenAI)
	h.websocketLimits.lanes = 1
	sink := &websocketStreamLimitLogSink{blocked: make(chan struct{}), release: make(chan struct{})}
	h.requestLogSink = sink
	releaseLog := sync.OnceFunc(func() { close(sink.release) })
	secondStarted, releaseError := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	h.forwarder = websocketScriptForwarder{AttemptForwarder: h.forwarder, open: func(context.Context, ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
		session := &websocketScriptSession{done: make(chan struct{})}
		session.turn = func(ctx context.Context, _ []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
			result := execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
			if calls.Add(1) == 1 {
				if err := emit(ctx, websocketCompleted("resp_prior", "active")); err != nil {
					result.Error = &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled}
				}
				return result
			}
			close(secondStarted)
			select {
			case <-releaseError:
			case <-ctx.Done():
				result.Error = &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled}
				return result
			}
			payload := []byte(`{"type":"error","stream_id":"active","status":429,"error":{"type":"usage_limit_reached","message":"quota exhausted"}}`)
			if err := emit(ctx, payload); err != nil {
				result.Error = &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled}
			} else {
				result.Error = &execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, StatusCode: http.StatusTooManyRequests,
					Code: "upstream_error", OriginHint: execution.ErrorOriginUpstream}
			}
			return result
		}
		return session, execution.WebsocketResult{DispatchState: execution.DispatchNotSent}
	}}
	server := httptest.NewServer(engine)
	defer server.Close()
	defer releaseLog()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	send := func(lane string) {
		t.Helper()
		if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "stream_id": lane, "input": "hello", "store": false}); err != nil {
			t.Fatal(err)
		}
	}
	send("active")
	if _, body, err := conn.ReadMessage(); err != nil || !strings.Contains(string(body), "resp_prior") {
		t.Fatalf("first turn body=%s err=%v", body, err)
	}
	waitWebsocketLogs(t, &sink.recordingRequestLogSink, 1)
	send("active")
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second turn did not start")
	}
	send("rejected")
	select {
	case <-sink.blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("stream-limit rejection did not reach finalization")
	}
	// 将超限请求停在日志提交处，再让活动请求遇到可重试错误。
	close(releaseError)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, body, err := conn.ReadMessage(); err != nil || !strings.Contains(string(body), "quota exhausted") {
		t.Fatalf("active error body=%s err=%v; must not reconnect during another turn's finalization", body, err)
	}
	releaseLog()
	if _, body, err := conn.ReadMessage(); err != nil || !strings.Contains(string(body), "websocket_stream_limit_reached") || !strings.Contains(string(body), `"stream_id":"rejected"`) {
		t.Fatalf("stream-limit response body=%s err=%v", body, err)
	}
	if logs := waitWebsocketLogs(t, &sink.recordingRequestLogSink, 3); len(logs) != 3 || calls.Load() != 2 {
		t.Fatalf("logs=%d upstream calls=%d, want 3 logs and 2 calls", len(logs), calls.Load())
	}
}

func TestWebsocketBoundReconnectPreservesErrorBoundaries(t *testing.T) {
	for _, test := range []struct {
		name         string
		beforeError  []byte
		errorPayload []byte
		evidence     execution.ErrorEvidence
	}{
		{
			name:         "explicit request rejection",
			errorPayload: []byte(`{"type":"error","status":400,"error":{"type":"invalid_parameter","message":"invalid input"}}`),
			evidence: execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, StatusCode: http.StatusBadRequest,
				Code: "upstream_error", OriginHint: execution.ErrorOriginUpstream},
		},
		{
			name:         "unknown replay safety",
			errorPayload: []byte(`{"type":"error","status":429,"error":{"type":"usage_limit_reached","message":"quota exhausted"}}`),
			evidence: execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, StatusCode: http.StatusTooManyRequests,
				Code: "upstream_error", OriginHint: execution.ErrorOriginUpstream, ReplaySafety: execution.ReplaySafetyUnknown},
		},
		{
			name:         "output already committed",
			beforeError:  []byte(`{"type":"response.created","response":{"id":"resp_partial","object":"response","status":"in_progress","model":"upstream"}}`),
			errorPayload: []byte(`{"type":"error","status":429,"error":{"type":"usage_limit_reached","message":"quota exhausted"}}`),
			evidence: execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, StatusCode: http.StatusTooManyRequests,
				Code: "upstream_error", OriginHint: execution.ErrorOriginUpstream},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h, engine, _ := websocketTestHandler(t, "http://127.0.0.1:1", channel.CLIProxyAPI)
			var processLogs websocketTestLogBuffer
			h.logger = logrus.New()
			h.logger.SetOutput(&processLogs)
			h.logger.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
			turns := 0
			open := func(_ context.Context, _ ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
				session := &websocketScriptSession{done: make(chan struct{})}
				session.turn = func(ctx context.Context, _ []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
					turns++
					if turns == 1 {
						_ = emit(ctx, websocketCompleted("resp_prior", ""))
						return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
					}
					if len(test.beforeError) > 0 {
						_ = emit(ctx, test.beforeError)
					}
					if err := emit(ctx, test.errorPayload); err != nil {
						return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled}}
					}
					evidence := test.evidence.Clone()
					return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &evidence}
				}
				return session, execution.WebsocketResult{DispatchState: execution.DispatchNotSent}
			}
			h.forwarder = websocketScriptForwarder{AttemptForwarder: h.forwarder, open: open}
			server := httptest.NewServer(engine)
			defer server.Close()
			conn := dialGatewayWebsocket(t, server.URL)
			request := map[string]any{"type": "response.create", "model": "public", "input": "one", "store": false}
			if err := conn.WriteJSON(request); err != nil {
				t.Fatal(err)
			}
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Fatal(err)
			}
			request["input"], request["previous_response_id"] = "two", "resp_prior"
			if err := conn.WriteJSON(request); err != nil {
				t.Fatal(err)
			}
			if len(test.beforeError) > 0 {
				if _, body, err := conn.ReadMessage(); err != nil || !strings.Contains(string(body), "response.created") {
					t.Fatalf("committed event body=%s err=%v", body, err)
				}
			}
			if _, body, err := conn.ReadMessage(); err != nil || !strings.Contains(string(body), `"type":"error"`) {
				t.Fatalf("original error body=%s err=%v", body, err)
			}
			if strings.Contains(processLogs.String(), `"event":"ws_reconnect_requested"`) {
				t.Fatalf("unsafe reconnect requested: %s", processLogs.String())
			}
		})
	}
}

func TestWebsocketNativeFirstQuotaErrorRetries(t *testing.T) {
	var mu sync.Mutex
	var attempts []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		key := r.Header.Get("Authorization")
		mu.Lock()
		attempts = append(attempts, key)
		mu.Unlock()
		body := websocketCompleted("resp_recovered", "")
		if key == "Bearer first-key" {
			body = []byte(`{"type":"error","status":429,"error":{"type":"usage_limit_reached","message":"quota exhausted"}}`)
		}
		_ = conn.WriteMessage(websocket.TextMessage, body)
		_, _, _ = conn.ReadMessage()
	}))
	defer upstream.Close()
	h, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.CLIProxyAPI)
	input.Credentials = append(input.Credentials, testCredentialConfig(2, 1))
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	registry := h.registry.(*state.CredentialRegistry)
	if err := registry.ReplaceCredentials([]state.CredentialEntry{testCredentialEntry(t, h.encryption, 1, 1, "first-key"), testCredentialEntry(t, h.encryption, 2, 1, "second-key")}); err != nil {
		t.Fatal(err)
	}
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": "hello", "store": false}); err != nil {
		t.Fatal(err)
	}
	_, body, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	logs := waitWebsocketLogs(t, sink, 1)
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(attempts, ",") != "Bearer first-key,Bearer second-key" || !strings.Contains(string(body), "resp_recovered") {
		t.Fatalf("attempts=%v response=%s", attempts, body)
	}
	if len(logs[0].Attempts) != 2 || logs[0].Attempts[0].Committed || !logs[0].Attempts[0].WillRetry {
		t.Fatalf("logs=%+v", logs)
	}
}

func TestWebsocketNativeBoundQuotaErrorRequestsClientReconnect(t *testing.T) {
	var turns atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			n := turns.Add(1)
			body := websocketCompleted("resp_prior", "")
			if n == 2 {
				body = []byte(`{"type":"error","status":429,"error":{"type":"usage_limit_reached","message":"quota exhausted"}}`)
			}
			if err := conn.WriteMessage(websocket.TextMessage, body); err != nil || n == 2 {
				return
			}
		}
	}))
	defer upstream.Close()
	h, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.CLIProxyAPI)
	input.SystemSettings = config.Settings{state.SettingRetryCount: 0}
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	request := map[string]any{"type": "response.create", "model": "public", "input": "one", "store": false}
	if err := conn.WriteJSON(request); err != nil {
		t.Fatal(err)
	}
	if _, body, err := conn.ReadMessage(); err != nil || !strings.Contains(string(body), "resp_prior") {
		t.Fatalf("first turn body=%s err=%v", body, err)
	}
	waitWebsocketLogs(t, sink, 1)
	request["input"], request["previous_response_id"] = "two", "resp_prior"
	if err := conn.WriteJSON(request); err != nil {
		t.Fatal(err)
	}
	if _, body, err := conn.ReadMessage(); !websocket.IsCloseError(err, websocket.CloseTryAgainLater) || !strings.Contains(err.Error(), "Reconnect to retry the request.") {
		t.Fatalf("bound quota body=%s err=%v, want close 1013", body, err)
	}
	events := sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("request log was not submitted before close: events=%d", len(events))
	}
	attempts := events[1].Attempts
	if turns.Load() != 2 || len(attempts) != 1 || attempts[0].RetryDirective != "next_candidate" ||
		attempts[0].Effect != "cooldown_model" || attempts[0].WillRetry || attempts[0].Committed {
		t.Fatalf("turns=%d attempts=%+v", turns.Load(), attempts)
	}
}
