package cpa

import (
	"net/http"
	"testing"

	"gpt-load/internal/execution"
)

func TestChatGPTProviderClassifiesOAuthFailures(t *testing.T) {
	bridge := newChatGPTProviderBridge()
	credential := chatgptProviderCredential{}
	for _, test := range []struct {
		name   string
		err    grokProviderTestError
		hint   execution.FailureHint
		scope  execution.ErrorScope
		replay execution.ReplaySafety
	}{
		{name: "unauthorized", err: grokProviderTestError{status: http.StatusUnauthorized}, hint: execution.FailureHintRefreshRequired, scope: execution.ErrorScopeCredential, replay: execution.ReplaySafetyRejectedBeforeProcessing},
		{name: "forbidden", err: grokProviderTestError{status: http.StatusForbidden}, hint: execution.FailureHintCandidateUnavailable, scope: execution.ErrorScopeModel, replay: execution.ReplaySafetyRejectedBeforeProcessing},
		{name: "host", err: grokProviderTestError{status: http.StatusServiceUnavailable}, hint: execution.FailureHintHostError, scope: execution.ErrorScopeGroup},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, evidence := bridge.ClassifyError(t.Context(), test.err, credential)
			if status != test.err.status || evidence == nil ||
				evidence.Hint != test.hint || evidence.OriginHint != execution.ErrorOriginUpstream ||
				evidence.ScopeHint != test.scope || evidence.ReplaySafety != test.replay {
				t.Fatalf("classification = %d/%#v", status, evidence)
			}
		})
	}
}
