package cpa

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/subscription/providers/codex"
)

func TestWebsocketUnsentNetworkFailureCanSelectAnotherCandidate(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := strings.Replace(upstream.URL, "http:", "https:", 1)
	upstream.Close()
	session, err := codex.NewWSSession(codex.WSSessionOptions{CredentialID: "fixture",
		Credential: codex.Credential{Type: "codex", AccessToken: "fixture-access", RefreshToken: "fixture-refresh", AccountID: "fixture-account"},
		BaseURL:    endpoint, ProxyURL: "direct", TurnTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.ExecuteTurn(t.Context(), json.RawMessage(`{"model":"test","input":"hello","store":false}`), nil)
	evidence := codexWebsocketEvidence(t.Context(), err)
	if result.DispatchState != codex.WSNotSent || evidence == nil || evidence.Kind != execution.ErrorKindTransport || evidence.OriginHint != execution.ErrorOriginUpstream {
		t.Fatalf("dispatch=%s evidence=%+v", result.DispatchState, evidence)
	}
	decision := health.JudgeExecution(health.ExecutionAttempt{DispatchState: execution.DispatchNotSent, Evidence: evidence}, health.DecisionContext{Operation: execution.OperationResponsesCreate})
	if decision.Retry != health.RetryNextCandidate || decision.Effect != health.EffectSkipGroup {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestWebsocketUnsentValidationAndTimeoutEvidence(t *testing.T) {
	for _, test := range []struct {
		code   string
		kind   execution.ErrorKind
		origin execution.ErrorOrigin
	}{
		{"unsupported_request", execution.ErrorKindInvalidRequest, execution.ErrorOriginInternal},
		{"invalid_proxy", execution.ErrorKindInvalidRequest, execution.ErrorOriginInternal},
		{"timeout", execution.ErrorKindTimeout, execution.ErrorOriginUpstream},
		{"canceled", execution.ErrorKindCanceled, execution.ErrorOriginDownstream},
	} {
		evidence := codexWebsocketEvidence(t.Context(), &codex.WSError{Code: test.code, DispatchState: codex.WSNotSent})
		if evidence.Kind != test.kind || evidence.OriginHint != test.origin {
			t.Errorf("%s: %+v", test.code, evidence)
		}
	}
}

func TestWebsocketMalformedEventRetainsProtocolEvidence(t *testing.T) {
	for _, code := range []string{"invalid_event", "event_too_large"} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		evidence := codexWebsocketEvidence(ctx, &codex.WSError{Code: code, DispatchState: codex.WSMaybeSent})
		if evidence.Kind != execution.ErrorKindProvider || evidence.OriginHint != execution.ErrorOriginUpstream || evidence.Code != "upstream_protocol_error" {
			t.Errorf("%s: %+v", code, evidence)
		}
	}
}

func TestWebsocketModelCapacityDoesNotApplyQuotaCooldown(t *testing.T) {
	for _, code := range []string{"model_at_capacity", "model_is_at_capacity"} {
		evidence := codexWebsocketEvidence(t.Context(), &codex.WSError{
			Code: "upstream_error", UpstreamCode: code, HTTPStatus: http.StatusTooManyRequests,
			DispatchState: codex.WSMaybeSent,
		})
		if evidence.Hint != execution.FailureHintCandidateUnavailable || evidence.ScopeHint != execution.ErrorScopeModel ||
			evidence.ReplaySafety != execution.ReplaySafetyUnknown {
			t.Fatalf("capacity evidence=%+v", evidence)
		}
		decision := health.JudgeExecution(health.ExecutionAttempt{
			DispatchState: execution.DispatchMaybeSent, StatusCode: http.StatusTooManyRequests, Evidence: evidence,
		}, health.DecisionContext{Method: http.MethodPost, Operation: execution.OperationResponsesCreate})
		if decision.Effect != health.EffectNone || decision.Retry != health.RetryNone {
			t.Fatalf("capacity rejection caused cooldown or unsafe replay: %+v", decision)
		}
	}
}

func TestWebsocketHTTPErrorEvidenceSurvivesCancellation(t *testing.T) {
	for _, status := range []int{401, 429} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		evidence := codexWebsocketEvidence(ctx, &codex.WSError{Code: "upstream_error", HTTPStatus: status, DispatchState: codex.WSNotSent})
		if evidence.Kind != execution.ErrorKindHTTP || evidence.OriginHint != execution.ErrorOriginUpstream || evidence.StatusCode != status {
			t.Fatalf("explicit HTTP error replaced by cancellation: %+v", evidence)
		}
	}
}

func TestWebsocketUsageLimitUsesUpstreamModelCooldownDeadline(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	evidence := codexWebsocketEvidence(t.Context(), &codex.WSError{
		Code: "upstream_error", UpstreamType: "usage_limit_reached",
		HTTPStatus: http.StatusTooManyRequests, RetryAfter: 2 * time.Hour,
		DispatchState: codex.WSMaybeSent,
	})
	if evidence.Type != "usage_limit_reached" || evidence.Hint != execution.FailureHintRateLimited ||
		evidence.ScopeHint != execution.ErrorScopeModel || evidence.RetryAfter != 2*time.Hour {
		t.Fatalf("usage-limit evidence=%+v", evidence)
	}
	decision := health.JudgeExecution(health.ExecutionAttempt{
		DispatchState: execution.DispatchMaybeSent, StatusCode: http.StatusTooManyRequests,
		Evidence: evidence, Now: now,
	}, health.DecisionContext{Method: http.MethodPost, Operation: execution.OperationResponsesCreate})
	if decision.Effect != health.EffectCooldownModel || decision.Scope != execution.ErrorScopeModel ||
		decision.RuleID != "rate_limit.retry_after" || !decision.CooldownUntil.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("usage-limit decision=%+v", decision)
	}
}

func TestWebsocketForwardsForceCredentialRefresh(t *testing.T) {
	adapter, _, _, service, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	preparer := &fakeCredentialPreparer{evidence: &execution.ErrorEvidence{Kind: execution.ErrorKindInternal, Code: "prepare_stopped"}}
	adapter.credentials = preparer
	spec := validSpec(t, row, service)
	spec.ForceCredentialRefresh = true
	_, result := adapter.OpenWebsocket(t.Context(), spec)
	if !preparer.force || preparer.calls != 1 || result.Error != preparer.evidence {
		t.Fatalf("refresh was not forwarded: force=%t calls=%d result=%+v", preparer.force, preparer.calls, result)
	}
}

func TestWebsocketOnlyUnsent401RequestsRefresh(t *testing.T) {
	for _, dispatch := range []string{codex.WSNotSent, codex.WSMaybeSent} {
		evidence := codexWebsocketEvidence(t.Context(), &codex.WSError{Code: "upstream_error", HTTPStatus: 401, DispatchState: dispatch})
		refresh := evidence.Hint == execution.FailureHintRefreshRequired && evidence.ReplaySafety == execution.ReplaySafetyRejectedBeforeProcessing
		if refresh != (dispatch == codex.WSNotSent) {
			t.Fatalf("dispatch=%s evidence=%+v", dispatch, evidence)
		}
	}
}

func TestWebsocketRejectsUnsupportedSubscriptionWithoutPreparingCredential(t *testing.T) {
	a := NewAdapter(nil, channel.NewRegistry())
	opener, ok := any(a).(execution.WebsocketOpener)
	if !ok {
		t.Fatal("subscription WS adapter unavailable")
	}
	s, result := opener.OpenWebsocket(context.Background(), execution.AttemptSpec{ChannelID: string(channel.Claude)})
	if s != nil || result.Error == nil || result.DispatchState != execution.DispatchNotSent {
		t.Fatalf("unsupported=%+v", result)
	}
}

func TestWebsocketPreparesCodexCredentialBeforeOpeningSession(t *testing.T) {
	adapter, _, _, service, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	preparer := &fakeCredentialPreparer{delegate: adapter.credentials}
	adapter.credentials = preparer
	s, result := adapter.OpenWebsocket(t.Context(), validSpec(t, row, service))
	if result.Error != nil || s == nil {
		t.Fatalf("open=%+v", result)
	}
	defer s.Close()
	if preparer.calls != 1 || result.DispatchState != execution.DispatchNotSent {
		t.Fatal("credential preparation or deferred dial contract changed")
	}
	select {
	case <-s.Done():
		t.Fatal("new session already closed")
	default:
	}
}
