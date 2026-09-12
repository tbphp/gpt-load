package embedded

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

func wsTestSession(t *testing.T, target string, proxyURLs ...string) *CodexWSSession {
	t.Helper()
	proxyURL := "direct"
	if len(proxyURLs) != 0 {
		proxyURL = proxyURLs[0]
	}
	session, err := NewCodexWSSession(CodexWSSessionOptions{
		CredentialID: "ws-test", Credential: CodexCredential{
			Type: ProviderCodex, AccessToken: "test-access", RefreshToken: "test-refresh", AccountID: "test-account",
		}, ProxyURL: proxyURL, TurnTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 生产入口保持 HTTPS 校验；测试仅将已固定的执行地址指向本地假上游。
	session.auth.Attributes["base_url"] = target
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	return session
}

func wsCompleted(id string) []byte {
	return []byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"object":"response","status":"completed","store":false,"output":[],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}}`, id))
}

func TestCodexWSSessionUsesEachTurnDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		for turn := 0; turn < 2; turn++ {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			if turn == 1 {
				time.Sleep(100 * time.Millisecond)
			}
			if err := conn.WriteMessage(websocket.TextMessage, wsCompleted(fmt.Sprintf("resp_%d", turn))); err != nil {
				return
			}
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	session := wsTestSession(t, server.URL)
	session.options.TurnTimeout = 25 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := session.ExecuteTurn(ctx, json.RawMessage(`{"model":"gpt-5","input":"first"}`), nil); err != nil {
		t.Fatal(err)
	}
	result, err := session.ExecuteTurn(ctx, json.RawMessage(`{"model":"gpt-5","previous_response_id":"resp_0","input":"next"}`), nil)
	if err != nil || result.ResponseID != "resp_1" {
		t.Fatalf("per-turn deadline was capped by session default: result=%+v err=%v", result, err)
	}
}

func TestCodexWSSessionForwardsPreparedHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Codex-Turn-State") != "state-fixture" {
			t.Error("prepared turn header missing")
		}
		if r.Header.Get("Authorization") != "Bearer test-access" {
			t.Error("prepared header replaced credential")
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err == nil {
			_ = conn.WriteMessage(websocket.TextMessage, wsCompleted("resp_headers"))
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	session := wsTestSession(t, server.URL)
	session.options.Headers = http.Header{"X-Codex-Turn-State": {"state-fixture"}, "Authorization": {"Bearer untrusted"}}
	if _, err := session.ExecuteTurn(t.Context(), json.RawMessage(`{"model":"gpt-5","input":"hello"}`), nil); err != nil {
		t.Fatal(err)
	}
}

func TestCodexWSSessionDatesHandshakeHeadersBeforeGeneration(t *testing.T) {
	generated := make(chan time.Time, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, http.Header{"X-Codex-Primary-Reset-After-Seconds": {"60"}})
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
		generated <- time.Now()
		_ = conn.WriteMessage(websocket.TextMessage, wsCompleted("resp_headers_time"))
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	session := wsTestSession(t, server.URL)
	result, err := session.ExecuteTurn(t.Context(), json.RawMessage(`{"model":"gpt-5","input":"hello"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.HeaderObservedAt.IsZero() || !result.HeaderObservedAt.Before(<-generated) {
		t.Fatal("handshake headers were dated at generation completion")
	}
}

func TestCodexWSSessionPrewarmUsesRealUpstreamState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for turn := 0; turn < 2; turn++ {
			var request map[string]any
			if conn.ReadJSON(&request) != nil {
				return
			}
			if turn == 0 && request["generate"] != false {
				t.Error("prewarm became a generation request")
			}
			if turn == 1 && request["previous_response_id"] != "resp_0" {
				t.Error("prewarm state was not continued")
			}
			if conn.WriteMessage(websocket.TextMessage, wsCompleted(fmt.Sprintf("resp_%d", turn))) != nil {
				return
			}
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	session := wsTestSession(t, server.URL)
	first, err := session.ExecuteTurn(t.Context(), json.RawMessage(`{"model":"gpt-5","input":"warm","generate":false}`), nil)
	if err != nil || first.ResponseID != "resp_0" {
		t.Fatalf("prewarm result=%+v err=%v", first, err)
	}
	if _, err = session.ExecuteTurn(t.Context(), json.RawMessage(`{"model":"gpt-5","input":"continue","previous_response_id":"resp_0"}`), nil); err != nil {
		t.Fatal(err)
	}
}

func TestCodexWSSessionContinuationAndIsolation(t *testing.T) {
	var connections, turns atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer test-access" {
			t.Error("unexpected request identity or path")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, http.Header{"X-Codex-Test": {"handshake"}})
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		n := connections.Add(1)
		previous := ""
		for {
			_, body, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var request struct {
				Type     string `json:"type"`
				Previous string `json:"previous_response_id"`
				Store    bool   `json:"store"`
			}
			if err := json.Unmarshal(body, &request); err != nil {
				t.Error(err)
				return
			}
			if request.Type != "response.create" || request.Previous != previous || request.Store {
				t.Error("request lost continuation or stateless semantics")
				return
			}
			previous = fmt.Sprintf("resp_%d_%d", n, turns.Add(1))
			if err := conn.WriteMessage(websocket.TextMessage, wsCompleted(previous)); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	session := wsTestSession(t, server.URL)
	events := 0
	headerCalls := 0
	var headerAt time.Time
	session.options.ObserveHeaders = func(headers http.Header, observedAt time.Time) {
		if events != 0 || observedAt.IsZero() || headers.Get("X-Codex-Test") != "handshake" {
			t.Error("handshake was not delivered before events with its original time")
		}
		headerCalls++
		headerAt = observedAt
		headers.Set("X-Codex-Test", "caller mutation")
	}
	consume := func(ctx context.Context, event json.RawMessage) error {
		if headerCalls != 1 {
			t.Error("event overtook handshake delivery")
		}
		if !json.Valid(event) {
			t.Error("event is not native JSON")
		}
		events++
		return nil
	}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	first, err := session.ExecuteTurn(firstCtx, json.RawMessage(`{"model":"gpt-5","input":"hello","store":false}`), consume)
	if err != nil {
		t.Fatal(err)
	}
	if first.ResponseID == "" || first.Status != "completed" || !json.Valid(first.Usage) {
		t.Fatalf("missing terminal result: %+v", first)
	}
	cancelFirst() // 成功请求的 context 结束不应关闭已空闲的会话。
	second, err := session.ExecuteTurn(context.Background(), json.RawMessage(fmt.Sprintf(`{"model":"gpt-5","input":"continue","previous_response_id":%q}`, first.ResponseID)), consume)
	if err != nil {
		t.Fatal(err)
	}
	if second.ResponseID == first.ResponseID || events != 2 || connections.Load() != 1 {
		t.Fatal("turns did not reuse one connection")
	}
	if first.Headers.Get("X-Codex-Test") != "handshake" || !first.HeaderObservedAt.Equal(headerAt) ||
		len(second.Headers) != 0 || !second.HeaderObservedAt.IsZero() || headerCalls != 1 {
		t.Fatal("handshake headers were lost or reused as fresh observation")
	}
	other := wsTestSession(t, server.URL)
	if _, err := other.ExecuteTurn(context.Background(), json.RawMessage(`{"model":"gpt-5","input":"another"}`), consume); err != nil {
		t.Fatal(err)
	}
	if connections.Load() != 2 {
		t.Fatal("independent sessions shared a connection")
	}
}

func TestCodexWSSessionDoesNotFallbackHTTP(t *testing.T) {
	var upgrades, posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			upgrades.Add(1)
		} else {
			posts.Add(1)
		}
		w.WriteHeader(http.StatusUpgradeRequired)
	}))
	defer server.Close()
	session := wsTestSession(t, server.URL)
	_, err := session.ExecuteTurn(context.Background(), json.RawMessage(`{"model":"gpt-5","input":"hello"}`), nil)
	if err == nil {
		t.Fatal("expected handshake rejection")
	}
	if upgrades.Load() != 1 || posts.Load() != 0 {
		t.Fatalf("upgrades=%d posts=%d", upgrades.Load(), posts.Load())
	}
}

func TestCodexWSSessionPreservesUpstreamFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","status":429,"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"test-access"}}`)); err != nil {
			t.Error(err)
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	session := wsTestSession(t, server.URL)
	var received bool
	result, err := session.ExecuteTurn(context.Background(), json.RawMessage(`{"model":"gpt-5","input":"hello"}`), func(ctx context.Context, event json.RawMessage) error {
		received = strings.Contains(string(event), `"type":"error"`)
		return nil
	})
	var failure *CodexWSError
	if !errors.As(err, &failure) || failure.Code != "upstream_error" || failure.HTTPStatus != 429 || failure.UpstreamCode != "rate_limit_exceeded" {
		t.Fatalf("upstream failure was lost: %v (%+v)", err, failure)
	}
	if !received || result.DispatchState != CodexWSMaybeSent || strings.Contains(err.Error(), "test-access") {
		t.Fatal("missing event/dispatch evidence or unsafe error text")
	}
	_, err = session.ExecuteTurn(context.Background(), json.RawMessage(`{"model":"gpt-5","input":"again"}`), nil)
	if !errors.As(err, &failure) || failure.Code != "session_closed" {
		t.Fatalf("failed session reused: %v", err)
	}
}

func TestCodexWSSessionCancellationAndBusy(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout", "close"} {
		t.Run(mode, func(t *testing.T) {
			received, disconnected := make(chan struct{}), make(chan struct{})
			var turns atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
				turns.Add(1)
				close(received)
				_, _, _ = conn.ReadMessage()
				close(disconnected)
			}))
			defer server.Close()
			session := wsTestSession(t, server.URL)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "timeout" {
				session.options.TurnTimeout = 200 * time.Millisecond
			}
			finished := make(chan error, 1)
			go func() {
				_, err := session.ExecuteTurn(ctx, json.RawMessage(`{"model":"gpt-5","input":"hello"}`), nil)
				finished <- err
			}()
			select {
			case <-received:
			case <-time.After(2 * time.Second):
				t.Fatal("first turn not received")
			}
			_, err := session.ExecuteTurn(context.Background(), json.RawMessage(`{"model":"gpt-5","input":"overlap"}`), nil)
			var failure *CodexWSError
			if !errors.As(err, &failure) || failure.Code != "session_busy" {
				t.Fatalf("overlap was not rejected: %v", err)
			}
			switch mode {
			case "cancel":
				cancel()
			case "close":
				if err := session.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-finished:
				want := context.Canceled
				if mode == "timeout" {
					want = context.DeadlineExceeded
				}
				if !errors.Is(err, want) {
					t.Fatalf("execution error=%v, want %v", err, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cancellation did not release execution")
			}
			select {
			case <-disconnected:
			case <-time.After(time.Second):
				t.Fatal("upstream connection leaked")
			}
			if turns.Load() != 1 {
				t.Fatal("canceled request was replayed")
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCodexWSSessionValidationKeepsSession(t *testing.T) {
	session := wsTestSession(t, "http://127.0.0.1:1")
	for _, payload := range []string{
		`{}`, `null`, `{"model":"gpt-5","previous_response_id":12}`,
		`{"model":"gpt-5","previous_response_id":"foreign"}`,
		`{"model":"gpt-5","stream_id":"other"}`, `{"model":"gpt-5","background":true}`,
	} {
		result, err := session.ExecuteTurn(context.Background(), json.RawMessage(payload), nil)
		if err == nil || result.DispatchState != CodexWSNotSent {
			t.Fatalf("invalid request accepted: %s", payload)
		}
		if session.closed || session.bound || session.running {
			t.Fatal("local rejection changed session state")
		}
	}
	session.options.MaxRequestBytes = 2
	if result, err := session.ExecuteTurn(context.Background(), json.RawMessage(`{"model":"gpt-5"}`), nil); err == nil || result.DispatchState != CodexWSNotSent {
		t.Fatal("oversized request accepted")
	}
}

func TestCodexWSSessionRejectsInvalidOptions(t *testing.T) {
	for _, proxy := range []string{"", "ftp://proxy.invalid", "http://proxy.invalid?password=secret"} {
		_, err := NewCodexWSSession(CodexWSSessionOptions{
			CredentialID: "test", Credential: CodexCredential{Type: ProviderCodex, AccessToken: "access", RefreshToken: "refresh", AccountID: "account"}, ProxyURL: proxy,
		})
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid proxy was accepted or leaked: %v", err)
		}
	}
}

func TestCodexWSSessionEventFailureClosesConnection(t *testing.T) {
	for _, mode := range []string{"invalid", "oversize", "consumer", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			disconnected := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				defer close(disconnected)
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
				if mode == "disconnect" {
					return
				}
				payload := wsCompleted("resp_test")
				if mode == "invalid" {
					payload = []byte(`{"invalid"`)
				}
				if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
					t.Error(err)
					return
				}
				_, _, _ = conn.ReadMessage()
			}))
			defer server.Close()
			session := wsTestSession(t, server.URL)
			if mode == "oversize" {
				session.options.MaxEventBytes = 16
			}
			_, err := session.ExecuteTurn(context.Background(), json.RawMessage(`{"model":"gpt-5","input":"hello"}`), func(context.Context, json.RawMessage) error {
				if mode == "consumer" {
					return errors.New("consumer stopped")
				}
				if mode == "oversize" || mode == "invalid" {
					t.Error("invalid event forwarded")
				}
				return nil
			})
			if err == nil {
				t.Fatal("expected failed turn")
			}
			select {
			case <-disconnected:
			case <-time.After(time.Second):
				t.Fatal("failed session leaked connection")
			}
			if !session.closed {
				t.Fatal("failed session was retained")
			}
		})
	}
}

// 在真实 SDK 的首次 Bind 后关闭连接，确定性制造业务写入失败。
// 仍使用生产 resource 的 Bind/End，验证 SDK 重拨不能再次发送业务帧。
type closeFirstWSBinding struct {
	resource *codexWSResource
	binds    atomic.Int32
}

func (lifecycle *closeFirstWSBinding) Bind(closeConnection func() error) error {
	lifecycle.binds.Add(1)
	if err := lifecycle.resource.Bind(closeConnection); err != nil {
		return err
	}
	return closeConnection()
}

func (lifecycle *closeFirstWSBinding) End(reason string) { lifecycle.resource.End(reason) }

func TestCodexWSSessionGuardsSDKSendRetry(t *testing.T) {
	var frames, handshakes atomic.Int32
	var handlersMu sync.Mutex
	var handlers []chan struct{}
	recordAfterSend := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		done := make(chan struct{})
		handlersMu.Lock()
		handlers = append(handlers, done)
		handlersMu.Unlock()
		defer close(done)
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		// 握手响应已发出，但服务端统计尚未更新，复现客户端先返回的调度顺序。
		<-recordAfterSend
		handshakes.Add(1)
		if _, _, err := conn.ReadMessage(); err == nil {
			frames.Add(1)
		}
	}))
	defer server.Close()
	session := wsTestSession(t, server.URL)
	lifecycle := &closeFirstWSBinding{resource: session.resource}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := session.inner.ExecuteStream(ctx, session.auth, cliproxyexecutor.Request{
		Model: "gpt-5", Payload: []byte(`{"model":"gpt-5","input":"hello"}`),
	}, cliproxyexecutor.Options{
		SourceFormat:       sdktranslator.FormatOpenAIResponse,
		Metadata:           map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: session.id},
		ExecutionLifecycle: lifecycle,
	})
	// 请求结束后立即撤销上下文，收尾统计仍必须有自己的等待时间。
	cancel()
	close(recordAfterSend)
	waitCtx, cancelWait := context.WithTimeout(context.Background(), time.Second)
	defer cancelWait()
	if err == nil {
		t.Fatal("failed send succeeded")
	}
	// 客户端返回不代表服务端已统计完连接和业务帧；等待真实处理结束再断言。
	handlersMu.Lock()
	pendingHandlers := append([]chan struct{}(nil), handlers...)
	handlersMu.Unlock()
	for _, done := range pendingHandlers {
		select {
		case <-done:
		case <-waitCtx.Done():
			t.Fatal("server handlers did not finish after SDK send failure")
		}
	}
	if frames.Load() != 0 || handshakes.Load() < 1 || handshakes.Load() > 2 {
		t.Fatalf("SDK send failure escaped the lifecycle guard: frames=%d handshakes=%d binds=%d", frames.Load(), handshakes.Load(), lifecycle.binds.Load())
	}
	if lifecycle.binds.Load() < 1 || lifecycle.binds.Load() > 2 {
		t.Fatal("unexpected SDK binding attempts")
	}
}

func TestCodexWSSessionRejectsSDKReplacementConnection(t *testing.T) {
	var frames, handshakes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		handshakes.Add(1)
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		frames.Add(1)
		if err := conn.WriteMessage(websocket.TextMessage, wsCompleted("resp_first")); err != nil {
			return
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	session := wsTestSession(t, server.URL)
	if _, err := session.ExecuteTurn(context.Background(), json.RawMessage(`{"model":"gpt-5","input":"hello"}`), nil); err != nil {
		t.Fatal(err)
	}
	session.inner.CloseExecutionSession(session.id)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// 模拟 SDK 已经重拨，并再次要求接管资源；它必须在写入业务帧之前失败。
	_, err := session.inner.ExecuteStream(ctx, session.auth, cliproxyexecutor.Request{
		Model: "gpt-5", Payload: []byte(`{"model":"gpt-5","input":"hello"}`),
	}, cliproxyexecutor.Options{
		SourceFormat:       sdktranslator.FormatOpenAIResponse,
		Metadata:           map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: session.id},
		ExecutionLifecycle: session.resource,
	})
	if err == nil || frames.Load() != 1 || handshakes.Load() != 2 {
		t.Fatalf("replacement sent a request: frames=%d handshakes=%d error=%v", frames.Load(), handshakes.Load(), err)
	}
}

func TestCodexWSSessionPreservesIncompleteStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.incomplete","response":{"id":"resp_partial","status":"incomplete","usage":{"input_tokens":5,"output_tokens":1},"incomplete_details":{"reason":"max_output_tokens"}}}`)); err != nil {
			t.Error(err)
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	session := wsTestSession(t, server.URL)
	result, err := session.ExecuteTurn(context.Background(), json.RawMessage(`{"model":"gpt-5","input":"hello"}`), nil)
	if err == nil || result.Status != "incomplete" || result.ResponseID != "resp_partial" || !json.Valid(result.Usage) {
		t.Fatalf("incomplete status/usage lost: result=%+v error=%v", result, err)
	}
}

func TestCodexWSSessionCancelDuringHandshake(t *testing.T) {
	started, disconnected := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(disconnected)
	}))
	defer server.Close()
	session := wsTestSession(t, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := session.ExecuteTurn(ctx, json.RawMessage(`{"model":"gpt-5","input":"hello"}`), nil)
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handshake did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("handshake error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("handshake ignored cancellation")
	}
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("handshake connection leaked")
	}
}

func TestCodexWSSessionUsesExplicitHTTPProxy(t *testing.T) {
	var tunnels atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, wsCompleted("resp_proxy")); err != nil {
			t.Error(err)
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer upstream.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "codex.invalid:80" {
			t.Error("unexpected proxy destination")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		remote, err := net.Dial("tcp", upstream.Listener.Addr().String())
		if err != nil {
			t.Error(err)
			return
		}
		defer remote.Close()
		client, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer client.Close()
		tunnels.Add(1)
		if _, err := rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			t.Error(err)
			return
		}
		if err := rw.Flush(); err != nil {
			t.Error(err)
			return
		}
		copied := make(chan struct{})
		go func() { _, _ = io.Copy(remote, rw); _ = remote.Close(); close(copied) }()
		_, _ = io.Copy(client, remote)
		_ = client.Close()
		<-copied
	}))
	defer proxy.Close()
	session := wsTestSession(t, "http://codex.invalid")
	session.auth.ProxyURL = proxy.URL
	result, err := session.ExecuteTurn(context.Background(), json.RawMessage(`{"model":"gpt-5","input":"hello"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ResponseID != "resp_proxy" || tunnels.Load() != 1 {
		t.Fatal("explicit proxy was not used")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCodexWSSessionHTTPProxyNegotiationHasDeadline(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer proxy.Close()
	session := wsTestSession(t, "http://codex.invalid")
	session.auth.ProxyURL = proxy.URL
	session.options.TurnTimeout = 200 * time.Millisecond
	finished := make(chan error, 1)
	go func() {
		_, err := session.ExecuteTurn(context.Background(), json.RawMessage(`{"model":"gpt-5","input":"hello"}`), nil)
		finished <- err
	}()
	select {
	case err := <-finished:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("proxy deadline not classified: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proxy CONNECT has no deadline")
	}
}

func TestCodexWSSessionContinuesThroughSOCKS5(t *testing.T) {
	var turns atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		previous := ""
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var request struct {
				Previous string `json:"previous_response_id"`
			}
			if json.Unmarshal(payload, &request) != nil || request.Previous != previous {
				t.Error("SOCKS5 continuation changed")
				return
			}
			previous = fmt.Sprintf("resp_socks_%d", turns.Add(1))
			if err := conn.WriteMessage(websocket.TextMessage, wsCompleted(previous)); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxyDone := make(chan struct{})
	go func() {
		defer close(proxyDone)
		conn, err := proxy.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				t.Error(err)
			}
			return
		}
		// 后续轮次必须复用此连接；额外拨号直接失败，避免坏实现卡在 SOCKS 协商。
		_ = proxy.Close()
		defer conn.Close()
		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Error(err)
			return
		}
		read := func(size int) []byte {
			buffer := make([]byte, size)
			if _, err := io.ReadFull(conn, buffer); err != nil {
				t.Error(err)
				return nil
			}
			return buffer
		}
		greeting := read(2)
		if greeting == nil || greeting[0] != 5 || read(int(greeting[1])) == nil {
			return
		}
		if _, err := conn.Write([]byte{5, 2}); err != nil {
			t.Error(err)
			return
		}
		auth := read(2)
		if auth == nil || auth[0] != 1 {
			t.Error("invalid SOCKS5 authentication")
			return
		}
		username := read(int(auth[1]))
		passwordLength := read(1)
		if passwordLength == nil {
			return
		}
		password := read(int(passwordLength[0]))
		if string(username) != "test-user" || string(password) != "test-password" {
			t.Error("SOCKS5 credentials were not forwarded")
			return
		}
		if _, err := conn.Write([]byte{1, 0}); err != nil {
			t.Error(err)
			return
		}
		command := read(4)
		if command == nil || command[0] != 5 || command[1] != 1 || command[3] != 3 {
			t.Error("invalid SOCKS5 CONNECT")
			return
		}
		length := read(1)
		if length == nil {
			return
		}
		hostname := read(int(length[0]))
		port := read(2)
		if string(hostname) != "codex.invalid" || len(port) != 2 || port[0] != 0 || port[1] != 80 {
			t.Error("SOCKS5 target changed")
			return
		}
		remote, err := net.Dial("tcp", upstream.Listener.Addr().String())
		if err != nil {
			t.Error(err)
			return
		}
		defer remote.Close()
		if _, err := conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80}); err != nil {
			t.Error(err)
			return
		}
		copied := make(chan struct{})
		go func() { _, _ = io.Copy(remote, conn); _ = remote.Close(); close(copied) }()
		_, _ = io.Copy(conn, remote)
		_ = conn.Close()
		<-copied
	}()
	t.Cleanup(func() {
		_ = proxy.Close()
		select {
		case <-proxyDone:
		case <-time.After(6 * time.Second):
			t.Error("SOCKS5 proxy did not close")
		}
	})
	session := wsTestSession(t, "http://codex.invalid", "socks5://test-user:test-password@"+proxy.Addr().String())
	first, err := session.ExecuteTurn(context.Background(), json.RawMessage(`{"model":"gpt-5","input":"hello"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := session.ExecuteTurn(context.Background(), json.RawMessage(fmt.Sprintf(`{"model":"gpt-5","input":"continue","previous_response_id":%q}`, first.ResponseID)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.ResponseID != "resp_socks_2" || turns.Load() != 2 {
		t.Fatal("SOCKS5 session did not complete two turns")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCodexWSSessionPreservesDoneStatus(t *testing.T) {
	for _, status := range []string{"completed", "failed", "incomplete", "cancelled", "in_progress", ""} {
		t.Run(status, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
				body := fmt.Sprintf(`{"type":"response.done","response":{"id":"resp_done","object":"response","status":%q,"output":[],"usage":{"input_tokens":3,"output_tokens":1}}}`, status)
				if err := conn.WriteMessage(websocket.TextMessage, []byte(body)); err != nil {
					t.Error(err)
					return
				}
				_, _, _ = conn.ReadMessage()
			}))
			defer server.Close()
			session := wsTestSession(t, server.URL)
			result, err := session.ExecuteTurn(context.Background(), json.RawMessage(`{"model":"gpt-5","input":"hello"}`), nil)
			wantFailure := status != "completed"
			if result.Status != status || (err != nil) != wantFailure || result.ResponseID != "resp_done" || !json.Valid(result.Usage) {
				t.Fatalf("done status=%q result=%+v error=%v", status, result, err)
			}
			session.mu.Lock()
			closed := session.closed
			session.mu.Unlock()
			if closed != wantFailure {
				t.Fatalf("done status=%q closed=%v", status, closed)
			}
		})
	}
}

func TestCodexWSSessionPreservesDoneErrorCode(t *testing.T) {
	for _, test := range []struct {
		name string
		code string
		want string
	}{
		{"safe", "rate_limit_exceeded", "rate_limit_exceeded"},
		{"unsafe", "invalid code\nprivate detail", ""},
		{"empty", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
				body := fmt.Sprintf(`{"type":"response.done","response":{"id":"resp_failed","object":"response","status":"failed","output":[],"error":{"code":%q,"message":"synthetic-upstream-detail"}}}`, test.code)
				if err := conn.WriteMessage(websocket.TextMessage, []byte(body)); err != nil {
					t.Error(err)
					return
				}
				_, _, _ = conn.ReadMessage()
			}))
			defer server.Close()
			session := wsTestSession(t, server.URL)
			result, err := session.ExecuteTurn(context.Background(), json.RawMessage(`{"model":"gpt-5","input":"hello"}`), nil)
			var failure *CodexWSError
			if !errors.As(err, &failure) || result.Status != "failed" || failure.UpstreamCode != test.want {
				t.Fatalf("failed done error code lost or unsafe: status=%q error=%+v", result.Status, failure)
			}
			if strings.Contains(err.Error(), "synthetic-upstream-detail") {
				t.Fatal("upstream error message leaked")
			}
		})
	}
}

func TestCodexWSSessionFixedIdentity(t *testing.T) {
	const wantUA = "codex-tui/0.153.3 (Mac OS 26.5.1; arm64) iTerm.app/3.6.11 (codex-tui; 0.153.3)"
	for _, model := range []string{"gpt-6-astra", "gpt-5.6-luna"} {
		for _, test := range []struct {
			name    string
			headers http.Header
		}{
			{name: "absent"},
			{name: "supplied", headers: http.Header{"Version": {"9.9.9"}, "User-Agent": {"codex_cli_rs/0.200.0"}}},
			{name: "empty", headers: http.Header{"Version": {""}, "User-Agent": {""}}},
			{name: "case variants", headers: http.Header{"version": {"9.9.9"}, "user-agent": {"custom-client/1.0"}}},
		} {
			t.Run(model+"/"+test.name, func(t *testing.T) {
				var handshakes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					handshakes.Add(1)
					if r.Header.Get("Version") != "0.153.3" || r.Header.Get("User-Agent") != wantUA {
						t.Errorf("handshake identity: version=%q UA=%q", r.Header.Get("Version"), r.Header.Get("User-Agent"))
					}
					if r.Header.Get("Authorization") != "Bearer test-access" {
						t.Error("identity normalization changed credential")
					}
					conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
					if err != nil {
						return
					}
					defer conn.Close()
					for turn := 0; turn < 2; turn++ {
						if _, _, err := conn.ReadMessage(); err != nil {
							return
						}
						if err := conn.WriteMessage(websocket.TextMessage, wsCompleted(fmt.Sprintf("resp_fixed_%d", turn))); err != nil {
							return
						}
					}
					_, _, _ = conn.ReadMessage()
				}))
				defer server.Close()
				session := wsTestSession(t, server.URL)
				session.options.Headers = test.headers.Clone()
				for turn := 0; turn < 2; turn++ {
					payload := json.RawMessage(fmt.Sprintf(`{"model":%q,"input":"hello"}`, model))
					if turn == 1 {
						payload = json.RawMessage(fmt.Sprintf(`{"model":%q,"input":"next","previous_response_id":"resp_fixed_0"}`, model))
					}
					if _, err := session.ExecuteTurn(t.Context(), payload, nil); err != nil {
						t.Fatal(err)
					}
				}
				if handshakes.Load() != 1 {
					t.Errorf("handshakes = %d, want one reused connection", handshakes.Load())
				}
			})
		}
	}
}
