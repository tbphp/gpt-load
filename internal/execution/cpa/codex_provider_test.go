package cpa

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

type codexClassifiedTestError struct {
	status     int
	payload    string
	retryAfter time.Duration
}

func TestCodexUpstreamProtocolUsesObservedRequestPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want protocol.Protocol
	}{
		{path: "/backend-api/codex/images/generations", want: protocol.OpenAIImages},
		{path: "/backend-api/codex/images/edits", want: protocol.OpenAIImages},
		{path: "/backend-api/codex/responses", want: protocol.OpenAIResponses},
		{path: "/unknown"},
	}
	for _, test := range tests {
		if got := codexUpstreamProtocol(test.path); got != test.want {
			t.Errorf("codexUpstreamProtocol(%q) = %q, want %q", test.path, got, test.want)
		}
	}
}

func (err *codexClassifiedTestError) Error() string   { return err.payload }
func (err *codexClassifiedTestError) StatusCode() int { return err.status }
func (err *codexClassifiedTestError) RetryAfter() *time.Duration {
	if err.retryAfter <= 0 {
		return nil
	}
	value := err.retryAfter
	return &value
}

func TestCodexProviderClassifiesStructuredRateLimitEvidence(t *testing.T) {
	t.Parallel()

	bridge := newCodexProviderBridge()
	credential := codexProviderCredential{}
	tests := []struct {
		name       string
		err        error
		wantType   string
		wantCode   string
		wantHint   execution.FailureHint
		wantScope  execution.ErrorScope
		wantReplay execution.ReplaySafety
		wantRetry  time.Duration
	}{
		{
			name: "wrapped credential usage limit",
			err: fmt.Errorf("wrapped: %w", &codexClassifiedTestError{
				status:     http.StatusTooManyRequests,
				payload:    `{"error":{"type":"usage_limit_reached","message":"usage limit reached"}}`,
				retryAfter: 17 * time.Second,
			}),
			wantType: "usage_limit_reached", wantHint: execution.FailureHintRateLimited,
			wantScope:  execution.ErrorScopeCredential,
			wantReplay: execution.ReplaySafetyRejectedBeforeProcessing,
			wantRetry:  17 * time.Second,
		},
		{
			name: "model capacity",
			err: &codexClassifiedTestError{
				status:  http.StatusTooManyRequests,
				payload: `{"error":{"message":"The selected model is at capacity. Please try a different model."}}`,
			},
			wantCode:   "selected_model_at_capacity",
			wantHint:   execution.FailureHintCandidateUnavailable,
			wantScope:  execution.ErrorScopeModel,
			wantReplay: execution.ReplaySafetyRejectedBeforeProcessing,
		},
		{
			name: "generic rate limit remains unscoped",
			err: &codexClassifiedTestError{
				status:  http.StatusTooManyRequests,
				payload: `{"error":{"type":"rate_limit_error","message":"too many requests"}}`,
			},
			wantType: "rate_limit_error", wantHint: execution.FailureHintRateLimited,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, evidence := bridge.ClassifyError(t.Context(), test.err, credential)
			if status != http.StatusTooManyRequests || evidence == nil ||
				evidence.Type != test.wantType || evidence.Code != test.wantCode ||
				evidence.Hint != test.wantHint || evidence.ScopeHint != test.wantScope ||
				evidence.ReplaySafety != test.wantReplay || evidence.RetryAfter != test.wantRetry {
				t.Fatalf("ClassifyError() = %d / %#v", status, evidence)
			}
		})
	}
}

func TestCodexProviderClassifiesBootstrapRejections(t *testing.T) {
	bridge := newCodexProviderBridge()
	credential := codexProviderCredential{}
	for _, test := range []struct {
		name      string
		payload   string
		wantType  string
		wantCode  string
		wantHint  execution.FailureHint
		wantScope execution.ErrorScope
	}{
		{
			name:     "server overload",
			payload:  `{"error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"overloaded"}}`,
			wantType: "service_unavailable_error", wantCode: "server_is_overloaded",
			wantHint: execution.FailureHintHostError, wantScope: execution.ErrorScopeGroup,
		},
		{
			name:     "transient rate limit",
			payload:  `{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"slow down"}}`,
			wantType: "rate_limit_error", wantCode: "rate_limit_exceeded",
			wantHint: execution.FailureHintRateLimited,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cause := &codexClassifiedTestError{status: http.StatusServiceUnavailable, payload: test.payload}
			status, evidence := bridge.ClassifyError(
				t.Context(),
				&codexBootstrapRejectionError{cause: cause},
				credential,
			)
			if status != http.StatusServiceUnavailable || evidence == nil ||
				evidence.Type != test.wantType || evidence.Code != test.wantCode ||
				evidence.Hint != test.wantHint || evidence.ScopeHint != test.wantScope ||
				evidence.ReplaySafety != execution.ReplaySafetyRejectedBeforeProcessing {
				t.Fatalf("ClassifyError() = %d / %#v", status, evidence)
			}

			_, ordinary := bridge.ClassifyError(t.Context(), cause, credential)
			if ordinary == nil || ordinary.ReplaySafety != "" {
				t.Fatalf("ordinary ClassifyError() = %#v", ordinary)
			}
		})
	}
}
