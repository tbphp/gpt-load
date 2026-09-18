package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/accessquota"
	"gpt-load/internal/automodel"
	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/state"
)

type autoDecisionClient func(*http.Request) (*http.Response, error)

func (call autoDecisionClient) Do(request *http.Request) (*http.Response, error) {
	return call(request)
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

func TestAutoModelWebsocketContinuationUsesOneDecision(t *testing.T) {
	upstream := websocketSettingsUpstream(t, false)
	handler, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	config := automodel.DefaultConfig()
	config.Enabled = true
	config.APIKey = "decision-secret"
	config.Models = []automodel.Entry{{ID: "auto-web", Name: "auto-web", Enabled: true, Fallback: "high", Presets: []automodel.Preset{{ID: "high", Name: "high", Description: "Complex tasks", Model: "public", ParameterOverrides: json.RawMessage(`[]`)}}}}
	input.AutoModel = &config
	if _, err := handler.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	handler.decisionClient = autoDecisionClient(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"answers":{"preset":{"choice":"high","confidence":0.9}},"usage":{"input_tokens":100,"output_tokens":0}}`))}, nil
	})
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	for index := range 2 {
		request := map[string]any{"type": "response.create", "model": "auto-web", "input": "task", "store": false}
		if index > 0 {
			request["previous_response_id"] = "resp_0"
		}
		if err := conn.WriteJSON(request); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, body, err := conn.ReadMessage()
		if err != nil || !strings.Contains(string(body), `"type":"response.completed"`) || !strings.Contains(string(body), `"model":"auto-web"`) {
			t.Fatalf("WebSocket automatic response=%s, %v", body, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("WebSocket binding repeated task classification")
	}
}

func configureAutoModelTest(t *testing.T, handler *Handler, manager *state.Manager, filters state.FilterSet) {
	t.Helper()
	config := automodel.DefaultConfig()
	config.Enabled = true
	config.APIKey = "decision-secret"
	config.Models = []automodel.Entry{{ID: "auto-probe", Name: "auto-probe", Enabled: true, Fallback: "balanced", Presets: []automodel.Preset{
		{ID: "balanced", Name: "balanced", Description: "Ordinary bounded implementation", Model: "gpt-4o", ParameterOverrides: json.RawMessage(`[{"match":{"protocol":"openai-completions"},"set":{"reasoning_effort":"medium"}}]`)},
	}}}
	_, err := manager.Publish(state.CompileInput{AutoModel: &config, ChannelRegistry: channel.NewRegistry(),
		Groups:      []state.GroupConfig{{ID: 1, Name: "openai", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true}},
		Credentials: []state.CredentialConfig{{ID: 1, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "credential-1"}, {ID: 2, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 2, Fingerprint: "credential-2"}},
		AccessKeys:  []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive, Filters: filters}},
	})
	if err != nil {
		t.Fatal(err)
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
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"model":"auto-probe","previous_response_id":"resp-before","input":"continue"}`))
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
