package gateway

import (
	"bytes"
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

func TestAutoModelWebsocketContinuationClassifiesEachTask(t *testing.T) {
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
	if calls.Load() != 2 {
		t.Fatalf("new WebSocket tasks must be classified independently: calls=%d", calls.Load())
	}
}

func TestAutoModelWebsocketPrewarmDoesNotFreezeLaterTask(t *testing.T) {
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
	config.Enabled, config.APIKey = true, "decision-secret"
	config.Models = []automodel.Entry{{ID: "auto-web", Name: "auto-web", Enabled: true, Fallback: "balanced", Presets: []automodel.Preset{
		{ID: "balanced", Name: "balanced", Description: "Simple work", Model: "public", ParameterOverrides: json.RawMessage(`[]`)},
		{ID: "strong", Name: "strong", Description: "Complex work", Model: "strong", ParameterOverrides: json.RawMessage(`[]`)},
	}}}
	input.AutoModel = &config
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
	for index, body := range []string{
		`{"type":"response.create","model":"auto-web","generate":false,"input":[],"store":false}`,
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
	for index, source := range []string{"prewarm", "jev", "binding", "jev"} {
		if events[index].AutoDecision == nil || events[index].AutoDecision.Source != source {
			t.Fatalf("turn %d decision=%#v", index, events[index].AutoDecision)
		}
	}
}

func configureAutoModelTest(t *testing.T, handler *Handler, manager *state.Manager, filters state.FilterSet) {
	t.Helper()
	config := automodel.DefaultConfig()
	config.Enabled = true
	config.APIKey = "decision-secret"
	config.Models = []automodel.Entry{{ID: "auto-probe", Name: "auto-probe", Enabled: true, Fallback: "balanced", Presets: []automodel.Preset{
		{ID: "balanced", Name: "balanced", Description: "Ordinary bounded implementation", Model: "gpt-4o", ParameterOverrides: json.RawMessage(`[{"match":{"protocol":"openai-completions"},"set":{"reasoning_effort":"medium"}}]`)},
		{ID: "strong", Name: "strong", Description: "Complex architecture", Model: "gpt-4.1", ParameterOverrides: json.RawMessage(`[{"match":{"protocol":"openai-responses"},"set":{"reasoning":{"effort":"high"}}}]`)},
	}}}
	_, err := manager.Publish(state.CompileInput{AutoModel: &config, ChannelRegistry: channel.NewRegistry(),
		Groups:      []state.GroupConfig{{ID: 1, Name: "openai", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}, {ID: "gpt-4.1"}}, Enabled: true}},
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
	_, err := manager.Publish(state.CompileInput{AutoModel: &config, ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{
			{ID: 1, Name: "bound", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true},
			{ID: 2, Name: "other", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4.1"}}, Enabled: true},
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
