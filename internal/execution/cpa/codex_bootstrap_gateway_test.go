package cpa

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/gateway"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/httproute"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
)

func TestCodexGatewayRetriesBootstrapOverload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const prelude = "data: " + `{"type":"response.created","response":{"id":"resp_primary","object":"response","status":"in_progress","model":"gpt-5","output":[]}}` + "\n\n" +
		"data: " + `{"type":"response.in_progress","response":{"id":"resp_primary","status":"in_progress","output":[]}}` + "\n\n"
	const overload = `{"type":"error","error":{"type":"server_error","code":"server_error","message":"Our servers are currently overloaded. Please try again later."}}`
	const standardOverload = `{"type":"error","error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}`
	const output = "data: " + `{"type":"response.output_text.delta","response_id":"resp_primary","output_index":0,"content_index":0,"delta":"partial answer"}` + "\n\n"
	const tool = "data: " + `{"type":"response.output_item.added","response_id":"resp_primary","output_index":0,"item":{"id":"ws_1","type":"web_search_call","status":"in_progress"}}` + "\n\n"
	for _, test := range []struct {
		name            string
		before          string
		errorEvent      string
		httpStatus      int
		rules           []health.ErrorRule
		wantRetry       bool
		wantUncommitted bool
	}{
		{name: "standard overload", errorEvent: standardOverload, wantRetry: true},
		{name: "overload reported as server error", errorEvent: overload, wantRetry: true},
		{name: "failed response overload", errorEvent: `{"type":"response.failed","response":{"id":"resp_primary","status":"failed","error":{"type":"server_error","code":"server_error","message":"Our servers are currently overloaded. Please try again later."}}}`, wantRetry: true},
		{name: "custom overload retry", errorEvent: overload, wantRetry: true,
			rules: []health.ErrorRule{{Keywords: []string{"overloaded"}, Retry: health.RetryNextCandidate, Effect: health.EffectNone}}},
		{name: "custom rule disables retry", errorEvent: overload, wantUncommitted: true,
			rules: []health.ErrorRule{{Keywords: []string{"overloaded"}, Retry: health.RetryNone, Effect: health.EffectNone}}},
		{name: "custom rule matches HTTP 500 overload", errorEvent: overload, httpStatus: http.StatusInternalServerError, wantUncommitted: true,
			rules: []health.ErrorRule{{StatusCodes: []int{500}, Retry: health.RetryNone, Effect: health.EffectNone}}},
		{name: "overload after empty reasoning", before: "data: " + `{"type":"response.output_item.added","item":{"type":"reasoning","summary":[]}}` + "\n\n", errorEvent: overload, wantRetry: true},
		{name: "overload after empty message", before: "data: " + `{"type":"response.output_item.added","item":{"type":"message","content":[]}}` + "\n\n", errorEvent: overload, wantRetry: true},
		{name: "overload after empty function call", before: "data: " + `{"type":"response.output_item.added","item":{"type":"function_call","name":"get_weather","arguments":""}}` + "\n\n", errorEvent: overload, wantRetry: true},
		{name: "overload after empty custom tool call", before: "data: " + `{"type":"response.output_item.added","item":{"type":"custom_tool_call","name":"get_weather","input":""}}` + "\n\n", errorEvent: overload, wantRetry: true},
		{name: "overload after empty text part", before: "data: " + `{"type":"response.content_part.added","part":{"type":"output_text","text":""}}` + "\n\n", errorEvent: overload, wantRetry: true},
		{name: "overload after empty reasoning part", before: "data: " + `{"type":"response.reasoning_summary_part.added","part":{"type":"summary_text","text":""}}` + "\n\n", errorEvent: overload, wantRetry: true},
		{name: "overload after large prelude", before: "data: " + `{"type":"response.in_progress","response":{"id":"resp_primary","instructions":"` + strings.Repeat("x", 300<<10) + `","output":[]}}` + "\n\n", errorEvent: overload, wantRetry: true},
		{name: "other server error remains in stream", errorEvent: `{"type":"error","error":{"type":"server_error","code":"server_error","message":"Unknown server failure"}}`},
		{name: "overload after output", before: output, errorEvent: overload},
		{name: "overload after reasoning content", before: "data: " + `{"type":"response.output_item.added","item":{"type":"reasoning","summary":[{"type":"summary_text","text":"thinking"}]}}` + "\n\n", errorEvent: overload},
		{name: "overload after encrypted reasoning", before: "data: " + `{"type":"response.output_item.added","item":{"type":"reasoning","summary":[],"encrypted_content":"ciphertext"}}` + "\n\n", errorEvent: overload},
		{name: "overload after unknown message content", before: "data: " + `{"type":"response.output_item.added","item":{"type":"message","content":[{"type":"output_audio","audio":"audio"}]}}` + "\n\n", errorEvent: overload},
		{name: "overload after function arguments", before: "data: " + `{"type":"response.output_item.added","item":{"type":"function_call","name":"get_weather","arguments":"{}"}}` + "\n\n", errorEvent: overload},
		{name: "overload after custom tool input", before: "data: " + `{"type":"response.output_item.added","item":{"type":"custom_tool_call","name":"get_weather","input":"weather"}}` + "\n\n", errorEvent: overload},
		{name: "overload after nonempty text part", before: "data: " + `{"type":"response.content_part.added","part":{"type":"output_text","text":"partial answer"}}` + "\n\n", errorEvent: overload},
		{name: "overload after unknown part", before: "data: " + `{"type":"response.content_part.added","part":{"type":"output_audio","audio":"audio"}}` + "\n\n", errorEvent: overload},
		{name: "overload after server tool", before: tool, errorEvent: overload},
		{name: "overload after unknown event", before: "data: " + `{"type":"response.unknown","response_id":"resp_primary"}` + "\n\n", errorEvent: overload},
		{name: "created event with output", before: "data: " + `{"type":"response.created","response":{"id":"resp_primary","output":[{"type":"message","content":[{"type":"output_text","text":"partial answer"}]}]}}` + "\n\n", errorEvent: overload},
		{name: "overload after prelude limit", before: strings.Repeat(prelude, 4), errorEvent: overload},
		{name: "overload after heartbeat limit", before: strings.Repeat(": keepalive\n\n", 49), errorEvent: overload},
		{name: "overload after byte limit", before: "data: " + `{"type":"response.in_progress","response":{"id":"resp_primary","instructions":"` + strings.Repeat("x", codexBootstrapBytes) + `"}}` + "\n\n", errorEvent: overload},
	} {
		t.Run(test.name, func(t *testing.T) {
			var primaryCalls, backupCalls atomic.Int32
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if r.Header.Get("Chatgpt-Account-Id") == "account-1" {
					primaryCalls.Add(1)
					if test.httpStatus != 0 {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(test.httpStatus)
						_, _ = io.WriteString(w, test.errorEvent)
						return
					}
					_, _ = io.WriteString(w, prelude+test.before)
					w.(http.Flusher).Flush()
					_, _ = io.WriteString(w, "data: "+test.errorEvent+"\n\n")
					return
				}
				backupCalls.Add(1)
				_, _ = io.WriteString(w, "data: "+`{"type":"response.completed","response":{"id":"resp_backup","object":"response","status":"completed","model":"gpt-5","output":[{"id":"msg_backup","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"backup answer","annotations":[]}]}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`+"\n\n")
			}))
			defer upstream.Close()
			engine, sink := newCodexBootstrapGateway(t, upstream.URL, test.rules)
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5","stream":true,"input":"hello"}`))
			request = request.WithContext(context.WithValue(t.Context(), "cliproxy.roundtripper", upstream.Client().Transport))
			request.Header.Set("Authorization", "Bearer gl-client")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if primaryCalls.Load() != 1 || len(sink.events) != 1 || len(sink.events[0].Attempts) == 0 {
				t.Fatalf("primary calls=%d logs=%+v response=%d %s", primaryCalls.Load(), sink.events, response.Code, response.Body)
			}
			log := sink.events[0]
			first := log.Attempts[0]
			t.Logf("primary=%d backup=%d attempts=%d committed=%t retry=%t status=%d error=%s", primaryCalls.Load(), backupCalls.Load(), len(log.Attempts), first.Committed, first.WillRetry, log.StatusCode, log.ErrorCode)
			if test.wantRetry {
				if backupCalls.Load() != 1 || len(log.Attempts) != 2 || first.Committed || !first.WillRetry || log.Status != telemetry.RequestStatusSuccess {
					t.Fatalf("bootstrap error did not fall back: first=%+v final=%s/%d backup=%d body_bytes=%d", first, log.Status, log.StatusCode, backupCalls.Load(), response.Body.Len())
				}
				if !strings.Contains(response.Body.String(), "backup answer") || strings.Contains(response.Body.String(), "resp_primary") || strings.Contains(response.Body.String(), "overloaded") {
					t.Fatalf("failed attempt leaked into successful response: %s", response.Body)
				}
				if first.StatusCode != http.StatusServiceUnavailable || first.ErrorCode != "server_is_overloaded" || first.Effect != telemetry.EffectNone {
					t.Fatalf("bootstrap overload lost capacity classification: %+v", first)
				}
				if len(test.rules) > 0 && first.RuleID != "custom.global.1" {
					t.Fatalf("custom rule did not handle bootstrap error: %+v", first)
				}
			} else if test.wantUncommitted {
				wantStatus := test.httpStatus
				if wantStatus == 0 {
					wantStatus = http.StatusServiceUnavailable
				}
				if backupCalls.Load() != 0 || len(log.Attempts) != 1 || first.Committed || first.WillRetry ||
					first.RuleID != "custom.global.1" || first.StatusCode != wantStatus || response.Code != wantStatus ||
					strings.Contains(response.Body.String(), "resp_primary") {
					t.Fatalf("custom stop rule was ignored: first=%+v response=%d %s backup=%d", first, response.Code, response.Body, backupCalls.Load())
				}
			} else if backupCalls.Load() != 0 || len(log.Attempts) != 1 || !first.Committed || first.WillRetry || log.ErrorCode != "upstream_sse_error" {
				t.Fatalf("started response was replayed: first=%+v error=%s backup=%d", first, log.ErrorCode, backupCalls.Load())
			}
		})
	}
}

func TestCodexGatewayRecoversHistoryRejectedAfterBootstrap(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Fernet 外层格式的合成密文（解码后 73 字节），避免被 CPA 当作无效格式提前清理。
	cipher := "gAAAA" + strings.Repeat("A", 93)
	const prelude = "data: " + `{"type":"response.created","response":{"id":"resp_primary","object":"response","status":"in_progress","model":"gpt-5","output":[]}}` + "\n\n" +
		"data: " + `{"type":"response.in_progress","response":{"id":"resp_primary","status":"in_progress","output":[]}}` + "\n\n"
	const encrypted = `{"type":"response.failed","response":{"id":"resp_primary","object":"response","status":"failed","model":"gpt-5","error":{"code":"invalid_encrypted_content","message":"The encrypted content for item rs_foreign could not be verified."}}}`
	const ownership = `{"type":"error","error":{"type":"invalid_request_error","message":"The requested item was created under a different Azure OpenAI resource. Use the same resource that created the item to access it."}}`
	const output = "data: " + `{"type":"response.output_text.delta","response_id":"resp_primary","output_index":0,"content_index":0,"delta":"partial answer"}` + "\n\n"
	for _, test := range []struct {
		name        string
		before      string
		rejection   string
		wantRecover bool
	}{
		{name: "encrypted content after prelude", rejection: encrypted, wantRecover: true},
		{name: "item ownership after prelude", rejection: ownership, wantRecover: true},
		{name: "encrypted content after output", before: output, rejection: encrypted},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			var accounts, bodies []string
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				accounts = append(accounts, r.Header.Get("Chatgpt-Account-Id"))
				bodies = append(bodies, string(body))
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				if strings.Contains(string(body), cipher) {
					_, _ = io.WriteString(w, prelude+test.before)
					w.(http.Flusher).Flush()
					_, _ = io.WriteString(w, "data: "+test.rejection+"\n\n")
					return
				}
				_, _ = io.WriteString(w, "data: "+`{"type":"response.completed","response":{"id":"resp_recovered","object":"response","status":"completed","model":"gpt-5","output":[{"id":"msg_recovered","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"recovered answer","annotations":[]}]}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`+"\n\n")
			}))
			defer upstream.Close()
			engine, sink := newCodexBootstrapGateway(t, upstream.URL, nil)
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5","stream":true,"store":false,"input":[`+
				`{"type":"reasoning","id":"rs_foreign","encrypted_content":"`+cipher+`","summary":[{"type":"summary_text","text":"kept summary"}]},`+
				`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"previous answer"}]},`+
				`{"type":"message","role":"user","content":"continue"}]}`))
			request = request.WithContext(context.WithValue(t.Context(), "cliproxy.roundtripper", upstream.Client().Transport))
			request.Header.Set("Authorization", "Bearer gl-client")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			mu.Lock()
			defer mu.Unlock()
			if len(accounts) == 0 || len(sink.events) != 1 || len(sink.events[0].Attempts) == 0 {
				t.Fatalf("calls=%v logs=%+v response=%d %s", accounts, sink.events, response.Code, response.Body)
			}
			log := sink.events[0]
			first := log.Attempts[0]
			if test.wantRecover {
				if len(accounts) != 2 || accounts[0] != "account-1" || accounts[1] != "account-1" || strings.Contains(bodies[1], cipher) ||
					first.Committed || !first.WillRetry || log.Status != telemetry.RequestStatusSuccess {
					t.Fatalf("rejected history was not replayed on the same credential: calls=%v first=%+v final=%s/%d body=%s", accounts, first, log.Status, log.StatusCode, response.Body)
				}
				if !strings.Contains(response.Body.String(), "recovered answer") || strings.Contains(response.Body.String(), "resp_primary") {
					t.Fatalf("rejected attempt leaked into recovered response: %s", response.Body)
				}
			} else if len(accounts) != 1 || !first.Committed || first.WillRetry || !strings.Contains(response.Body.String(), "partial answer") {
				t.Fatalf("started response was replayed: calls=%v first=%+v body=%s", accounts, first, response.Body)
			}
		})
	}
}

func newCodexBootstrapGateway(t *testing.T, baseURL string, rules []health.ErrorRule) (*gin.Engine, *searchRequestLogSink) {
	t.Helper()
	adapter, db, registry, keyService, first := newAdapterFixture(t, credentialJSON("access-1", "refresh-1", time.Now().Add(time.Hour)))
	params, err := json.Marshal(map[string]string{"base_url": baseURL})
	if err != nil {
		t.Fatal(err)
	}
	input := state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		SystemSettings:  config.Settings{state.SettingRetryCount: 1},
		AccessKeys:      []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"), Status: state.AccessKeyStatusActive}},
	}
	if rules != nil {
		input.SystemSettings[state.SettingErrorRules] = rules
	}
	var entries []state.CredentialEntry
	for index := 0; index < 2; index++ {
		row := first
		if index == 1 {
			group := models.Group{Name: "backup", ChannelID: "codex", ConnectionType: models.ConnectionTypeSubscription, Params: models.JSON(`{}`), Models: models.JSON(`[]`), Overrides: models.JSON(`{}`), Enabled: true}
			if err := db.Create(&group).Error; err != nil {
				t.Fatal(err)
			}
			canonical := strings.ReplaceAll(string(credentialJSON("access-2", "refresh-2", time.Now().Add(time.Hour))), "account-1", "account-2")
			ciphertext, err := keyService.Encrypt(canonical)
			if err != nil {
				t.Fatal(err)
			}
			row = models.Credential{GroupID: group.ID, Data: ciphertext, Fingerprint: keyService.Hash(canonical), IdentityFingerprint: keyService.Hash("identity|account-2"), SecretVersion: 1, AuthState: models.CredentialAuthStateReady, Status: models.CredentialStatusActive}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
		}
		generation := stateloader.CredentialIdentityGeneration(row.IdentityFingerprint, "codex", "subscription", json.RawMessage(`{}`))
		input.Groups = append(input.Groups, state.GroupConfig{ID: row.GroupID, Name: fmt.Sprintf("codex-%d", index), ChannelID: channel.Codex,
			Priority: int32(100 - index*100), ConnectionType: "subscription", Params: params,
			Models: []state.ModelConfig{{ID: "gpt-5"}}, Enabled: true})
		input.Credentials = append(input.Credentials, state.CredentialConfig{ID: row.ID, GroupID: row.GroupID, Version: row.SecretVersion, IdentityGeneration: generation, Fingerprint: row.Fingerprint, Status: state.CredentialStatusActive})
		entries = append(entries, state.CredentialEntry{ID: row.ID, GroupID: row.GroupID, Version: row.SecretVersion, IdentityGeneration: generation, Fingerprint: row.Fingerprint, Status: state.CredentialStatusActive, EncryptedValue: row.Data})
	}
	if err := registry.ReplaceCredentials(entries); err != nil {
		t.Fatal(err)
	}
	manager := state.NewManager()
	if _, err := manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	sink := &searchRequestLogSink{}
	handler := gateway.NewHandler(manager, registry, keyService, gateway.NewExecutionForwarder(contextTransportCPAExecutor{adapter}),
		dialect.NewSet(dialect.NewOpenAIResponses()), health.NewStatsStore(), health.NewMutationCoordinator(), nil, sink, nil)
	engine := gin.New()
	routes, err := httproute.NewRegistry(handler.HTTPModule())
	if err != nil {
		t.Fatal(err)
	}
	if err := routes.Bind(engine); err != nil {
		t.Fatal(err)
	}
	return engine, sink
}
