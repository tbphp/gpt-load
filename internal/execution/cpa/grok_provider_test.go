package cpa

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/execution"
)

type grokProviderTestError struct {
	status int
	code   string
	retry  time.Duration
}

func (err grokProviderTestError) Error() string     { return "Grok provider test error" }
func (err grokProviderTestError) StatusCode() int   { return err.status }
func (err grokProviderTestError) ErrorCode() string { return err.code }
func (err grokProviderTestError) RetryAfter() *time.Duration {
	if err.retry <= 0 {
		return nil
	}
	value := err.retry
	return &value
}

func TestGrokProviderClassifiesOAuthAndQuotaFailures(t *testing.T) {
	bridge := newGrokProviderBridge()
	credential := grokProviderCredential{}
	for _, test := range []struct {
		name   string
		err    error
		hint   execution.FailureHint
		scope  execution.ErrorScope
		replay execution.ReplaySafety
	}{
		{name: "unauthorized", err: grokProviderTestError{status: http.StatusUnauthorized}, hint: execution.FailureHintRefreshRequired, scope: execution.ErrorScopeCredential, replay: execution.ReplaySafetyRejectedBeforeProcessing},
		{name: "forbidden", err: grokProviderTestError{status: http.StatusForbidden}, hint: execution.FailureHintCandidateUnavailable, scope: execution.ErrorScopeModel, replay: execution.ReplaySafetyRejectedBeforeProcessing},
		{name: "payment required", err: grokProviderTestError{status: http.StatusPaymentRequired}, hint: execution.FailureHintCandidateUnavailable, scope: execution.ErrorScopeModel, replay: execution.ReplaySafetyRejectedBeforeProcessing},
		{name: "invalid request", err: grokProviderTestError{status: http.StatusBadRequest}, hint: execution.FailureHintRequestRejected, scope: execution.ErrorScopeRequest},
		{name: "free usage", err: grokProviderTestError{status: http.StatusTooManyRequests, code: "subscription:free-usage-exhausted", retry: 24 * time.Hour}, hint: execution.FailureHintRateLimited, scope: execution.ErrorScopeCredential},
		{name: "host", err: grokProviderTestError{status: http.StatusServiceUnavailable}, hint: execution.FailureHintHostError, scope: execution.ErrorScopeGroup},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, evidence := bridge.ClassifyError(t.Context(), test.err, credential)
			if status != test.err.(grokProviderTestError).status || evidence == nil ||
				evidence.Hint != test.hint || evidence.OriginHint != execution.ErrorOriginUpstream ||
				evidence.ScopeHint != test.scope || evidence.ReplaySafety != test.replay {
				t.Fatalf("classification = %d/%#v", status, evidence)
			}
			if test.name == "free usage" && evidence.RetryAfter != 24*time.Hour {
				t.Fatalf("RetryAfter = %v", evidence.RetryAfter)
			}
		})
	}
}

func TestGrokLocalTokenCountRejectsStatefulAndMultimodalInputs(t *testing.T) {
	bridge := newGrokProviderBridge()
	for _, test := range []struct {
		name    string
		payload string
		wantErr bool
	}{
		{name: "text", payload: `{"model":"grok-4.3","input":"hello"}`},
		{name: "stateful", payload: `{"model":"grok-4.3","previous_response_id":"resp_1","input":"hello"}`, wantErr: true},
		{name: "image", payload: `{"model":"grok-4.3","input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}]}`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := bridge.ValidateLocalTokenCount(providerRequest{
				Model: "grok-4.3", Format: "openai-response", Payload: []byte(test.payload),
			})
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateLocalTokenCount() error = %v", err)
			}
		})
	}
}

func TestGrokLocalTokenCountDoesNotRequireCredential(t *testing.T) {
	bridge := newGrokProviderBridge()
	response, err := bridge.CountTokensLocal(context.Background(), providerRequest{
		Model: "grok-4.3", Format: "openai-response",
		Payload: []byte(`{"model":"grok-4.3","input":"hello"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Payload) == 0 || response.Headers.Get(localTokenCountHeader) != "local-estimate" {
		t.Fatalf("response = %#v", response)
	}
	if !strings.Contains(string(response.Payload), `"object":"response.input_tokens"`) ||
		!strings.Contains(string(response.Payload), `"input_tokens":`) {
		t.Fatalf("Responses token count = %s", response.Payload)
	}
}

func TestGrokContinuityScopeIsCredentialAndModelScoped(t *testing.T) {
	left := grokContinuityScope("tenant", "credential-1", "grok-4.3", "attempt")
	right := grokContinuityScope("tenant", "credential-2", "grok-4.3", "attempt")
	otherModel := grokContinuityScope("tenant", "credential-1", "grok-code-fast-1", "attempt")
	if left == "" || left == right || left == otherModel {
		t.Fatalf("continuity scopes = %q / %q / %q", left, right, otherModel)
	}
}
