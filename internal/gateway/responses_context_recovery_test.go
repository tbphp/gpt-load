package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

const responseItemResourceError = "The requested item was created under a different Azure OpenAI resource. Use the same resource that created the item to access it."
const contextRecoverySuccess = `{"id":"resp_ok","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`
const contextRecoveryRequest = `{"model":"model-a","store":false,"input":[{"type":"message","id":"msg_old","role":"assistant","content":[{"type":"output_text","text":"history"}]},{"type":"function_call","id":"fc_old","call_id":"call_keep","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_keep","output":"result"},{"type":"reasoning","id":"rs_old","encrypted_content":"opaque","summary":[{"type":"summary_text","text":"summary"}]},{"type":"message","role":"user","content":"continue"}]}`

func TestResponsesContextRecoveryAfterOriginalCredentialDisabled(t *testing.T) {
	for _, mode := range []string{"resource_http", "resource_id_only_http", "resource_sse", "encrypted_sse", "missing_item_http"} {
		t.Run(mode, func(t *testing.T) {
			observed := &encryptedRecoveryBodies{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := readAllString(t, r)
				observed.add(r.Header.Get("Authorization"), body)
				if strings.Contains(body, "msg_old") || strings.Contains(body, "opaque") {
					message := responseItemResourceError
					if mode == "encrypted_sse" {
						message = "The encrypted content for item rs_old could not be verified."
					} else if mode == "missing_item_http" {
						message = "Item with id 'msg_old' not found. Items are not persisted when store is set to false."
					}
					payload, _ := json.Marshal(map[string]any{"error": map[string]any{"code": "invalid_request_error", "message": message}})
					if strings.HasSuffix(mode, "sse") {
						w.Header().Set("Content-Type", "text/event-stream")
						failed, _ := json.Marshal(map[string]any{"type": "response.failed", "response": map[string]any{"id": "resp_failed", "object": "response", "status": "failed", "model": "model-a", "output": []any{}, "error": map[string]any{"code": "invalid_request_error", "message": message}}})
						_, _ = w.Write([]byte("event: response.failed\ndata: " + string(failed) + "\n\n"))
					} else {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write(payload)
					}
					return
				}
				if strings.HasSuffix(mode, "sse") {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + contextRecoverySuccess + "}\n\n"))
				} else {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(contextRecoverySuccess))
				}
			}))
			defer upstream.Close()
			engine, registry := newDialectGatewayEngine(t, protocol.OpenAIResponses, "model-a", dialect.NewSet(dialect.NewOpenAIResponses()),
				dialectGatewayGroup{id: 1, name: "relay", channelID: channel.NewAPI, params: openAIBaseURLParams(t, upstream.URL), apiKeys: []string{"sk-old", "sk-new"}},
			)
			if err := registry.SetCredentialStatus(1, state.CredentialStatusDisabled); err != nil {
				t.Fatal(err)
			}
			body := contextRecoveryRequest
			if mode == "resource_id_only_http" {
				body = strings.ReplaceAll(body, `"encrypted_content":"opaque",`, "")
			}
			if strings.HasSuffix(mode, "sse") {
				body = strings.TrimSuffix(body, "}") + `,"stream":true}`
			}
			serveContinuation(t, engine, "gl-client", body, http.StatusOK)
			keys, bodies := observed.snapshot()
			if len(keys) != 2 || keys[0] != "Bearer sk-new" || keys[1] != keys[0] {
				t.Fatalf("expected recovery on the replacement credential, got %v", keys)
			}
			if !strings.Contains(bodies[0], "msg_old") || (mode != "resource_id_only_http" && !strings.Contains(bodies[0], "opaque")) {
				t.Fatal("normal first attempt was rewritten")
			}
			assertRecoveredContext(t, bodies[1])
		})
	}
}

func TestResponsesContextRecoveryKeepsCleanedHistoryAcrossFailover(t *testing.T) {
	for _, crossGroup := range []bool{false, true} {
		t.Run(map[bool]string{false: "same_group", true: "different_group"}[crossGroup], func(t *testing.T) {
			failure := UpstreamResult{StatusCode: http.StatusBadRequest, Header: make(http.Header), ErrorSummary: responseItemResourceError, RequestWritten: true}
			unavailable := UpstreamResult{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: []byte(`{"error":"invalid_api_key"}`), ClassificationBody: []byte(`{"error":"invalid_api_key"}`), RequestWritten: true}
			success := UpstreamResult{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(contextRecoverySuccess), RequestWritten: true}
			forwarder := &scriptedForwarder{results: []UpstreamResult{failure, unavailable, success, success}}
			groups := []dialectGatewayGroup{{id: 1, name: "first", channelID: channel.NewAPI, params: openAIBaseURLParams(t, "https://first.example"), apiKeys: []string{"sk-old", "sk-new", "sk-next"}}}
			if crossGroup {
				groups[0].apiKeys = []string{"sk-old", "sk-new"}
				groups[0].settings = config.Settings{state.SettingParameterOverrides: []any{map[string]any{"set": map[string]any{"first_group": true}}}}
				groups = append(groups, dialectGatewayGroup{id: 2, name: "second", channelID: channel.NewAPI, params: openAIBaseURLParams(t, "https://second.example"), apiKeys: []string{"sk-next"}, settings: config.Settings{state.SettingParameterOverrides: []any{map[string]any{"set": map[string]any{"second_group": true}}}}})
			}
			engine, registry := newDialectGatewayEngineWithSystemSettings(t, protocol.OpenAIResponses, "model-a", dialect.NewSet(dialect.NewOpenAIResponses()), forwarder, config.Settings{state.SettingRetryCount: 3}, groups...)
			if err := registry.SetCredentialStatus(1, state.CredentialStatusDisabled); err != nil {
				t.Fatal(err)
			}
			serveContinuation(t, engine, "gl-client", contextRecoveryRequest, http.StatusOK)
			if len(forwarder.inputs) != 3 {
				t.Fatalf("attempt count = %d, want 3", len(forwarder.inputs))
			}
			assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-new", "sk-new", "sk-next"})
			assertRecoveredContext(t, string(forwarder.inputs[1].Request.Body))
			assertRecoveredContext(t, string(forwarder.inputs[2].Request.Body))
			if crossGroup && (strings.Contains(string(forwarder.inputs[2].Request.Body), "first_group") || !strings.Contains(string(forwarder.inputs[2].Request.Body), "second_group")) {
				t.Fatal("failover inherited the previous group's parameter overrides")
			}
			serveContinuation(t, engine, "gl-client", contextRecoveryRequest, http.StatusOK)
			if !strings.Contains(string(forwarder.inputs[3].Request.Body), "opaque") {
				t.Fatal("recovery state leaked into another client request")
			}
		})
	}
}

func TestResponsesContextRecoveryLeavesOrdinaryErrorsUnchanged(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: []byte(`{"error":{"message":"Please check the request parameters"}}`), RequestWritten: true}}}
	engine, _ := newDialectGatewayEngineWithForwarder(t, protocol.OpenAIResponses, "model-a", dialect.NewSet(dialect.NewOpenAIResponses()), forwarder,
		dialectGatewayGroup{id: 1, name: "relay", channelID: channel.NewAPI, params: openAIBaseURLParams(t, "https://relay.example"), apiKeys: []string{"sk-one"}},
	)
	serveContinuation(t, engine, "gl-client", contextRecoveryRequest, http.StatusBadRequest)
	if len(forwarder.inputs) != 1 || string(forwarder.inputs[0].Request.Body) != contextRecoveryRequest {
		t.Fatal("ordinary parameter failure triggered context recovery")
	}
}

func TestResponsesContextRecoverySkipsCredentialDisabledBeforeReplay(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{StatusCode: http.StatusBadRequest, Header: make(http.Header), ErrorSummary: responseItemResourceError, RequestWritten: true},
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(contextRecoverySuccess), RequestWritten: true},
	}}
	engine, registry := newDialectGatewayEngineWithSystemSettings(t, protocol.OpenAIResponses, "model-a", dialect.NewSet(dialect.NewOpenAIResponses()), forwarder, config.Settings{state.SettingRetryCount: 3},
		dialectGatewayGroup{id: 1, name: "relay", channelID: channel.NewAPI, params: openAIBaseURLParams(t, "https://relay.example"), apiKeys: []string{"sk-old", "sk-new", "sk-next"}},
	)
	if err := registry.SetCredentialStatus(1, state.CredentialStatusDisabled); err != nil {
		t.Fatal(err)
	}
	forwarder.onCall = func(index int) {
		if index == 0 {
			if err := registry.SetCredentialStatus(2, state.CredentialStatusDisabled); err != nil {
				t.Fatal(err)
			}
		}
	}
	serveContinuation(t, engine, "gl-client", contextRecoveryRequest, http.StatusOK)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-new", "sk-next"})
	assertRecoveredContext(t, string(forwarder.inputs[1].Request.Body))
}

func TestResponsesContextRecoveryPreservesRejectionWhenReplayUnavailable(t *testing.T) {
	for _, providerFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "http", true: "provider_before_commit"}[providerFailure], func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"error": map[string]any{"code": "invalid_request_error", "message": responseItemResourceError}})
			result := UpstreamResult{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: body, ClassificationBody: body, ErrorSummary: responseItemResourceError, RequestWritten: true}
			if providerFailure {
				result.StatusCode = http.StatusOK
				result.ProviderErrorBeforeCommit = true
			}
			forwarder := &scriptedForwarder{results: []UpstreamResult{result}}
			engine, registry := newDialectGatewayEngineWithSystemSettings(t, protocol.OpenAIResponses, "model-a", dialect.NewSet(dialect.NewOpenAIResponses()), forwarder, config.Settings{state.SettingRetryCount: 0},
				dialectGatewayGroup{id: 1, name: "relay", channelID: channel.NewAPI, params: openAIBaseURLParams(t, "https://relay.example"), apiKeys: []string{"sk-one"}},
			)
			forwarder.onCall = func(int) {
				if err := registry.SetCredentialStatus(1, state.CredentialStatusDisabled); err != nil {
					t.Fatal(err)
				}
			}
			status, evidence := http.StatusBadRequest, responseItemResourceError
			if providerFailure {
				status, evidence = reasonUpstreamProtocol.Status, reasonUpstreamProtocol.Code
			}
			response := serveContinuation(t, engine, "gl-client", contextRecoveryRequest, status)
			if len(forwarder.inputs) != 1 || !strings.Contains(response.Body.String(), evidence) {
				t.Fatalf("first rejection was lost: attempts=%d body=%s", len(forwarder.inputs), response.Body.String())
			}
		})
	}
}

func TestResponsesContextRecoveryReturnsLaterCredentialFailure(t *testing.T) {
	for _, mode := range []string{"http", "sse"} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests} {
			t.Run(mode+"/"+http.StatusText(status), func(t *testing.T) {
				observed := &encryptedRecoveryBodies{}
				failureCode := "invalid_api_key"
				if status == http.StatusTooManyRequests {
					failureCode = "rate_limit_exceeded"
				}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body := readAllString(t, r)
					observed.add(r.Header.Get("Authorization"), body)
					if strings.Contains(body, "opaque") {
						errorObject := map[string]any{"code": "invalid_encrypted_content", "message": "The encrypted content for item rs_old could not be verified."}
						if mode == "sse" {
							w.Header().Set("Content-Type", "text/event-stream")
							failed, _ := json.Marshal(map[string]any{"type": "response.failed", "response": map[string]any{"id": "resp_failed", "object": "response", "status": "failed", "model": "model-a", "output": []any{}, "error": errorObject}})
							_, _ = w.Write([]byte("event: response.failed\ndata: " + string(failed) + "\n\n"))
						} else {
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(http.StatusBadRequest)
							payload, _ := json.Marshal(map[string]any{"error": errorObject})
							_, _ = w.Write(payload)
						}
						return
					}
					w.Header().Set("Content-Type", "application/json")
					if status == http.StatusTooManyRequests {
						w.Header().Set("Retry-After", "5")
					}
					w.WriteHeader(status)
					payload, _ := json.Marshal(map[string]any{"error": map[string]any{"code": failureCode, "message": "credential unavailable"}})
					_, _ = w.Write(payload)
				}))
				defer upstream.Close()
				engine, _ := newDialectGatewayEngineWithSystemSettings(t, protocol.OpenAIResponses, "model-a", dialect.NewSet(dialect.NewOpenAIResponses()), newTestExecutionForwarder(t), config.Settings{state.SettingRetryCount: 1},
					dialectGatewayGroup{id: 1, name: "relay", channelID: channel.NewAPI, params: openAIBaseURLParams(t, upstream.URL), apiKeys: []string{"sk-one"}},
				)
				body := contextRecoveryRequest
				if mode == "sse" {
					body = strings.TrimSuffix(body, "}") + `,"stream":true}`
				}
				response := serveContinuation(t, engine, "gl-client", body, status)
				keys, bodies := observed.snapshot()
				if len(keys) != 2 || keys[0] != keys[1] || !strings.Contains(response.Body.String(), failureCode) {
					t.Fatalf("later credential failure was lost: keys=%v body=%s", keys, response.Body.String())
				}
				assertRecoveredContext(t, bodies[1])
				if status == http.StatusTooManyRequests && response.Header().Get("Retry-After") != "5" {
					t.Fatalf("later rate-limit header was lost: %v", response.Header())
				}
			})
		}
	}
}

func assertRecoveredContext(t *testing.T, body string) {
	t.Helper()
	for _, removed := range []string{"msg_old", "fc_old", "rs_old", "opaque"} {
		if strings.Contains(body, removed) {
			t.Fatalf("rejected state %q survived: %s", removed, body)
		}
	}
	for _, preserved := range []string{"history", "call_keep", "lookup", "result", "summary", "continue"} {
		if !strings.Contains(body, preserved) {
			t.Fatalf("history %q was lost: %s", preserved, body)
		}
	}
}

func TestResponsesContextRecoveryDoesNotReplayCommittedStream(t *testing.T) {
	observed := &encryptedRecoveryBodies{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed.add(r.Header.Get("Authorization"), readAllString(t, r))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"already delivered\"}\n\n"))
		w.(http.Flusher).Flush()
		failed, _ := json.Marshal(map[string]any{"type": "response.failed", "response": map[string]any{"id": "resp_failed", "object": "response", "status": "failed", "error": map[string]any{"code": "invalid_request_error", "message": responseItemResourceError}}})
		_, _ = w.Write([]byte("event: response.failed\ndata: " + string(failed) + "\n\n"))
	}))
	defer upstream.Close()
	engine, _ := newDialectGatewayEngine(t, protocol.OpenAIResponses, "model-a", dialect.NewSet(dialect.NewOpenAIResponses()),
		dialectGatewayGroup{id: 1, name: "relay", channelID: channel.NewAPI, params: openAIBaseURLParams(t, upstream.URL), apiKeys: []string{"sk-one", "sk-two"}},
	)
	body := strings.TrimSuffix(contextRecoveryRequest, "}") + `,"stream":true}`
	response := serveContinuation(t, engine, "gl-client", body, http.StatusOK)
	keys, _ := observed.snapshot()
	if len(keys) != 1 || !strings.Contains(response.Body.String(), "already delivered") {
		t.Fatalf("committed output was replayed or lost: keys=%v response=%s", keys, response.Body.String())
	}
}

func TestResponsesContextRecoveryUsesCapturedErrorEvidence(t *testing.T) {
	body := []byte(`{"error":{"code":"item_not_found","message":"shortened"}}`)
	if encryptedContentRecoverySignal(UpstreamResult{StatusCode: 400, ClassificationBody: body}) == "" {
		t.Fatal("captured upstream error code was ignored")
	}
	if encryptedContentRecoverySignal(UpstreamResult{StatusCode: 200, ClassificationBody: body}) != "" {
		t.Fatal("successful response body was used as failure evidence")
	}
}
