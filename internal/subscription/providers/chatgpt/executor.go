package chatgpt

import (
	"context"
	"net/http"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"

	"gpt-load/internal/subscription/providers/codex"
)

type ExecuteRequest struct {
	AttemptID       string
	Model           string
	Payload         []byte
	Format          string
	Headers         http.Header
	OriginalRequest []byte
	ContinuityKey   string
	BaseURL         string
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
	Execute(context.Context, string, codex.Credential, ExecuteRequest) (ExecuteResponse, error)
	CountTokens(context.Context, ExecuteRequest) (ExecuteResponse, error)
	ExecuteStream(context.Context, string, codex.Credential, ExecuteRequest) (*ExecuteStreamResponse, error)
}

type executor struct{ bridge cpaembedded.ChatGPTHTTPExecutor }

func NewExecutor() Executor { return &executor{bridge: cpaembedded.NewChatGPTHTTPExecutor()} }

func (value *executor) Execute(ctx context.Context, credentialID string, credential codex.Credential, request ExecuteRequest) (ExecuteResponse, error) {
	response, err := value.bridge.ExecuteCanonical(ctx, credentialID, credentialToBridge(credential), requestToBridge(request))
	return responseFromBridge(response), err
}

func (value *executor) CountTokens(ctx context.Context, request ExecuteRequest) (ExecuteResponse, error) {
	response, err := value.bridge.CountTokensCanonical(ctx, requestToBridge(request))
	return responseFromBridge(response), err
}

func (value *executor) ExecuteStream(ctx context.Context, credentialID string, credential codex.Credential, request ExecuteRequest) (*ExecuteStreamResponse, error) {
	response, err := value.bridge.ExecuteStreamCanonical(ctx, credentialID, credentialToBridge(credential), requestToBridge(request))
	if response == nil {
		return nil, err
	}
	chunks := make(chan ExecuteStreamChunk)
	go func() {
		defer close(chunks)
		for chunk := range response.Chunks {
			converted := ExecuteStreamChunk{Payload: append([]byte(nil), chunk.Payload...), Err: chunk.Err}
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
	}, err
}

func requestToBridge(request ExecuteRequest) cpaembedded.ExecuteRequest {
	return cpaembedded.ExecuteRequest{
		AttemptID: request.AttemptID, Model: request.Model, Payload: append([]byte(nil), request.Payload...),
		Format: request.Format, Headers: request.Headers.Clone(),
		OriginalRequest: append([]byte(nil), request.OriginalRequest...), ContinuityKey: request.ContinuityKey,
		BaseURL: request.BaseURL, ProxyURL: request.ProxyURL,
	}
}

func responseFromBridge(response cpaembedded.ExecuteResponse) ExecuteResponse {
	return ExecuteResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort: response.AppliedReasoningEffort,
	}
}

func credentialToBridge(value codex.Credential) cpaembedded.CodexCredential {
	return cpaembedded.CodexCredential{
		Type:         value.Type,
		IDToken:      value.IDToken,
		AccessToken:  value.AccessToken,
		RefreshToken: value.RefreshToken,
		AccountID:    value.AccountID,
		Email:        value.Email,
		Expire:       value.Expire,
		LastRefresh:  value.LastRefresh,
	}
}
