package gateway

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
)

func TestHandlerCustomErrorRulesRespectOverridesAndBlacklistThreshold(t *testing.T) {
	rule := []health.ErrorRule{{StatusCodes: []int{403}, Keywords: []string{"欠费"}, Retry: health.RetryNone, Effect: health.EffectRecordCredentialFailure}}
	for _, test := range []struct {
		name        string
		overrides   config.Settings
		blacklisted bool
		ruleID      string
	}{
		{name: "global", blacklisted: true, ruleID: "custom.global.1"},
		{name: "empty override", overrides: config.Settings{state.SettingErrorRules: []any{}}, ruleID: "fallback.upstream_response"},
		{name: "group override", overrides: config.Settings{state.SettingErrorRules: []health.ErrorRule{{StatusCodes: []int{403}, Retry: health.RetryNone, Effect: health.EffectNone}}}, ruleID: "custom.group.1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			failure := customGatewayFailure(403)
			forwarder := &scriptedForwarder{results: []UpstreamResult{failure, failure}}
			handler, manager, registry := newHandlerForTest(t, forwarder, "test-error-rule-key")
			publishHandlerPolicySettings(t, handler, manager, 1, config.Settings{state.SettingErrorRules: rule, state.SettingBlacklistThreshold: 2}, test.overrides)
			sink := &recordingRequestLogSink{}
			handler.requestLogSink = sink
			engine := gin.New()
			bindGatewayRoutesForTest(t, engine, handler)
			for attempt := 0; attempt < 2; attempt++ {
				request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
				request.Header.Set("Authorization", "Bearer gl-client")
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)
				if response.Code != 403 || (attempt == 0 && len(registry.BlacklistedCredentials()) != 0) {
					t.Fatalf("attempt=%d status=%d blacklist=%+v", attempt, response.Code, registry.BlacklistedCredentials())
				}
			}
			if got := len(registry.BlacklistedCredentials()) != 0; got != test.blacklisted {
				t.Fatalf("blacklisted=%t, want %t", got, test.blacklisted)
			}
			logs := sink.snapshot()
			if len(logs) != 2 || len(logs[0].Attempts) != 1 || logs[0].Attempts[0].RuleID != test.ruleID {
				t.Fatalf("logs=%+v", logs)
			}
		})
	}
}

func TestHandlerCustomErrorRulesCoolDownAndRetryExistingStreamError(t *testing.T) {
	failure := customGatewayFailure(200)
	failure.ProviderErrorBeforeCommit = true
	failure.ExecutionError.Kind = execution.ErrorKindProvider
	forwarder := &scriptedForwarder{streamResults: []UpstreamResult{failure,
		{StatusCode: 200, RequestWritten: true, Committed: true},
	}}
	handler, manager, registry := newHandlerForTest(t, forwarder, "test-error-first", "test-error-second")
	publishHandlerPolicySettings(t, handler, manager, 2, config.Settings{state.SettingErrorRules: []health.ErrorRule{{
		StatusCodes: []int{200}, Keywords: []string{"欠费"}, Retry: health.RetryNextCandidate,
		Effect: health.EffectCooldownCredential, CooldownSeconds: 120,
	}}}, nil)
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	engine.ServeHTTP(httptest.NewRecorder(), request)
	logs := sink.snapshot()
	if len(forwarder.streamInputs) != 2 || len(logs) != 1 || len(logs[0].Attempts) != 2 {
		t.Fatalf("attempts=%d logs=%+v", len(forwarder.streamInputs), logs)
	}
	until, exists := registry.CredentialCooldownUntil(logs[0].Attempts[0].CredentialID)
	if !exists || !until.After(handler.now()) {
		t.Fatal("the first credential did not enter cooldown")
	}
	if logs[0].Attempts[0].RuleID != "custom.global.1" || !logs[0].Attempts[0].WillRetry {
		t.Fatalf("first attempt=%+v", logs[0].Attempts[0])
	}
}

func customGatewayFailure(status int) UpstreamResult {
	return UpstreamResult{StatusCode: status, Header: make(http.Header), Body: []byte(`{"error":{"message":"账户欠费"}}`),
		RequestWritten: true, ExecutionError: &execution.ErrorEvidence{
			Kind: execution.ErrorKindHTTP, OriginHint: execution.ErrorOriginUpstream,
			StatusCode: status, Summary: "账户欠费",
		}}
}
