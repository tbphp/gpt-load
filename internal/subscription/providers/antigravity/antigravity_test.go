package antigravity

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"
)

func TestParseCredentialJSONProducesCanonicalAntigravityCredential(t *testing.T) {
	credential, err := ParseCredentialJSON([]byte(`{
		"type":"antigravity",
		"access_token":"access-secret",
		"refresh_token":"refresh-secret",
		"account_id":"google-account-one",
		"email":"owner@example.com",
		"project_id":"project-one",
		"expired":"2030-01-01T00:00:00Z"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccountID != "google-account-one" || credential.ProjectID != "project-one" {
		t.Fatalf("credential = %#v", credential)
	}
	if got := credential.SecretValues(); !reflect.DeepEqual(got, []string{
		"access-secret", "refresh-secret", "google-account-one", "owner@example.com", "project-one",
	}) {
		t.Fatalf("SecretValues() = %q", got)
	}
}

func TestNormalizeExecutionErrorRecognizesWrappedBridgeError(t *testing.T) {
	bridge := &cpaembedded.AntigravityExecutionError{}
	normalized := normalizeExecutionError(fmt.Errorf("execute Antigravity request: %w", bridge))
	var executionError *ExecutionError
	if !errors.As(normalized, &executionError) {
		t.Fatalf("normalizeExecutionError() = %T, want *ExecutionError", normalized)
	}
}

func TestNormalizeErrorPreservesTokenRetryAfter(t *testing.T) {
	err := normalizeError(&cpaembedded.AntigravityTokenEndpointError{
		StatusCode: http.StatusTooManyRequests, Code: "rate_limit_exceeded",
		RetryAfter: 30 * time.Minute,
	})
	var tokenErr *TokenEndpointError
	if !errors.As(err, &tokenErr) || tokenErr.StatusCode != http.StatusTooManyRequests ||
		tokenErr.Code != "rate_limit_exceeded" || tokenErr.RetryAfter != 30*time.Minute {
		t.Fatalf("normalized error = %#v / %v", tokenErr, err)
	}
}
