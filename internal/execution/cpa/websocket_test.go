package cpa

import (
	"context"
	"net/http"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/subscription/providers/codex"
)

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
