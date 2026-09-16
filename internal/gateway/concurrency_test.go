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
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"gpt-load/internal/channel"
	"gpt-load/internal/concurrency"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
)

func concurrencyTestInput(handler *Handler, count int) state.CompileInput {
	input := state.CompileInput{
		ChannelRegistry:     channel.NewRegistry(),
		SystemSettings:      config.Settings{state.SettingRetryCount: 0},
		Groups:              []state.GroupConfig{{ID: 1, Name: "openai", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true}},
		AccessKeys:          []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive}},
		ConcurrencyPolicies: map[concurrency.Subject]int64{},
	}
	for id := 1; id <= count; id++ {
		input.Credentials = append(input.Credentials, testCredentialConfig(uint(id), 1))
	}
	return input
}

func concurrencyRequest(engine http.Handler, ctx context.Context, stream bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(fmt.Sprintf(`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}],"stream":%t}`, stream))).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

type concurrencyBlockingForwarder struct{ entered chan struct{} }

func (f *concurrencyBlockingForwarder) Forward(ctx context.Context, _ ForwardInput) UpstreamResult {
	close(f.entered)
	<-ctx.Done()
	return UpstreamResult{Err: ctx.Err()}
}
func (f *concurrencyBlockingForwarder) ForwardStream(ctx context.Context, input ForwardInput, writer http.ResponseWriter) UpstreamResult {
	writer.Header().Set("Content-Type", "text/event-stream")
	_, _ = writer.Write([]byte("data: {}\n\n"))
	result := f.Forward(ctx, input)
	result.Committed = true
	return result
}

func TestConcurrencyHTTPAndStreamHoldSlotsUntilCancellation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, scope := range []concurrency.Subject{concurrency.Global, concurrency.AccessKey(1), concurrency.Group(1), concurrency.Credential(1)} {
			t.Run(fmt.Sprintf("%s/stream=%t", scope, stream), func(t *testing.T) {
				forwarder := &concurrencyBlockingForwarder{entered: make(chan struct{})}
				h, manager, _ := newHandlerForTest(t, forwarder, "upstream-key")
				input := concurrencyTestInput(h, 1)
				input.ConcurrencyPolicies[scope] = 1
				if _, err := manager.Publish(input); err != nil {
					t.Fatal(err)
				}
				engine := gin.New()
				bindGatewayRoutesForTest(t, engine, h)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan struct{})
				go func() { defer close(done); concurrencyRequest(engine, ctx, stream) }()
				select {
				case <-forwarder.entered:
				case <-time.After(2 * time.Second):
					t.Fatal("first request did not start")
				}
				response := concurrencyRequest(engine, t.Context(), stream)
				want := 503
				if scope == concurrency.AccessKey(1) {
					want = 429
				}
				if response.Code != want || !strings.Contains(response.Body.String(), "concurrency_limit") {
					t.Fatalf("rejection=%d %s", response.Code, response.Body.String())
				}
				subjects := []concurrency.Subject{concurrency.Global, concurrency.AccessKey(1), concurrency.Group(1), concurrency.Credential(1), concurrency.Upstream}
				for subject, count := range manager.ObserveConcurrency(subjects) {
					if count != 1 {
						t.Fatalf("in-flight %s=%d", subject, count)
					}
				}
				cancel()
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("canceled request did not finish")
				}
				for subject, count := range manager.ObserveConcurrency(subjects) {
					if count != 0 {
						t.Fatalf("leaked %s=%d", subject, count)
					}
				}
			})
		}
	}
}

func TestConcurrencyBusyCandidatesDoNotSpendRetryBudget(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{StatusCode: 200, Header: make(http.Header), Body: []byte(`{"ok":true}`)}}}
	h, manager, registry := newHandlerForTest(t, forwarder, "one", "two")
	input := concurrencyTestInput(h, 2)
	input.ConcurrencyPolicies[concurrency.Credential(1)] = 1
	input.ConcurrencyPolicies[concurrency.Credential(2)] = 1
	if _, err := manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	ref, _ := registry.CredentialRef(1)
	lease, admitted := manager.AdmitUpstreamConcurrency(ref)
	if !admitted {
		t.Fatal("cannot fill credential")
	}
	defer lease.Release()
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, h)
	response := concurrencyRequest(engine, t.Context(), false)
	if response.Code != 200 || len(forwarder.inputs) != 1 || forwarder.inputs[0].Credential.ID != 2 {
		t.Fatalf("status=%d attempts=%d body=%s", response.Code, len(forwarder.inputs), response.Body.String())
	}
}

func TestConcurrencyRetryUsesLatestLimitWithoutResettingCounters(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{StatusCode: 401, Header: make(http.Header), Body: []byte(`{"error":"invalid_api_key"}`), ClassificationBody: []byte(`{"error":"invalid_api_key"}`), RequestWritten: true}, {StatusCode: 200, Header: make(http.Header), Body: []byte(`{"ok":true}`)}}}
	h, manager, _ := newHandlerForTest(t, forwarder, "one", "two")
	input := concurrencyTestInput(h, 2)
	input.SystemSettings[state.SettingRetryCount] = 1
	input.ConcurrencyPolicies[concurrency.Group(1)] = 1
	if _, err := manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	forwarder.onCall = func(index int) {
		counts := manager.ObserveConcurrency([]concurrency.Subject{concurrency.Global, concurrency.AccessKey(1), concurrency.Group(1)})
		for subject, count := range counts {
			if count != 1 {
				t.Fatalf("attempt %d %s=%d", index, subject, count)
			}
		}
		if index == 0 {
			if _, err := manager.Publish(input); err != nil {
				t.Fatal(err)
			}
		}
	}
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, h)
	if response := concurrencyRequest(engine, t.Context(), false); response.Code != 200 || len(forwarder.inputs) != 2 {
		t.Fatalf("retry: %d %s", response.Code, response.Body.String())
	}
	for subject, count := range manager.ObserveConcurrency([]concurrency.Subject{concurrency.Global, concurrency.Group(1), concurrency.Credential(1), concurrency.Credential(2)}) {
		if count != 0 {
			t.Fatalf("leak %s=%d", subject, count)
		}
	}
}

func TestConcurrencyWebsocketCountsTurnsAndSharesHTTPBudget(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("blocked request reached upstream")
		w.WriteHeader(500)
	}))
	defer upstream.Close()
	h, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	input.ConcurrencyPolicies = map[concurrency.Subject]int64{concurrency.AccessKey(1): 1}
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	snapshot := h.manager.Current()
	lease, blocked, _ := h.manager.AdmitConcurrency(snapshot, snapshot.RequestConcurrencyLimits(1))
	if blocked != "" {
		t.Fatal(blocked)
	}
	defer lease.Release()
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	// An idle connection holds no request slot. Each turn uses the HTTP budget.
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, body, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("access_key_concurrency_limit")) {
		t.Fatalf("response=%s", body)
	}
	if got := h.manager.ObserveConcurrency([]concurrency.Subject{concurrency.AccessKey(1)})[concurrency.AccessKey(1)]; got != 1 {
		t.Fatalf("WS rejection leaked slot: %d", got)
	}
}

func TestConcurrencyWebsocketReleasesOnCompletionAndDisconnect(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(fmt.Sprintf("disconnect=%t", disconnect), func(t *testing.T) {
			h, engine, input := websocketTestHandler(t, "http://127.0.0.1:1/v1", channel.OpenAI)
			input.ConcurrencyPolicies = map[concurrency.Subject]int64{concurrency.Credential(1): 1}
			if _, err := h.manager.Publish(input); err != nil {
				t.Fatal(err)
			}
			entered, finish := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(finish) })
			sink := &recordingRequestLogSink{}
			h.requestLogSink = sink
			h.forwarder = websocketScriptForwarder{AttemptForwarder: h.forwarder, open: func(context.Context, ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
				session := &websocketScriptSession{done: make(chan struct{})}
				session.turn = func(ctx context.Context, body []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
					close(entered)
					select {
					case <-ctx.Done():
						return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled}}
					case <-finish:
						if err := emit(ctx, websocketCompleted("resp_done", "a")); err != nil {
							return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled}}
						}
						return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
					}
				}
				return session, execution.WebsocketResult{DispatchState: execution.DispatchNotSent}
			}}
			server := httptest.NewServer(engine)
			defer server.Close()
			conn := dialGatewayWebsocket(t, server.URL)
			defer conn.Close()
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","stream_id":"a","model":"public","input":"hello"}`))
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("turn did not start")
			}
			subjects := []concurrency.Subject{concurrency.Global, concurrency.AccessKey(1), concurrency.Group(1), concurrency.Credential(1), concurrency.Upstream}
			for subject, count := range h.manager.ObserveConcurrency(subjects) {
				if count != 1 {
					t.Fatalf("active %s=%d", subject, count)
				}
			}
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","stream_id":"b","model":"public","input":"busy"}`))
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, body, err := conn.ReadMessage()
			if err != nil || !bytes.Contains(body, []byte("upstream_concurrency_limit")) {
				t.Fatalf("second turn=%s %v", body, err)
			}
			if disconnect {
				_ = conn.Close()
			} else {
				once.Do(func() { close(finish) })
				_, body, err = conn.ReadMessage()
				if err != nil || !bytes.Contains(body, []byte("response.completed")) {
					t.Fatalf("completed=%s %v", body, err)
				}
			}
			waitWebsocketLogs(t, sink, 2)
			for subject, count := range h.manager.ObserveConcurrency(subjects) {
				if count != 0 {
					t.Fatalf("leaked %s=%d", subject, count)
				}
			}
		})
	}
}
