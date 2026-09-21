package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/automodel"
	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/jev"
	"gpt-load/internal/parameteroverride"
	"gpt-load/internal/requestaudit"
	"gpt-load/internal/state"
)

const auditPass = `{"answers":{"personal_data":{"type":"noul","noul":0.01},"prompt_injection":{"type":"noul","noul":0.01}}}`
const auditHit = `{"answers":{"personal_data":{"type":"noul","noul":0.99},"prompt_injection":{"type":"noul","noul":0.01}}}`

func TestProtectedJevContentPreservesNumericIdentity(t *testing.T) {
	if sameDecisionContent([]byte(`{"state":{"value":9007199254740992},"questions":{}}`), []byte(`{"state":{"value":9007199254740993},"questions":{}}`)) {
		t.Fatal("numeric precision loss hid a content override")
	}
}

func auditReply(body string) UpstreamResult {
	return UpstreamResult{StatusCode: 200, Header: http.Header{}, Body: []byte(body)}
}

func auditEngine(t *testing.T, forwarder *scriptedForwarder) (*Handler, *gin.Engine) {
	t.Helper()
	h, manager, registry := newHandlerForTest(t, forwarder, "answer-key")
	configureAutoModelTest(t, h, manager, state.FilterSet{})
	if err := registry.ReplaceCredentials([]state.CredentialEntry{testCredentialEntry(t, h.encryption, 1, 1, "answer-key"), testCredentialEntry(t, h.encryption, 3, 2, "decision-key")}); err != nil {
		t.Fatal(err)
	}
	cfg := requestaudit.DefaultConfig()
	cfg.Enabled = true
	for i := range cfg.Rules {
		cfg.Rules[i].Action = requestaudit.ActionBlock
	}
	manager.Current().RequestAudit = cfg
	manager.Current().Jev = jev.Config{Model: "jev-router", GroupID: 2, TimeoutSeconds: 2}
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, h)
	return h, engine
}

func sendAuditRequest(engine *gin.Engine, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer gl-client")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, r)
	return w
}

func TestRequestAuditOutcomesAndExplicitRoute(t *testing.T) {
	for _, test := range []struct {
		name, answer, action string
		status, calls        int
	}{
		{"passed", auditPass, "block", 200, 2},
		{"blocked", auditHit, "block", 403, 1},
		{"warned", auditHit, "warn", 200, 2},
		{"below threshold", `{"answers":{"personal_data":{"type":"noul","noul":0.5},"prompt_injection":{"type":"noul","noul":0.01}}}`, "block", 200, 2},
		{"invalid", `{"answers":{}}`, "warn", 503, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{auditReply(test.answer), auditReply(`{"choices":[]}`)}}
			h, engine := auditEngine(t, forwarder)
			h.manager.Current().RequestAudit.Rules[0].Action = test.action
			response := sendAuditRequest(engine, `{"model":"gpt-4o","messages":[{"role":"user","content":"password=example-value"}]}`)
			if response.Code != test.status || len(forwarder.inputs) != test.calls {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, len(forwarder.inputs), response.Body)
			}
			if forwarder.inputs[0].Group.ID != 2 || forwarder.inputs[0].Operation != execution.OperationDecisionsCreate {
				t.Fatal("guardrail escaped its configured route")
			}
		})
	}
}

func TestRequestAuditInspectsParameterOverrides(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{auditReply(auditHit)}}
	h, engine := auditEngine(t, forwarder)
	var raw any
	if err := json.Unmarshal([]byte(`[{"set":{"messages":[{"role":"user","content":"overridden-content"}]}}]`), &raw); err != nil {
		t.Fatal(err)
	}
	rules, err := parameteroverride.Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	group := h.manager.Current().Groups[1]
	group.ParameterOverrides = rules
	h.manager.Current().Groups[1] = group
	response := sendAuditRequest(engine, `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
	if response.Code != 403 || len(forwarder.inputs) != 1 || !bytes.Contains(forwarder.inputs[0].Request.Body, []byte("overridden-content")) {
		t.Fatalf("final content escaped review: %d / %d", response.Code, len(forwarder.inputs))
	}
}

func TestRequestAuditReusesHistoryButChecksNewToolContent(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{auditReply(auditPass), auditReply(`{"choices":[]}`), auditReply(`{"choices":[]}`), auditReply(auditHit)}}
	_, engine := auditEngine(t, forwarder)
	for range 2 {
		if r := sendAuditRequest(engine, `{"model":"gpt-4o","messages":[{"role":"user","content":"read config"}]}`); r.Code != 200 {
			t.Fatal(r.Body)
		}
	}
	if len(forwarder.inputs) != 3 {
		t.Fatal("unchanged history triggered another Jev call")
	}
	r := sendAuditRequest(engine, `{"model":"gpt-4o","messages":[{"role":"user","content":"read config"},{"role":"tool","content":"new-tool-result"}]}`)
	if r.Code != 403 || len(forwarder.inputs) != 4 {
		t.Fatal("new tool content reused an earlier review")
	}
}

func TestRequestAuditOneCallAcrossRetriesAndChangedContentFails(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{auditReply(auditPass)}}
	h, _ := auditEngine(t, forwarder)
	snapshot := h.manager.Current()
	recorder := &requestRecorder{}
	admit := func() *reason { return nil }
	body := []byte(`{"input":"original"}`)
	for range 2 {
		if failure := h.checkRequestAudit(t.Context(), snapshot, snapshot.AccessKeysByID[1], execution.OperationResponsesCreate, body, recorder, admit); failure != nil {
			t.Fatal(failure)
		}
	}
	failure := h.checkRequestAudit(t.Context(), snapshot, snapshot.AccessKeysByID[1], execution.OperationResponsesCreate, []byte(`{"input":"changed"}`), recorder, admit)
	if failure != &reasonAuditIncomplete || recorder.audit.Reason != "content_changed" || len(forwarder.inputs) != 1 {
		t.Fatal("retry changed content or caused a second review")
	}
}

func TestRequestAuditAncillaryContentIsReviewedAndResourceOperationsSkip(t *testing.T) {
	for _, operation := range []execution.Operation{execution.OperationCountTokens, execution.OperationResponsesCompact, execution.OperationResponsesInputTokens, execution.OperationEmbeddingsCreate} {
		forwarder := &scriptedForwarder{results: []UpstreamResult{auditReply(auditHit)}}
		h, _ := auditEngine(t, forwarder)
		s := h.manager.Current()
		failure := h.checkRequestAudit(t.Context(), s, s.AccessKeysByID[1], operation, []byte(`{"input":"new-content","previous_response_id":"opaque"}`), &requestRecorder{}, func() *reason { return nil })
		if failure != &reasonAuditBlocked || len(forwarder.inputs) != 1 {
			t.Fatalf("content-bearing operation skipped: %s", operation)
		}
	}
	for _, operation := range []execution.Operation{execution.OperationListModels, execution.OperationResponsesRetrieve, execution.OperationResponsesCancel, execution.OperationResponsesDelete} {
		if auditHasContent(operation, []byte(`{}`)) {
			t.Fatal("resource operation reviewed")
		}
	}
}

type auditWebsocketForwarder struct {
	*scriptedForwarder
	opener interface {
		OpenWebsocket(context.Context, ForwardInput) (execution.WebsocketSession, execution.WebsocketResult)
	}
}

func (f *auditWebsocketForwarder) OpenWebsocket(ctx context.Context, input ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
	return f.opener.OpenWebsocket(ctx, input)
}

func TestRequestAuditWebsocketChecksCurrentPayloadOnEveryTurn(t *testing.T) {
	upstream := websocketSettingsUpstream(t, false)
	h, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	cfg := requestaudit.DefaultConfig()
	cfg.Enabled = true
	cfg.Rules[0].Action = requestaudit.ActionBlock
	input.RequestAudit = &cfg
	input.Jev = &jev.Config{Model: "jev-latest", GroupID: 2, TimeoutSeconds: 2}
	input.Groups = append(input.Groups, state.GroupConfig{ID: 2, Name: "jev", ChannelID: channel.Jev, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "jev-latest"}}, Enabled: true})
	input.Credentials = append(input.Credentials, testCredentialConfig(2, 2))
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	if err := h.registry.(*state.CredentialRegistry).ReplaceCredentials([]state.CredentialEntry{testCredentialEntry(t, h.encryption, 1, 1, "upstream-key"), testCredentialEntry(t, h.encryption, 2, 2, "decision-key")}); err != nil {
		t.Fatal(err)
	}
	forwarder := &auditWebsocketForwarder{scriptedForwarder: &scriptedForwarder{results: []UpstreamResult{auditReply(auditPass), auditReply(auditHit)}}, opener: h.forwarder.(interface {
		OpenWebsocket(context.Context, ForwardInput) (execution.WebsocketSession, execution.WebsocketResult)
	})}
	h.forwarder = forwarder
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	for index, content := range []string{"hello", "new-tool-result"} {
		payload := map[string]any{"type": "response.create", "model": "public", "input": content, "store": false}
		if index == 1 {
			payload["previous_response_id"] = "resp_0"
		}
		if err := conn.WriteJSON(payload); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, body, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		want := `"type":"response.completed"`
		if index == 1 {
			want = "request_audit_blocked"
		}
		if !strings.Contains(string(body), want) {
			t.Fatalf("unexpected WebSocket result %s", body)
		}
	}
	if len(forwarder.inputs) != 2 || bytes.Contains(forwarder.inputs[1].Request.Body, []byte("hello")) {
		t.Fatal("continuation retained history or skipped new content")
	}
}
func TestSharedJevPreservesSanitizedResponseMetadata(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: 200, Header: http.Header{"X-Request-Id": {"decision-key-header"}},
		Body:                  []byte(`{"model":"decision-key-body","answers":{"preset":{"choice":"balanced","confidence":0.9}}}`),
		ResponseModelObserved: true, UpstreamReportedModel: "decision-key-effective-model", UpstreamRequestID: "decision-key-effective-request",
	}}}
	h, _ := auditEngine(t, forwarder)
	snapshot := h.manager.Current()
	entry, _ := snapshot.AutoModels.Lookup("auto-probe")
	decision := h.executeAutoDecision(t.Context(), snapshot, snapshot.AccessKeysByID[1], entry.Presets, automodel.TaskState{CurrentTask: "hello"})
	if strings.Contains(decision.ReportedModel, "decision-key") || strings.Contains(decision.RequestID, "decision-key") || !strings.HasSuffix(decision.ReportedModel, "-effective-model") || !strings.HasSuffix(decision.RequestID, "-effective-request") {
		t.Fatalf("unsafe or overwritten decision metadata: %q %q", decision.ReportedModel, decision.RequestID)
	}
}
