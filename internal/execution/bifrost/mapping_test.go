package bifrost

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"syscall"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/execution"
)

func TestPassthroughHTTPErrorProducesNeutralFailureHints(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   execution.FailureHint
	}{
		{
			name: "Gemini invalid credential", status: http.StatusBadRequest,
			body: `{"error":{"status":"PERMISSION_DENIED","message":"API key not valid"}}`,
			want: execution.FailureHintInvalidCredential,
		},
		{
			name: "OpenAI rate limit", status: http.StatusBadRequest,
			body: `{"error":{"type":"rate_limit_error","code":"quota_exceeded"}}`,
			want: execution.FailureHintRateLimited,
		},
		{
			name: "model unavailable", status: http.StatusBadRequest,
			body: `{"error":{"code":"model_not_available"}}`,
			want: execution.FailureHintModelUnavailable,
		},
		{
			name: "server error", status: http.StatusServiceUnavailable,
			body: `{"error":{"message":"overloaded"}}`,
			want: execution.FailureHintHostError,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := passthroughHTTPError(test.status, nil, []byte(test.body), nil)
			if got.Hint != test.want {
				t.Fatalf("hint = %q, want %q; evidence=%+v", got.Hint, test.want, got)
			}
		})
	}
}

func TestPassthroughHTTPErrorRetainsSanitizedErrorMessage(t *testing.T) {
	const secret = "provider-secret-value"
	evidence := passthroughHTTPError(
		http.StatusServiceUnavailable,
		nil,
		[]byte("auth_unavailable: no auth available (providers=codex, model=gpt-5.6-sol), token="+secret),
		[]string{secret},
	)

	const want = "auth_unavailable: no auth available (providers=codex, model=gpt-5.6-sol), token=[REDACTED]"
	if evidence.Summary != want {
		t.Fatalf("summary = %q, want %q", evidence.Summary, want)
	}
}

func TestSDKTransportErrorDispatchEvidence(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want execution.DispatchState
	}{
		{
			name: "DNS lookup never dispatched",
			err:  &net.DNSError{Err: "no such host", Name: "missing.invalid"},
			want: execution.DispatchNotSent,
		},
		{
			name: "dial refused never dispatched",
			err:  &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED},
			want: execution.DispatchNotSent,
		},
		{
			name: "link local policy rejection never dispatched",
			err:  errors.New("connection to link-local IP 169.254.169.254 is not allowed"),
			want: execution.DispatchNotSent,
		},
		{
			name: "unspecified address policy rejection never dispatched",
			err:  errors.New("connection to unspecified IP 0.0.0.0 is not allowed"),
			want: execution.DispatchNotSent,
		},
		{
			name: "connection reset may have dispatched",
			err:  &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET},
			want: execution.DispatchMaybeSent,
		},
		{
			name: "EOF may have dispatched",
			err:  io.EOF,
			want: execution.DispatchMaybeSent,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status := 502
			typeValue := schemas.ProviderConnectionFailed
			bifrostError := &schemas.BifrostError{
				StatusCode: &status,
				Error: &schemas.ErrorField{
					Type:  &typeValue,
					Error: test.err,
				},
			}
			result := unaryErrorResult(
				bifrostError,
				schemas.NewBifrostContext(context.Background(), schemas.NoDeadline),
				nil,
			)
			if result.DispatchState != test.want || result.ResponseStarted ||
				result.Error == nil || result.Error.Kind != execution.ErrorKindTransport {
				t.Fatalf("result = %+v, want dispatch=%s transport without response", result, test.want)
			}
		})
	}
}

func TestSDKTransportErrorAfterStreamStartIsAlwaysMaybeSent(t *testing.T) {
	status := 502
	typeValue := schemas.ProviderConnectionFailed
	bifrostError := &schemas.BifrostError{
		StatusCode: &status,
		Error: &schemas.ErrorField{
			Type:  &typeValue,
			Error: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED},
		},
	}
	result := streamErrorResult(
		bifrostError,
		schemas.NewBifrostContext(context.Background(), schemas.NoDeadline),
		nil,
		true,
		200,
		nil,
		"model",
		nil,
	)
	if result.DispatchState != execution.DispatchMaybeSent || !result.ResponseStarted {
		t.Fatalf("result = %+v", result)
	}
}
