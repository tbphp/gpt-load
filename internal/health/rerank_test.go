package health

import (
	"net/http"
	"testing"

	"gpt-load/internal/execution"
)

func TestRerankRetryRequiresProofAndKeepsModelFailuresScoped(t *testing.T) {
	for _, test := range []struct {
		name     string
		evidence execution.ErrorEvidence
		retry    RetryDirective
	}{
		{"model rejected", execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintModelUnavailable, OriginHint: execution.ErrorOriginUpstream, ScopeHint: execution.ErrorScopeModel, StatusCode: 404, Code: "model_not_found", ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing}, RetryNextCandidate},
		{"unknown timeout", execution.ErrorEvidence{Kind: execution.ErrorKindTimeout, OriginHint: execution.ErrorOriginUpstream, ScopeHint: execution.ErrorScopeGroup, ReplaySafety: execution.ReplaySafetyUnknown}, RetryNone},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := JudgeExecution(ExecutionAttempt{DispatchState: execution.DispatchMaybeSent, StatusCode: test.evidence.StatusCode, Evidence: &test.evidence}, DecisionContext{Method: http.MethodPost, Operation: execution.OperationRerank})
			if got.Retry != test.retry || got.Effect != EffectNone {
				t.Fatalf("decision=%#v", got)
			}
			if test.name == "model rejected" && (got.Scope != execution.ErrorScopeModel || got.RuleID != "rerank.model_unavailable") {
				t.Fatalf("model decision=%#v", got)
			}
		})
	}
}
