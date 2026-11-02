package devin

import (
	"context"
	"io"
	"net/http"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v8/gptload-embedded/embedded"
)

type ExecuteRequest struct {
	AttemptID            string
	Model                string
	Payload              []byte
	Format               string
	Headers              http.Header
	OriginalRequest      []byte
	ContinuityKey        string
	BaseURL              string
	ProxyURL             string
	ProxyFromEnvironment bool
	ConfiguredHeaders    []string
}

type ExecuteResponse struct {
	Payload                      []byte
	Headers                      http.Header
	AppliedReasoningEffort       string
	AppliedReasoningMode         string
	AppliedReasoningBudgetTokens *int64
}

type ExecuteStreamChunk struct {
	Payload    []byte
	Incomplete bool
	Err        error
}

type ExecuteStreamResponse struct {
	Headers                      http.Header
	Chunks                       <-chan ExecuteStreamChunk
	AppliedReasoningEffort       string
	AppliedReasoningMode         string
	AppliedReasoningBudgetTokens *int64
}

type Executor interface {
	Execute(context.Context, string, Credential, ExecuteRequest) (ExecuteResponse, error)
	ExecuteStream(context.Context, string, Credential, ExecuteRequest) (*ExecuteStreamResponse, error)
}

type executor struct{ bridge cpaembedded.DevinHTTPExecutor }

func NewExecutor() Executor { return &executor{bridge: cpaembedded.NewDevinHTTPExecutor()} }

func (value *executor) Execute(ctx context.Context, credentialID string, credential Credential, request ExecuteRequest) (ExecuteResponse, error) {
	response, err := value.bridge.ExecuteCanonical(ctx, credentialID, cpaembedded.DevinCredential(credential), requestToBridge(request))
	return responseFromBridge(response), err
}

func (value *executor) ExecuteStream(ctx context.Context, credentialID string, credential Credential, request ExecuteRequest) (*ExecuteStreamResponse, error) {
	response, err := value.bridge.ExecuteStreamCanonical(ctx, credentialID, cpaembedded.DevinCredential(credential), requestToBridge(request))
	if response == nil {
		return nil, err
	}
	result := &ExecuteStreamResponse{
		Headers:                      response.Headers.Clone(),
		AppliedReasoningEffort:       response.AppliedReasoningEffort,
		AppliedReasoningMode:         response.AppliedReasoningMode,
		AppliedReasoningBudgetTokens: response.AppliedReasoningBudgetTokens,
	}
	if err != nil {
		return result, err
	}
	chunks := make(chan ExecuteStreamChunk)
	go func() {
		defer close(chunks)
		for chunk := range response.Chunks {
			converted := ExecuteStreamChunk{Payload: append([]byte(nil), chunk.Payload...), Incomplete: chunk.Incomplete, Err: chunk.Err}
			select {
			case chunks <- converted:
			case <-ctx.Done():
				return
			}
		}
	}()
	result.Chunks = chunks
	return result, nil
}

func requestToBridge(request ExecuteRequest) cpaembedded.ExecuteRequest {
	return cpaembedded.ExecuteRequest{
		AttemptID: request.AttemptID, Model: request.Model, Payload: append([]byte(nil), request.Payload...),
		Format: request.Format, Headers: request.Headers.Clone(),
		OriginalRequest: append([]byte(nil), request.OriginalRequest...), ContinuityKey: request.ContinuityKey,
		BaseURL: request.BaseURL, ProxyURL: request.ProxyURL, ProxyFromEnvironment: request.ProxyFromEnvironment, ConfiguredHeaders: append([]string(nil), request.ConfiguredHeaders...),
	}
}

func responseFromBridge(response cpaembedded.ExecuteResponse) ExecuteResponse {
	return ExecuteResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort:       response.AppliedReasoningEffort,
		AppliedReasoningMode:         response.AppliedReasoningMode,
		AppliedReasoningBudgetTokens: response.AppliedReasoningBudgetTokens,
	}
}

// ObserveStreamBody 由嵌入层识别 Connect 帧，主执行层只接收首响信号。
func ObserveStreamBody(body io.ReadCloser, notify func()) io.ReadCloser {
	return cpaembedded.ObserveDevinStreamBody(body, notify)
}
