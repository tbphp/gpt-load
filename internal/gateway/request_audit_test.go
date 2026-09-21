package gateway

import (
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

func TestRequestAuditLocalBlockPreventsEveryUpstreamCall(t *testing.T) {
	for _, mode := range []string{"observe", "enforce"} {
		t.Run(mode, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{{StatusCode: 200, Header: http.Header{}, Body: []byte(`{"choices":[]}`)}}}
			handler, manager, _ := newHandlerForTest(t, forwarder, "key-a")
			config := requestaudit.DefaultConfig()
			config.Enabled = true
			config.Mode = mode
			manager.Current().RequestAudit = config
			engine := gin.New()
			bindGatewayRoutesForTest(t, engine, handler)
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"-----BEGIN PRIVATE KEY-----"}]}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if mode == "enforce" {
				if response.Code != 403 || len(forwarder.inputs) != 0 {
					t.Fatalf("blocked request sent: status %d calls %d", response.Code, len(forwarder.inputs))
				}
			} else if response.Code != 200 || len(forwarder.inputs) != 1 {
				t.Fatalf("observation changed request: %d", response.Code)
			}
		})
	}
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
	cfg.Mode = "enforce"
	cfg.SemanticEnabled = true
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

func TestRequestAuditSemanticOutcomesAndExplicitRoute(t *testing.T) {
	for _, test := range []struct {
		name, answer  string
		status, calls int
	}{
		{"passed", `{"answers":{"personal_data":{"type":"noul","noul":0.01},"prompt_injection":{"type":"noul","noul":0.01}}}`, 200, 2},
		{"matched", `{"answers":{"personal_data":{"type":"noul","noul":0.99},"prompt_injection":{"type":"noul","noul":0.01}}}`, 403, 1},
		{"uncertain", `{"answers":{"personal_data":{"type":"noul","noul":0.5},"prompt_injection":{"type":"noul","noul":0.01}}}`, 503, 1},
		{"invalid", `{"answers":{}}`, 503, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{{StatusCode: 200, Header: http.Header{}, Body: []byte(test.answer)}, {StatusCode: 200, Header: http.Header{}, Body: []byte(`{"choices":[]}`)}}}
			_, engine := auditEngine(t, forwarder)
			response := sendAuditRequest(engine, `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
			if response.Code != test.status || len(forwarder.inputs) != test.calls {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, len(forwarder.inputs), response.Body)
			}
			if forwarder.inputs[0].Group.ID != 2 || forwarder.inputs[0].Operation != execution.OperationDecisionsCreate {
				t.Fatal("audit escaped its configured route")
			}
		})
	}
}

func TestRequestAuditChecksLocalSecretsBeforeAutomaticSelection(t *testing.T) {
	forwarder := &scriptedForwarder{}
	_, engine := auditEngine(t, forwarder)
	response := sendAuditRequest(engine, `{"model":"auto-probe","messages":[{"role":"tool","content":"-----BEGIN PRIVATE KEY-----"}]}`)
	if response.Code != 403 || len(forwarder.inputs) != 0 {
		t.Fatalf("secret reached decision model: %d / %d", response.Code, len(forwarder.inputs))
	}
}

func TestRequestAuditInspectsParameterOverrides(t *testing.T) {
	forwarder := &scriptedForwarder{}
	h, engine := auditEngine(t, forwarder)
	var raw any
	if err := json.Unmarshal([]byte(`[{"set":{"messages":[{"role":"user","content":"-----BEGIN PRIVATE KEY-----"}]}}]`), &raw); err != nil {
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
	if response.Code != 403 || len(forwarder.inputs) != 0 {
		t.Fatalf("overridden secret escaped: %d / %d", response.Code, len(forwarder.inputs))
	}
}

func TestRequestAuditDoesNotTrustCachedToolContinuations(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{StatusCode: 200, Header: http.Header{}, Body: []byte(`{"answers":{"personal_data":{"type":"noul","noul":0.01},"prompt_injection":{"type":"noul","noul":0.01}}}`)},
		{StatusCode: 200, Header: http.Header{}, Body: []byte(`{"choices":[]}`)},
	}}
	_, engine := auditEngine(t, forwarder)
	if r := sendAuditRequest(engine, `{"model":"gpt-4o","messages":[{"role":"user","content":"read config"}]}`); r.Code != 200 {
		t.Fatal(r.Body)
	}
	r := sendAuditRequest(engine, `{"model":"gpt-4o","messages":[{"role":"user","content":"read config"},{"role":"tool","content":"-----BEGIN PRIVATE KEY-----"}]}`)
	if r.Code != 403 || len(forwarder.inputs) != 2 {
		t.Fatal("new tool content reused an earlier audit")
	}
}

func TestRequestAuditWebsocketChecksEveryTurn(t *testing.T) {
	upstream := websocketSettingsUpstream(t, false)
	h, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
	cfg := requestaudit.DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = "enforce"
	input.RequestAudit = &cfg
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	for index, content := range []string{"hello", "-----BEGIN PRIVATE KEY-----"} {
		if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": content, "store": false}); err != nil {
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
