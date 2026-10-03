package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"gpt-load/internal/automodel"
	"gpt-load/internal/channel"
	"gpt-load/internal/policy"
	"gpt-load/internal/state"
)

func autoModelExclusionPolicy(model string) policy.BindingConfig {
	return policy.BindingConfig{Scope: "group", GroupID: 1, Config: []byte(`{
		"schema_version":1,"rules":[{"id":"exclude-auto","name":"Exclude automatic model",
		"domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"` + model + `"},
		"then":{"type":"exclude_candidate"}}]}`)}
}

func TestAutoModelPolicyHTTPUsesOriginalClientModel(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
		Body: []byte(`{"model":"gpt-4o","choices":[{"message":{"content":"ok"}}]}`),
	}}}
	handler, manager, _ := newHandlerForTest(t, forwarder, "key-a")
	configureAutoModelTest(t, handler, manager, state.FilterSet{}, autoModelExclusionPolicy("auto-probe"))
	handler.decisionClient = autoDecisionClient(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"answers":{"preset":{"choice":"balanced","confidence":0.9}}}`))}, nil
	})
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	for _, model := range []string{"auto-probe", "gpt-4o"} {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"`+model+`","messages":[{"role":"user","content":"task"}]}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if model == "auto-probe" {
			if response.Code != http.StatusServiceUnavailable || len(forwarder.inputs) != 0 {
				t.Fatalf("excluded auto model: status=%d attempts=%d body=%s", response.Code, len(forwarder.inputs), response.Body)
			}
		} else if response.Code != http.StatusOK || len(forwarder.inputs) != 1 {
			t.Fatalf("direct model: status=%d attempts=%d body=%s", response.Code, len(forwarder.inputs), response.Body)
		}
	}
}

func TestAutoModelPolicyWebsocketUsesOriginalClientModel(t *testing.T) {
	upstream := websocketSettingsUpstream(t, false)
	handler, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	config := automodel.DefaultConfig()
	config.Enabled, config.Model = true, "jev-router"
	config.Models = []automodel.Entry{{ID: "auto-web", Name: "auto-web", Enabled: true, Fallback: "balanced",
		Presets: []automodel.Preset{{ID: "balanced", Name: "balanced", Description: "Fallback", Model: "public", ParameterOverrides: json.RawMessage(`[]`)}}}}
	input.AutoModel = &config
	input.Groups = append(input.Groups, state.GroupConfig{
		ID: 99, Name: "jev", ChannelID: channel.Jev, ConnectionType: "api_key",
		Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "jev-latest", Alias: "jev-router"}}, Enabled: true,
	})
	input.PolicyBindings = []policy.BindingConfig{autoModelExclusionPolicy("auto-web")}
	if _, err := handler.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine)
	defer server.Close()
	connection := dialGatewayWebsocket(t, server.URL)
	defer connection.Close()
	for _, model := range []string{"auto-web", "public"} {
		body := `{"type":"response.create","model":"` + model + `","input":"task","store":false}`
		if err := connection.WriteMessage(websocket.TextMessage, []byte(body)); err != nil {
			t.Fatal(err)
		}
		_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, response, err := connection.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		wantType := `"type":"response.completed"`
		if model == "auto-web" {
			wantType = `"type":"error"`
		}
		if !strings.Contains(string(response), wantType) {
			t.Fatalf("model=%s response=%s", model, response)
		}
	}
}
