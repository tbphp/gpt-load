package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"gpt-load/internal/accessquota"
	"gpt-load/internal/automodel"
	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/usage"
)

type autoDecisionClient func(*http.Request) (*http.Response, error)

func (call autoDecisionClient) Decide(
	ctx context.Context,
	snapshot *state.ConfigSnapshot,
	_ state.AccessKeyView,
	presets []automodel.CompiledPreset,
	view automodel.TaskState,
) automodel.Decision {
	payload, decision := automodel.BuildDecisionRequest(snapshot.AutoModels, presets, view)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://decision.test/v1/systemone", bytes.NewReader(payload))
	if err != nil {
		decision.Reason = "invalid_request"
		return decision
	}
	response, err := call(request)
	if err != nil || response == nil {
		decision.Reason = "transport_error"
		return decision
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		decision.Reason = "invalid_response"
		return decision
	}
	observed, _ := dialect.NewDecisions().ExtractUsage(body)
	decision.Called = true
	decision = automodel.InterpretDecisionResponse(decision, presets, response.StatusCode, response.Header, body, observed)
	if decision.InputTokens != nil {
		decision.EstimatedCostNanoUSD = *decision.InputTokens * 42
		decision.CostState = "priced"
		decision.PricingCompleteness = "complete"
	}
	return decision
}

func TestAutoModelDecisionCostSettlesWhenAnswerFails(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"error":{"message":"invalid task"}}`)}}}
	handler, manager, _ := newHandlerForTest(t, forwarder, "key-a", "key-b")
	configureAutoModelTest(t, handler, manager, state.FilterSet{})
	runtime := accessquota.NewRuntime()
	if err := runtime.Reconcile(map[uint][]accessquota.Rule{1: {{ID: 1, Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 84000}}}); err != nil {
		t.Fatal(err)
	}
	handler.accessQuota = runtime
	calls := 0
	handler.decisionClient = autoDecisionClient(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"answers":{"preset":{"choice":"balanced","confidence":0.9}},"usage":{"input_tokens":2000,"output_tokens":0}}`))}, nil
	})
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	for _, want := range []int{400, 429} {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"auto-probe","messages":[{"role":"user","content":"task"}]}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("status=%d want=%d body=%s", response.Code, want, response.Body)
		}
	}
	if calls != 1 {
		t.Fatalf("quota admission happened after decision: calls=%d", calls)
	}
}

func TestAutoModelWebsocketContinuationClassifiesEachTask(t *testing.T) {
	upstream := websocketSettingsUpstream(t, false)
	handler, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	config := automodel.DefaultConfig()
	config.Enabled = true
	config.Model = "jev-router"
	config.Models = []automodel.Entry{{ID: "auto-web", Name: "auto-web", Enabled: true, Fallback: "high", Presets: []automodel.Preset{
		{ID: "low", Name: "low", Description: "Simple tasks", Model: "public", ParameterOverrides: json.RawMessage(`[]`)},
		{ID: "high", Name: "high", Description: "Complex tasks", Model: "public", ParameterOverrides: json.RawMessage(`[]`)},
	}}}
	input.AutoModel = &config
	input.Groups = append(input.Groups, state.GroupConfig{ID: 99, Name: "jev", ChannelID: channel.Jev, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "jev-latest", Alias: "jev-router"}}, Enabled: true})
	if _, err := handler.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	handler.manager.Current().Settings.RequestTimeout = time.Nanosecond
	var calls atomic.Int32
	handler.decisionClient = autoDecisionClient(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"answers":{"preset":{"choice":"high","confidence":0.9}},"usage":{"input_tokens":100,"output_tokens":0}}`))}, nil
	})
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	for index := range 3 {
		request := map[string]any{"type": "response.create", "model": "auto-web", "input": "task", "store": false}
		if index > 0 {
			request["previous_response_id"] = "resp_0"
		}
		if index == 2 {
			request["model"] = "public"
		}
		if err := conn.WriteJSON(request); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, body, err := conn.ReadMessage()
		if err != nil || !strings.Contains(string(body), `"type":"response.completed"`) || !strings.Contains(string(body), fmt.Sprintf(`"model":%q`, request["model"])) {
			t.Fatalf("WebSocket automatic response=%s, %v", body, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("new WebSocket tasks must be classified independently: calls=%d", calls.Load())
	}
}

func TestAutoModelWebsocketPrewarmDoesNotFreezeLaterTask(t *testing.T) {
	for _, withTask := range []bool{false, true} {
		t.Run(fmt.Sprintf("with_task=%t", withTask), func(t *testing.T) {
			requests := make(chan map[string]any, 4)
			var connections atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				connections.Add(1)
				for index := 0; ; index++ {
					var request map[string]any
					if conn.ReadJSON(&request) != nil {
						return
					}
					requests <- request
					response := map[string]any{"type": "response.completed", "response": map[string]any{"id": fmt.Sprintf("resp_%d", index), "object": "response", "status": "completed", "model": request["model"], "usage": map[string]int{"input_tokens": 2, "output_tokens": 0}}}
					if conn.WriteJSON(response) != nil {
						return
					}
				}
			}))
			defer upstream.Close()
			handler, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
			input.Groups[0].Models = append(input.Groups[0].Models, state.ModelConfig{ID: "upstream-strong", Alias: "strong"})
			config := automodel.DefaultConfig()
			config.Enabled, config.Model = true, "jev-router"
			config.Models = []automodel.Entry{{ID: "auto-web", Name: "auto-web", Enabled: true, Fallback: "balanced", Presets: []automodel.Preset{
				{ID: "balanced", Name: "balanced", Description: "Simple work", Model: "public", ParameterOverrides: json.RawMessage(`[]`)},
				{ID: "strong", Name: "strong", Description: "Complex work", Model: "strong", ParameterOverrides: json.RawMessage(`[]`)},
			}}}
			input.AutoModel = &config
			input.Groups = append(input.Groups, state.GroupConfig{ID: 99, Name: "jev", ChannelID: channel.Jev, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "jev-latest", Alias: "jev-router"}}, Enabled: true})
			if _, err := handler.manager.Publish(input); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			handler.decisionClient = autoDecisionClient(func(*http.Request) (*http.Response, error) {
				choice := "strong"
				if calls.Add(1) > 1 {
					choice = "balanced"
				}
				body := fmt.Sprintf(`{"answers":{"preset":{"choice":%q,"confidence":0.9}},"usage":{"input_tokens":100,"output_tokens":0}}`, choice)
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			sink := &recordingRequestLogSink{}
			handler.requestLogSink = sink
			server := httptest.NewServer(engine)
			defer server.Close()
			conn := dialGatewayWebsocket(t, server.URL)
			defer conn.Close()
			warmInput := `[]`
			if withTask {
				warmInput = `"Design the architecture"`
			}
			for index, body := range []string{
				fmt.Sprintf(`{"type":"response.create","model":"auto-web","generate":false,"input":%s,"store":false}`, warmInput),
				`{"type":"response.create","model":"auto-web","previous_response_id":"resp_0","input":"Design the architecture","store":false}`,
				`{"type":"response.create","model":"auto-web","previous_response_id":"resp_1","input":[{"type":"function_call_output","call_id":"call_1","output":"tool result"}],"store":false}`,
				`{"type":"response.create","model":"auto-web","previous_response_id":"resp_2","input":"Say hello","store":false}`,
			} {
				if conn.WriteMessage(websocket.TextMessage, []byte(body)) != nil {
					t.Fatal("write automatic request")
				}
				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				_, response, err := conn.ReadMessage()
				if err != nil || !strings.Contains(string(response), `"type":"response.completed"`) {
					t.Fatalf("turn %d response=%s error=%v", index, response, err)
				}
				request := <-requests
				want := []string{"upstream", "upstream-strong", "upstream-strong", "upstream"}[index]
				if withTask && index == 0 {
					want = "upstream-strong"
				}
				if request["model"] != want {
					t.Fatalf("turn %d model=%v want=%s", index, request["model"], want)
				}
				if index == 0 && request["generate"] != false {
					t.Fatal("prewarm payload was altered")
				}
			}
			events := waitWebsocketLogs(t, sink, 4)
			if calls.Load() != 2 || connections.Load() != 1 {
				t.Fatalf("decisions=%d connections=%d", calls.Load(), connections.Load())
			}
			sources := []string{"prewarm", "jev", "binding", "jev"}
			if withTask {
				sources[0], sources[1] = "jev", "task_cache"
			}
			for index, source := range sources {
				if events[index].AutoDecision == nil || events[index].AutoDecision.Source != source {
					t.Fatalf("turn %d decision=%#v", index, events[index].AutoDecision)
				}
			}
		})
	}
}

func configureAutoModelTest(t *testing.T, handler *Handler, manager *state.Manager, filters state.FilterSet) {
	t.Helper()
	config := automodel.DefaultConfig()
	config.Enabled = true
	config.Model = "jev-router"
	config.Models = []automodel.Entry{{ID: "auto-probe", Name: "auto-probe", Enabled: true, Fallback: "balanced", Presets: []automodel.Preset{
		{ID: "balanced", Name: "balanced", Description: "Ordinary bounded implementation", Model: "gpt-4o", ParameterOverrides: json.RawMessage(`[{"match":{"protocol":"openai-completions"},"set":{"reasoning_effort":"medium"}}]`)},
		{ID: "strong", Name: "strong", Description: "Complex architecture", Model: "gpt-4.1", ParameterOverrides: json.RawMessage(`[{"match":{"protocol":"openai-responses"},"set":{"reasoning":{"effort":"high"}}}]`)},
	}}}
	_, err := manager.Publish(state.CompileInput{AutoModel: &config, ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{
			{ID: 1, Name: "openai", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}, {ID: "gpt-4.1"}}, Enabled: true},
			{ID: 2, Name: "jev", ChannelID: channel.Jev, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "jev-latest", Alias: "jev-router"}}, Enabled: true},
		},
		Credentials: []state.CredentialConfig{{ID: 1, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "credential-1"}, {ID: 2, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 2, Fingerprint: "credential-2"}, {ID: 3, GroupID: 2, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 3, Fingerprint: "credential-3"}},
		AccessKeys:  []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive, Filters: filters}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAutoModelUsesInternalDecisionsRouteOutsideClientFilters(t *testing.T) {
	groupMultiplier, _ := pricing.ParsePriceMultiplier("2")
	accessMultiplier, _ := pricing.ParsePriceMultiplier("3")
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{
			StatusCode:            http.StatusOK,
			Header:                http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"decision-1"}},
			Body:                  []byte(`{"model":"jev-latest","answers":{"preset":{"choice":"strong","confidence":0.9}},"usage":{"input_tokens":7,"output_tokens":1}}`),
			Usage:                 usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 7, Output: 1}},
			UpstreamReportedModel: "decision-key-model",
			ResponseModelObserved: true,
			UpstreamProtocol:      protocol.Decisions,
			UpstreamRequestID:     "decision-key-request",
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       []byte(`{"model":"gpt-4.1","choices":[{"message":{"content":"ok"}}]}`),
		},
	}}
	handler, manager, registry := newHandlerForTest(t, forwarder, "answer-key")
	config := automodel.DefaultConfig()
	config.Enabled = true
	config.Model = "jev-router"
	config.Models = []automodel.Entry{{
		ID: "auto-probe", Name: "auto-probe", Enabled: true, Fallback: "balanced",
		Presets: []automodel.Preset{
			{ID: "balanced", Name: "balanced", Description: "Routine", Model: "gpt-4o", ParameterOverrides: json.RawMessage(`[]`)},
			{ID: "strong", Name: "strong", Description: "Complex", Model: "gpt-4.1", ParameterOverrides: json.RawMessage(`[]`)},
		},
	}}
	input := state.CompileInput{
		AutoModel:       &config,
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{
			{ID: 1, Name: "answers", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}, {ID: "gpt-4.1"}}, Enabled: true},
			{ID: 2, Name: "decisions", ChannelID: channel.Jev, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "jev-latest", Alias: "jev-router"}}, PriceMultiplier: &groupMultiplier, Enabled: true},
		},
		Credentials: []state.CredentialConfig{testCredentialConfig(1, 1), testCredentialConfig(2, 2)},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive, PriceMultiplier: &accessMultiplier,
			Filters: state.FilterSet{
				Groups:    map[uint]struct{}{1: {}},
				Protocols: map[protocol.Protocol]struct{}{protocol.OpenAICompletions: {}},
				Models:    map[string]struct{}{"auto-probe": {}, "gpt-4o": {}, "gpt-4.1": {}},
			},
		}},
	}
	if _, err := manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	if err := registry.ReplaceCredentials([]state.CredentialEntry{
		testCredentialEntry(t, handler.encryption, 1, 1, "answer-key"),
		testCredentialEntry(t, handler.encryption, 2, 2, "decision-key"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.IncrFailure(2); !ok {
		t.Fatal("cannot seed decision credential failure")
	}
	table, err := pricing.NewTable([]pricing.Rule{{
		Identity: pricing.Identity{ChannelID: string(channel.Jev), ModelID: "jev-latest"},
		Prices:   pricing.Prices{Input: pricing.Price{NanoUSDPerMillion: 42_000_000, Set: true}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	replacementTable, err := pricing.NewTable([]pricing.Rule{{
		Identity: pricing.Identity{ChannelID: string(channel.Jev), ModelID: "jev-latest"},
		Prices:   pricing.Prices{Input: pricing.Price{NanoUSDPerMillion: 420_000_000, Set: true}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	priceTables := &mutableGatewayPriceTableProvider{table: table}
	handler.priceTables = priceTables
	forwarder.onCall = func(index int) {
		if index == 0 {
			priceTables.Publish(replacementTable)
		}
	}
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	handler.dialects = dialect.NewSet(dialect.NewOpenAI())
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"auto-probe","messages":[{"role":"user","content":"Design a migration"}]}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(forwarder.inputs) != 2 {
		t.Fatalf("status=%d inputs=%d body=%s", response.Code, len(forwarder.inputs), response.Body)
	}
	decisionInput, answerInput := forwarder.inputs[0], forwarder.inputs[1]
	if decisionInput.ClientProtocol != protocol.Decisions || decisionInput.Operation != execution.OperationDecisionsCreate ||
		decisionInput.Group.ID != 2 || decisionInput.Credential.ID != 2 || decisionInput.ExternalModel != "jev-router" ||
		decisionInput.UpstreamModelID != "jev-latest" {
		t.Fatalf("decision input = %#v", decisionInput)
	}
	if answerInput.Group.ID != 1 || answerInput.UpstreamModelID != "gpt-4.1" {
		t.Fatalf("answer input = %#v", answerInput)
	}
	events := sink.snapshot()
	if len(events) != 1 || events[0].AutoDecision == nil ||
		events[0].AutoDecision.EstimatedCostNanoUSD != 1_764 ||
		events[0].AutoDecision.GroupID != 2 || events[0].AutoDecision.ChannelID != "jev" ||
		events[0].AutoDecision.CredentialID != 2 {
		t.Fatalf("decision observation = %#v", events)
	}
	var decisionReceipt pricing.Receipt
	if err := json.Unmarshal(events[0].AutoDecision.Receipt, &decisionReceipt); err != nil {
		t.Fatal(err)
	}
	var inputRate *int64
	for _, line := range decisionReceipt.LineItems {
		if line.Code == "input" {
			inputRate = line.RateNanoUSDPerMillion
		}
	}
	if inputRate == nil || *inputRate != 42_000_000 {
		t.Fatalf("decision receipt = %#v", decisionReceipt)
	}
	encoded, err := json.Marshal(events[0].AutoDecision)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("decision-key")) {
		t.Fatalf("decision observation leaked its credential: %s", encoded)
	}
	for _, credential := range registry.Snapshot() {
		if credential.ID == 2 && credential.FailureCount != 0 {
			t.Fatalf("successful decision did not clear credential failures: %#v", credential)
		}
	}
}

func TestAutoModelLocalDecisionFailureIsNotBilledAsAnUpstreamCall(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		DispatchState: execution.DispatchNotSent,
		ExecutionError: &execution.ErrorEvidence{
			Kind: execution.ErrorKindInvalidRequest, OriginHint: execution.ErrorOriginInternal,
			ScopeHint: execution.ErrorScopeRequest, Summary: "invalid decision target",
		},
	}}}
	handler, manager, registry := newHandlerForTest(t, forwarder, "answer-key")
	config := automodel.DefaultConfig()
	config.Enabled = true
	config.Model = "jev-router"
	config.Models = []automodel.Entry{{
		ID: "auto-probe", Name: "auto-probe", Fallback: "balanced",
		Presets: []automodel.Preset{
			{ID: "balanced", Name: "balanced", Description: "Routine", Model: "gpt-4o", ParameterOverrides: json.RawMessage(`[]`)},
			{ID: "strong", Name: "strong", Description: "Complex", Model: "gpt-4.1", ParameterOverrides: json.RawMessage(`[]`)},
		},
	}}
	if _, err := manager.Publish(state.CompileInput{
		AutoModel: &config, ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{
			{ID: 1, Name: "answers", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}, {ID: "gpt-4.1"}}, Enabled: true},
			{ID: 2, Name: "decisions", ChannelID: channel.Jev, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "jev-latest", Alias: "jev-router"}}, Enabled: true},
		},
		Credentials: []state.CredentialConfig{testCredentialConfig(1, 1), testCredentialConfig(2, 2)},
		AccessKeys:  []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.ReplaceCredentials([]state.CredentialEntry{
		testCredentialEntry(t, handler.encryption, 1, 1, "answer-key"),
		testCredentialEntry(t, handler.encryption, 2, 2, "decision-key"),
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := manager.Current()
	entry, ok := snapshot.AutoModels.Lookup("auto-probe")
	if !ok {
		t.Fatal("automatic model is missing")
	}
	decision := handler.executeAutoDecision(
		t.Context(), snapshot, snapshot.AccessKeysByID[1], entry.Presets,
		automodel.TaskState{CurrentTask: "classify this task"},
	)
	if decision.Called || decision.Reason != "invalid_request" ||
		decision.CostState != "not_applicable" || decision.PricingCompleteness != "not_applicable" ||
		decision.EstimatedCostNanoUSD != 0 {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestAutoModelRunsOnceBeforeAnswerRetryAndPreservesAlias(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{StatusCode: 429, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"error":{"message":"rate limited"}}`)},
		{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"model":"gpt-4o","choices":[{"message":{"content":"ok"}}]}`)},
	}}
	handler, manager, _ := newHandlerForTest(t, forwarder, "key-a", "key-b")
	configureAutoModelTest(t, handler, manager, state.FilterSet{})
	calls := 0
	handler.decisionClient = autoDecisionClient(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"answers":{"preset":{"choice":"balanced","confidence":0.9}},"usage":{"input_tokens":2000,"output_tokens":0}}`))}, nil
	})
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"auto-probe","messages":[{"role":"user","content":"Implement the small parser"}],"reasoning_effort":"low","custom":{"keep":true}}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != 200 || calls != 1 || len(forwarder.inputs) != 2 {
		t.Fatalf("status=%d decisions=%d attempts=%d body=%s", response.Code, calls, len(forwarder.inputs), response.Body)
	}
	for _, input := range forwarder.inputs {
		if input.ExternalModel != "auto-probe" || !strings.Contains(string(input.Request.Body), `"reasoning_effort":"medium"`) || !strings.Contains(string(input.Request.Body), `"keep":true`) {
			t.Fatalf("answer parameters/alias changed incorrectly: %#v", input)
		}
	}
}

func TestAutoModelDecisionDoesNotCapGroupRequestTimeout(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"model":"gpt-4o","choices":[{"message":{"content":"ok"}}]}`)}}}
	handler, manager, _ := newHandlerForTest(t, forwarder, "key-a", "key-b")
	configureAutoModelTest(t, handler, manager, state.FilterSet{})
	// 模拟比目标 Group 更短的全局默认值；Group 已编译的执行超时保持不变。
	handler.manager.Current().Settings.RequestTimeout = time.Nanosecond
	handler.decisionClient = autoDecisionClient(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"answers":{"preset":{"choice":"balanced","confidence":0.9}},"usage":{"input_tokens":1,"output_tokens":0}}`))}, nil
	})
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"auto-probe","messages":[{"role":"user","content":"task"}]}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(forwarder.inputs) != 1 {
		t.Fatalf("status=%d attempts=%d body=%s", response.Code, len(forwarder.inputs), response.Body)
	}
}

func TestAutoModelCannotGrantTargetPermission(t *testing.T) {
	forwarder := &scriptedForwarder{}
	handler, manager, _ := newHandlerForTest(t, forwarder, "key-a", "key-b")
	configureAutoModelTest(t, handler, manager, state.FilterSet{Models: map[string]struct{}{"auto-probe": {}}})
	calls := 0
	handler.decisionClient = autoDecisionClient(func(*http.Request) (*http.Response, error) {
		calls++
		t.Fatal("unauthorized target must fail before decision")
		return nil, nil
	})
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"auto-probe","messages":[{"role":"user","content":"hello"}]}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code == 200 || calls != 0 || len(forwarder.inputs) != 0 {
		t.Fatal("auto entry bypassed target permission")
	}
}

func TestAutoModelResponsesContinuationReusesFrozenSelection(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"id":"resp-next","object":"response","model":"gpt-4o"}`)}}}
	handler, manager, _ := newHandlerForTest(t, forwarder, "key-a", "key-b")
	handler.dialects = dialect.NewSet(dialect.NewOpenAI(), dialect.NewOpenAIResponses())
	configureAutoModelTest(t, handler, manager, state.FilterSet{})
	selection := &automodel.Selection{EntryID: "auto-probe", EntryName: "auto-probe", PresetID: "old-preset", PresetName: "old preset", TargetModel: "gpt-4o", ParameterOverrides: json.RawMessage(`[{"match":{"protocol":"openai-responses"},"set":{"reasoning":{"effort":"high"}}}]`), ConfigRevision: 1}
	if !handler.responseBindings.Record(1, "resp-before", state.CredentialRef{ID: 1, GroupID: 1, IdentityGeneration: 1}, selection) {
		t.Fatal("cannot store binding")
	}
	handler.decisionClient = autoDecisionClient(func(*http.Request) (*http.Response, error) {
		t.Fatal("continuation must not reclassify")
		return nil, nil
	})
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"model":"auto-probe","previous_response_id":"resp-before","input":[{"type":"function_call_output","call_id":"call-1","output":"result"}]}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != 200 || len(forwarder.inputs) != 1 || !strings.Contains(string(forwarder.inputs[0].Request.Body), `"effort":"high"`) {
		t.Fatalf("continuation status=%d body=%s", response.Code, response.Body)
	}
	events := sink.snapshot()
	if len(events) != 1 || events[0].ClientModel != "auto-probe" || events[0].AutoDecision == nil || events[0].AutoDecision.Source != "binding" || events[0].AutoDecision.Selection.PresetID != "old-preset" {
		t.Fatalf("continuation observation = %#v", events)
	}
}

func TestAutoModelResponsesFullHistoryToolContinuationReusesMatchingTask(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"id":"resp-next","object":"response","model":"gpt-4o"}`)}}}
	handler, manager, _ := newHandlerForTest(t, forwarder, "key-a", "key-b")
	handler.dialects = dialect.NewSet(dialect.NewOpenAI(), dialect.NewOpenAIResponses())
	configureAutoModelTest(t, handler, manager, state.FilterSet{})
	initialBody := []byte(`{"model":"auto-probe","instructions":"keep compatibility","input":[{"role":"user","content":"fix the parser"}]}`)
	view, reason := automodel.Extract(protocol.OpenAIResponses, initialBody)
	if reason != "" || view.TaskFingerprint == "" {
		t.Fatalf("initial view=%#v reason=%q", view, reason)
	}
	selection := &automodel.Selection{EntryID: "auto-probe", EntryName: "auto-probe", PresetID: "balanced", PresetName: "balanced", TargetModel: "gpt-4o", ParameterOverrides: json.RawMessage(`[]`), ConfigRevision: manager.Current().Revision, TaskFingerprint: view.TaskFingerprint}
	if !handler.responseBindings.Record(1, "resp-before", state.CredentialRef{ID: 1, GroupID: 1, IdentityGeneration: 1}, selection) {
		t.Fatal("cannot store binding")
	}
	handler.decisionClient = autoDecisionClient(func(*http.Request) (*http.Response, error) {
		t.Fatal("matching tool continuation must reuse the bound preset")
		return nil, nil
	})
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"auto-probe","instructions":"keep compatibility","previous_response_id":"resp-before","input":[{"role":"user","content":"fix the parser"},{"type":"function_call","name":"test","arguments":"{}"},{"type":"function_call_output","call_id":"call-1","output":"tests failed"}]}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != 200 || len(forwarder.inputs) != 1 {
		t.Fatalf("status=%d inputs=%d body=%s", response.Code, len(forwarder.inputs), response.Body)
	}
	events := sink.snapshot()
	if len(events) != 1 || events[0].AutoDecision == nil || events[0].AutoDecision.Source != "binding" || events[0].AutoDecision.Reason != "same_task" {
		t.Fatalf("decision=%#v", events)
	}
}

func TestAutoModelResponsesFullHistoryToolContinuationReclassifiesDifferentTask(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"id":"resp-next","object":"response","model":"gpt-4.1"}`)}}}
	handler, manager, _ := newHandlerForTest(t, forwarder, "key-a", "key-b")
	handler.dialects = dialect.NewSet(dialect.NewOpenAI(), dialect.NewOpenAIResponses())
	configureAutoModelTest(t, handler, manager, state.FilterSet{})
	view, reason := automodel.Extract(protocol.OpenAIResponses, []byte(`{"model":"auto-probe","input":[{"role":"user","content":"fix the parser"}]}`))
	if reason != "" || view.TaskFingerprint == "" {
		t.Fatalf("initial view=%#v reason=%q", view, reason)
	}
	selection := &automodel.Selection{EntryID: "auto-probe", EntryName: "auto-probe", PresetID: "balanced", PresetName: "balanced", TargetModel: "gpt-4o", ParameterOverrides: json.RawMessage(`[]`), ConfigRevision: manager.Current().Revision, TaskFingerprint: view.TaskFingerprint}
	if !handler.responseBindings.Record(1, "resp-before", state.CredentialRef{ID: 1, GroupID: 1, IdentityGeneration: 1}, selection) {
		t.Fatal("cannot store binding")
	}
	calls := 0
	handler.decisionClient = autoDecisionClient(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"answers":{"preset":{"choice":"strong","confidence":0.9}},"usage":{"input_tokens":100,"output_tokens":0}}`))}, nil
	})
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"auto-probe","previous_response_id":"resp-before","input":[{"role":"user","content":"replace the parser"},{"type":"function_call_output","call_id":"call-1","output":"tests failed"}]}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != 200 || calls != 1 || len(forwarder.inputs) != 1 || forwarder.inputs[0].UpstreamModelID != "gpt-4.1" {
		t.Fatalf("status=%d calls=%d inputs=%#v body=%s", response.Code, calls, forwarder.inputs, response.Body)
	}
}

func TestAutoModelSkipsJevWhenOnlyFallbackPresetIsAvailable(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"model":"gpt-4o","choices":[{"message":{"content":"ok"}}]}`)}}}
	handler, manager, _ := newHandlerForTest(t, forwarder, "key-a")
	config := automodel.DefaultConfig()
	config.Enabled, config.Model = true, "jev-router"
	config.Models = []automodel.Entry{{ID: "auto-one", Name: "auto-one", Enabled: true, Fallback: "only", Presets: []automodel.Preset{{ID: "only", Name: "only", Description: "All permitted work", Model: "gpt-4o", ParameterOverrides: json.RawMessage(`[]`)}}}}
	_, err := manager.Publish(state.CompileInput{AutoModel: &config, ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{
			{ID: 1, Name: "openai", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true},
			{ID: 2, Name: "jev", ChannelID: channel.Jev, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "jev-latest", Alias: "jev-router"}}, Enabled: true},
		},
		Credentials: []state.CredentialConfig{{ID: 1, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "credential-1"}},
		AccessKeys:  []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive}},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler.decisionClient = autoDecisionClient(func(*http.Request) (*http.Response, error) {
		t.Fatal("single permitted preset must not call Jev")
		return nil, nil
	})
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"auto-one","messages":[{"role":"user","content":"task"}]}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != 200 || len(forwarder.inputs) != 1 {
		t.Fatalf("status=%d inputs=%d body=%s", response.Code, len(forwarder.inputs), response.Body)
	}
	events := sink.snapshot()
	if len(events) != 1 || events[0].AutoDecision == nil || events[0].AutoDecision.Source != "single_preset" || events[0].AutoDecision.Called {
		t.Fatalf("decision=%#v", events)
	}
}

func TestAutoModelResponsesNewTaskCanChangeModelOnBoundCredential(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"id":"resp-next","object":"response","model":"gpt-4.1"}`)}}}
	handler, manager, _ := newHandlerForTest(t, forwarder, "key-a", "key-b")
	handler.dialects = dialect.NewSet(dialect.NewOpenAI(), dialect.NewOpenAIResponses())
	configureAutoModelTest(t, handler, manager, state.FilterSet{})
	selection := &automodel.Selection{EntryID: "auto-probe", EntryName: "auto-probe", PresetID: "balanced", PresetName: "balanced", TargetModel: "gpt-4o", ParameterOverrides: json.RawMessage(`[]`), ConfigRevision: 1}
	if !handler.responseBindings.Record(1, "resp-before", state.CredentialRef{ID: 1, GroupID: 1, IdentityGeneration: 1}, selection) {
		t.Fatal("cannot store binding")
	}
	calls := 0
	handler.decisionClient = autoDecisionClient(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"answers":{"preset":{"choice":"strong","confidence":0.9}},"usage":{"input_tokens":100,"output_tokens":0}}`))}, nil
	})
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"auto-probe","previous_response_id":"resp-before","input":"Design a complex architecture","custom":{"keep":true}}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != 200 || calls != 1 || len(forwarder.inputs) != 1 {
		t.Fatalf("status=%d decisions=%d attempts=%d body=%s", response.Code, calls, len(forwarder.inputs), response.Body)
	}
	input := forwarder.inputs[0]
	if input.Credential.ID != 1 || input.UpstreamModelID != "gpt-4.1" || !strings.Contains(string(input.Request.Body), `"effort":"high"`) || !strings.Contains(string(input.Request.Body), `"previous_response_id":"resp-before"`) || !strings.Contains(string(input.Request.Body), `"keep":true`) {
		t.Fatalf("continuation input=%#v body=%s", input, input.Request.Body)
	}
	binding, found := handler.responseBindings.Lookup(1, "resp-next")
	if !found || binding.AutoSelection == nil || binding.AutoSelection.TargetModel != "gpt-4.1" || binding.CredentialID != 1 {
		t.Fatalf("new response binding=%#v found=%v", binding, found)
	}
}

func TestAutoModelContinuationDecisionExcludesOtherGroups(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"id":"resp-next","object":"response","model":"gpt-4o"}`)}}}
	handler, manager, _ := newHandlerForTest(t, forwarder, "key-a", "key-b")
	handler.dialects = dialect.NewSet(dialect.NewOpenAI(), dialect.NewOpenAIResponses())
	configureAutoModelTest(t, handler, manager, state.FilterSet{})
	config := manager.Current().AutoModels.Config()
	config.Models[0].Presets = append(config.Models[0].Presets, automodel.Preset{ID: "alternate", Name: "alternate", Description: "Another permitted task class", Model: "gpt-4o", ParameterOverrides: json.RawMessage(`[]`)})
	_, err := manager.Publish(state.CompileInput{AutoModel: &config, ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{
			{ID: 1, Name: "bound", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true},
			{ID: 2, Name: "other", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4.1"}}, Enabled: true},
			{ID: 3, Name: "jev", ChannelID: channel.Jev, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "jev-latest", Alias: "jev-router"}}, Enabled: true},
		},
		Credentials: []state.CredentialConfig{testCredentialConfig(1, 1)},
		AccessKeys:  []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 普通模型的响应也可以续接到自动入口，绑定仅限制凭据。
	if !handler.responseBindings.Record(1, "resp-before", state.CredentialRef{ID: 1, GroupID: 1, IdentityGeneration: 1}) {
		t.Fatal("cannot store ordinary response binding")
	}
	calls := 0
	handler.decisionClient = autoDecisionClient(func(request *http.Request) (*http.Response, error) {
		calls++
		var payload struct {
			Questions map[string]struct {
				Criteria map[string]string `json:"criteria"`
			} `json:"questions"`
		}
		if json.NewDecoder(request.Body).Decode(&payload) != nil {
			t.Fatal("cannot decode decision criteria")
		}
		if _, exists := payload.Questions["preset"].Criteria["strong"]; exists {
			t.Fatal("Jev received a preset outside the bound credential's Group")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"answers":{"preset":{"choice":"balanced","confidence":0.9}},"usage":{"input_tokens":100,"output_tokens":0}}`))}, nil
	})
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"auto-probe","previous_response_id":"resp-before","input":"A new task"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != 200 || calls != 1 || len(forwarder.inputs) != 1 || forwarder.inputs[0].Credential.ID != 1 || forwarder.inputs[0].UpstreamModelID != "gpt-4o" {
		t.Fatalf("bound decision status=%d calls=%d inputs=%#v body=%s", response.Code, calls, forwarder.inputs, response.Body)
	}
}

func TestAutoModelRequestsSupportFourTextProtocols(t *testing.T) {
	for _, test := range []struct{ name, path, body string }{
		{"chat", "/v1/chat/completions", `{"model":"auto-probe","messages":[{"role":"user","content":"task"}]}`},
		{"responses", "/v1/responses", `{"model":"auto-probe","input":"task","store":false}`},
		{"anthropic", "/v1/messages", `{"model":"auto-probe","max_tokens":32,"messages":[{"role":"user","content":"task"}]}`},
		{"gemini", "/v1beta/models/auto-probe:generateContent", `{"contents":[{"role":"user","parts":[{"text":"task"}]}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"model":"gpt-4o"}`)}}}
			handler, manager, _ := newHandlerForTest(t, forwarder, "key-a", "key-b")
			handler.dialects = dialect.NewSet(dialect.NewOpenAI(), dialect.NewOpenAIResponses(), dialect.NewAnthropic(), dialect.NewGemini())
			configureAutoModelTest(t, handler, manager, state.FilterSet{})
			calls := 0
			handler.decisionClient = autoDecisionClient(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"answers":{"preset":{"choice":"balanced","confidence":0.9}},"usage":{"input_tokens":1,"output_tokens":0}}`))}, nil
			})
			engine := gin.New()
			bindGatewayRoutesForTest(t, engine, handler)
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != 200 || calls != 1 || len(forwarder.inputs) != 1 || forwarder.inputs[0].UpstreamModelID != "gpt-4o" || forwarder.inputs[0].ExternalModel != "auto-probe" {
				t.Fatalf("protocol status=%d calls=%d inputs=%d body=%s", response.Code, calls, len(forwarder.inputs), response.Body)
			}
		})
	}
}

func TestAutoModelBindingDoesNotInterceptOrdinaryResponse(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("disabled=%t", disabled), func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"id":"resp-next","object":"response","model":"gpt-4.1"}`)}}}
			handler, manager, _ := newHandlerForTest(t, forwarder, "key-a", "key-b")
			handler.dialects = dialect.NewSet(dialect.NewOpenAI(), dialect.NewOpenAIResponses())
			configureAutoModelTest(t, handler, manager, state.FilterSet{})
			selection := &automodel.Selection{EntryID: "auto-probe", EntryName: "auto-probe", PresetID: "balanced", TargetModel: "gpt-4o", ParameterOverrides: json.RawMessage(`[]`)}
			if !handler.responseBindings.Record(1, "resp-before", state.CredentialRef{ID: 2, GroupID: 1, IdentityGeneration: 2}, selection) {
				t.Fatal("cannot store binding")
			}
			if disabled {
				config := manager.Current().AutoModels.Config()
				config.Enabled = false
				compiled, err := automodel.Compile(config, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				manager.Current().AutoModels = compiled
			}
			handler.decisionClient = autoDecisionClient(func(*http.Request) (*http.Response, error) {
				t.Fatal("ordinary model must not call Jev")
				return nil, nil
			})
			sink := &recordingRequestLogSink{}
			handler.requestLogSink = sink
			engine := gin.New()
			bindGatewayRoutesForTest(t, engine, handler)
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4.1","previous_response_id":"resp-before","input":"new task"}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != 200 || len(forwarder.inputs) != 1 || forwarder.inputs[0].APIKey != "key-b" || forwarder.inputs[0].UpstreamModelID != "gpt-4.1" {
				t.Fatalf("ordinary continuation status=%d body=%s inputs=%#v", response.Code, response.Body, forwarder.inputs)
			}
			if events := sink.snapshot(); len(events) != 1 || events[0].AutoDecision != nil {
				t.Fatalf("unexpected auto decision: %#v", events)
			}
		})
	}
}

func TestAutoModelReusesTaskAcrossStatelessToolRequests(t *testing.T) {
	for _, test := range []struct{ name, path, initial, continued string }{
		{"chat", "/v1/chat/completions", `{"model":"auto-probe","messages":[{"role":"user","content":"fix parser"}]}`, `{"model":"auto-probe","messages":[{"role":"user","content":"fix parser"},{"role":"tool","content":"tests failed"}]}`},
		{"responses", "/v1/responses", `{"model":"auto-probe","input":[{"role":"user","content":"fix parser"}],"store":false}`, `{"model":"auto-probe","input":[{"role":"user","content":"fix parser"},{"type":"function_call_output","call_id":"call-1","output":"tests failed"}],"store":false}`},
		{"anthropic", "/v1/messages", `{"model":"auto-probe","max_tokens":32,"messages":[{"role":"user","content":"fix parser"}]}`, `{"model":"auto-probe","max_tokens":32,"messages":[{"role":"user","content":"fix parser"},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-1","content":"tests failed"}]}]}`},
		{"gemini", "/v1beta/models/auto-probe:generateContent", `{"contents":[{"role":"user","parts":[{"text":"fix parser"}]}]}`, `{"contents":[{"role":"user","parts":[{"text":"fix parser"}]},{"role":"user","parts":[{"functionResponse":{"name":"test","response":{"text":"tests failed"}}}]}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{}
			for range 4 {
				forwarder.results = append(forwarder.results, UpstreamResult{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"model":"gpt-4.1"}`)})
			}
			handler, manager, _ := newHandlerForTest(t, forwarder, "key-a", "key-b")
			handler.dialects = dialect.NewSet(dialect.NewOpenAI(), dialect.NewOpenAIResponses(), dialect.NewAnthropic(), dialect.NewGemini())
			configureAutoModelTest(t, handler, manager, state.FilterSet{})
			calls := 0
			handler.decisionClient = autoDecisionClient(func(*http.Request) (*http.Response, error) {
				calls++
				choice := "strong"
				if calls > 1 {
					choice = "balanced"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"answers":{"preset":{"choice":%q,"confidence":0.9}},"usage":{"input_tokens":1,"output_tokens":0}}`, choice)))}, nil
			})
			sink := &recordingRequestLogSink{}
			handler.requestLogSink = sink
			engine := gin.New()
			bindGatewayRoutesForTest(t, engine, handler)
			for index, body := range []string{test.initial, test.continued, test.continued, test.initial} {
				request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(body))
				request.Header.Set("Authorization", "Bearer gl-client")
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)
				wantModel, wantCalls := "gpt-4.1", 1
				if index == 3 {
					wantModel, wantCalls = "gpt-4o", 2
				}
				if response.Code != 200 || calls != wantCalls || len(forwarder.inputs) != index+1 || forwarder.inputs[index].UpstreamModelID != wantModel {
					t.Fatalf("turn=%d status=%d calls=%d body=%s", index, response.Code, calls, response.Body)
				}
			}
			for _, event := range sink.snapshot()[1:3] {
				if event.AutoDecision == nil || event.AutoDecision.Source != "task_cache" || event.AutoDecision.Called || event.AutoDecision.EstimatedCostNanoUSD != 0 {
					t.Fatalf("reused selection must not bill a decision: %#v", event.AutoDecision)
				}
			}
		})
	}
}

func TestAutoModelCacheDoesNotReuseFailedOrForbiddenChoice(t *testing.T) {
	for _, failed := range []bool{true, false} {
		t.Run(fmt.Sprintf("failed=%t", failed), func(t *testing.T) {
			handler, manager, _ := newHandlerForTest(t, &scriptedForwarder{}, "key-a", "key-b")
			configureAutoModelTest(t, handler, manager, state.FilterSet{})
			calls := 0
			handler.decisionClient = autoDecisionClient(func(*http.Request) (*http.Response, error) {
				calls++
				status, choice := 200, "strong"
				if calls == 1 && failed {
					status = 503
				}
				if calls == 2 && !failed {
					choice = "balanced"
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"answers":{"preset":{"choice":%q,"confidence":0.9}}}`, choice)))}, nil
			})
			snapshot := manager.Current()
			key := snapshot.AccessKeysByID[1]
			selectedDialect := dialect.NewOpenAI()
			for index, body := range []string{
				`{"model":"auto-probe","messages":[{"role":"user","content":"fix parser"}]}`,
				`{"model":"auto-probe","messages":[{"role":"user","content":"fix parser"},{"role":"tool","content":"result"}]}`,
			} {
				if index == 1 && !failed {
					key.Filters.Models = map[string]struct{}{"auto-probe": {}, "gpt-4o": {}}
				}
				request := &dialect.ParsedRequest{Method: http.MethodPost, Path: "/v1/chat/completions", Body: []byte(body)}
				metadata, err := selectedDialect.InspectRequest(request)
				if err != nil {
					t.Fatal(err)
				}
				_, _, decision, failure := handler.prepareAutoModel(context.Background(), snapshot, key, selectedDialect, request, metadata, nil, func() *reason { return nil })
				if failure != nil || decision == nil {
					t.Fatalf("failure=%v decision=%#v", failure, decision)
				}
				if index == 1 && (decision.Source == "task_cache" || (!failed && decision.Selection.TargetModel != "gpt-4o")) {
					t.Fatalf("unsafe reused decision=%#v", decision)
				}
			}
			if failed && calls != 2 {
				t.Fatalf("failed decision was cached: calls=%d", calls)
			}
		})
	}
}

func TestAutoModelPrewarmReuseDoesNotFreezeNextUserTask(t *testing.T) {
	for _, test := range []struct{ name, warmInput, continuation string }{
		{"same task", `"fix parser"`, `"fix parser"`},
		{"incremental", `"fix parser"`, `[]`},
		{"new task", `"fix parser"`, `"different task"`},
		{"tool task", `[{"role":"user","content":"fix parser"},{"type":"function_call_output","call_id":"call-1","output":"result"}]`, `[]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, manager, _ := newHandlerForTest(t, &scriptedForwarder{}, "key-a", "key-b")
			configureAutoModelTest(t, handler, manager, state.FilterSet{})
			calls := 0
			handler.decisionClient = autoDecisionClient(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"answers":{"preset":{"choice":"strong","confidence":0.9}}}`))}, nil
			})
			selectedDialect := dialect.NewOpenAIResponses()
			var bound *automodel.Selection
			for index, body := range []string{
				fmt.Sprintf(`{"model":"auto-probe","input":%s,"generate":false}`, test.warmInput),
				fmt.Sprintf(`{"model":"auto-probe","input":%s,"previous_response_id":"resp-warm"}`, test.continuation),
				`{"model":"auto-probe","input":"fix parser","previous_response_id":"resp-answer"}`,
			} {
				request := &dialect.ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(body)}
				metadata, err := selectedDialect.InspectRequest(request)
				if err != nil {
					t.Fatal(err)
				}
				_, _, decision, failure := handler.prepareAutoModel(context.Background(), manager.Current(), manager.Current().AccessKeysByID[1], selectedDialect, request, metadata, bound, func() *reason { return nil })
				wantCalls := 1
				if index == 2 {
					wantCalls = 2
				}
				if test.continuation == `"different task"` && index > 0 {
					wantCalls++
				}
				if failure != nil || decision == nil || calls != wantCalls {
					t.Fatalf("turn=%d calls=%d failure=%v decision=%#v", index, calls, failure, decision)
				}
				bound = &decision.Selection
			}
		})
	}
}
