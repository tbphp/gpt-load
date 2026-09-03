package kiro

import (
	"context"
	"net/http"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"
)

type ExecuteRequest struct {
	AttemptID       string
	Model           string
	Payload         []byte
	Format          string
	Headers         http.Header
	OriginalRequest []byte
	ContinuityKey   string
	ProxyURL        string
}

type ExecuteResponse struct {
	Payload                []byte
	Headers                http.Header
	AppliedReasoningEffort string
}

type ExecuteStreamChunk struct {
	Payload []byte
	Err     error
}

type ExecuteStreamResponse struct {
	Headers                http.Header
	Chunks                 <-chan ExecuteStreamChunk
	AppliedReasoningEffort string
}

type Executor interface {
	Execute(context.Context, string, Credential, ExecuteRequest) (ExecuteResponse, error)
	CountTokens(context.Context, string, Credential, ExecuteRequest) (ExecuteResponse, error)
	ExecuteStream(context.Context, string, Credential, ExecuteRequest) (*ExecuteStreamResponse, error)
}

type executor struct{ bridge cpaembedded.KiroHTTPExecutor }

func NewExecutor() Executor { return &executor{bridge: cpaembedded.NewKiroHTTPExecutor()} }

func (value *executor) Execute(ctx context.Context, credentialID string, credential Credential, request ExecuteRequest) (ExecuteResponse, error) {
	response, err := value.bridge.ExecuteCanonical(ctx, credentialID, credentialToBridge(credential), requestToBridge(request))
	return responseFromBridge(response), normalizeError(err)
}

func (value *executor) CountTokens(ctx context.Context, credentialID string, credential Credential, request ExecuteRequest) (ExecuteResponse, error) {
	response, err := value.bridge.CountTokensCanonical(ctx, credentialID, credentialToBridge(credential), requestToBridge(request))
	return responseFromBridge(response), normalizeError(err)
}

func (value *executor) ExecuteStream(ctx context.Context, credentialID string, credential Credential, request ExecuteRequest) (*ExecuteStreamResponse, error) {
	response, err := value.bridge.ExecuteStreamCanonical(ctx, credentialID, credentialToBridge(credential), requestToBridge(request))
	if response == nil {
		return nil, normalizeError(err)
	}
	chunks := make(chan ExecuteStreamChunk)
	go func() {
		defer close(chunks)
		for chunk := range response.Chunks {
			converted := ExecuteStreamChunk{Payload: append([]byte(nil), chunk.Payload...), Err: normalizeError(chunk.Err)}
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
	}, normalizeError(err)
}

func requestToBridge(request ExecuteRequest) cpaembedded.ExecuteRequest {
	return cpaembedded.ExecuteRequest{
		AttemptID: request.AttemptID, Model: request.Model, Payload: append([]byte(nil), request.Payload...),
		Format: request.Format, Headers: request.Headers.Clone(),
		OriginalRequest: append([]byte(nil), request.OriginalRequest...), ContinuityKey: request.ContinuityKey,
		ProxyURL: request.ProxyURL,
	}
}

func responseFromBridge(response cpaembedded.ExecuteResponse) ExecuteResponse {
	return ExecuteResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort: response.AppliedReasoningEffort,
	}
}
