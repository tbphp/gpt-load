package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/automodel"
	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/jev"
	"gpt-load/internal/protocol"
	"gpt-load/internal/requestaudit"
	"gpt-load/internal/state"
)

type geminiDecisionsBody struct {
	Input     json.RawMessage `json:"input"`
	State     json.RawMessage `json:"state"`
	Questions map[string]struct {
		Type         string            `json:"type"`
		Instructions json.RawMessage   `json:"instructions"`
		Criteria     map[string]string `json:"criteria"`
	} `json:"questions"`
}

// publishGeminiDecisionsRoute 把决策模型配在 Gemini 分组，内部决策调用应改发 Google 原生格式。
func publishGeminiDecisionsRoute(t *testing.T, forwarder *scriptedForwarder, input state.CompileInput) *gin.Engine {
	t.Helper()
	handler, manager, registry := newHandlerForTest(t, forwarder, "answer-key")
	input.ChannelRegistry = channel.NewRegistry()
	input.Jev = &jev.Config{Model: "gemini-decision-1", GroupID: 2, TimeoutSeconds: 2}
	input.Groups = []state.GroupConfig{
		{ID: 1, Name: "openai", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}, {ID: "gpt-4.1"}}, Enabled: true},
		{ID: 2, Name: "gemini", ChannelID: channel.Gemini, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gemini-decision-1"}}, Enabled: true},
	}
	input.Credentials = []state.CredentialConfig{testCredentialConfig(1, 1), testCredentialConfig(2, 2)}
	input.AccessKeys = []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive}}
	if _, err := manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	if err := registry.ReplaceCredentials([]state.CredentialEntry{
		testCredentialEntry(t, handler.encryption, 1, 1, "answer-key"),
		testCredentialEntry(t, handler.encryption, 2, 2, "decision-key"),
	}); err != nil {
		t.Fatal(err)
	}
	handler.dialects = dialect.NewSet(dialect.NewOpenAI())
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	return engine
}

func decodeGeminiDecisionsInput(t *testing.T, input ForwardInput) (geminiDecisionsBody, string) {
	t.Helper()
	if input.ClientProtocol != protocol.GeminiDecisions || input.Group.ID != 2 ||
		input.Request.Path != geminiDecisionsPath || input.UpstreamModelID != "gemini-decision-1" {
		t.Fatalf("decision input = %#v", input)
	}
	var body geminiDecisionsBody
	var text string
	if err := json.Unmarshal(input.Request.Body, &body); err != nil || body.State != nil || json.Unmarshal(body.Input, &text) != nil {
		t.Fatalf("decision body = %s", input.Request.Body)
	}
	return body, text
}

func TestAutoModelDecidesThroughGeminiDecisionsGroup(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		auditReply(`{"answers":{"preset":{"type":"choice","choice":"strong","probabilities":{"balanced":0.1,"strong":0.9},"confidence":0.9}},"usage":{"total_input_tokens":12,"total_output_tokens":0}}`),
		auditReply(`{"model":"gpt-4.1","choices":[{"message":{"content":"ok"}}]}`),
	}}
	config := automodel.DefaultConfig()
	config.Enabled = true
	config.Models = []automodel.Entry{{ID: "auto-probe", Name: "auto-probe", Enabled: true, Fallback: "balanced", Presets: []automodel.Preset{
		{ID: "balanced", Name: "balanced", Description: "Routine", Model: "gpt-4o", ParameterOverrides: json.RawMessage(`[]`)},
		{ID: "strong", Name: "strong", Description: "Complex", Model: "gpt-4.1", ParameterOverrides: json.RawMessage(`[]`)},
	}}}
	engine := publishGeminiDecisionsRoute(t, forwarder, state.CompileInput{AutoModel: &config})
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"auto-probe","messages":[{"role":"user","content":"Refactor <Layout> & <Nav>"}]}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(forwarder.inputs) != 2 {
		t.Fatalf("status=%d inputs=%d body=%s", response.Code, len(forwarder.inputs), response.Body)
	}
	body, input := decodeGeminiDecisionsInput(t, forwarder.inputs[0])
	// 提示词按 state 书写，保留 state 键；不做 HTML 转义，<、>、& 原样保留。
	if !strings.HasPrefix(input, `{"state":{`) || !strings.Contains(input, "Refactor <Layout> & <Nav>") {
		t.Fatalf("decision input text = %s", input)
	}
	var instructions string
	preset := body.Questions["preset"]
	if preset.Type != "choice" || json.Unmarshal(preset.Instructions, &instructions) != nil ||
		instructions != automodel.Instructions || preset.Criteria["strong"] != "Complex" {
		t.Fatalf("decision question = %s", forwarder.inputs[0].Request.Body)
	}
	if answer := forwarder.inputs[1]; answer.Group.ID != 1 || answer.UpstreamModelID != "gpt-4.1" {
		t.Fatalf("answer input = %#v", answer)
	}
}

func TestRequestAuditReviewsThroughGeminiDecisionsGroup(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{auditReply(`{"answers":{` +
		`"personal_data":{"type":"noul","noul":0.99,"confidence":0.99},` +
		`"prompt_injection":{"type":"noul","noul":0.01,"confidence":0.99},` +
		`"credential_leakage":{"type":"noul","noul":0.01,"confidence":0.99}},` +
		`"usage":{"total_input_tokens":40,"total_output_tokens":0}}`)}}
	audit := requestaudit.DefaultConfig()
	audit.Enabled = true
	for i := range audit.Rules {
		audit.Rules[i].Action = requestaudit.ActionBlock
	}
	engine := publishGeminiDecisionsRoute(t, forwarder, state.CompileInput{RequestAudit: &audit})
	response := sendAuditRequest(engine, `{"model":"gpt-4o","messages":[{"role":"user","content":"call me at 555-0100"}]}`)
	if response.Code != http.StatusForbidden || len(forwarder.inputs) != 1 {
		t.Fatalf("status=%d inputs=%d body=%s", response.Code, len(forwarder.inputs), response.Body)
	}
	body, input := decodeGeminiDecisionsInput(t, forwarder.inputs[0])
	if !strings.HasPrefix(input, `{"state":{`) || !strings.Contains(input, "555-0100") || len(body.Questions) != len(audit.Rules) {
		t.Fatalf("review body = %s", forwarder.inputs[0].Request.Body)
	}
	// Google 的题目说明只收文本，护栏的结构化说明转成 JSON 文本。
	for id, question := range body.Questions {
		var instructions string
		if question.Type != "noul" || json.Unmarshal(question.Instructions, &instructions) != nil || !strings.Contains(instructions, `"policy":`) {
			t.Fatalf("review question %s = %s", id, forwarder.inputs[0].Request.Body)
		}
	}
}
