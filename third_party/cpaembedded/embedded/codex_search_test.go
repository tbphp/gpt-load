package embedded

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCodexSearchResponseLimitPreservesFailureMetadata(t *testing.T) {
	body := strings.Repeat("x", maxObservedBodyBytes+1)
	for _, status := range []int{http.StatusUnauthorized, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			transport := claudeRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"17"}},
					Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			})
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
			response, err := NewCodexHTTPExecutor().ExecuteCanonical(ctx, "credential", CodexCredential{
				Type: ProviderCodex, AccessToken: "test-access", RefreshToken: "test-refresh", AccountID: "account-one",
			}, ExecuteRequest{Model: "gpt-5", Payload: []byte(`{"id":"session","model":"gpt-5"}`),
				Format: "openai-response", RequestPath: "/v1/alpha/search"})
			if err == nil || response.StatusCode != status || response.Headers.Get("Retry-After") != "17" || len(response.Payload) != 0 {
				t.Fatalf("oversized response = %#v, error = %v", response, err)
			}
			var statusError interface{ StatusCode() int }
			if status == http.StatusUnauthorized && (!errors.As(err, &statusError) || statusError.StatusCode() != status) {
				t.Fatalf("oversized 401 lost authentication failure: %v", err)
			}
		})
	}
}
