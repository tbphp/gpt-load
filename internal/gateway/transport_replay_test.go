package gateway

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	platformhttp "gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

type staleKeepAliveReplayServer struct {
	listener  net.Listener
	wirePosts atomic.Int32
	errors    chan error
	done      chan struct{}
	closeOnce sync.Once
	closing   atomic.Bool
}

func newStaleKeepAliveReplayServer(t *testing.T) *staleKeepAliveReplayServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for stale keep-alive fixture: %v", err)
	}
	server := &staleKeepAliveReplayServer{
		listener: listener,
		errors:   make(chan error, 1),
		done:     make(chan struct{}),
	}
	go server.serve()
	t.Cleanup(func() {
		server.closeOnce.Do(func() {
			server.closing.Store(true)
			_ = server.listener.Close()
		})
		select {
		case <-server.done:
		case <-time.After(time.Second):
			t.Error("stale keep-alive fixture did not stop")
		}
	})
	return server
}

func (server *staleKeepAliveReplayServer) URL() string {
	return "http://" + server.listener.Addr().String()
}

func (server *staleKeepAliveReplayServer) serve() {
	defer close(server.done)

	firstConnection, err := server.listener.Accept()
	if err != nil {
		server.reportError(fmt.Errorf("accept warm-up connection: %w", err))
		return
	}
	firstReader := bufio.NewReader(firstConnection)
	warmup, err := readReplayFixtureRequest(firstReader)
	if err != nil {
		_ = firstConnection.Close()
		server.reportError(fmt.Errorf("read warm-up request: %w", err))
		return
	}
	if warmup.Method != http.MethodGet || warmup.URL.Path != "/warmup" {
		_ = firstConnection.Close()
		server.reportError(fmt.Errorf("warm-up request = %s %s", warmup.Method, warmup.URL.Path))
		return
	}
	if _, err := io.WriteString(
		firstConnection,
		"HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n",
	); err != nil {
		_ = firstConnection.Close()
		server.reportError(fmt.Errorf("write warm-up response: %w", err))
		return
	}

	firstPOST, err := readReplayFixtureRequest(firstReader)
	if err != nil {
		_ = firstConnection.Close()
		server.reportError(fmt.Errorf("read first POST: %w", err))
		return
	}
	if err := server.recordPOST(firstPOST); err != nil {
		_ = firstConnection.Close()
		server.reportError(err)
		return
	}

	// Fail the reused keep-alive connection only after the server has received
	// the complete POST. A Transport replay therefore becomes a second,
	// independently counted wire request on the next connection.
	if err := firstConnection.Close(); err != nil {
		server.reportError(fmt.Errorf("close reused connection: %w", err))
		return
	}

	secondConnection, err := server.listener.Accept()
	if err != nil {
		if server.isClosed() {
			return
		}
		server.reportError(fmt.Errorf("accept replay connection: %w", err))
		return
	}
	defer secondConnection.Close()
	secondPOST, err := readReplayFixtureRequest(bufio.NewReader(secondConnection))
	if err != nil {
		server.reportError(fmt.Errorf("read replay POST: %w", err))
		return
	}
	if err := server.recordPOST(secondPOST); err != nil {
		server.reportError(err)
		return
	}
	body := []byte(`{"id":"chatcmpl-replay-test","choices":[]}`)
	if _, err := fmt.Fprintf(
		secondConnection,
		"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		len(body),
		body,
	); err != nil {
		server.reportError(fmt.Errorf("write replay response: %w", err))
	}
}

func readReplayFixtureRequest(reader *bufio.Reader) (*http.Request, error) {
	request, err := http.ReadRequest(reader)
	if err != nil {
		return nil, err
	}
	defer request.Body.Close()
	if _, err := io.Copy(io.Discard, request.Body); err != nil {
		return nil, err
	}
	return request, nil
}

func (server *staleKeepAliveReplayServer) recordPOST(request *http.Request) error {
	if request.Method != http.MethodPost ||
		request.URL.Path != "/v1/chat/completions" {
		return fmt.Errorf("wire request = %s %s, want POST /v1/chat/completions", request.Method, request.URL.Path)
	}
	if request.Header.Get("Idempotency-Key") != "client-idempotency" ||
		request.Header.Get("X-Idempotency-Key") != "client-x-idempotency" {
		return fmt.Errorf("wire idempotency headers = %#v", request.Header)
	}
	server.wirePosts.Add(1)
	return nil
}

func (server *staleKeepAliveReplayServer) reportError(err error) {
	select {
	case server.errors <- err:
	default:
	}
}

func (server *staleKeepAliveReplayServer) isClosed() bool {
	return server.closing.Load()
}

func (server *staleKeepAliveReplayServer) assertNoError(t *testing.T) {
	t.Helper()
	select {
	case err := <-server.errors:
		t.Fatal(err)
	default:
	}
}

func TestGatewayAttemptDoesNotImplicitlyReplayBodyWithIdempotencyKey(t *testing.T) {
	upstream := newStaleKeepAliveReplayServer(t)
	clients := platformhttp.NewHTTPClientManager()
	forwarder := NewForwarder(clients, redact.New())
	sink := &recordingRequestLogSink{}
	engine, handler, manager, _ := newRequestLogHandlerTestRuntime(
		t,
		forwarder,
		&recordingAccessKeyRPMLimiter{},
		sink,
		"sk-provider",
	)
	if _, err := manager.Publish(state.CompileInput{
		Groups: []state.GroupConfig{{
			ID: 1, Name: "openai", UpstreamURL: testUpstreamBaseURL(upstream.URL(), protocol.OpenAICompletions),
			Protocols: []protocol.Protocol{protocol.OpenAICompletions},
			Models:    []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"),
			Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish(upstream fixture) error = %v", err)
	}
	group := manager.Current().Groups[1]
	warmup, err := clients.GetClient(nonStreamingClientConfig(group.Timeouts)).Get(upstream.URL() + "/warmup")
	if err != nil {
		t.Fatalf("warm reused gateway client: %v", err)
	}
	_ = warmup.Body.Close()

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"gpt-4o","messages":[]}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	request.Header.Set("Idempotency-Key", "client-idempotency")
	request.Header.Set("X-Idempotency-Key", "client-x-idempotency")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	upstream.assertNoError(t)

	events := sink.snapshot()
	if len(events) != 1 || len(events[0].Attempts) != 1 {
		t.Fatalf("RequestLog events/attempts = %d/%#v, want 1/1", len(events), events)
	}
	if got := upstream.wirePosts.Load(); got != int32(len(events[0].Attempts)) {
		t.Fatalf(
			"wire POST sends/gateway attempts/RequestLog attempts = %d/%s/%d, want 1/1/1",
			got,
			recorder.Header().Get(debugHeaderAttempts),
			len(events[0].Attempts),
		)
	}
	if recorder.Header().Get(debugHeaderAttempts) != "1" {
		t.Fatalf("gateway attempt header = %q, want 1", recorder.Header().Get(debugHeaderAttempts))
	}
}
