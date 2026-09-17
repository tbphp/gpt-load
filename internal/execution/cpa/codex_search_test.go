package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/accessquota"
	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/gateway"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/httproute"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/subscription/providers/codex"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

type searchRequestLogSink struct {
	events []telemetry.RequestEvent
}

type searchReadFailureBody struct{}

func (searchReadFailureBody) Read(buffer []byte) (int, error) {
	return copy(buffer, `{"output":"must not succeed"}`), io.ErrUnexpectedEOF
}

func (searchReadFailureBody) Close() error { return nil }

func TestCodexSearchAdapterPreservesReadFailureMetadata(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			adapter, _, _, encryption, credential := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			spec := validSpec(t, credential, encryption)
			spec.Operation, spec.Path = execution.OperationWebSearch, "/v1/alpha/search"
			spec.Body = []byte(`{"id":"session","model":"gpt-5"}`)
			calls := 0
			transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"17"}},
					Body: searchReadFailureBody{}, Request: req}, nil
			})
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
			result := adapter.Execute(ctx, spec)
			if err := result.Validate(); err != nil || result.Error == nil || !result.ResponseStarted ||
				result.StatusCode != status || result.Header.Get("Retry-After") != "17" || len(result.Body) != 0 || calls != 1 {
				t.Fatalf("read failure result = %#v, validation = %v, calls = %d", result, err, calls)
			}
			decision := health.JudgeExecution(health.ExecutionAttempt{
				DispatchState: result.DispatchState, StatusCode: result.StatusCode, Header: result.Header,
				Evidence: result.Error, Now: time.Now(),
			}, health.DecisionContext{Method: http.MethodPost, Operation: execution.OperationWebSearch, CredentialRefreshable: true})
			if status == http.StatusUnauthorized && decision.Retry != health.RetryRefreshCredential {
				t.Fatalf("truncated 401 did not request credential refresh: %#v", decision)
			}
			if status == http.StatusOK && (result.Error.Kind != execution.ErrorKindTransport || decision.ShouldRetry() || decision.Category == health.FailureCategoryOK) {
				t.Fatalf("failed 200 became successful or replayable: result=%#v, decision=%#v", result, decision)
			}
		})
	}
}

func (sink *searchRequestLogSink) Emit(event telemetry.RequestEvent) {
	sink.events = append(sink.events, event)
}

func TestCodexStandaloneSearchGateway(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adapter, _, registry, encryption, credential := newAdapterFixture(
		t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)),
	)
	responseBody := `{"model":"server-metadata","encrypted_output":"opaque","output":"search result","results":[{"ref_id":"turn0search0"}]}`
	fake := &fakeExecutor{result: codex.ExecuteResponse{Payload: []byte(responseBody)}}
	setCodexExecutor(t, adapter, fake)
	ref, ok := registry.CredentialRef(credential.ID)
	if !ok {
		t.Fatal("credential ref is unavailable")
	}
	manager := state.NewManager()
	input := state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ID: credential.GroupID, Name: "codex", ChannelID: channel.Codex,
			ConnectionType: "subscription", Params: json.RawMessage(`{}`),
			Models: []state.ModelConfig{{ID: "gpt-5", Alias: "public"}}, Enabled: true,
			Settings: config.Settings{state.SettingParameterOverrides: []any{
				map[string]any{"set": map[string]any{"temperature": 0.25}},
			}},
		}, {
			ID: credential.GroupID + 1, Name: "openai", ChannelID: channel.OpenAI,
			ConnectionType: "api_key", Params: json.RawMessage(`{}`),
			Models: []state.ModelConfig{{ID: "gpt-5", Alias: "public"}}, Enabled: true,
		}},
		Credentials: []state.CredentialConfig{{
			ID: ref.ID, GroupID: ref.GroupID, Status: state.CredentialStatusActive,
			Version: ref.Version, IdentityGeneration: ref.IdentityGeneration, Fingerprint: ref.Fingerprint,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: encryption.Hash("gl-search-client"), Status: state.AccessKeyStatusActive,
		}},
	}
	if _, err := manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	if candidates := manager.Current().ExecutionCandidates[protocol.OpenAIResponses][execution.Operation("web_search")]["public"]; len(candidates) != 1 || candidates[0].GroupID != credential.GroupID {
		t.Fatalf("search candidates must contain only Codex: %#v", candidates)
	}
	quota := accessquota.NewRuntime()
	if err := quota.Reconcile(map[uint][]accessquota.Rule{1: {{
		ID: 1, Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 100,
	}}}); err != nil {
		t.Fatal(err)
	}
	ticket, decision := quota.Admit(1, time.Now())
	if !decision.Allowed {
		t.Fatal("quota admission failed")
	}
	quota.Complete(ticket, 100)
	sink := &searchRequestLogSink{}
	stats := health.NewStatsStore()
	handler := gateway.NewHandler(manager, registry, encryption, gateway.NewExecutionForwarder(adapter),
		dialect.NewSet(dialect.NewOpenAIResponses()), stats, health.NewMutationCoordinator(), nil, sink, nil, quota)
	engine := gin.New()
	routes, err := httproute.NewRegistry(handler.HTTPModule())
	if err != nil {
		t.Fatal(err)
	}
	if err := routes.Bind(engine); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"search-session","model":"public","commands":{"search_query":[{"q":"OpenAI"}]},"settings":{"external_web_access":true},"future_field":{"preserved":true}}`
	request := httptest.NewRequest(http.MethodPost, "/v1/alpha/search", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer gl-search-client")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != responseBody {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if fake.calls != 1 || fake.request.RequestPath != "/v1/alpha/search" ||
		!bytes.Contains(fake.request.Payload, []byte(`"model":"gpt-5"`)) ||
		!bytes.Contains(fake.request.Payload, []byte(`"future_field":{"preserved":true}`)) ||
		bytes.Contains(fake.request.Payload, []byte(`"stream"`)) || bytes.Contains(fake.request.Payload, []byte(`"temperature"`)) {
		t.Fatalf("search executor request = calls=%d %#v", fake.calls, fake.request)
	}
	if len(sink.events) != 1 || sink.events[0].Operation != execution.Operation("web_search") ||
		sink.events[0].Stream || sink.events[0].Usage.Result.State != usage.StateNotApplicable ||
		sink.events[0].Usage.Pricing.CostState != string(pricing.CostStateNotApplicable) {
		t.Fatalf("search request events = %#v", sink.events)
	}
	if got := stats.Snapshot(ref.ID, time.Now()); got != (health.CredentialStats{}) {
		t.Fatalf("search polluted model statistics: %#v", got)
	}
	for _, test := range []struct {
		name, method, body, token string
		status                    int
	}{
		{name: "authentication", method: http.MethodPost, body: body, status: http.StatusUnauthorized},
		{name: "method", method: http.MethodGet, token: "gl-search-client", status: http.StatusMethodNotAllowed},
		{name: "streaming", method: http.MethodPost, body: `{"id":"session","model":"public","stream":true}`, token: "gl-search-client", status: http.StatusBadRequest},
		{name: "missing id", method: http.MethodPost, body: `{"model":"public"}`, token: "gl-search-client", status: http.StatusBadRequest},
		{name: "unknown model", method: http.MethodPost, body: `{"id":"session","model":"unconfigured"}`, token: "gl-search-client", status: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "/v1/alpha/search", strings.NewReader(test.body))
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.status || fake.calls != 1 {
				t.Fatalf("response = %d %s, executor calls = %d", response.Code, response.Body.String(), fake.calls)
			}
		})
	}
	readFailure := &fakeExecutor{result: codex.ExecuteResponse{StatusCode: http.StatusOK, Headers: http.Header{"Retry-After": {"17"}}}, err: io.ErrUnexpectedEOF}
	setCodexExecutor(t, adapter, readFailure)
	request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer gl-search-client")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway || readFailure.calls != 1 ||
		!strings.Contains(response.Body.String(), `"code":"upstream_protocol_error"`) {
		t.Fatalf("failed successful body = %d %s, calls = %d", response.Code, response.Body.String(), readFailure.calls)
	}
	setCodexExecutor(t, adapter, fake)
	input.AccessKeys[0].Filters = state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.Anthropic: {}}}
	if _, err := manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer gl-search-client")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || fake.calls != 1 {
		t.Fatalf("protocol filter response = %d %s, executor calls = %d", response.Code, response.Body.String(), fake.calls)
	}
}

func TestCodexStandaloneSearchAdapterPreservesHTTPStatusAndErrors(t *testing.T) {
	adapter, _, _, encryption, credential := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	spec := validSpec(t, credential, encryption)
	spec.Operation, spec.Path = execution.OperationWebSearch, "/v1/alpha/search"
	spec.Body = []byte(`{"id":"session","model":"gpt-5","commands":{"open":[{"ref_id":"turn0search0"}]}}`)
	for _, status := range []int{http.StatusCreated, http.StatusNotFound, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			body := `{"output":"result"}`
			fake := &fakeExecutor{result: codex.ExecuteResponse{StatusCode: status, Payload: []byte(body)}}
			if status >= http.StatusBadRequest {
				body = `{"error":{"message":"search unavailable","search_detail":"preserved"}}`
				fake.result.Payload = []byte(body)
				fake.err = statusError{status: status, message: body}
			}
			setCodexExecutor(t, adapter, fake)
			result := adapter.Execute(t.Context(), spec)
			if result.StatusCode != status || string(result.Body) != body || result.Usage != nil || fake.calls != 1 {
				t.Fatalf("search result = %#v, calls = %d", result, fake.calls)
			}
		})
	}
}
