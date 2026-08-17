package antigravity

import (
	"context"
	"net/http"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"
)

// ExecuteRequest is the canonical request accepted by the embedded
// Antigravity execution bridge.
type ExecuteRequest struct {
	AttemptID       string
	Model           string
	Payload         []byte
	Format          string
	Headers         http.Header
	OriginalRequest []byte
	ContinuityKey   string
}

type ExecuteResponse struct {
	Payload                []byte
	Headers                http.Header
	AppliedReasoningEffort string
}

type ExecuteStreamResponse struct {
	Headers                http.Header
	Chunks                 <-chan ExecuteStreamChunk
	AppliedReasoningEffort string
}

type ExecuteStreamChunk struct {
	Payload []byte
	Err     error
}

type ExecutionError struct {
	status        int
	typeValue     string
	codeValue     string
	summary       string
	retryAfter    *time.Duration
	requestScoped bool
}

func (err *ExecutionError) Error() string {
	if err == nil || err.summary == "" {
		return "Antigravity upstream request failed"
	}
	return err.summary
}

func (err *ExecutionError) StatusCode() int {
	if err == nil {
		return 0
	}
	return err.status
}

func (err *ExecutionError) ErrorType() string {
	if err == nil {
		return ""
	}
	return err.typeValue
}

func (err *ExecutionError) ErrorCode() string {
	if err == nil {
		return ""
	}
	return err.codeValue
}

func (err *ExecutionError) RetryAfter() *time.Duration {
	if err == nil || err.retryAfter == nil {
		return nil
	}
	value := *err.retryAfter
	return &value
}

func (err *ExecutionError) IsRequestScoped() bool {
	return err != nil && err.requestScoped
}

// Executor is the isolated CPA execution surface consumed by GPT-Load.
type Executor interface {
	Execute(context.Context, string, Credential, ExecuteRequest) (ExecuteResponse, error)
	CountTokens(context.Context, string, Credential, ExecuteRequest) (ExecuteResponse, error)
	ExecuteStream(context.Context, string, Credential, ExecuteRequest) (*ExecuteStreamResponse, error)
}

type executor struct {
	bridge cpaembedded.AntigravityHTTPExecutor
}

func NewExecutor() Executor { return &executor{bridge: cpaembedded.NewAntigravityHTTPExecutor()} }

func (executor *executor) Execute(ctx context.Context, credentialID string, credential Credential, request ExecuteRequest) (ExecuteResponse, error) {
	response, err := executor.bridge.ExecuteCanonical(ctx, credentialID, credentialToBridge(credential), executeRequestToBridge(request))
	return ExecuteResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort: response.AppliedReasoningEffort,
	}, normalizeExecutionError(err)
}

func (executor *executor) CountTokens(ctx context.Context, credentialID string, credential Credential, request ExecuteRequest) (ExecuteResponse, error) {
	response, err := executor.bridge.CountTokensCanonical(ctx, credentialID, credentialToBridge(credential), executeRequestToBridge(request))
	return ExecuteResponse{Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone()}, normalizeExecutionError(err)
}

func (executor *executor) ExecuteStream(ctx context.Context, credentialID string, credential Credential, request ExecuteRequest) (*ExecuteStreamResponse, error) {
	response, err := executor.bridge.ExecuteStreamCanonical(ctx, credentialID, credentialToBridge(credential), executeRequestToBridge(request))
	if response == nil {
		return nil, normalizeExecutionError(err)
	}
	if err != nil {
		return &ExecuteStreamResponse{Headers: response.Headers.Clone(), AppliedReasoningEffort: response.AppliedReasoningEffort}, normalizeExecutionError(err)
	}
	chunks := make(chan ExecuteStreamChunk)
	go func() {
		defer close(chunks)
		for chunk := range response.Chunks {
			converted := ExecuteStreamChunk{Payload: append([]byte(nil), chunk.Payload...), Err: normalizeExecutionError(chunk.Err)}
			select {
			case chunks <- converted:
			case <-ctx.Done():
				return
			}
		}
	}()
	return &ExecuteStreamResponse{Headers: response.Headers.Clone(), Chunks: chunks, AppliedReasoningEffort: response.AppliedReasoningEffort}, nil
}

func executeRequestToBridge(value ExecuteRequest) cpaembedded.ExecuteRequest {
	return cpaembedded.ExecuteRequest{
		AttemptID: value.AttemptID, Model: value.Model, Payload: append([]byte(nil), value.Payload...), Format: value.Format,
		Headers: value.Headers.Clone(), OriginalRequest: append([]byte(nil), value.OriginalRequest...),
		ContinuityKey: value.ContinuityKey,
	}
}

func normalizeExecutionError(err error) error {
	if err == nil {
		return nil
	}
	bridge, ok := err.(*cpaembedded.AntigravityExecutionError)
	if !ok || bridge == nil {
		return err
	}
	result := &ExecutionError{
		status: bridge.StatusCode(), typeValue: bridge.ErrorType(), codeValue: bridge.ErrorCode(),
		summary: bridge.Error(), requestScoped: bridge.IsRequestScoped(),
	}
	if retryAfter := bridge.RetryAfter(); retryAfter != nil && *retryAfter > 0 {
		value := *retryAfter
		result.retryAfter = &value
	}
	return result
}

var _ Executor = (*executor)(nil)
