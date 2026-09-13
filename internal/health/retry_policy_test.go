package health

import (
	"net/http"
	"testing"

	"gpt-load/internal/execution"
)

func TestUnknownHTTPRejectionsRetryWithoutCredentialEffects(t *testing.T) {
	for _, status := range []int{400, 402, 403, 404, 409, 422} {
		for _, code := range []string{"", "unknown_provider_error"} {
			result := JudgeExecution(ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent, StatusCode: status,
				Evidence: &execution.ErrorEvidence{
					Kind: execution.ErrorKindHTTP, StatusCode: status,
					OriginHint: execution.ErrorOriginUpstream,
					Type:       "invalid_request_error", Code: code, Summary: "request could not be served",
				},
			}, DecisionContext{Method: http.MethodPost, Operation: execution.OperationChatCompletion})
			if result.Category != FailureCategoryAmbiguous || result.Retry != RetryNextCandidate ||
				result.Effect != EffectNone || !result.CooldownUntil.IsZero() {
				t.Errorf("status=%d code=%q decision=%#v", status, code, result)
			}
		}
	}
}

func TestExplicitRequestRejectionCodesStillTerminate(t *testing.T) {
	for _, code := range []string{"context_length_exceeded", "invalid_parameter", "content_policy_violation"} {
		result := JudgeExecution(ExecutionAttempt{
			DispatchState: execution.DispatchMaybeSent, StatusCode: http.StatusBadRequest,
			Evidence: &execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, Code: code, OriginHint: execution.ErrorOriginUpstream,
			},
		}, DecisionContext{Method: http.MethodPost, Operation: execution.OperationChatCompletion})
		if result.Category != FailureCategoryClientError || result.Retry != RetryNone || result.Effect != EffectNone {
			t.Errorf("code=%s decision=%#v", code, result)
		}
	}
}

func TestUpstreamResponseRetryPreservesExecutionBoundaries(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		evidence  execution.ErrorEvidence
		operation execution.Operation
		committed bool
		retry     RetryDirective
		effect    Effect
	}{
		{name: "generation server error", status: 503, evidence: execution.ErrorEvidence{Kind: execution.ErrorKindHTTP}, retry: RetryNextCandidate, effect: EffectSkipGroup},
		{name: "unknown error event", status: 200, evidence: execution.ErrorEvidence{Kind: execution.ErrorKindProvider, Code: "upstream_sse_error"}, retry: RetryNextCandidate, effect: EffectNone},
		{name: "unknown provider code", status: 200, evidence: execution.ErrorEvidence{Kind: execution.ErrorKindProvider, Code: "new_provider_error"}, retry: RetryNextCandidate, effect: EffectNone},
		{name: "response status in evidence", evidence: execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, StatusCode: 403}, retry: RetryNextCandidate, effect: EffectNone},
		{name: "transport after headers", status: 200, evidence: execution.ErrorEvidence{Kind: execution.ErrorKindTransport}, retry: RetryNone, effect: EffectNone},
		{name: "timeout after headers", status: 200, evidence: execution.ErrorEvidence{Kind: execution.ErrorKindTimeout}, retry: RetryNone, effect: EffectNone},
		{name: "protocol failure", status: 200, evidence: execution.ErrorEvidence{Kind: execution.ErrorKindProvider, Code: "upstream_protocol_error"}, retry: RetryNone, effect: EffectNone},
		{name: "incomplete response", status: 200, evidence: execution.ErrorEvidence{Kind: execution.ErrorKindProvider, Code: "upstream_response_incomplete"}, retry: RetryNone, effect: EffectNone},
		{name: "internal result with HTTP status", status: 400, evidence: execution.ErrorEvidence{Kind: execution.ErrorKindInternal}, retry: RetryNone, effect: EffectNone},
		{name: "unknown execution result", status: 503, evidence: execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, ReplaySafety: execution.ReplaySafetyUnknown}, retry: RetryNone, effect: EffectSkipGroup},
		{name: "committed event", status: 200, evidence: execution.ErrorEvidence{Kind: execution.ErrorKindProvider, Code: "upstream_sse_error"}, committed: true, retry: RetryNone, effect: EffectNone},
		{name: "image needs rejection proof", status: 503, evidence: execution.ErrorEvidence{Kind: execution.ErrorKindHTTP}, operation: execution.OperationImagesGenerate, retry: RetryNone, effect: EffectSkipGroup},
		{name: "resource mutation remains restricted", status: 503, evidence: execution.ErrorEvidence{Kind: execution.ErrorKindHTTP}, operation: execution.OperationResponsesCancel, retry: RetryNone, effect: EffectSkipGroup},
	} {
		t.Run(test.name, func(t *testing.T) {
			operation := test.operation
			if operation == "" {
				operation = execution.OperationChatCompletion
			}
			result := JudgeExecution(ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent, StatusCode: test.status,
				Evidence: &test.evidence, DownstreamCommitted: test.committed,
			}, DecisionContext{Method: http.MethodPost, Operation: operation})
			if result.Retry != test.retry || result.Effect != test.effect {
				t.Fatalf("decision=%#v, want retry=%s effect=%s", result, test.retry, test.effect)
			}
		})
	}
}
