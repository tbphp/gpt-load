package bifrost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/maximhq/bifrost/core/providers/anthropic"
	"github.com/maximhq/bifrost/core/providers/gemini"
	"github.com/maximhq/bifrost/core/providers/openai"
	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

type listModelsSDKResult struct {
	response *schemas.BifrostListModelsResponse
	err      *schemas.BifrostError
}

func (r *Runtime) executeListModels(
	parent context.Context,
	spec execution.AttemptSpec,
	prepared preparedAttempt,
) execution.AttemptResult {
	requestContext, requestCancel := boundedRequestContext(parent, spec.Timeouts.Request)
	defer requestCancel()
	callContext, callCancel := context.WithCancel(requestContext)
	defer callCancel()

	bifrostContext := r.newSDKContext(callContext, spec, prepared.directKey)
	setTypedRequestURL(bifrostContext, prepared.typedURL)
	outcomeChannel := make(chan listModelsSDKResult, 1)
	go func() {
		response, bifrostError := r.core.ListModelsRequest(bifrostContext, prepared.listModelsRequest)
		outcomeChannel <- listModelsSDKResult{response: response, err: bifrostError}
	}()

	var outcome listModelsSDKResult
	select {
	case outcome = <-outcomeChannel:
		if requestContext.Err() != nil {
			return unaryContextFailure(requestContext, false)
		}
	case <-callContext.Done():
		return unaryContextFailure(requestContext, false)
	}
	if outcome.err != nil {
		return unaryErrorResult(outcome.err, bifrostContext, prepared.secrets)
	}
	if failure := largeUnaryResponseFailure(bifrostContext); failure != nil {
		return *failure
	}
	if outcome.response == nil {
		return attemptedUnaryFailure(execution.ErrorKindInternal, "execution runtime returned no model list")
	}
	body, err := encodeListModelsResponse(prepared.clientProtocol, prepared.provider, outcome.response)
	if err != nil {
		return startedUnaryFailure(http.StatusOK, responseHeaders(outcome.response.ExtraFields.ProviderResponseHeaders, bifrostContext, false), execution.ErrorKindInternal, "encode model list")
	}
	headers := responseHeaders(outcome.response.ExtraFields.ProviderResponseHeaders, bifrostContext, false)
	return execution.AttemptResult{
		DispatchState:     execution.DispatchMaybeSent,
		ResponseStarted:   true,
		StatusCode:        http.StatusOK,
		Header:            headers,
		Body:              body,
		UpstreamRequestID: upstreamRequestID(headers),
	}
}

func encodeListModelsResponse(clientProtocol protocol.Protocol, provider schemas.ModelProvider, response *schemas.BifrostListModelsResponse) ([]byte, error) {
	response = sanitizeListModelsResponse(provider, response)
	var wire any
	switch clientProtocol {
	case protocol.OpenAICompletions:
		converted := openai.ToOpenAIListModelsResponse(response)
		if converted != nil {
			converted.Object = "list"
		}
		wire = converted
	case protocol.Anthropic:
		wire = anthropic.ToAnthropicListModelsResponse(response)
	case protocol.Gemini:
		wire = gemini.ToGeminiListModelsResponse(response)
	default:
		return nil, fmt.Errorf("unsupported model list protocol")
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("marshal model list")
	}
	return body, nil
}

func sanitizeListModelsResponse(provider schemas.ModelProvider, response *schemas.BifrostListModelsResponse) *schemas.BifrostListModelsResponse {
	if response == nil {
		return nil
	}
	clone := *response
	clone.Data = append([]schemas.Model(nil), response.Data...)
	prefix := string(provider) + "/"
	filtered := clone.Data[:0]
	for _, model := range clone.Data {
		if provider == schemas.OpenRouter && embeddingOnlyOutputModel(model) {
			continue
		}
		model.ID = strings.TrimPrefix(model.ID, prefix)
		filtered = append(filtered, model)
	}
	clone.Data = filtered
	clone.ExtraFields = schemas.BifrostResponseExtraFields{}
	clone.KeyStatuses = nil
	return &clone
}

func embeddingOnlyOutputModel(model schemas.Model) bool {
	if model.Architecture == nil || len(model.Architecture.OutputModalities) == 0 {
		return false
	}
	for _, modality := range model.Architecture.OutputModalities {
		switch strings.ToLower(strings.TrimSpace(modality)) {
		case "embedding", "embeddings":
		default:
			return false
		}
	}
	return true
}
