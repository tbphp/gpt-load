package bifrost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/maximhq/bifrost/core/providers/anthropic"
	"github.com/maximhq/bifrost/core/providers/gemini"
	"github.com/maximhq/bifrost/core/providers/openai"
	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/reasoning"
)

type responsesUnarySDKResult struct {
	response *schemas.BifrostResponsesResponse
	err      *schemas.BifrostError
}

type responsesStreamSDKResult struct {
	stream chan *schemas.BifrostStreamChunk
	err    *schemas.BifrostError
}

type targetConversionError struct {
	code    string
	summary string
}

func (err *targetConversionError) Error() string {
	if err == nil {
		return "target conversion is not supported"
	}
	return err.summary
}

func (err *targetConversionError) ConversionCode() string {
	if err == nil || err.code == "" {
		return execution.ErrorCodeTargetConversionNotSupported
	}
	return err.code
}

func unsupportedTargetConversion(summary string) error {
	return &targetConversionError{summary: summary}
}

func criticalSemanticLoss(summary string) error {
	return &targetConversionError{
		code:    execution.ErrorCodeCriticalSemanticLoss,
		summary: summary,
	}
}

func buildConvertedResponsesRequest(spec execution.AttemptSpec, provider schemas.ModelProvider) (*schemas.BifrostResponsesRequest, error) {
	conversionContext := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	defer conversionContext.Cancel()

	var request *schemas.BifrostResponsesRequest
	switch spec.ClientProtocol {
	case protocol.OpenAIResponses:
		if spec.Operation != execution.OperationResponsesCreate &&
			spec.Operation != execution.OperationResponsesInputTokens {
			return nil, unsupportedTargetConversion("converted Responses operation is not supported")
		}
		var wire openai.OpenAIResponsesRequest
		if err := json.Unmarshal(spec.Body, &wire); err != nil {
			return nil, fmt.Errorf("invalid OpenAI Responses request body")
		}
		request = wire.ToBifrostResponsesRequest(conversionContext)
	case protocol.Anthropic:
		if spec.Operation != execution.OperationChatCompletion &&
			spec.Operation != execution.OperationCountTokens {
			return nil, unsupportedTargetConversion("converted Anthropic operation is not supported")
		}
		var wire anthropic.AnthropicMessageRequest
		if err := json.Unmarshal(spec.Body, &wire); err != nil {
			return nil, fmt.Errorf("invalid Anthropic request body")
		}
		if provider == schemas.DeepSeek && wire.Container != nil {
			return nil, criticalSemanticLoss("DeepSeek Anthropic route cannot preserve container semantics")
		}
		request = wire.ToBifrostResponsesRequest(conversionContext)
		preserveDeepSeekAnthropicEffort(request, provider, wire.OutputConfig)
	case protocol.Gemini:
		if spec.Operation != execution.OperationChatCompletion &&
			spec.Operation != execution.OperationCountTokens {
			return nil, unsupportedTargetConversion("converted Gemini operation is not supported")
		}
		if spec.Operation == execution.OperationCountTokens {
			var wire gemini.GeminiCountTokensRequest
			if err := json.Unmarshal(spec.Body, &wire); err != nil {
				return nil, fmt.Errorf("invalid Gemini count tokens request body")
			}
			generation := wire.ToGeminiGenerationRequest()
			if generation == nil {
				return nil, fmt.Errorf("invalid Gemini count tokens request body")
			}
			generation.Model = spec.ClientModel
			request = generation.ToBifrostResponsesRequest(conversionContext)
		} else {
			var wire gemini.GeminiGenerationRequest
			if err := json.Unmarshal(spec.Body, &wire); err != nil {
				return nil, fmt.Errorf("invalid Gemini request body")
			}
			wire.Model = spec.ClientModel
			request = wire.ToBifrostResponsesRequest(conversionContext)
		}
	default:
		return nil, unsupportedTargetConversion("converted protocol is not supported")
	}
	if request == nil || len(request.Input) == 0 {
		return nil, fmt.Errorf("converted request input is required")
	}
	request.Provider = provider
	request.Model = spec.UpstreamModel
	request.Fallbacks = nil
	request.RawRequestBody = nil
	stripResponsesControlParams(request.Params)
	return request, nil
}

type countTokensSDKResult struct {
	response *schemas.BifrostCountTokensResponse
	err      *schemas.BifrostError
}

func (r *Runtime) executeCountTokens(
	parent context.Context,
	spec execution.AttemptSpec,
	prepared preparedAttempt,
) execution.AttemptResult {
	requestContext, requestCancel := boundedRequestContext(parent, spec.Timeouts.Request)
	defer requestCancel()
	callContext, callCancel := context.WithCancel(requestContext)
	defer callCancel()

	bifrostContext := r.newSDKContext(callContext, spec, prepared.directKey)
	enableConvertedWireCapture(bifrostContext, prepared)
	setTypedRequestURL(bifrostContext, prepared.typedURL)
	outcomeChannel := make(chan countTokensSDKResult, 1)
	go func() {
		response, bifrostError := r.core.CountTokensRequest(bifrostContext, prepared.countTokensRequest)
		outcomeChannel <- countTokensSDKResult{response: response, err: bifrostError}
	}()

	var outcome countTokensSDKResult
	select {
	case outcome = <-outcomeChannel:
		if requestContext.Err() != nil {
			return unaryContextFailure(requestContext, false)
		}
	case <-callContext.Done():
		return unaryContextFailure(requestContext, false)
	}
	if outcome.err != nil {
		return convertedUnaryErrorResult(outcome.err, bifrostContext, prepared.secrets)
	}
	if failure := largeUnaryResponseFailure(bifrostContext); failure != nil {
		return *failure
	}
	if outcome.response == nil {
		return attemptedUnaryFailure(execution.ErrorKindInternal, "execution runtime returned no count tokens response")
	}
	headers := responseHeaders(outcome.response.ExtraFields.ProviderResponseHeaders, bifrostContext, false)
	body, err := encodeCountTokensResponse(prepared.clientProtocol, outcome.response)
	if err != nil {
		return startedUnaryFailure(http.StatusOK, headers, execution.ErrorKindInternal, "encode count tokens response")
	}
	return execution.AttemptResult{
		DispatchState:     execution.DispatchMaybeSent,
		ResponseStarted:   true,
		StatusCode:        http.StatusOK,
		Header:            headers,
		Body:              body,
		Model:             outcome.response.Model,
		UpstreamRequestID: upstreamRequestID(headers),
	}
}

func encodeCountTokensResponse(
	clientProtocol protocol.Protocol,
	response *schemas.BifrostCountTokensResponse,
) ([]byte, error) {
	if response == nil {
		return nil, fmt.Errorf("response is nil")
	}
	var wire any
	switch clientProtocol {
	case protocol.OpenAIResponses:
		object := map[string]any{
			"object":       "response.input_tokens",
			"input_tokens": response.InputTokens,
		}
		if response.InputTokensDetails != nil {
			object["input_tokens_details"] = response.InputTokensDetails
		}
		wire = object
	case protocol.Anthropic:
		wire = anthropic.ToAnthropicCountTokensResponse(response)
	case protocol.Gemini:
		wire = gemini.ToGeminiCountTokensResponse(response)
	default:
		return nil, fmt.Errorf("unsupported count tokens response protocol")
	}
	if wire == nil {
		return nil, fmt.Errorf("count tokens response conversion returned nil")
	}
	return json.Marshal(wire)
}

func countTokensTypedTarget(
	providerKind channel.ProviderKind,
	baseURL string,
	model string,
	rawQuery string,
) (string, protocol.Protocol, error) {
	var resourcePath string
	var upstreamProtocol protocol.Protocol
	switch providerKind {
	case channel.ProviderOpenAI:
		resourcePath = "/v1/responses/input_tokens"
		upstreamProtocol = protocol.OpenAIResponses
	case channel.ProviderAnthropic:
		resourcePath = "/v1/messages/count_tokens"
		upstreamProtocol = protocol.Anthropic
	case channel.ProviderGemini:
		resourcePath = "/models/" + url.PathEscape(model) + ":countTokens"
		upstreamProtocol = protocol.Gemini
	default:
		return "", "", fmt.Errorf("provider does not support upstream token counting")
	}
	if baseURL != "" {
		target, err := resolveTypedTargetURL(baseURL, resourcePath, rawQuery)
		return target, upstreamProtocol, err
	}
	return appendTypedQuery(resourcePath, rawQuery), upstreamProtocol, nil
}

func preserveDeepSeekAnthropicEffort(
	request *schemas.BifrostResponsesRequest,
	provider schemas.ModelProvider,
	outputConfig *anthropic.AnthropicOutputConfig,
) {
	if request == nil || provider != schemas.DeepSeek || outputConfig == nil || outputConfig.Effort == nil {
		return
	}
	if request.Params == nil {
		request.Params = &schemas.ResponsesParameters{}
	}
	if request.Params.ExtraParams == nil {
		request.Params.ExtraParams = make(map[string]any)
	}
	request.Params.ExtraParams["output_config"] = map[string]any{
		"effort": *outputConfig.Effort,
	}
}

func stripResponsesControlParams(params *schemas.ResponsesParameters) {
	if params == nil || len(params.ExtraParams) == 0 {
		return
	}
	for key := range params.ExtraParams {
		normalized := strings.ReplaceAll(strings.ToLower(key), "-", "_")
		switch normalized {
		case "provider", "fallback", "fallbacks", "authorization", "proxy_authorization",
			"api_key", "apikey", "x_api_key", "x_goog_api_key":
			delete(params.ExtraParams, key)
		}
	}
}

func (r *Runtime) executeConvertedResponses(
	parent context.Context,
	spec execution.AttemptSpec,
	prepared preparedAttempt,
) (result execution.AttemptResult) {
	var appliedReasoning *reasoning.Config
	defer func() {
		result.AppliedReasoning = appliedReasoning
	}()

	requestContext, requestCancel := boundedRequestContext(parent, spec.Timeouts.Request)
	defer requestCancel()
	callContext, callCancel := context.WithCancel(requestContext)
	defer callCancel()

	bifrostContext := r.newSDKContext(callContext, spec, prepared.directKey)
	enableConvertedWireCapture(bifrostContext, prepared)
	setTypedRequestURL(bifrostContext, prepared.typedURL)
	outcomeChannel := make(chan responsesUnarySDKResult, 1)
	go func() {
		response, bifrostError := r.core.ResponsesRequest(bifrostContext, prepared.responsesRequest)
		outcomeChannel <- responsesUnarySDKResult{response: response, err: bifrostError}
	}()

	var outcome responsesUnarySDKResult
	select {
	case outcome = <-outcomeChannel:
		if requestContext.Err() != nil {
			return unaryContextFailure(requestContext, false)
		}
	case <-callContext.Done():
		return unaryContextFailure(requestContext, false)
	}

	if outcome.err != nil {
		captureAppliedReasoning(&outcome.err.ExtraFields.RawRequest, &appliedReasoning)
		return convertedUnaryErrorResult(outcome.err, bifrostContext, prepared.secrets)
	}
	if outcome.response != nil {
		captureAppliedReasoning(&outcome.response.ExtraFields.RawRequest, &appliedReasoning)
	}
	if failure := largeUnaryResponseFailure(bifrostContext); failure != nil {
		return *failure
	}
	if outcome.response == nil {
		return attemptedUnaryFailure(execution.ErrorKindInternal, "execution runtime returned no response")
	}
	headers := responseHeaders(outcome.response.ExtraFields.ProviderResponseHeaders, bifrostContext, false)
	body, usageEvidence, err := encodeConvertedResponsesResponse(prepared.clientProtocol, bifrostContext, outcome.response)
	if err != nil {
		return startedUnaryFailure(http.StatusOK, headers, execution.ErrorKindInternal, "encode converted response")
	}
	if needsClientModelAlias(spec) {
		body, err = rewriteClientResponseModel(spec.ClientProtocol, body, spec.ClientModel)
		if err != nil {
			return startedUnaryFailure(http.StatusOK, headers, execution.ErrorKindInternal, "rewrite converted response model")
		}
	}
	return execution.AttemptResult{
		DispatchState:     execution.DispatchMaybeSent,
		ResponseStarted:   true,
		StatusCode:        http.StatusOK,
		Header:            headers,
		Body:              body,
		Model:             outcome.response.Model,
		UpstreamRequestID: upstreamRequestID(headers),
		Usage:             usageEvidence,
	}
}

func encodeConvertedResponsesResponse(
	clientProtocol protocol.Protocol,
	ctx *schemas.BifrostContext,
	response *schemas.BifrostResponsesResponse,
) ([]byte, *execution.UsageEvidence, error) {
	if response == nil {
		return nil, nil, fmt.Errorf("response is nil")
	}
	usageEvidence, err := usageEvidenceFromResponses(response.Usage)
	if err != nil {
		return nil, nil, err
	}
	var wire any
	switch clientProtocol {
	case protocol.OpenAIResponses:
		wire = response.WithDefaults()
	case protocol.Anthropic:
		wire = anthropic.ToAnthropicResponsesResponse(ctx, response)
	case protocol.Gemini:
		wire = gemini.ToGeminiResponsesResponse(response)
	default:
		return nil, nil, fmt.Errorf("unsupported converted response protocol")
	}
	if wire == nil {
		return nil, nil, fmt.Errorf("response conversion returned nil")
	}
	body, err := marshalClientWire(clientProtocol, wire)
	if err != nil {
		return nil, nil, err
	}
	return body, usageEvidence, nil
}

func marshalClientWire(clientProtocol protocol.Protocol, value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal converted response")
	}
	if clientProtocol != protocol.OpenAIResponses {
		return body, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return nil, fmt.Errorf("sanitize converted OpenAI response")
	}
	delete(object, "extra_fields")
	delete(object, "provider_extra_fields")
	if rawUsage := object["usage"]; len(rawUsage) > 0 {
		var usageObject map[string]json.RawMessage
		if json.Unmarshal(rawUsage, &usageObject) == nil {
			delete(usageObject, "cost")
			object["usage"], _ = json.Marshal(usageObject)
		}
	}
	body, err = json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("marshal sanitized OpenAI response")
	}
	return body, nil
}

func convertedTypedTarget(
	providerKind channel.ProviderKind,
	baseURL string,
	model string,
	responses bool,
	stream bool,
	rawQuery string,
) (string, protocol.Protocol, error) {
	var resourcePath string
	var upstreamProtocol protocol.Protocol
	switch providerKind {
	case channel.ProviderOpenAI, channel.ProviderOpenAICompatible:
		resourcePath = "/chat/completions"
		upstreamProtocol = protocol.OpenAICompletions
		// Compatible targets are configured with Chat Completions only. Bifrost
		// translates a converted Responses request back to that operation; the
		// Responses path is reserved for native OpenAI targets.
		if responses && providerKind == channel.ProviderOpenAI {
			resourcePath = "/responses"
			upstreamProtocol = protocol.OpenAIResponses
		}
		if providerKind == channel.ProviderOpenAI {
			resourcePath = "/v1" + resourcePath
		}
	case channel.ProviderAnthropic:
		resourcePath = "/v1/messages"
		upstreamProtocol = protocol.Anthropic
	case channel.ProviderGemini:
		upstreamProtocol = protocol.Gemini
		action := "generateContent"
		if stream {
			action = "streamGenerateContent"
		}
		resourcePath = "/models/" + url.PathEscape(model) + ":" + action
		if stream {
			rawQuery = setRawQueryValue(rawQuery, "alt", "sse")
		}
	case channel.ProviderDeepSeek:
		resourcePath = "/chat/completions"
		upstreamProtocol = protocol.OpenAICompletions
	case channel.ProviderOpenRouter:
		resourcePath = "/v1/chat/completions"
		upstreamProtocol = protocol.OpenAICompletions
		if responses {
			resourcePath = "/v1/responses"
			upstreamProtocol = protocol.OpenAIResponses
		}
	case channel.ProviderGroq:
		if stream && rawQuery != "" {
			return "", "", fmt.Errorf("provider streaming query is not supported")
		}
		resourcePath = "/v1/chat/completions"
		upstreamProtocol = protocol.OpenAICompletions
	case channel.ProviderXAI:
		resourcePath = "/v1/chat/completions"
		upstreamProtocol = protocol.OpenAICompletions
		if responses {
			resourcePath = "/v1/responses"
			upstreamProtocol = protocol.OpenAIResponses
		} else if stream && rawQuery != "" {
			return "", "", fmt.Errorf("provider streaming query is not supported")
		}
	case channel.ProviderAzureOpenAI, channel.ProviderAWSBedrock, channel.ProviderGoogleVertex:
		if rawQuery != "" {
			return "", "", fmt.Errorf("provider-specific query is not supported")
		}
		return "", "", nil
	default:
		return "", "", fmt.Errorf("unsupported provider kind")
	}
	if baseURL != "" {
		target, err := resolveTypedTargetURL(baseURL, resourcePath, rawQuery)
		return target, upstreamProtocol, err
	}
	return appendTypedQuery(resourcePath, rawQuery), upstreamProtocol, nil
}

func deepSeekNativeTypedTarget(baseURL string, clientProtocol protocol.Protocol, rawQuery string) (string, protocol.Protocol, error) {
	resourcePath := ""
	upstreamProtocol := clientProtocol
	switch clientProtocol {
	case protocol.OpenAICompletions:
		resourcePath = "/chat/completions"
	case protocol.OpenAIResponses:
		resourcePath = "/responses"
	case protocol.Anthropic:
		resourcePath = "/anthropic/v1/messages"
	default:
		return "", "", fmt.Errorf("unsupported DeepSeek native protocol")
	}
	if baseURL != "" {
		target, err := resolveTypedTargetURL(baseURL, resourcePath, rawQuery)
		return target, upstreamProtocol, err
	}
	return appendTypedQuery(resourcePath, rawQuery), upstreamProtocol, nil
}

func convertedListModelsTarget(providerKind channel.ProviderKind, baseURL string, rawQuery string) (string, protocol.Protocol, error) {
	resourcePath := "/models"
	upstreamProtocol := protocol.OpenAICompletions
	switch providerKind {
	case channel.ProviderOpenAI:
		resourcePath = "/v1/models"
	case channel.ProviderAnthropic:
		resourcePath = "/v1/models"
		upstreamProtocol = protocol.Anthropic
	case channel.ProviderGemini:
		resourcePath = "/models"
		upstreamProtocol = protocol.Gemini
	case channel.ProviderDeepSeek:
		resourcePath = "/models"
	case channel.ProviderOpenRouter, channel.ProviderGroq, channel.ProviderXAI:
		resourcePath = "/v1/models"
	case channel.ProviderOpenAICompatible:
		if baseURL == "" {
			return "", "", fmt.Errorf("compatible target is required")
		}
		target, err := resolveTypedTargetURL(baseURL, resourcePath, rawQuery)
		return target, upstreamProtocol, err
	case channel.ProviderAzureOpenAI, channel.ProviderAWSBedrock, channel.ProviderGoogleVertex:
		if rawQuery != "" {
			return "", "", fmt.Errorf("provider-specific query is not supported")
		}
		return "", "", nil
	default:
		return "", "", fmt.Errorf("unsupported provider kind")
	}
	return appendTypedQuery(resourcePath, rawQuery), upstreamProtocol, nil
}

func appendTypedQuery(path, rawQuery string) string {
	if rawQuery == "" {
		return path
	}
	return path + "?" + rawQuery
}

func setRawQueryValue(source, key, value string) string {
	kept := removeRawQueryValue(source, key)
	if kept == "" {
		return url.QueryEscape(key) + "=" + url.QueryEscape(value)
	}
	return kept + "&" + url.QueryEscape(key) + "=" + url.QueryEscape(value)
}

func removeRawQueryValue(source, key string) string {
	segments := strings.Split(source, "&")
	kept := make([]string, 0, len(segments))
	for _, segment := range segments {
		rawKey := segment
		if separator := strings.IndexByte(rawKey, '='); separator >= 0 {
			rawKey = rawKey[:separator]
		}
		decodedKey, err := url.QueryUnescape(rawKey)
		if err == nil && strings.EqualFold(decodedKey, key) {
			continue
		}
		if source != "" || segment != "" {
			kept = append(kept, segment)
		}
	}
	return strings.Join(kept, "&")
}

func (r *Runtime) executeConvertedResponsesStream(
	parent context.Context,
	spec execution.AttemptSpec,
	prepared preparedAttempt,
	sink execution.StreamSink,
) (result execution.StreamResult) {
	var appliedReasoning *reasoning.Config
	defer func() {
		result.AppliedReasoning = appliedReasoning
	}()

	requestContext, requestCancel := boundedRequestContext(parent, spec.Timeouts.Request)
	defer requestCancel()
	callContext, callCancel := context.WithCancel(requestContext)
	defer callCancel()
	preResponse := startPreResponseGate(callCancel, spec.Timeouts)
	defer preResponse.stop()

	bifrostContext := r.newStreamingSDKContext(callContext, spec, prepared.directKey)
	enableConvertedWireCapture(bifrostContext, prepared)
	setTypedRequestURL(bifrostContext, prepared.typedURL)
	outcomeChannel := make(chan responsesStreamSDKResult, 1)
	go func() {
		stream, bifrostError := r.core.ResponsesStreamRequest(bifrostContext, prepared.responsesRequest)
		outcomeChannel <- responsesStreamSDKResult{stream: stream, err: bifrostError}
	}()

	var outcome responsesStreamSDKResult
	select {
	case outcome = <-outcomeChannel:
		if preResponse.expired() || requestContext.Err() != nil {
			return streamContextFailure(requestContext, preResponse.expired(), false, nil, "", "", nil)
		}
	case <-callContext.Done():
		return streamContextFailure(requestContext, preResponse.expired(), false, nil, "", "", nil)
	}
	preResponse.stop()
	if outcome.err != nil {
		captureAppliedReasoning(&outcome.err.ExtraFields.RawRequest, &appliedReasoning)
		result := convertedStreamErrorResult(outcome.err, bifrostContext, prepared.secrets, false, 0, nil, "", nil)
		markPromotedStreamRejectionReplaySafe(&result)
		return result
	}
	if outcome.stream == nil {
		return attemptedStreamFailure(execution.ErrorKindInternal, "execution runtime returned no stream")
	}

	headers := responseHeaders(nil, bifrostContext, true)
	requestID := upstreamRequestID(headers)
	if err := sink(execution.StreamEvent{
		Sequence:          1,
		Kind:              execution.StreamEventReady,
		StatusCode:        http.StatusOK,
		Header:            headers.Clone(),
		UpstreamRequestID: requestID,
	}); err != nil {
		callCancel()
		return streamSinkFailure(headers, requestID, "", nil)
	}

	encoder := newConvertedResponsesStreamEncoder(prepared.clientProtocol)
	sequence := uint64(1)
	model := spec.UpstreamModel
	var usageEvidence *execution.UsageEvidence
	idleTimer := newIdleTimer(spec.Timeouts.StreamIdle)
	defer idleTimer.stop()

	for {
		select {
		case <-requestContext.Done():
			callCancel()
			return streamContextFailure(requestContext, false, true, headers, requestID, model, usageEvidence)
		case <-idleTimer.channel():
			callCancel()
			return streamContextFailure(requestContext, true, true, headers, requestID, model, usageEvidence)
		case chunk, open := <-outcome.stream:
			idleTimer.pause()
			if requestContext.Err() != nil {
				callCancel()
				return streamContextFailure(requestContext, false, true, headers, requestID, model, usageEvidence)
			}
			if !open {
				if !sdkStreamEndedNormally(bifrostContext) {
					return terminatedStreamFailure(headers, requestID, model, usageEvidence)
				}
				return execution.StreamResult{
					DispatchState:     execution.DispatchMaybeSent,
					ResponseStarted:   true,
					StatusCode:        http.StatusOK,
					Header:            headers,
					Model:             model,
					UpstreamRequestID: requestID,
					Usage:             usageEvidence,
				}
			}
			if chunk == nil || chunk.BifrostResponsesStreamResponse == nil {
				callCancel()
				if chunk != nil && chunk.BifrostError != nil {
					captureAppliedReasoning(&chunk.BifrostError.ExtraFields.RawRequest, &appliedReasoning)
					return streamErrorResult(chunk.BifrostError, bifrostContext, prepared.secrets, true, http.StatusOK, headers, model, usageEvidence)
				}
				return streamErrorResult(nil, bifrostContext, prepared.secrets, true, http.StatusOK, headers, model, usageEvidence)
			}

			response := chunk.BifrostResponsesStreamResponse
			captureAppliedReasoning(&response.ExtraFields.RawRequest, &appliedReasoning)
			var chunkUsage *execution.UsageEvidence
			if response.Response != nil {
				if response.Response.Model != "" {
					model = response.Response.Model
				}
				var err error
				chunkUsage, err = usageEvidenceFromResponses(response.Response.Usage)
				if err != nil {
					callCancel()
					return streamErrorResult(nil, bifrostContext, prepared.secrets, true, http.StatusOK, headers, model, usageEvidence)
				}
			}
			frames, err := encoder.encode(bifrostContext, response)
			if err != nil {
				callCancel()
				return streamErrorResult(nil, bifrostContext, prepared.secrets, true, http.StatusOK, headers, model, usageEvidence)
			}
			for _, frame := range frames {
				if needsClientModelAlias(spec) {
					frame, err = rewriteClientSSEEvent(frame, spec.ClientProtocol, spec.ClientModel)
					if err != nil {
						callCancel()
						return streamAliasFailure(http.StatusOK, headers, requestID, model, usageEvidence)
					}
				}
				sequence++
				if err := sink(execution.StreamEvent{Sequence: sequence, Kind: execution.StreamEventData, Data: frame}); err != nil {
					callCancel()
					return streamSinkFailure(headers, requestID, model, usageEvidence)
				}
			}
			if chunkUsage != nil {
				usageEvidence = cloneUsage(chunkUsage)
				sequence++
				if err := sink(execution.StreamEvent{Sequence: sequence, Kind: execution.StreamEventUsage, Usage: cloneUsage(chunkUsage)}); err != nil {
					callCancel()
					return streamSinkFailure(headers, requestID, model, usageEvidence)
				}
			}
			idleTimer.resume()
		}
	}
}

type convertedResponsesStreamEncoder struct {
	clientProtocol protocol.Protocol
	geminiState    *gemini.BifrostToGeminiStreamState
}

func newConvertedResponsesStreamEncoder(clientProtocol protocol.Protocol) *convertedResponsesStreamEncoder {
	encoder := &convertedResponsesStreamEncoder{clientProtocol: clientProtocol}
	if clientProtocol == protocol.Gemini {
		encoder.geminiState = gemini.NewBifrostToGeminiStreamState()
	}
	return encoder
}

func (e *convertedResponsesStreamEncoder) encode(
	ctx *schemas.BifrostContext,
	response *schemas.BifrostResponsesStreamResponse,
) ([][]byte, error) {
	switch e.clientProtocol {
	case protocol.OpenAIResponses:
		wire := response.WithDefaults()
		if wire == nil {
			return nil, nil
		}
		body, err := marshalClientWire(protocol.OpenAIResponses, wire)
		if err != nil {
			return nil, err
		}
		return [][]byte{frameNamedSSE(string(wire.Type), body)}, nil
	case protocol.Anthropic:
		events := anthropic.ToAnthropicResponsesStreamResponse(ctx, response)
		frames := make([][]byte, 0, len(events))
		for _, event := range events {
			if event == nil {
				continue
			}
			body, err := json.Marshal(event)
			if err != nil {
				return nil, fmt.Errorf("marshal Anthropic stream event")
			}
			frames = append(frames, frameNamedSSE(string(event.Type), body))
		}
		return frames, nil
	case protocol.Gemini:
		wire := gemini.ToGeminiResponsesStreamResponse(response, e.geminiState)
		if wire == nil {
			return nil, nil
		}
		body, err := json.Marshal(wire)
		if err != nil {
			return nil, fmt.Errorf("marshal Gemini stream response")
		}
		return [][]byte{frameSSE(body)}, nil
	default:
		return nil, fmt.Errorf("unsupported converted stream protocol")
	}
}

func frameNamedSSE(eventName string, payload []byte) []byte {
	if eventName == "" {
		return frameSSE(payload)
	}
	framed := make([]byte, 0, len(eventName)+len(payload)+16)
	framed = append(framed, "event: "...)
	framed = append(framed, eventName...)
	framed = append(framed, '\n')
	framed = append(framed, "data: "...)
	framed = append(framed, payload...)
	framed = append(framed, '\n', '\n')
	return framed
}
