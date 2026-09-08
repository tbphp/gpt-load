package health

import (
	"net/http"
	"testing"
	"time"

	"gpt-load/internal/execution"
)

func TestModelRejectionRetriesWithoutCredentialPenalty(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound} {
		result := JudgeExecution(ExecutionAttempt{
			DispatchState: execution.DispatchMaybeSent, StatusCode: status, Now: time.Now(),
			Evidence: &execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintModelUnavailable,
				OriginHint: execution.ErrorOriginUpstream, ScopeHint: execution.ErrorScopeModel,
				StatusCode: status, Code: "unsupported_model", Summary: "selected model is unavailable",
			},
		}, DecisionContext{Method: http.MethodPost, Operation: execution.OperationChatCompletion})
		if result.Category != FailureCategoryModelUnavailable || result.Retry != RetryNextCandidate ||
			result.Effect != EffectNone || !result.CooldownUntil.IsZero() {
			t.Errorf("status %d decision = %#v, want retry without credential penalty", status, result)
		}
	}
}

func TestModelRejectionKeepsResourceAndReplayBoundaries(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation execution.Operation
		committed bool
		replay    execution.ReplaySafety
	}{
		{name: "unknown execution", operation: execution.OperationChatCompletion, replay: execution.ReplaySafetyUnknown},
		{name: "committed", operation: execution.OperationChatCompletion, committed: true},
		{name: "image without rejection proof", operation: execution.OperationImagesGenerate},
		{name: "embeddings without rejection proof", operation: execution.OperationEmbeddingsCreate},
		{name: "retrieve resource", operation: execution.OperationResponsesRetrieve},
		{name: "delete resource", operation: execution.OperationResponsesDelete},
		{name: "cancel resource", operation: execution.OperationResponsesCancel},
		{name: "resource input items", operation: execution.OperationResponsesInputItems},
		{name: "unknown responses path", operation: execution.OperationResponsesPassthrough},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := JudgeExecution(ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent, StatusCode: http.StatusNotFound,
				DownstreamCommitted: test.committed, Now: time.Now(),
				Evidence: &execution.ErrorEvidence{
					Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintModelUnavailable,
					OriginHint: execution.ErrorOriginUpstream, ScopeHint: execution.ErrorScopeModel,
					Code: "model_not_found", ReplaySafety: test.replay,
				},
			}, DecisionContext{Method: http.MethodPost, Operation: test.operation})
			if result.Retry != RetryNone || result.Effect != EffectNone {
				t.Fatalf("decision = %#v, want no replay and no credential penalty", result)
			}
		})
	}
}
