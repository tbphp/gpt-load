package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/automodel"
	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/jev"
	"gpt-load/internal/parameteroverride"
	"gpt-load/internal/requestaudit"
	"gpt-load/internal/state"
)

const auditPass = `{"answers":{"personal_data":{"type":"noul","noul":0.01},"prompt_injection":{"type":"noul","noul":0.01},"credential_leakage":{"type":"noul","noul":0.01}}}`
const auditHit = `{"answers":{"personal_data":{"type":"noul","noul":0.99},"prompt_injection":{"type":"noul","noul":0.01},"credential_leakage":{"type":"noul","noul":0.01}}}`

func TestRequestAuditFinishesEveryBatchBeforeBusinessDispatch(t *testing.T) {
	for _, scenario := range []struct {
		name, answer, action string
		status               int
	}{
		{"pass", auditPass, "block", 200},
		{"late block", auditHit, "block", 403},
		{"late warning", auditHit, "warn", 200},
		{"late failure", `{"answers":{}}`, "warn", 200},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{}
			h, engine := auditEngine(t, forwarder)
			h.manager.Current().RequestAudit.Rules[0].Action = scenario.action
			messages := []any{}
			for i := range 80 {
				messages = append(messages, map[string]string{"role": "user", "content": fmt.Sprintf("message-%03d ", i) + strings.Repeat("text ", 350)})
			}
			body, _ := json.Marshal(map[string]any{"model": "gpt-4o", "messages": messages})
			seen := map[string]bool{}
			auditCalls, businessCalls := 0, 0
			forwarder.onCall = func(index int) {
				input := forwarder.inputs[index]
				if input.Operation != execution.OperationDecisionsCreate {
					businessCalls++
					if len(seen) != 80 {
						t.Errorf("business dispatched after reviewing only %d messages", len(seen))
					}
					forwarder.results = append(forwarder.results, auditReply(`{"choices":[]}`))
					return
				}
				auditCalls++
				if len(input.Request.Body) > requestaudit.MaxRequestBytes {
					t.Fatal("oversized Jev request")
				}
				for i := range 80 {
					marker := fmt.Sprintf("message-%03d", i)
					if bytes.Contains(input.Request.Body, []byte(marker)) {
						seen[marker] = true
					}
				}
				answer := auditPass
				if bytes.Contains(input.Request.Body, []byte("message-079")) {
					answer = scenario.answer
				}
				forwarder.results = append(forwarder.results, auditReply(answer))
			}
			response := sendAuditRequest(engine, string(body))
			if response.Code != scenario.status || auditCalls < 2 {
				t.Fatalf("status=%d audit calls=%d, want %d and multiple calls", response.Code, auditCalls, scenario.status)
			}
			if (businessCalls == 1) != (scenario.status == 200) {
				t.Fatalf("unexpected business calls: %d", businessCalls)
			}
		})
	}
}

func TestRequestAuditOverallBudgetAllowsBusinessRequest(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{auditReply(`{"choices":[]}`)}}
	h, engine := auditEngine(t, forwarder)
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	body, _ := json.Marshal(map[string]any{"model": "gpt-4o", "messages": []any{map[string]string{"role": "user", "content": strings.Repeat("x", 2<<20)}}})
	response := sendAuditRequest(engine, string(body))
	if response.Code != http.StatusOK || len(forwarder.inputs) != 1 || forwarder.inputs[0].Operation != execution.OperationChatCompletion || !bytes.Equal(forwarder.inputs[0].Request.Body, body) {
		t.Fatalf("budget failure: status=%d calls=%d", response.Code, len(forwarder.inputs))
	}
	events := sink.snapshot()
	if len(events) != 1 || events[0].RequestAudit == nil || events[0].RequestAudit.Status != "incomplete" || events[0].RequestAudit.Reason != "content_too_large" {
		t.Fatalf("review budget failure missing from request log: %+v", events)
	}
}

func TestRequestAuditBatchFailureKeepsFindingsAndCallObservations(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{auditReply(auditHit), auditReply(`{"answers":{}}`)}}
	h, _ := auditEngine(t, forwarder)
	snapshot := h.manager.Current()
	snapshot.RequestAudit.Rules[0].Action = requestaudit.ActionWarn
	body, _ := json.Marshal(map[string]any{"input": strings.Repeat("review this text ", 10000)})
	recorder := &requestRecorder{}
	failure := h.checkRequestAudit(t.Context(), snapshot, snapshot.AccessKeysByID[1], body, recorder, func() *reason { return nil })
	if failure != nil || recorder.audit.Status != "incomplete" || recorder.audit.Reason != "invalid_response" || len(recorder.audit.Findings) != 1 || len(recorder.audit.Calls) != 2 || len(recorder.auditCache) != 0 {
		t.Fatalf("partial review was lost or treated as complete: failure=%v audit=%+v", failure, recorder.audit)
	}
	for _, call := range recorder.audit.Calls {
		if !call.Called {
			t.Fatal("dispatched batch missing from call accounting")
		}
	}
}

func TestRequestAuditCancellationStopsRemainingBatches(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	forwarder := &scriptedForwarder{results: []UpstreamResult{auditReply(auditPass)}, onCall: func(int) { cancel() }}
	h, _ := auditEngine(t, forwarder)
	snapshot := h.manager.Current()
	body, _ := json.Marshal(map[string]any{"input": strings.Repeat("review this text ", 10000)})
	recorder := &requestRecorder{}
	failure := h.checkRequestAudit(ctx, snapshot, snapshot.AccessKeysByID[1], body, recorder, func() *reason { return nil })
	if failure != nil || recorder.audit.Reason != "canceled" || len(forwarder.inputs) != 1 || len(recorder.auditCache) != 0 {
		t.Fatalf("canceled review continued or was cached: failure=%v calls=%d", failure, len(forwarder.inputs))
	}
}

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
		{"below threshold", `{"answers":{"personal_data":{"type":"noul","noul":0.5},"prompt_injection":{"type":"noul","noul":0.01},"credential_leakage":{"type":"noul","noul":0.01}}}`, "block", 200, 2},
		{"invalid", `{"answers":{}}`, "warn", 200, 2},
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

func TestRequestAuditMixedResponsesContentStillChecksText(t *testing.T) {
	for _, input := range []string{
		`{"type":"reasoning","summary":[],"encrypted_content":"opaque-reasoning"}`,
		`{"role":"user","content":[{"type":"input_image","image_url":"opaque-image"}]}`,
		`{"role":"user","content":[{"type":"input_file","file_id":"opaque-file"}]}`,
		`{"type":"item_reference","id":"opaque-reference"}`,
	} {
		for _, scenario := range []struct {
			answer, status string
			code, calls    int
		}{
			{auditPass, "passed", http.StatusOK, 2},
			{auditHit, "blocked", http.StatusForbidden, 1},
		} {
			t.Run(scenario.status+input, func(t *testing.T) {
				forwarder := &scriptedForwarder{results: []UpstreamResult{auditReply(scenario.answer), auditReply(`{"id":"resp_example","output":[]}`)}}
				h, engine := auditEngine(t, forwarder)
				h.dialects = dialect.NewSet(dialect.NewOpenAIResponses())
				sink := &recordingRequestLogSink{}
				h.requestLogSink = sink
				body := `{"model":"gpt-4o","input":[` + input + `,{"role":"user","content":"review-this-text"}]}`
				request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
				request.Header.Set("Authorization", "Bearer gl-client")
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)
				if response.Code != scenario.code || len(forwarder.inputs) != scenario.calls {
					t.Fatalf("text review was skipped: status=%d calls=%d", response.Code, len(forwarder.inputs))
				}
				if forwarder.inputs[0].Operation != execution.OperationDecisionsCreate || !bytes.Contains(forwarder.inputs[0].Request.Body, []byte("review-this-text")) || bytes.Contains(forwarder.inputs[0].Request.Body, []byte("opaque-")) {
					t.Fatal("JEV received opaque content or missed readable text")
				}
				if scenario.code == http.StatusOK && (forwarder.inputs[1].Operation != execution.OperationResponsesCreate || string(forwarder.inputs[1].Request.Body) != body) {
					t.Fatal("filtering changed the business request")
				}
				events := sink.snapshot()
				if len(events) != 1 || events[0].RequestAudit == nil {
					t.Fatalf("missing request audit log: %+v", events)
				}
				audit := events[0].RequestAudit
				if audit.Status != scenario.status || audit.Reason != "" || len(audit.Calls) != 1 || len(events[0].Attempts) != scenario.calls-1 {
					t.Fatalf("text review outcome was lost: %+v", events[0])
				}
			})
		}
	}
}

func TestRequestAuditOpaqueOnlyContentDoesNotCallJev(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{auditReply(`{"id":"resp_example","output":[]}`)}}
	h, engine := auditEngine(t, forwarder)
	h.dialects = dialect.NewSet(dialect.NewOpenAIResponses())
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	body := `{"model":"gpt-4o","input":[{"type":"reasoning","summary":[],"encrypted_content":"opaque-reasoning"}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	events := sink.snapshot()
	if response.Code != http.StatusOK || len(forwarder.inputs) != 1 || forwarder.inputs[0].Operation != execution.OperationResponsesCreate || string(forwarder.inputs[0].Request.Body) != body || len(events) != 1 || events[0].RequestAudit != nil {
		t.Fatalf("opaque-only content was reviewed or changed: status=%d calls=%d events=%+v", response.Code, len(forwarder.inputs), events)
	}
}

func TestRequestAuditUnavailableJevAllowsBusinessRequest(t *testing.T) {
	for _, scenario := range []struct {
		reason string
		result UpstreamResult
	}{
		{"invalid_response", auditReply(`{"answers":{}}`)},
		{"upstream_error", UpstreamResult{StatusCode: http.StatusServiceUnavailable, Body: []byte(`{"error":"unavailable"}`)}},
		{"transport_error", UpstreamResult{Err: errors.New("connection unavailable")}},
		{"canceled", UpstreamResult{Err: context.Canceled}},
		{"timeout", UpstreamResult{Err: context.DeadlineExceeded}},
		{"no_candidate", UpstreamResult{}},
	} {
		t.Run(scenario.reason, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{scenario.result, auditReply(`{"choices":[]}`)}}
			h, engine := auditEngine(t, forwarder)
			wantCalls := 2
			if scenario.reason == "timeout" {
				h.manager.Current().Jev.TimeoutSeconds = 1
				forwarder.onCall = func(index int) {
					if index == 0 {
						time.Sleep(1100 * time.Millisecond)
					}
				}
			}
			if scenario.reason == "no_candidate" {
				if err := h.registry.(*state.CredentialRegistry).ReplaceCredentials([]state.CredentialEntry{testCredentialEntry(t, h.encryption, 1, 1, "answer-key")}); err != nil {
					t.Fatal(err)
				}
				forwarder.results = forwarder.results[1:]
				wantCalls = 1
			}
			sink := &recordingRequestLogSink{}
			h.requestLogSink = sink
			response := sendAuditRequest(engine, `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
			if response.Code != http.StatusOK || len(forwarder.inputs) != wantCalls || forwarder.inputs[wantCalls-1].Operation != execution.OperationChatCompletion {
				t.Fatalf("JEV failure blocked business request: status=%d calls=%d", response.Code, len(forwarder.inputs))
			}
			events := sink.snapshot()
			if len(events) != 1 || events[0].RequestAudit == nil || events[0].RequestAudit.Status != "incomplete" || events[0].RequestAudit.Reason != scenario.reason || len(events[0].RequestAudit.Calls) != 1 || events[0].ErrorCode != "" {
				t.Fatalf("JEV failure missing from successful request log: %+v", events)
			}
		})
	}
}

func TestRequestAuditFailureIsNotRepeatedOrCachedAsPassed(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{auditReply(`{"answers":{}}`), auditReply(auditPass)}}
	h, _ := auditEngine(t, forwarder)
	snapshot := h.manager.Current()
	body := []byte(`{"input":"review this"}`)
	admit := func() *reason { return nil }
	recorder := &requestRecorder{}
	for range 2 {
		if failure := h.checkRequestAudit(t.Context(), snapshot, snapshot.AccessKeysByID[1], body, recorder, admit); failure != nil {
			t.Fatal(failure)
		}
	}
	if recorder.audit.Status != "incomplete" || recorder.audit.Reason != "invalid_response" || len(forwarder.inputs) != 1 || len(recorder.auditCache) != 0 {
		t.Fatalf("retry repeated or overwrote the failed review: %+v", recorder.audit)
	}
	next := &requestRecorder{}
	if failure := h.checkRequestAudit(t.Context(), snapshot, snapshot.AccessKeysByID[1], body, next, admit); failure != nil || next.audit.Status != "passed" || len(forwarder.inputs) != 2 {
		t.Fatalf("next request reused an incomplete review: failure=%v audit=%+v", failure, next.audit)
	}
}

func TestRequestAuditCancellationDoesNotDispatchBusinessRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	forwarder := &scriptedForwarder{results: []UpstreamResult{auditReply(auditPass)}, onCall: func(int) { cancel() }}
	h, engine := auditEngine(t, forwarder)
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer gl-client")
	engine.ServeHTTP(httptest.NewRecorder(), request)
	events := sink.snapshot()
	if len(forwarder.inputs) != 1 || len(events) != 1 || events[0].ErrorCode != "client_canceled" || len(events[0].Attempts) != 0 {
		t.Fatalf("canceled request was dispatched or logged as an audit failure: calls=%d events=%+v", len(forwarder.inputs), events)
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

func TestRequestAuditKeepsCachedFindingsWhenOtherRulesCannotFinish(t *testing.T) {
	for _, action := range []string{requestaudit.ActionBlock, requestaudit.ActionWarn} {
		t.Run(action, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{auditReply(auditHit), auditReply(`{"answers":{}}`)}}
			h, _ := auditEngine(t, forwarder)
			snapshot := h.manager.Current()
			body := []byte(`{"input":"review this"}`)
			admit := func() *reason { return nil }
			h.checkRequestAudit(t.Context(), snapshot, snapshot.AccessKeysByID[1], body, &requestRecorder{}, admit)
			snapshot.RequestAudit.Rules[0].Action = action
			snapshot.RequestAudit.Rules = append(snapshot.RequestAudit.Rules, requestaudit.Rule{ID: "extra", Name: "Extra", Enabled: true, Action: requestaudit.ActionBlock, Instructions: "Check another condition", Threshold: .8})
			recorder := &requestRecorder{}
			failure := h.checkRequestAudit(t.Context(), snapshot, snapshot.AccessKeysByID[1], body, recorder, admit)
			if len(recorder.audit.Findings) != 1 || recorder.audit.Findings[0].RuleID != "personal_data" || recorder.audit.Findings[0].Action != action {
				t.Fatalf("cached finding was lost: %+v", recorder.audit)
			}
			if action == requestaudit.ActionBlock {
				if failure != &reasonAuditBlocked || len(forwarder.inputs) != 1 || recorder.audit.Status != "blocked" {
					t.Fatal("known block triggered an unnecessary call or was replaced by an error")
				}
			} else if failure != nil || len(forwarder.inputs) != 2 || recorder.audit.Status != "incomplete" {
				t.Fatal("warning blocked or concealed an incomplete review")
			}
		})
	}
}

func TestRequestAuditOneCallAcrossRetriesAndChangedContentAllows(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{auditReply(auditPass)}}
	h, _ := auditEngine(t, forwarder)
	snapshot := h.manager.Current()
	recorder := &requestRecorder{}
	admit := func() *reason { return nil }
	body := []byte(`{"input":"original"}`)
	for range 2 {
		if failure := h.checkRequestAudit(t.Context(), snapshot, snapshot.AccessKeysByID[1], body, recorder, admit); failure != nil {
			t.Fatal(failure)
		}
	}
	failure := h.checkRequestAudit(t.Context(), snapshot, snapshot.AccessKeysByID[1], []byte(`{"input":"changed"}`), recorder, admit)
	if failure != nil || recorder.audit.Status != "incomplete" || recorder.audit.Reason != "content_changed" || len(forwarder.inputs) != 1 {
		t.Fatal("retry changed content or caused a second review")
	}
}

func TestRequestAuditResourceRoutesCannotBypassContentReview(t *testing.T) {
	for _, endpoint := range []struct{ method, path string }{
		{http.MethodGet, "/v1/responses/resp_test"},
		{http.MethodDelete, "/v1/responses/resp_test"},
		{http.MethodPost, "/v1/responses/resp_test/cancel"},
		{http.MethodGet, "/v1/responses/resp_test/input_items"},
	} {
		for _, body := range []string{"", `{"input":"content that needs review"}`} {
			t.Run(endpoint.method+endpoint.path+body, func(t *testing.T) {
				forwarder := &scriptedForwarder{results: []UpstreamResult{auditReply(auditHit)}}
				h, engine := auditEngine(t, forwarder)
				h.dialects = dialect.NewSet(dialect.NewOpenAIResponses())
				request := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(body))
				request.Header.Set("Authorization", "Bearer gl-client")
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)
				status := http.StatusOK
				if body != "" {
					status = http.StatusForbidden
				}
				if response.Code != status || len(forwarder.inputs) != 1 || (body != "" && forwarder.inputs[0].Operation != execution.OperationDecisionsCreate) {
					t.Fatalf("resource body bypassed review or empty resource was reviewed: status=%d calls=%d", response.Code, len(forwarder.inputs))
				}
			})
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
	forwarder := &auditWebsocketForwarder{scriptedForwarder: &scriptedForwarder{results: []UpstreamResult{auditReply(auditPass), auditReply(`{"answers":{}}`), auditReply(auditHit)}}, opener: h.forwarder.(interface {
		OpenWebsocket(context.Context, ForwardInput) (execution.WebsocketSession, execution.WebsocketResult)
	})}
	h.forwarder = forwarder
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	for index, content := range []string{"hello", "review-unavailable", "new-tool-result"} {
		payload := map[string]any{"type": "response.create", "model": "public", "input": content, "store": false}
		if index > 0 {
			payload["previous_response_id"] = fmt.Sprintf("resp_%d", index-1)
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
		if index == 2 {
			want = "request_audit_blocked"
		}
		if !strings.Contains(string(body), want) {
			t.Fatalf("unexpected WebSocket result %s", body)
		}
	}
	if len(forwarder.inputs) != 3 || bytes.Contains(forwarder.inputs[1].Request.Body, []byte("hello")) {
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
