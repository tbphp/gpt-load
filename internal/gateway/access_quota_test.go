package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/accessquota"
	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/state"
	"gpt-load/internal/usage"
)

func TestHandlerBlocksAllDataPlaneRoutesUntilPeriodicQuotaRecovers(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true,
	}}}
	engine, handler, _, _ := newRequestLogHandlerTestRuntime(
		t, forwarder, &recordingAccessKeyRPMLimiter{}, &recordingRequestLogSink{}, "sk-first",
	)
	runtime := accessquota.NewRuntime()
	if err := runtime.Reconcile(map[uint][]accessquota.Rule{1: {
		{ID: 101, Revision: 1, Kind: accessquota.KindPeriodic, LimitNanoUSD: 100, PeriodSeconds: 300},
	}}); err != nil {
		t.Fatal(err)
	}
	windowStart := time.Unix(1_000, 0)
	ticket, decision := runtime.Admit(1, windowStart)
	if !decision.Allowed {
		t.Fatalf("Admit() = %#v", decision)
	}
	runtime.Complete(ticket, 100)
	handler.accessQuota = runtime
	handler.now = func() time.Time { return windowStart.Add(time.Minute) }

	for _, test := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/v1/models"},
		{method: http.MethodPost, path: "/v1/chat/completions", body: `{"model":"gpt-4o"}`},
	} {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		request.Header.Set("Authorization", "Bearer gl-client")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusTooManyRequests ||
			!strings.Contains(response.Body.String(), `"code":"access_key_cost_limit_exceeded"`) ||
			!strings.Contains(response.Body.String(), `"next_available_at_ms":1300000`) ||
			response.Header().Get("Retry-After") != "240" {
			t.Fatalf("quota response = %d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
		}
	}
	if len(forwarder.inputs) != 0 {
		t.Fatalf("blocked forward calls = %d, want 0", len(forwarder.inputs))
	}

	handler.now = func() time.Time { return windowStart.Add(5 * time.Minute) }
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("recovered model list = %d %s", response.Code, response.Body.String())
	}
	view := runtime.Snapshot(1, windowStart.Add(5*time.Minute))
	if len(view.Rules) != 1 || view.Rules[0].Status != accessquota.RuleStatusInactive {
		t.Fatalf("recovered view = %#v", view)
	}
}

func TestHandlerTotalQuotaBlockIsNotRetryableAndReturnsEveryBlocker(t *testing.T) {
	engine, handler, _, _ := newRequestLogHandlerTestRuntime(
		t, &scriptedForwarder{}, &recordingAccessKeyRPMLimiter{}, &recordingRequestLogSink{},
	)
	runtime := accessquota.NewRuntime()
	if err := runtime.Reconcile(map[uint][]accessquota.Rule{1: {
		{ID: 201, Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 100},
		{ID: 202, Revision: 1, Kind: accessquota.KindPeriodic, LimitNanoUSD: 20, PeriodSeconds: 300},
	}}); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(4_000, 0)
	ticket, _ := runtime.Admit(1, now)
	runtime.Complete(ticket, 100)
	handler.accessQuota = runtime
	handler.now = func() time.Time { return now.Add(time.Minute) }

	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "" ||
		!strings.Contains(response.Body.String(), `"recoverable":false`) ||
		!strings.Contains(response.Body.String(), `"next_available_at_ms":null`) {
		t.Fatalf("total quota response = %d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	var envelope struct {
		Data struct {
			BlockingRules []struct {
				ID uint `json:"id"`
			} `json:"blocking_rules"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.BlockingRules) != 2 || envelope.Data.BlockingRules[0].ID != 201 ||
		envelope.Data.BlockingRules[1].ID != 202 {
		t.Fatalf("blocking rules = %#v", envelope.Data.BlockingRules)
	}
}

func TestHandlerStartsPeriodicWindowOnlyAtFirstGatewayExecutionAttempt(t *testing.T) {
	runtime := accessquota.NewRuntime()
	if err := runtime.Reconcile(map[uint][]accessquota.Rule{1: {
		{ID: 102, Revision: 1, Kind: accessquota.KindPeriodic, LimitNanoUSD: 100, PeriodSeconds: 300},
	}}); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2_000, 0)

	noCandidateEngine, noCandidateHandler, _, _ := newRequestLogHandlerTestRuntime(
		t, &scriptedForwarder{}, &recordingAccessKeyRPMLimiter{}, &recordingRequestLogSink{},
	)
	noCandidateHandler.accessQuota = runtime
	noCandidateHandler.now = func() time.Time { return now }
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	noCandidateEngine.ServeHTTP(httptest.NewRecorder(), request)
	if view := runtime.Snapshot(1, now); view.Rules[0].Status != accessquota.RuleStatusInactive {
		t.Fatalf("local no-candidate request started window = %#v", view)
	}

	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		Err: errors.New("not sent"), DispatchState: execution.DispatchNotSent,
	}}}
	engine, handler, _, _ := newRequestLogHandlerTestRuntime(
		t, forwarder, &recordingAccessKeyRPMLimiter{}, &recordingRequestLogSink{}, "sk-first",
	)
	handler.accessQuota = runtime
	handler.now = func() time.Time { return now }
	request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	engine.ServeHTTP(httptest.NewRecorder(), request)
	view := runtime.Snapshot(1, now)
	if len(forwarder.inputs) != 1 || view.Rules[0].Status != accessquota.RuleStatusAvailable ||
		view.Rules[0].WindowStartedAtMS == nil || *view.Rules[0].WindowStartedAtMS != now.UnixMilli() {
		t.Fatalf("execution attempts/view = %d/%#v", len(forwarder.inputs), view)
	}
}

func TestHandlerAccountsFinalEstimateWhenRequestIDGenerationFails(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK, Header: make(http.Header), RequestWritten: true,
		Usage: usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1_000_000}},
	}}}
	sink := &recordingRequestLogSink{}
	engine, handler, _, _ := newRequestLogHandlerTestRuntime(
		t, forwarder, &recordingAccessKeyRPMLimiter{}, sink, "sk-first",
	)
	runtime := accessquota.NewRuntime()
	if err := runtime.Reconcile(map[uint][]accessquota.Rule{1: {
		{ID: 103, Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 10_000_000_000},
	}}); err != nil {
		t.Fatal(err)
	}
	handler.accessQuota = runtime
	handler.priceTables = &mutableGatewayPriceTableProvider{table: mustGatewayPriceTable(t, 2_000_000_000, true)}
	handler.newRequestID = func() (string, error) { return "", errors.New("entropy unavailable") }
	handler.requestNow = func() time.Time { return time.Unix(3_000, 0) }
	handler.now = handler.requestNow

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	view := runtime.Snapshot(1, time.Unix(3_001, 0))
	if len(view.Rules) != 1 || view.Rules[0].UsedNanoUSD != 2_000_000_000 {
		t.Fatalf("quota view = %#v", view)
	}
	if events := sink.snapshot(); len(events) != 0 {
		t.Fatalf("request log events = %#v, want none without request ID", events)
	}
}

func TestHandlerLogsAccessQuotaCompletionFault(t *testing.T) {
	var logs bytes.Buffer
	handler := &Handler{logger: newGatewayJSONLogger(&logs)}
	handler.logAccessQuotaCompletionFault(17, accessquota.CompletionResult{
		Fault: accessquota.CompletionFaultNegativeEstimate,
	})
	entry := logs.String()
	if !strings.Contains(entry, `"access_key_id":17`) ||
		!strings.Contains(entry, `"failure_type":"negative_estimate"`) ||
		!strings.Contains(entry, `"level":"error"`) {
		t.Fatalf("quota completion fault log = %s", entry)
	}
}

func TestHandlerRejectsStaleSnapshotBeforeQuotaCheck(t *testing.T) {
	forwarder := &scriptedForwarder{}
	_, handler, manager, _ := newRequestLogHandlerTestRuntime(
		t, forwarder, &recordingAccessKeyRPMLimiter{}, &recordingRequestLogSink{}, "sk-first",
	)
	runtime := accessquota.NewRuntime()
	manager.SetSnapshotReconciler(gatewayAccessQuotaSnapshotReconciler{runtime: runtime})
	oldRules := []accessquota.Rule{{
		ID: 301, Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 100,
	}}
	if _, err := manager.Publish(gatewayAccessQuotaCompileInput(handler, oldRules)); err != nil {
		t.Fatal(err)
	}
	ticket, decision := runtime.Admit(1, time.Unix(5_000, 0))
	if !decision.Allowed {
		t.Fatalf("Admit() = %#v", decision)
	}
	runtime.Complete(ticket, 100)
	handler.accessQuota = runtime

	context, response := prepareAuthenticatedGatewayRequest(t, handler, http.MethodGet, "/v1/models", nil)
	if _, err := manager.Publish(gatewayAccessQuotaCompileInput(handler, []accessquota.Rule{{
		ID: 301, Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 200,
	}})); err != nil {
		t.Fatal(err)
	}

	handler.Handle(context)
	if response.Code != http.StatusServiceUnavailable ||
		!strings.Contains(response.Body.String(), `"code":"configuration_changed"`) ||
		response.Header().Get("Retry-After") != "1" {
		t.Fatalf(
			"stale snapshot response = %d headers=%v body=%s",
			response.Code,
			response.Header(),
			response.Body.String(),
		)
	}
	if len(forwarder.inputs) != 0 {
		t.Fatalf("stale snapshot forward calls = %d, want 0", len(forwarder.inputs))
	}
}

func TestHandlerRejectsSnapshotChangedBetweenQuotaCheckAndAdmit(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true,
	}}}
	_, handler, manager, _ := newRequestLogHandlerTestRuntime(
		t, forwarder, &recordingAccessKeyRPMLimiter{}, &recordingRequestLogSink{}, "sk-first",
	)
	runtime := accessquota.NewRuntime()
	manager.SetSnapshotReconciler(gatewayAccessQuotaSnapshotReconciler{runtime: runtime})
	oldRules := []accessquota.Rule{{
		ID: 302, Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 100,
	}}
	if _, err := manager.Publish(gatewayAccessQuotaCompileInput(handler, oldRules)); err != nil {
		t.Fatal(err)
	}
	first, _ := runtime.Admit(1, time.Unix(6_000, 0))
	runtime.Complete(first, 90)
	second, decision := runtime.Admit(1, time.Unix(6_001, 0))
	if !decision.Allowed {
		t.Fatalf("second Admit() = %#v", decision)
	}
	handler.accessQuota = runtime

	body := newBlockingRequestBody(`{"model":"gpt-4o"}`, nil)
	context, response := prepareAuthenticatedGatewayRequest(
		t,
		handler,
		http.MethodPost,
		"/v1/chat/completions",
		body,
	)
	done := make(chan struct{})
	go func() {
		handler.Handle(context)
		close(done)
	}()
	receiveTestSignal(t, body.started, "quota request body read")

	runtime.Complete(second, 10)
	if _, err := manager.Publish(gatewayAccessQuotaCompileInput(handler, []accessquota.Rule{{
		ID: 302, Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 200,
	}})); err != nil {
		t.Fatal(err)
	}
	close(body.release)
	receiveTestSignal(t, done, "stale quota admission completion")

	if response.Code != http.StatusServiceUnavailable ||
		!strings.Contains(response.Body.String(), `"code":"configuration_changed"`) ||
		response.Header().Get("Retry-After") != "1" {
		t.Fatalf(
			"stale admission response = %d headers=%v body=%s",
			response.Code,
			response.Header(),
			response.Body.String(),
		)
	}
	if len(forwarder.inputs) != 0 {
		t.Fatalf("stale admission forward calls = %d, want 0", len(forwarder.inputs))
	}
}

type gatewayAccessQuotaSnapshotReconciler struct {
	runtime *accessquota.Runtime
}

func (reconciler gatewayAccessQuotaSnapshotReconciler) ReconcileConfigSnapshot(
	snapshot *state.ConfigSnapshot,
) error {
	return reconciler.runtime.Reconcile(snapshot.AccessQuotaDefinitions())
}

func gatewayAccessQuotaCompileInput(
	handler *Handler,
	rules []accessquota.Rule,
) state.CompileInput {
	return state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ConnectionType: "api_key", ID: 1, Name: "openai", ChannelID: channel.OpenAI,
			Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true,
		}},
		Credentials: []state.CredentialConfig{{
			ID: 1, GroupID: 1, Status: state.CredentialStatusActive,
			Version: 1, IdentityGeneration: 1, Fingerprint: "credential-1",
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"),
			Status: state.AccessKeyStatusActive, CostLimitRules: append([]accessquota.Rule(nil), rules...),
		}},
	}
}

func prepareAuthenticatedGatewayRequest(
	t *testing.T,
	handler *Handler,
	method string,
	path string,
	body io.Reader,
) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	var selected dataPlaneEndpoint
	found := false
	for _, endpoint := range dataPlaneEndpointCatalog() {
		if endpoint.path == path {
			selected = endpoint
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("data-plane endpoint %q is missing", path)
	}
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(method, path, body)
	context.Request.Header.Set("Authorization", "Bearer gl-client")
	handler.prepareDataPlaneRequest(selected)(context)
	handler.authenticateDataPlaneRequest(context)
	requestContext, ok := dataPlaneRequestContextFrom(context)
	if !ok || !requestContext.authenticated || requestContext.snapshot == nil {
		t.Fatalf("authenticated request context = %#v", requestContext)
	}
	return context, response
}
