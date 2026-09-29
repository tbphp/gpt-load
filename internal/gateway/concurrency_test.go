package gateway

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/telemetry"
)

type concurrencyForwarder struct {
	call func(context.Context, ForwardInput, http.ResponseWriter) UpstreamResult
}

func (f concurrencyForwarder) Forward(ctx context.Context, in ForwardInput) UpstreamResult {
	return f.call(ctx, in, nil)
}
func (f concurrencyForwarder) ForwardStream(ctx context.Context, in ForwardInput, w http.ResponseWriter) UpstreamResult {
	return f.call(ctx, in, w)
}

func concurrencyRequest(engine http.Handler, stream bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":"gpt-4o","stream":%t,"messages":[{"role":"user","content":"hello"}]}`, stream)))
	req.Header.Set("Authorization", "Bearer gl-client")
	out := httptest.NewRecorder()
	engine.ServeHTTP(out, req)
	return out
}

func TestConcurrencyHTTPHoldsUntilForwardFinishes(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, layer := range []string{"global", "key", "group"} {
			t.Run(fmt.Sprintf("%s/stream=%t", layer, stream), func(t *testing.T) {
				started, finish := make(chan struct{}), make(chan struct{})
				f := concurrencyForwarder{call: func(ctx context.Context, in ForwardInput, w http.ResponseWriter) UpstreamResult {
					if w != nil {
						w.Header().Set("Content-Type", "text/event-stream")
						if _, err := w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")); err != nil {
							t.Error(err)
						}
						if flusher, ok := w.(http.Flusher); ok {
							flusher.Flush()
						}
					}
					select {
					case <-started:
					default:
						close(started)
					}
					select {
					case <-finish:
					case <-ctx.Done():
					}
					return UpstreamResult{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"choices":[]}`)}
				}}
				h, m, _ := newHandlerForTest(t, f, "test-upstream")
				settings, groupSettings := config.Settings{}, config.Settings{}
				switch layer {
				case "global":
					settings["global_concurrency_limit"] = 1
				case "key":
					settings["default_access_key_concurrency_limit"] = 1
				case "group":
					groupSettings["concurrency_limit"] = 1
				}
				publishHandlerPolicySettings(t, h, m, 1, settings, groupSettings)
				engine := gin.New()
				bindGatewayRoutesForTest(t, engine, h)
				done := make(chan struct{})
				go func() { defer close(done); concurrencyRequest(engine, stream) }()
				<-started
				counts := m.Concurrency().Snapshot()
				if counts.Global != 1 || counts.AccessKeys[1] != 1 || counts.Groups[1] != 1 {
					close(finish)
					<-done
					t.Fatalf("in-flight counts: %+v", counts)
				}
				secondDone := make(chan *httptest.ResponseRecorder, 1)
				go func() { secondDone <- concurrencyRequest(engine, stream) }()
				// 首个请求被阻塞时，第二个应立即拒绝；取消测试等待仅用于避免失败时挂死。
				var response *httptest.ResponseRecorder
				select {
				case response = <-secondDone:
				case <-time.After(2 * time.Second):
					close(finish)
					<-done
					<-secondDone
					t.Fatal("full request waited for capacity")
				}
				close(finish)
				<-done
				if response.Code != 429 || !bytes.Contains(response.Body.Bytes(), []byte("concurrency_limit_exceeded")) {
					t.Fatalf("rejection=%d %s", response.Code, response.Body)
				}
				if s := m.Concurrency().Snapshot(); s.Global != 0 || len(s.Groups) != 0 || len(s.AccessKeys) != 0 {
					t.Fatalf("leaked counts: %+v", s)
				}
			})
		}
	}
}

func TestConcurrencyAuditFullGroupRejectsWithoutFallback(t *testing.T) {
	f := &scriptedForwarder{results: []UpstreamResult{auditReply(auditPass), auditReply(`{"choices":[]}`)}}
	h, engine := auditEngine(t, f)
	group := h.manager.Current().Groups[2]
	group.ConcurrencyLimit = 1
	h.manager.Current().Groups[2] = group
	release, _ := h.manager.Concurrency().TryAcquireGroup(2, 1)
	defer release()
	response := sendAuditRequest(engine, `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
	if response.Code != 429 || len(f.inputs) != 0 {
		t.Fatalf("audit capacity was bypassed: status=%d calls=%d", response.Code, len(f.inputs))
	}
	if h.manager.Concurrency().Snapshot().Global != 0 {
		t.Fatal("entry leaked")
	}
}

func TestConcurrencyAuditCountsAuxiliaryAndAnswerSeparately(t *testing.T) {
	f := &scriptedForwarder{results: []UpstreamResult{auditReply(auditPass), auditReply(`{"choices":[]}`)}}
	h, engine := auditEngine(t, f)
	group := h.manager.Current().Groups[1]
	group.ConcurrencyLimit = 1
	h.manager.Current().Groups[1] = group
	f.onCall = func(index int) {
		groupID := uint(2 - index)
		if s := h.manager.Concurrency().Snapshot(); s.Global != 1 || s.Groups[groupID] != 1 || len(s.Groups) != 1 {
			t.Fatalf("incorrect audit/answer counts: %+v", s)
		}
	}
	response := sendAuditRequest(engine, `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
	if response.Code != 200 || len(f.inputs) != 2 {
		t.Fatalf("sequential audit and answer rejected: %d", response.Code)
	}
}

type concurrencySlowErrorWriter struct {
	*httptest.ResponseRecorder
	started, finish chan struct{}
}

func (w *concurrencySlowErrorWriter) Write(body []byte) (int, error) {
	close(w.started)
	<-w.finish
	return w.ResponseRecorder.Write(body)
}

func TestConcurrencyRejectedRequestHoldsEntryDuringErrorWrite(t *testing.T) {
	f := &scriptedForwarder{}
	h, m, _ := newHandlerForTest(t, f, "test-upstream")
	publishHandlerPolicySettings(t, h, m, 1, nil, config.Settings{"concurrency_limit": 1})
	release, _ := m.Concurrency().TryAcquireGroup(1, 1)
	defer release()
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, h)
	writer := &concurrencySlowErrorWriter{httptest.NewRecorder(), make(chan struct{}), make(chan struct{})}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[]}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	done := make(chan struct{})
	go func() { defer close(done); engine.ServeHTTP(writer, request) }()
	<-writer.started
	count := m.Concurrency().Snapshot().Global
	close(writer.finish)
	<-done
	if count != 1 {
		t.Fatalf("entry released before error response: %d", count)
	}
	if writer.Code != 429 || len(f.inputs) != 0 || m.Concurrency().Snapshot().Global != 0 {
		t.Fatal("incorrect rejection or cleanup")
	}
}

func TestConcurrencyRetryUsesPublishedGroupLimit(t *testing.T) {
	f := &scriptedForwarder{results: []UpstreamResult{{StatusCode: 401, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"error":"invalid_api_key"}`), ClassificationBody: []byte(`{"error":"invalid_api_key"}`), RequestWritten: true}}}
	h, m, _ := newHandlerForTest(t, f, "upstream-a", "upstream-b")
	var releaseOther func()
	f.onCall = func(_ int) {
		publishHandlerPolicySettings(t, h, m, 2, config.Settings{"retry_count": 2}, config.Settings{"concurrency_limit": 1})
		releaseOther, _ = m.Concurrency().TryAcquireGroup(1, 0)
	}
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, h)
	response := concurrencyRequest(engine, false)
	if releaseOther != nil {
		releaseOther()
	}
	if response.Code != 429 || len(f.inputs) != 1 {
		t.Fatalf("retry ignored fresh limit: status=%d calls=%d", response.Code, len(f.inputs))
	}
	if s := m.Concurrency().Snapshot(); s.Global != 0 || len(s.Groups) != 0 {
		t.Fatalf("retry leaked: %+v", s)
	}
}

func TestConcurrencyControlAndLocalQuery(t *testing.T) {
	f := &scriptedForwarder{results: []UpstreamResult{{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{}`)}}}
	h, m, _ := newHandlerForTest(t, f, "test-upstream")
	h.dialects = dialect.NewSet(dialect.NewOpenAI(), dialect.NewOpenAIResponses())
	publishHandlerPolicySettings(t, h, m, 1, config.Settings{"global_concurrency_limit": 1}, config.Settings{"concurrency_limit": 1})
	release, _ := m.Concurrency().TryAcquireRequest(1, 1, 0)
	defer release()
	group, _ := m.Concurrency().TryAcquireGroup(1, 1)
	defer group()
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, h)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/v1/models", 429},
		{http.MethodPost, "/v1/responses/resp_test/cancel", 200},
		{http.MethodPost, "/v1/responses/resp_test/cancel/nested", 429},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Authorization", "Bearer gl-client")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, req)
		if response.Code != tc.status {
			t.Errorf("%s => %d, want %d", tc.path, response.Code, tc.status)
		}
	}
}

func TestConcurrencyCancellationWaitsForExecutionCleanup(t *testing.T) {
	started, canceled, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	f := concurrencyForwarder{call: func(ctx context.Context, _ ForwardInput, _ http.ResponseWriter) UpstreamResult {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-finish
		return UpstreamResult{Err: ctx.Err()}
	}}
	h, m, _ := newHandlerForTest(t, f, "test-upstream")
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, h)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[]}`)).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer gl-client")
	done := make(chan struct{})
	go func() { defer close(done); engine.ServeHTTP(httptest.NewRecorder(), request) }()
	<-started
	cancel()
	<-canceled
	counts := m.Concurrency().Snapshot()
	close(finish)
	<-done
	if counts.Global != 1 || counts.Groups[1] != 1 {
		t.Fatalf("cancellation released before cleanup: %+v", counts)
	}
	if counts := m.Concurrency().Snapshot(); counts.Global != 0 || len(counts.Groups) != 0 {
		t.Fatalf("cleanup leaked: %+v", counts)
	}
}

func TestConcurrencyAutoModelFullGroupRejectsWithoutFallback(t *testing.T) {
	f := &scriptedForwarder{}
	h, engine := auditEngine(t, f)
	h.manager.Current().RequestAudit.Enabled = false
	group := h.manager.Current().Groups[2]
	group.ConcurrencyLimit = 1
	h.manager.Current().Groups[2] = group
	release, ok := h.manager.Concurrency().TryAcquireGroup(2, 1)
	if !ok {
		t.Fatal("cannot reserve decision group")
	}
	defer release()
	response := sendAuditRequest(engine, `{"model":"auto-probe","messages":[{"role":"user","content":"hello"}]}`)
	if response.Code != 429 || len(f.inputs) != 0 || !bytes.Contains(response.Body.Bytes(), []byte("concurrency_limit_exceeded")) {
		t.Fatalf("automatic model bypassed capacity: status=%d calls=%d", response.Code, len(f.inputs))
	}
	if h.manager.Concurrency().Snapshot().Global != 0 {
		t.Fatal("entry leaked")
	}
}

// 用受控收尾观察中间状态，不等待真实日志持久化。
type concurrencyFinishSink struct{ started, finish chan struct{} }

func (s concurrencyFinishSink) Emit(telemetry.RequestEvent) { close(s.started); <-s.finish }
func TestConcurrencyEntryRemainsUntilHandlerCleanup(t *testing.T) {
	f := &scriptedForwarder{results: []UpstreamResult{auditReply(`{"choices":[]}`)}}
	h, m, _ := newHandlerForTest(t, f, "test-upstream")
	sink := concurrencyFinishSink{make(chan struct{}), make(chan struct{})}
	h.requestLogSink = sink
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, h)
	done := make(chan struct{})
	go func() { defer close(done); concurrencyRequest(engine, false) }()
	<-sink.started
	counts := m.Concurrency().Snapshot()
	close(sink.finish)
	<-done
	if counts.Global != 1 || counts.AccessKeys[1] != 1 || len(counts.Groups) != 0 {
		t.Fatalf("entry released before handler cleanup: %+v", counts)
	}
	if m.Concurrency().Snapshot().Global != 0 {
		t.Fatal("entry leaked")
	}
}
