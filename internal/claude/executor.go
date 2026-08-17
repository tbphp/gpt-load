package claude

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"
)

const maxExecutionErrorSummaryRunes = 512

// ExecuteRequest is the canonical request accepted by the embedded Claude bridge.
type ExecuteRequest struct {
	Model           string
	Payload         []byte
	Format          string
	Headers         http.Header
	OriginalRequest []byte
}

// ExecuteResponse is one converted non-streaming bridge response.
type ExecuteResponse struct {
	Payload                []byte
	Headers                http.Header
	AppliedReasoningEffort string
}

// ExecuteStreamResponse contains converted streaming chunks and metadata.
type ExecuteStreamResponse struct {
	Headers                http.Header
	Chunks                 <-chan ExecuteStreamChunk
	AppliedReasoningEffort string
}

// ExecuteStreamChunk contains one converted payload or terminal bridge error.
type ExecuteStreamChunk struct {
	Payload []byte
	Err     error
}

// ExecutionError carries only bounded provider classification. It deliberately
// cannot expose the embedded CPA error or the original provider response body.
type ExecutionError struct {
	status        int
	typeValue     string
	codeValue     string
	summary       string
	retryAfter    time.Duration
	requestScoped bool
}

type credentialScopedExecutionError struct {
	*ExecutionError
	credentialScoped bool
}

func (e *ExecutionError) Error() string {
	if e == nil || e.summary == "" {
		return "Claude upstream request failed"
	}
	return e.summary
}

func (e *ExecutionError) StatusCode() int {
	if e == nil {
		return 0
	}
	return e.status
}

func (e *ExecutionError) ErrorType() string {
	if e == nil {
		return ""
	}
	return e.typeValue
}

func (e *ExecutionError) ErrorCode() string {
	if e == nil {
		return ""
	}
	return e.codeValue
}

func (e *ExecutionError) RetryAfter() *time.Duration {
	if e == nil || e.retryAfter <= 0 {
		return nil
	}
	value := e.retryAfter
	return &value
}

func (e *ExecutionError) IsRequestScoped() bool {
	return e != nil && e.requestScoped
}

func (e *credentialScopedExecutionError) IsCredentialScoped() bool {
	return e != nil && e.credentialScoped
}

func (e *credentialScopedExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.ExecutionError
}

// Executor is the isolated CPA execution surface consumed by GPT-Load.
type Executor interface {
	Execute(context.Context, string, Credential, ExecuteRequest) (ExecuteResponse, error)
	CountTokens(context.Context, string, Credential, ExecuteRequest) (ExecuteResponse, error)
	ExecuteStream(context.Context, string, Credential, ExecuteRequest) (*ExecuteStreamResponse, error)
}

type executor struct {
	bridge cpaembedded.ClaudeHTTPExecutor
}

// NewExecutor creates the production Claude bridge executor.
func NewExecutor() Executor {
	return &executor{bridge: cpaembedded.NewClaudeHTTPExecutor()}
}

func (e *executor) Execute(
	ctx context.Context,
	credentialID string,
	credential Credential,
	request ExecuteRequest,
) (ExecuteResponse, error) {
	response, err := e.bridge.ExecuteCanonical(
		ctx,
		credentialID,
		credentialToBridge(credential),
		executeRequestToBridge(request),
	)
	return ExecuteResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort: response.AppliedReasoningEffort,
	}, normalizeExecutionError(err)
}

func (e *executor) CountTokens(
	ctx context.Context,
	credentialID string,
	credential Credential,
	request ExecuteRequest,
) (ExecuteResponse, error) {
	response, err := e.bridge.CountTokensCanonical(
		ctx,
		credentialID,
		credentialToBridge(credential),
		executeRequestToBridge(request),
	)
	return ExecuteResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
	}, normalizeExecutionError(err)
}

func (e *executor) ExecuteStream(
	ctx context.Context,
	credentialID string,
	credential Credential,
	request ExecuteRequest,
) (*ExecuteStreamResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	response, err := e.bridge.ExecuteStreamCanonical(
		ctx,
		credentialID,
		credentialToBridge(credential),
		executeRequestToBridge(request),
	)
	if response == nil {
		return nil, normalizeExecutionError(err)
	}
	if err != nil {
		return &ExecuteStreamResponse{
			Headers: response.Headers.Clone(), AppliedReasoningEffort: response.AppliedReasoningEffort,
		}, normalizeExecutionError(err)
	}
	chunks := make(chan ExecuteStreamChunk)
	go func() {
		defer close(chunks)
		for chunk := range response.Chunks {
			converted := ExecuteStreamChunk{
				Payload: append([]byte(nil), chunk.Payload...), Err: normalizeExecutionError(chunk.Err),
			}
			select {
			case chunks <- converted:
			case <-ctx.Done():
				return
			}
		}
	}()
	return &ExecuteStreamResponse{
		Headers: response.Headers.Clone(), Chunks: chunks,
		AppliedReasoningEffort: response.AppliedReasoningEffort,
	}, nil
}

func executeRequestToBridge(value ExecuteRequest) cpaembedded.ExecuteRequest {
	return cpaembedded.ExecuteRequest{
		Model: value.Model, Payload: append([]byte(nil), value.Payload...),
		Format: value.Format, Headers: value.Headers.Clone(),
		OriginalRequest: append([]byte(nil), value.OriginalRequest...),
	}
}

func normalizeExecutionError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	value := &ExecutionError{summary: boundedExecutionSummary(err.Error())}
	var status interface{ StatusCode() int }
	if errors.As(err, &status) && status != nil {
		value.status = status.StatusCode()
	}
	var typed interface {
		ErrorType() string
		ErrorCode() string
	}
	if errors.As(err, &typed) && typed != nil {
		value.typeValue = boundedExecutionScalar(typed.ErrorType())
		value.codeValue = boundedExecutionScalar(typed.ErrorCode())
	}
	var retry interface{ RetryAfter() *time.Duration }
	if errors.As(err, &retry) && retry != nil && retry.RetryAfter() != nil && *retry.RetryAfter() > 0 {
		value.retryAfter = *retry.RetryAfter()
	}
	var scoped interface{ IsRequestScoped() bool }
	if errors.As(err, &scoped) && scoped != nil {
		value.requestScoped = scoped.IsRequestScoped()
	}
	credentialScoped, credentialScopeKnown := false, false
	var credentialScope interface{ IsCredentialScoped() bool }
	if errors.As(err, &credentialScope) && credentialScope != nil {
		credentialScopeKnown = true
		credentialScoped = credentialScope.IsCredentialScoped()
	}
	if value.status == 0 && value.typeValue == "" && value.codeValue == "" &&
		value.retryAfter == 0 && !value.requestScoped {
		return err
	}
	if credentialScopeKnown {
		return &credentialScopedExecutionError{
			ExecutionError:   value,
			credentialScoped: credentialScoped,
		}
	}
	return value
}

func boundedExecutionSummary(value string) string {
	value = strings.Join(strings.Fields(strings.ToValidUTF8(value, "\uFFFD")), " ")
	if value == "" {
		return "Claude upstream request failed"
	}
	if utf8.RuneCountInString(value) > maxExecutionErrorSummaryRunes {
		value = string([]rune(value)[:maxExecutionErrorSummaryRunes])
	}
	return value
}

func boundedExecutionScalar(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

var _ Executor = (*executor)(nil)
