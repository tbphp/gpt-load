package bifrost

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"syscall"
	"testing"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestSDKStreamIdleTimeoutIsClassifiedAsTimeout(t *testing.T) {
	bifrostError := &schemas.BifrostError{
		IsBifrostError: true,
		Error: &schemas.ErrorField{
			Message: "Error reading stream: " + providerUtils.ErrStreamIdleTimeout.Error(),
			Error:   providerUtils.ErrStreamIdleTimeout,
		},
	}

	if got := classifyError(bifrostError, "", true, true); got != execution.ErrorKindTimeout {
		t.Fatalf("error kind = %q, want %q", got, execution.ErrorKindTimeout)
	}
	if got := unaryErrorResult(bifrostError, nil, nil).Error.Kind; got != execution.ErrorKindInternal {
		t.Fatalf("non-streaming error kind = %q, want %q", got, execution.ErrorKindInternal)
	}
}

type conversionCodedError interface {
	ConversionCode() string
}

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
			name: "forbidden model unavailable", status: http.StatusForbidden,
			body: `{"error":{"code":"model_not_found"}}`,
			want: execution.FailureHintModelUnavailable,
		},
		{
			name: "generic forbidden permission", status: http.StatusForbidden,
			body: `{"error":{"code":"permission_denied"}}`,
		},
		{
			name: "payment required", status: http.StatusPaymentRequired,
			body: `{"error":{"message":"billing disabled"}}`,
		},
		{
			name: "explicit invalid key under forbidden", status: http.StatusForbidden,
			body: `{"error":{"message":"API key not valid"}}`,
			want: execution.FailureHintInvalidCredential,
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

func TestConvertedSDKPreflightFailureIsAGroupScopedConversionFailure(t *testing.T) {
	t.Parallel()

	errorType := "invalid_request_error"
	bifrostError := &schemas.BifrostError{
		IsBifrostError: true,
		Error: &schemas.ErrorField{
			Type:    &errorType,
			Message: "cannot marshal target request",
		},
	}
	context := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	unary := convertedUnaryErrorResult(bifrostError, context, nil)
	if unary.DispatchState != execution.DispatchNotSent || unary.ResponseStarted || unary.Error == nil ||
		unary.Error.Kind != execution.ErrorKindConversionUnsupported ||
		unary.Error.Code != execution.ErrorCodeTargetSerializationFailed {
		t.Fatalf("converted unary result = %+v", unary)
	}

	stream := convertedStreamErrorResult(bifrostError, context, nil, false, 0, nil, "", nil)
	if stream.DispatchState != execution.DispatchNotSent || stream.ResponseStarted || stream.Error == nil ||
		stream.Error.Kind != execution.ErrorKindConversionUnsupported ||
		stream.Error.Code != execution.ErrorCodeTargetSerializationFailed {
		t.Fatalf("converted stream result = %+v", stream)
	}
}

func TestBuildConvertedRequestDistinguishesMalformedInputFromUnsupportedTargetConversion(t *testing.T) {
	t.Parallel()

	_, unsupportedErr := buildConvertedResponsesRequest(execution.AttemptSpec{
		ClientProtocol: protocol.Anthropic,
		Operation:      execution.OperationResponsesCreate,
		Body:           []byte(`{"model":"claude","messages":[]}`),
	}, schemas.OpenAI)
	var classified conversionCodedError
	if !errors.As(unsupportedErr, &classified) ||
		classified.ConversionCode() != execution.ErrorCodeTargetConversionNotSupported {
		t.Fatalf("unsupported error = %T %v", unsupportedErr, unsupportedErr)
	}

	_, malformedErr := buildConvertedResponsesRequest(execution.AttemptSpec{
		ClientProtocol: protocol.Anthropic,
		Operation:      execution.OperationChatCompletion,
		Body:           []byte(`{"model":`),
	}, schemas.OpenAI)
	classified = nil
	if malformedErr == nil || errors.As(malformedErr, &classified) {
		t.Fatalf("malformed error = %T %v, want unclassified client error", malformedErr, malformedErr)
	}
}
