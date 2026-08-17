package bifrost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/providers/openai"
	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/reasoning"
)

const (
	openAIChatPath             = "/v1/chat/completions"
	defaultRequestBudget       = 5 * time.Minute
	streamingSDKIdleGuardGrace = time.Second
)

type preparedAttempt struct {
	provider           schemas.ModelProvider
	mode               channel.RouteMode
	upstreamProtocol   protocol.Protocol
	request            *schemas.BifrostChatRequest
	responsesRequest   *schemas.BifrostResponsesRequest
	countTokensRequest *schemas.BifrostResponsesRequest
	listModelsRequest  *schemas.BifrostListModelsRequest
	passthrough        *schemas.BifrostPassthroughRequest
	typedURL           string
	clientProtocol     protocol.Protocol
	directKey          schemas.Key
	secrets            []string
}

type unarySDKResult struct {
	response *schemas.BifrostChatResponse
	err      *schemas.BifrostError
}

type streamSDKResult struct {
	stream chan *schemas.BifrostStreamChunk
	err    *schemas.BifrostError
}

// Execute executes one non-streaming attempt.
func (r *Runtime) Execute(parent context.Context, spec execution.AttemptSpec) (result execution.AttemptResult) {
	prepared, preflightError := r.prepare(spec, false)
	if preflightError != nil {
		return *preflightError
	}
	var appliedReasoning *reasoning.Config
	defer func() {
		if result.AppliedReasoning == nil {
			result.AppliedReasoning = appliedReasoning
		}
		result.UpstreamProtocol = prepared.upstreamProtocol
		if result.Error != nil && execution.UpstreamCountTokensUnsupported(
			spec.Operation,
			result.Error.StatusCode,
			result.Error.Type,
			result.Error.Code,
		) {
			result.Error.Hint = execution.FailureHintRequestRejected
		}
	}()
	if prepared.passthrough != nil {
		return r.executeNative(parent, spec, prepared)
	}
	if prepared.countTokensRequest != nil {
		return r.executeCountTokens(parent, spec, prepared)
	}
	if prepared.responsesRequest != nil {
		return r.executeConvertedResponses(parent, spec, prepared)
	}
	if prepared.listModelsRequest != nil {
		return r.executeListModels(parent, spec, prepared)
	}

	requestContext, requestCancel := boundedRequestContext(parent, spec.Timeouts.Request)
	defer requestCancel()
	callContext, callCancel := context.WithCancel(requestContext)
	defer callCancel()

	bifrostContext := r.newSDKContext(callContext, spec, prepared.directKey)
	enableConvertedWireCapture(bifrostContext, prepared)
	setTypedRequestURL(bifrostContext, prepared.typedURL)
	outcomeChannel := make(chan unarySDKResult, 1)
	go func() {
		response, bifrostError := r.core.ChatCompletionRequest(bifrostContext, prepared.request)
		outcomeChannel <- unarySDKResult{response: response, err: bifrostError}
	}()

	var outcome unarySDKResult
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
		if prepared.mode == channel.RouteConverted {
			return convertedUnaryErrorResult(outcome.err, bifrostContext, prepared.secrets)
		}
		return unaryErrorResult(outcome.err, bifrostContext, prepared.secrets)
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
	body, usageEvidence, err := encodeChatResponse(outcome.response)
	if err != nil {
		return startedUnaryFailure(
			http.StatusOK,
			responseHeaders(outcome.response.ExtraFields.ProviderResponseHeaders, bifrostContext, false),
			execution.ErrorKindInternal,
			"encode upstream response",
		)
	}
	headers := responseHeaders(outcome.response.ExtraFields.ProviderResponseHeaders, bifrostContext, false)
	if needsClientModelAlias(spec) {
		body, err = rewriteClientResponseModel(spec.ClientProtocol, body, spec.ClientModel)
		if err != nil {
			return startedUnaryFailure(http.StatusOK, headers, execution.ErrorKindInternal, "rewrite converted response model")
		}
	}
	requestID := upstreamRequestID(headers)
	return execution.AttemptResult{
		DispatchState:     execution.DispatchMaybeSent,
		ResponseStarted:   true,
		StatusCode:        http.StatusOK,
		Header:            headers,
		Body:              body,
		Model:             outcome.response.Model,
		UpstreamRequestID: requestID,
		Usage:             usageEvidence,
	}
}

// ExecuteStream executes one streaming attempt and emits ordered events synchronously.
func (r *Runtime) ExecuteStream(
	parent context.Context,
	spec execution.AttemptSpec,
	sink execution.StreamSink,
) (result execution.StreamResult) {
	if sink == nil {
		return notSentStreamFailure(execution.ErrorKindInvalidRequest, "stream sink is required")
	}
	prepared, preflightError := r.prepare(spec, true)
	if preflightError != nil {
		return streamFromAttemptFailure(*preflightError)
	}
	var appliedReasoning *reasoning.Config
	defer func() {
		if result.AppliedReasoning == nil {
			result.AppliedReasoning = appliedReasoning
		}
		result.UpstreamProtocol = prepared.upstreamProtocol
	}()
	if prepared.passthrough != nil {
		return r.executeNativeStream(parent, spec, prepared, sink)
	}
	if prepared.countTokensRequest != nil {
		return streamFromAttemptFailure(notSentUnaryFailure(
			execution.ErrorKindInvalidRequest,
			"count tokens does not support streaming",
		))
	}
	if prepared.responsesRequest != nil {
		return r.executeConvertedResponsesStream(parent, spec, prepared, sink)
	}

	requestContext, requestCancel := boundedRequestContext(parent, spec.Timeouts.Request)
	defer requestCancel()
	callContext, callCancel := context.WithCancel(requestContext)
	defer callCancel()
	preResponse := startPreResponseGate(callCancel, spec.Timeouts)
	defer preResponse.stop()

	bifrostContext := r.newStreamingSDKContext(callContext, spec, prepared.directKey)
	enableConvertedWireCapture(bifrostContext, prepared)
	setTypedRequestURL(bifrostContext, prepared.typedURL)
	outcomeChannel := make(chan streamSDKResult, 1)
	go func() {
		stream, bifrostError := r.core.ChatCompletionStreamRequest(bifrostContext, prepared.request)
		outcomeChannel <- streamSDKResult{stream: stream, err: bifrostError}
	}()

	var outcome streamSDKResult
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
		if prepared.mode == channel.RouteConverted {
			return convertedStreamErrorResult(outcome.err, bifrostContext, prepared.secrets, false, 0, nil, "", nil)
		}
		return streamErrorResult(outcome.err, bifrostContext, prepared.secrets, false, 0, nil, "", nil)
	}
	if outcome.stream == nil {
		return attemptedStreamFailure(execution.ErrorKindInternal, "execution runtime returned no stream")
	}

	headers := responseHeaders(nil, bifrostContext, true)
	requestID := upstreamRequestID(headers)
	ready := execution.StreamEvent{
		Sequence:          1,
		Kind:              execution.StreamEventReady,
		StatusCode:        http.StatusOK,
		Header:            headers.Clone(),
		UpstreamRequestID: requestID,
	}
	if err := sink(ready); err != nil {
		callCancel()
		return streamSinkFailure(headers, requestID, "", nil)
	}

	sequence := uint64(1)
	model := ""
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
			if !open {
				if !sdkStreamEndedNormally(bifrostContext) {
					return terminatedStreamFailure(headers, requestID, model, usageEvidence)
				}
				sequence++
				if err := sink(execution.StreamEvent{
					Sequence: sequence,
					Kind:     execution.StreamEventData,
					Data:     []byte("data: [DONE]\n\n"),
				}); err != nil {
					callCancel()
					return streamSinkFailure(headers, requestID, model, usageEvidence)
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
			if chunk == nil {
				callCancel()
				return streamErrorResult(nil, bifrostContext, prepared.secrets, true, http.StatusOK, headers, model, usageEvidence)
			}
			if chunk.BifrostError != nil {
				captureAppliedReasoning(&chunk.BifrostError.ExtraFields.RawRequest, &appliedReasoning)
				callCancel()
				return streamErrorResult(chunk.BifrostError, bifrostContext, prepared.secrets, true, http.StatusOK, headers, model, usageEvidence)
			}
			if chunk.BifrostChatResponse == nil {
				callCancel()
				return streamErrorResult(nil, bifrostContext, prepared.secrets, true, http.StatusOK, headers, model, usageEvidence)
			}

			response := chunk.BifrostChatResponse
			captureAppliedReasoning(&response.ExtraFields.RawRequest, &appliedReasoning)
			if response.Model != "" {
				model = response.Model
			}
			payload, chunkUsage, err := encodeChatResponse(response)
			if err != nil {
				callCancel()
				return streamErrorResult(nil, bifrostContext, prepared.secrets, true, http.StatusOK, headers, model, usageEvidence)
			}
			if needsClientModelAlias(spec) {
				payload, err = rewriteClientResponseModel(spec.ClientProtocol, payload, spec.ClientModel)
				if err != nil {
					callCancel()
					return streamAliasFailure(http.StatusOK, headers, requestID, model, usageEvidence)
				}
			}
			sequence++
			if err := sink(execution.StreamEvent{
				Sequence: sequence,
				Kind:     execution.StreamEventData,
				Data:     frameSSE(payload),
			}); err != nil {
				callCancel()
				return streamSinkFailure(headers, requestID, model, usageEvidence)
			}
			if chunkUsage != nil {
				usageEvidence = cloneUsage(chunkUsage)
				sequence++
				if err := sink(execution.StreamEvent{
					Sequence: sequence,
					Kind:     execution.StreamEventUsage,
					Usage:    cloneUsage(chunkUsage),
				}); err != nil {
					callCancel()
					return streamSinkFailure(headers, requestID, model, usageEvidence)
				}
			}
			idleTimer.resume()
		}
	}
}

func (r *Runtime) prepare(spec execution.AttemptSpec, stream bool) (preparedAttempt, *execution.AttemptResult) {
	if r == nil || r.core == nil || r.registry == nil {
		failure := notSentUnaryFailure(execution.ErrorKindInternal, "execution runtime is unavailable")
		return preparedAttempt{}, &failure
	}
	if r.closed.Load() {
		failure := notSentUnaryFailure(execution.ErrorKindInternal, "execution runtime is shut down")
		return preparedAttempt{}, &failure
	}
	if err := spec.Validate(); err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid execution attempt: "+safeValidationReason(err))
		return preparedAttempt{}, &failure
	}
	if !supportedRequestShape(spec, stream) {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "unsupported protocol or operation")
		return preparedAttempt{}, &failure
	}

	channelID := channel.ID(spec.ChannelID)
	if len(bytes.TrimSpace(spec.TargetConfig)) == 0 {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "channel target is required")
		return preparedAttempt{}, &failure
	}
	resolved, err := r.registry.ResolveExecutionTarget(channelID, spec.TargetConfig)
	if err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid channel target: "+safeValidationReason(err))
		return preparedAttempt{}, &failure
	}
	providerKind := resolved.ProviderKind
	if r.fixedConfig == nil {
		failure := notSentUnaryFailure(execution.ErrorKindInternal, "provider runtime config is unavailable")
		return preparedAttempt{}, &failure
	}
	effective, effectiveErr := buildEffectiveProviderConfigForAttempt(resolved, spec, r.allowPrivate)
	fixedFingerprint := r.fixedConfig.fingerprint
	fixedCanonical := r.fixedConfig.canonical
	if r.fixedConfig.baseFingerprint != "" {
		fixedFingerprint = r.fixedConfig.baseFingerprint
		fixedCanonical = r.fixedConfig.baseCanonical
	}
	if effectiveErr != nil || effective.fingerprint != fixedFingerprint ||
		!bytes.Equal(effective.canonical, fixedCanonical) {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "execution target does not match provider runtime")
		return preparedAttempt{}, &failure
	}
	mode := channel.RouteMode(spec.RouteMode)
	declaredMode, ok := resolved.ModeForModel(spec.ClientProtocol, spec.Operation, spec.UpstreamModel)
	if !ok || declaredMode != mode {
		failure := notSentConversionFailure(
			execution.ErrorCodeTargetConversionNotSupported,
			"channel does not support the requested route",
		)
		return preparedAttempt{}, &failure
	}
	if !spec.RouteRequirement.Allows(execution.RouteMode(mode)) {
		failure := notSentConversionFailure(
			execution.ErrorCodeCriticalSemanticLoss,
			"converted route cannot preserve provider resource semantics",
		)
		return preparedAttempt{}, &failure
	}
	if spec.Operation == execution.OperationResponsesCreate &&
		spec.RouteRequirement.Normalize() == execution.RouteRequirementNative &&
		!resolved.SupportsResponsesLifecycle() {
		failure := notSentConversionFailure(
			execution.ErrorCodeCriticalSemanticLoss,
			"target does not support Responses resource lifecycle",
		)
		return preparedAttempt{}, &failure
	}
	if spec.Operation == execution.OperationResponsesPassthrough &&
		providerKind != channel.ProviderOpenAI &&
		providerKind != channel.ProviderOpenAICompatible {
		failure := notSentConversionFailure(
			execution.ErrorCodeTargetConversionNotSupported,
			"Responses passthrough requires an OpenAI provider",
		)
		return preparedAttempt{}, &failure
	}
	provider := r.fixedConfig.provider
	targetBaseURL := ""
	if r.fixedConfig.custom {
		targetBaseURL = r.fixedConfig.targetBaseURL
	}
	if mode == channel.RouteNative && spec.Operation == execution.OperationListModels &&
		!providerKindNativeForClient(providerKind, spec.ClientProtocol) {
		failure := notSentConversionFailure(
			execution.ErrorCodeTargetConversionNotSupported,
			"native route does not match the client protocol",
		)
		return preparedAttempt{}, &failure
	}
	safeQuery := safeAttemptQuery(spec)
	if mode == channel.RouteNative && spec.ClientProtocol == protocol.Gemini &&
		(providerKind == channel.ProviderGemini || providerKind == channel.ProviderGoogleVertex) {
		safeQuery = removeRawQueryValue(safeQuery, "alt")
		if stream {
			safeQuery = setRawQueryValue(safeQuery, "alt", "sse")
		}
	}

	credential, err := r.registry.ValidateCredential(channelID, spec.Credential.Data())
	if err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid credential: "+safeValidationReason(err))
		return preparedAttempt{}, &failure
	}
	directKey, secrets, err := directKeyForAttempt(spec, providerKind, spec.TargetConfig, credential)
	if err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid credential")
		return preparedAttempt{}, &failure
	}
	if providerKind == channel.ProviderDeepSeek && spec.ClientProtocol == protocol.Anthropic {
		directKey.UseAnthropicEndpoints = schemas.Ptr(true)
	}
	if spec.Operation == execution.OperationProbe {
		if mode == channel.RouteNative && providerKind == channel.ProviderGoogleVertex {
			passthroughPath, pathErr := vertexNativeGeminiPath(spec.UpstreamModel, "generateContent")
			if pathErr != nil {
				failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid channel probe target")
				return preparedAttempt{}, &failure
			}
			return preparedAttempt{
				provider:         provider,
				mode:             mode,
				upstreamProtocol: protocol.Gemini,
				passthrough: &schemas.BifrostPassthroughRequest{
					Provider: provider,
					Model:    spec.UpstreamModel,
					Method:   http.MethodPost,
					Path:     passthroughPath,
					Body:     []byte(`{"contents":[{"role":"user","parts":[{"text":"ping"}]}],"generationConfig":{"maxOutputTokens":1}}`),
					SafeHeaders: map[string]string{
						"Content-Type": "application/json",
					},
				},
				directKey: directKey,
				secrets:   secrets,
			}, nil
		}
		request := newProbeRequest(provider, providerKind, spec.UpstreamModel)
		responses := spec.ClientProtocol == protocol.OpenAIResponses
		typedURL, upstreamProtocol, targetErr := convertedTypedTarget(
			providerKind,
			targetBaseURL,
			spec.UpstreamModel,
			responses,
			false,
			"",
		)
		if targetErr == nil && mode == channel.RouteNative && providerKind == channel.ProviderDeepSeek {
			typedURL, upstreamProtocol, targetErr = deepSeekNativeTypedTarget(targetBaseURL, spec.ClientProtocol, "")
		}
		if targetErr != nil {
			failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid channel probe target")
			return preparedAttempt{}, &failure
		}
		prepared := preparedAttempt{
			provider: provider, mode: mode, upstreamProtocol: upstreamProtocol, typedURL: typedURL,
			clientProtocol: spec.ClientProtocol, directKey: directKey, secrets: secrets,
		}
		if responses {
			prepared.responsesRequest = request.ToResponsesRequest()
		} else {
			prepared.request = request
		}
		return prepared, nil
	}
	if mode == channel.RouteNative && providerSupportsPassthrough(providerKind, targetBaseURL) {
		body, err := sanitizeNativeRequestBody(spec, stream)
		if err != nil {
			failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid native request body")
			return preparedAttempt{}, &failure
		}
		passthroughPath, err := nativePassthroughPath(spec, providerKind)
		if err != nil {
			failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid native request path")
			return preparedAttempt{}, &failure
		}
		passthroughHeaders := safePassthroughHeaders(spec.Header)
		if providerKind == channel.ProviderGoogleVertex {
			if passthroughHeaders == nil {
				passthroughHeaders = make(map[string]string, 1)
			}
			if passthroughHeaders["Content-Type"] == "" {
				passthroughHeaders["Content-Type"] = "application/json"
			}
		}
		upstreamProtocol := spec.ClientProtocol
		return preparedAttempt{
			provider:         provider,
			mode:             mode,
			upstreamProtocol: upstreamProtocol,
			passthrough: &schemas.BifrostPassthroughRequest{
				Provider:    provider,
				Model:       spec.UpstreamModel,
				Method:      spec.Method,
				Path:        passthroughPath,
				RawQuery:    safeQuery,
				Body:        body,
				SafeHeaders: passthroughHeaders,
			},
			directKey: directKey,
			secrets:   secrets,
		}, nil
	}
	if spec.Operation == execution.OperationListModels {
		typedURL, upstreamProtocol, targetErr := convertedListModelsTarget(providerKind, targetBaseURL, safeQuery)
		if targetErr != nil {
			failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid compatible channel target")
			return preparedAttempt{}, &failure
		}
		return preparedAttempt{
			provider:          provider,
			mode:              mode,
			upstreamProtocol:  upstreamProtocol,
			listModelsRequest: &schemas.BifrostListModelsRequest{Provider: provider},
			typedURL:          typedURL,
			clientProtocol:    spec.ClientProtocol,
			directKey:         directKey,
			secrets:           secrets,
		}, nil
	}
	if spec.Operation == execution.OperationCountTokens ||
		spec.Operation == execution.OperationResponsesInputTokens {
		request, conversionErr := buildConvertedResponsesRequest(spec, provider)
		if conversionErr != nil {
			var classified interface{ ConversionCode() string }
			if errors.As(conversionErr, &classified) {
				failure := notSentConversionFailure(classified.ConversionCode(), conversionErr.Error())
				return preparedAttempt{}, &failure
			}
			failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, conversionErr.Error())
			return preparedAttempt{}, &failure
		}
		typedURL, upstreamProtocol, targetErr := countTokensTypedTarget(
			providerKind,
			targetBaseURL,
			spec.UpstreamModel,
			safeQuery,
		)
		if targetErr != nil {
			failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid count tokens target")
			return preparedAttempt{}, &failure
		}
		return preparedAttempt{
			provider:           provider,
			mode:               mode,
			upstreamProtocol:   upstreamProtocol,
			countTokensRequest: request,
			typedURL:           typedURL,
			clientProtocol:     spec.ClientProtocol,
			directKey:          directKey,
			secrets:            secrets,
		}, nil
	}
	if spec.ClientProtocol != protocol.OpenAICompletions || spec.Operation != execution.OperationChatCompletion {
		request, conversionErr := buildConvertedResponsesRequest(spec, provider)
		if conversionErr != nil {
			var classified interface{ ConversionCode() string }
			if errors.As(conversionErr, &classified) {
				failure := notSentConversionFailure(classified.ConversionCode(), conversionErr.Error())
				return preparedAttempt{}, &failure
			}
			failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, conversionErr.Error())
			return preparedAttempt{}, &failure
		}
		typedURL, upstreamProtocol, targetErr := convertedTypedTarget(providerKind, targetBaseURL, spec.UpstreamModel, true, stream, safeQuery)
		if targetErr == nil && mode == channel.RouteNative && providerKind == channel.ProviderDeepSeek {
			typedURL, upstreamProtocol, targetErr = deepSeekNativeTypedTarget(targetBaseURL, spec.ClientProtocol, safeQuery)
		}
		if targetErr != nil {
			failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid compatible channel target")
			return preparedAttempt{}, &failure
		}
		return preparedAttempt{
			provider:         provider,
			mode:             mode,
			upstreamProtocol: upstreamProtocol,
			responsesRequest: request,
			typedURL:         typedURL,
			clientProtocol:   spec.ClientProtocol,
			directKey:        directKey,
			secrets:          secrets,
		}, nil
	}

	var openAIRequest openai.OpenAIChatRequest
	if err := json.Unmarshal(spec.Body, &openAIRequest); err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid OpenAI chat request body")
		return preparedAttempt{}, &failure
	}
	conversionContext := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	request := openAIRequest.ToBifrostChatRequest(conversionContext)
	conversionContext.Cancel()
	if request == nil || request.Input == nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "OpenAI chat messages are required")
		return preparedAttempt{}, &failure
	}
	request.Provider = provider
	request.Model = spec.UpstreamModel
	request.Fallbacks = nil
	request.RawRequestBody = nil
	if request.Params != nil && request.Params.ExtraParams != nil {
		delete(request.Params.ExtraParams, "provider")
		delete(request.Params.ExtraParams, "fallback")
		delete(request.Params.ExtraParams, "fallbacks")
	}
	typedURL, upstreamProtocol, err := convertedTypedTarget(providerKind, targetBaseURL, spec.UpstreamModel, false, stream, safeQuery)
	if err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid compatible channel target")
		return preparedAttempt{}, &failure
	}
	return preparedAttempt{
		provider: provider, mode: mode, upstreamProtocol: upstreamProtocol, request: request, typedURL: typedURL,
		clientProtocol: spec.ClientProtocol, directKey: directKey, secrets: secrets,
	}, nil
}

func newProbeRequest(
	provider schemas.ModelProvider,
	providerKind channel.ProviderKind,
	model string,
) *schemas.BifrostChatRequest {
	content := "ping"
	params := &schemas.ChatParameters{MaxCompletionTokens: schemas.Ptr(1)}
	if providerKind == channel.ProviderOpenAICompatible {
		params.MaxCompletionTokens = nil
		params.ExtraParams = map[string]any{"max_tokens": 1}
	}
	return &schemas.BifrostChatRequest{
		Provider: provider,
		Model:    model,
		Input: []schemas.ChatMessage{{
			Role: schemas.ChatMessageRoleUser,
			Content: &schemas.ChatMessageContent{
				ContentStr: &content,
			},
		}},
		Params: params,
	}
}

func providerSupportsPassthrough(providerKind channel.ProviderKind, baseURL string) bool {
	switch providerKind {
	case channel.ProviderOpenAI, channel.ProviderAnthropic, channel.ProviderGemini, channel.ProviderGoogleVertex:
		return true
	case channel.ProviderOpenAICompatible:
		// Bifrost v1.7.7's OpenAI passthrough always inserts /v1. A compatible
		// full API prefix with another suffix must use the typed custom path,
		// without changing the frozen route mode or channel capability.
		return strings.HasSuffix(baseURL, "/v1")
	default:
		return false
	}
}

func providerKindNativeForClient(providerKind channel.ProviderKind, clientProtocol protocol.Protocol) bool {
	switch providerKind {
	case channel.ProviderOpenAI, channel.ProviderOpenAICompatible:
		return clientProtocol == protocol.OpenAICompletions || clientProtocol == protocol.OpenAIResponses
	case channel.ProviderAnthropic:
		return clientProtocol == protocol.Anthropic
	case channel.ProviderGemini:
		return clientProtocol == protocol.Gemini
	case channel.ProviderGoogleVertex:
		return clientProtocol == protocol.Gemini
	case channel.ProviderDeepSeek, channel.ProviderOpenRouter, channel.ProviderGroq, channel.ProviderXAI:
		return clientProtocol == protocol.OpenAICompletions || clientProtocol == protocol.OpenAIResponses
	default:
		return false
	}
}

func supportedRequestShape(spec execution.AttemptSpec, stream bool) bool {
	if spec.Operation == execution.OperationListModels {
		if stream || spec.Method != http.MethodGet {
			return false
		}
		switch spec.ClientProtocol {
		case protocol.OpenAICompletions, protocol.Anthropic:
			return spec.Path == "/v1/models"
		case protocol.Gemini:
			return spec.Path == "/v1beta/models"
		default:
			return false
		}
	}
	if spec.Operation == execution.OperationProbe {
		if stream || strings.TrimSpace(spec.UpstreamModel) == "" || spec.Method != "" ||
			spec.Path != "" || len(spec.Query) != 0 || spec.RawQuery != "" || len(spec.Body) != 0 {
			return false
		}
		switch spec.ClientProtocol {
		case protocol.OpenAICompletions, protocol.OpenAIResponses, protocol.Anthropic, protocol.Gemini:
			return true
		default:
			return false
		}
	}
	switch spec.ClientProtocol {
	case protocol.OpenAICompletions:
		return spec.Operation == execution.OperationChatCompletion &&
			spec.Method == http.MethodPost && spec.Path == openAIChatPath
	case protocol.OpenAIResponses:
		switch spec.Operation {
		case execution.OperationResponsesCreate:
			return spec.Method == http.MethodPost && spec.Path == "/v1/responses"
		case execution.OperationResponsesRetrieve:
			return spec.Method == http.MethodGet && validResponsesResourcePath(spec.Path, "")
		case execution.OperationResponsesDelete:
			return !stream && spec.Method == http.MethodDelete && validResponsesResourcePath(spec.Path, "")
		case execution.OperationResponsesCancel:
			return !stream && spec.Method == http.MethodPost && validResponsesResourcePath(spec.Path, "cancel")
		case execution.OperationResponsesInputItems:
			return !stream && spec.Method == http.MethodGet && validResponsesResourcePath(spec.Path, "input_items")
		case execution.OperationResponsesCompact:
			return !stream && spec.Method == http.MethodPost && spec.Path == "/v1/responses/compact"
		case execution.OperationResponsesInputTokens:
			return !stream && spec.Method == http.MethodPost && spec.Path == "/v1/responses/input_tokens"
		case execution.OperationResponsesPassthrough:
			return validResponsesPassthroughShape(spec, stream)
		}
	case protocol.Anthropic:
		return spec.Method == http.MethodPost &&
			((spec.Operation == execution.OperationChatCompletion && spec.Path == "/v1/messages") ||
				(!stream && spec.Operation == execution.OperationCountTokens && spec.Path == "/v1/messages/count_tokens"))
	case protocol.Gemini:
		if spec.Method != http.MethodPost {
			return false
		}
		if spec.Operation == execution.OperationCountTokens {
			return !stream && validGeminiGeneratePath(spec.Path, "countTokens")
		}
		if spec.Operation != execution.OperationChatCompletion {
			return false
		}
		action := "generateContent"
		if stream {
			action = "streamGenerateContent"
		}
		return validGeminiGeneratePath(spec.Path, action)
	}
	return false
}

func validResponsesPassthroughShape(spec execution.AttemptSpec, stream bool) bool {
	remainder, ok := strings.CutPrefix(spec.Path, "/v1/responses/")
	if !ok || remainder == "" {
		return false
	}
	switch spec.Method {
	case http.MethodOptions, http.MethodConnect, http.MethodTrace:
		return false
	case http.MethodHead:
		return !stream
	default:
		return true
	}
}

func validGeminiGeneratePath(path, action string) bool {
	prefix := "/v1beta/models/"
	remainder, ok := strings.CutPrefix(path, prefix)
	if !ok {
		return false
	}
	model, suffix, ok := strings.Cut(remainder, ":")
	return ok && suffix == action && validResourceID(model) && !strings.Contains(model, "/")
}

func nativePassthroughPath(spec execution.AttemptSpec, providerKind channel.ProviderKind) (string, error) {
	switch providerKind {
	case channel.ProviderOpenAI, channel.ProviderOpenAICompatible:
		return spec.Path, nil
	case channel.ProviderAnthropic:
		return spec.Path, nil
	case channel.ProviderGemini:
		path, ok := strings.CutPrefix(spec.Path, "/v1beta")
		if !ok || path == "" {
			return "", fmt.Errorf("invalid Gemini path")
		}
		if spec.Operation == execution.OperationListModels {
			if path != "/models" {
				return "", fmt.Errorf("invalid Gemini models path")
			}
			return path, nil
		}
		marker := "/models/"
		modelStart := strings.Index(path, marker)
		colon := strings.LastIndex(path, ":")
		if modelStart < 0 || colon <= modelStart+len(marker) {
			return "", fmt.Errorf("invalid Gemini model path")
		}
		return path[:modelStart+len(marker)] + url.PathEscape(spec.UpstreamModel) + path[colon:], nil
	case channel.ProviderGoogleVertex:
		colon := strings.LastIndex(spec.Path, ":")
		if colon < 0 || colon == len(spec.Path)-1 {
			return "", fmt.Errorf("invalid Vertex Gemini path")
		}
		return vertexNativeGeminiPath(spec.UpstreamModel, spec.Path[colon+1:])
	default:
		return "", fmt.Errorf("unsupported native channel")
	}
}

func vertexNativeGeminiPath(model, action string) (string, error) {
	model, ok := channel.NormalizeVertexGeminiModel(model)
	if !ok || (action != "generateContent" && action != "streamGenerateContent") {
		return "", fmt.Errorf("invalid Vertex Gemini target")
	}
	if allDigitsASCII(model) {
		return "/endpoints/" + url.PathEscape(model) + ":" + action, nil
	}
	return "/publishers/google/models/" + url.PathEscape(model) + ":" + action, nil
}

func allDigitsASCII(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func validResponsesResourcePath(path, action string) bool {
	remainder, ok := strings.CutPrefix(path, "/v1/responses/")
	if !ok || remainder == "" {
		return false
	}
	parts := strings.Split(remainder, "/")
	if action == "" {
		return len(parts) == 1 && validResourceID(parts[0])
	}
	return len(parts) == 2 && validResourceID(parts[0]) && parts[1] == action
}

func validResourceID(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, "\\?#")
}

func customProviderKey(baseProvider schemas.ModelProvider, baseURL string) schemas.ModelProvider {
	digest := sha256.Sum256([]byte(string(baseProvider) + "\x00" + baseURL))
	return schemas.ModelProvider("gptload-custom-" + hex.EncodeToString(digest[:]))
}

func directKeyForAttempt(
	spec execution.AttemptSpec,
	providerKind channel.ProviderKind,
	targetConfig json.RawMessage,
	credential channel.Credential,
) (schemas.Key, []string, error) {
	key := schemas.Key{
		ID:     fmt.Sprintf("%d:%d:%d", spec.Credential.ID, spec.Credential.IdentityGeneration, spec.Credential.Version),
		Name:   "selected credential",
		Models: schemas.WhiteList{"*"},
		Weight: 1,
	}
	secrets := credentialSecrets(credential)
	apiKey, _ := credential.Value("api_key")
	switch providerKind {
	case channel.ProviderOpenAI, channel.ProviderAnthropic, channel.ProviderGemini,
		channel.ProviderDeepSeek, channel.ProviderOpenRouter, channel.ProviderGroq, channel.ProviderXAI,
		channel.ProviderOpenAICompatible:
		if apiKey == "" {
			return schemas.Key{}, nil, fmt.Errorf("api_key is required")
		}
		key.Value = plainSecret(apiKey)
	case channel.ProviderAzureOpenAI:
		var target struct {
			Endpoint string `json:"endpoint"`
		}
		if json.Unmarshal(targetConfig, &target) != nil || target.Endpoint == "" {
			return schemas.Key{}, nil, fmt.Errorf("azure endpoint is required")
		}
		key.AzureKeyConfig = &schemas.AzureKeyConfig{Endpoint: plainSecret(target.Endpoint)}
		if apiKey != "" {
			key.Value = plainSecret(apiKey)
		} else {
			clientID, clientOK := credential.Value("client_id")
			clientSecret, secretOK := credential.Value("client_secret")
			tenantID, tenantOK := credential.Value("tenant_id")
			if !clientOK || !secretOK || !tenantOK {
				return schemas.Key{}, nil, fmt.Errorf("complete Entra credentials are required")
			}
			key.AzureKeyConfig.ClientID = plainSecretPtr(clientID)
			key.AzureKeyConfig.ClientSecret = plainSecretPtr(clientSecret)
			key.AzureKeyConfig.TenantID = plainSecretPtr(tenantID)
		}
	case channel.ProviderAWSBedrock:
		var target struct {
			Region string `json:"region"`
		}
		if json.Unmarshal(targetConfig, &target) != nil || target.Region == "" {
			return schemas.Key{}, nil, fmt.Errorf("Bedrock region is required")
		}
		config := &schemas.BedrockKeyConfig{Region: plainSecretPtr(target.Region)}
		if apiKey != "" {
			key.Value = plainSecret(apiKey)
		} else {
			if value, ok := credential.Value("access_key"); ok {
				config.AccessKey = plainSecret(value)
			}
			if value, ok := credential.Value("secret_key"); ok {
				config.SecretKey = plainSecret(value)
			}
			config.SessionToken = optionalPlainSecret(credential, "session_token")
			config.RoleARN = optionalPlainSecret(credential, "role_arn")
			config.ExternalID = optionalPlainSecret(credential, "external_id")
			config.RoleSessionName = optionalPlainSecret(credential, "session_name")
			if config.RoleARN != nil && config.RoleARN.GetValue() != "" {
				sessionName := bedrockRoleSessionName(config.RoleSessionName, spec.Credential)
				config.RoleSessionName = plainSecretPtr(sessionName)
				secrets = appendUniqueString(secrets, sessionName)
			}
		}
		key.BedrockKeyConfig = config
	case channel.ProviderGoogleVertex:
		var target struct {
			Location string `json:"location"`
		}
		if json.Unmarshal(targetConfig, &target) != nil {
			return schemas.Key{}, nil, fmt.Errorf("Vertex location is invalid")
		}
		if target.Location == "" {
			target.Location = "global"
		}
		serviceAccount, ok := credential.Value("service_account_json")
		if !ok || serviceAccount == "" {
			return schemas.Key{}, nil, fmt.Errorf("Vertex service account is required")
		}
		var account struct {
			ProjectID string `json:"project_id"`
		}
		if json.Unmarshal([]byte(serviceAccount), &account) != nil || strings.TrimSpace(account.ProjectID) == "" {
			return schemas.Key{}, nil, fmt.Errorf("Vertex service account project is required")
		}
		account.ProjectID = strings.TrimSpace(account.ProjectID)
		key.VertexKeyConfig = &schemas.VertexKeyConfig{
			ProjectID:       plainSecret(account.ProjectID),
			Region:          plainSecret(target.Location),
			AuthCredentials: plainSecret(serviceAccount),
		}
	default:
		return schemas.Key{}, nil, fmt.Errorf("unsupported provider kind")
	}
	return key, secrets, nil
}

func plainSecret(value string) schemas.SecretVar {
	return schemas.SecretVar{Val: value, SecretType: schemas.SecretTypePlainText}
}

func plainSecretPtr(value string) *schemas.SecretVar {
	secret := plainSecret(value)
	return &secret
}

func optionalPlainSecret(credential channel.Credential, field string) *schemas.SecretVar {
	value, ok := credential.Value(field)
	if !ok || value == "" {
		return nil
	}
	return plainSecretPtr(value)
}

func bedrockRoleSessionName(
	original *schemas.SecretVar,
	credential execution.CredentialSnapshot,
) string {
	base := "bifrost-session"
	if original != nil && original.GetValue() != "" {
		base = original.GetValue()
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf(
		"%d:%d",
		credential.ID,
		credential.IdentityGeneration,
	)))
	suffix := "-gl-" + hex.EncodeToString(digest[:8])
	if maximum := 64 - len(suffix); len(base) > maximum {
		base = base[:maximum]
	}
	return base + suffix
}

func appendUniqueString(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func credentialSecrets(credential channel.Credential) []string {
	fields := []string{
		"api_key", "client_id", "client_secret", "tenant_id", "access_key", "secret_key",
		"session_token", "role_arn", "external_id", "session_name", "service_account_json",
	}
	seen := make(map[string]struct{}, len(fields))
	result := make([]string, 0, len(fields))
	appendSecret := func(value string) {
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	for _, field := range fields {
		value, ok := credential.Value(field)
		if !ok {
			continue
		}
		appendSecret(value)
		if field == "service_account_json" {
			var object map[string]json.RawMessage
			if json.Unmarshal([]byte(value), &object) == nil {
				for _, nestedField := range []string{"private_key", "private_key_id", "client_email"} {
					var nested string
					if json.Unmarshal(object[nestedField], &nested) == nil {
						appendSecret(nested)
					}
				}
			}
		}
	}
	return result
}

func (r *Runtime) newSDKContext(parent context.Context, spec execution.AttemptSpec, directKey schemas.Key) *schemas.BifrostContext {
	bifrostContext := schemas.NewBifrostContext(parent, schemas.NoDeadline)
	bifrostContext.SetValue(schemas.BifrostContextKeyRequestID, spec.RequestID)
	bifrostContext.SetValue(schemas.BifrostContextKeyDirectKey, directKey)
	bifrostContext.SetValue(schemas.BifrostContextKeyLargeResponseThreshold, r.maxUnaryResponseBodyBytes)
	if spec.Operation == execution.OperationChatCompletion &&
		r.providerKind(spec) == channel.ProviderDeepSeek &&
		spec.ClientProtocol == protocol.Anthropic {
		bifrostContext.SetValue(schemas.BifrostContextKeyPassthroughExtraParams, true)
	}
	if spec.Operation == execution.OperationProbe &&
		r.providerKind(spec) == channel.ProviderOpenAICompatible {
		bifrostContext.SetValue(schemas.BifrostContextKeyPassthroughExtraParams, true)
	}
	if spec.Timeouts.StreamIdle > 0 {
		bifrostContext.SetValue(schemas.BifrostContextKeyStreamIdleTimeout, spec.Timeouts.StreamIdle)
	}
	if headers := safeRequestHeaders(spec.Header); len(headers) > 0 {
		bifrostContext.SetValue(schemas.BifrostContextKeyExtraHeaders, headers)
	}
	return bifrostContext
}

func (r *Runtime) providerKind(spec execution.AttemptSpec) channel.ProviderKind {
	if r == nil || r.registry == nil {
		return ""
	}
	providerKind, _ := r.registry.ProviderKind(channel.ID(spec.ChannelID))
	return providerKind
}

func (r *Runtime) newStreamingSDKContext(parent context.Context, spec execution.AttemptSpec, directKey schemas.Key) *schemas.BifrostContext {
	bifrostContext := r.newSDKContext(parent, spec, directKey)
	if spec.Timeouts.StreamIdle > 0 {
		// GPT-Load owns the user-facing chunk deadline; Bifrost remains a later raw-read backstop.
		bifrostContext.SetValue(
			schemas.BifrostContextKeyStreamIdleTimeout,
			streamingSDKIdleGuard(spec.Timeouts.StreamIdle),
		)
	}
	return bifrostContext
}

func streamingSDKIdleGuard(configured time.Duration) time.Duration {
	maximum := time.Duration(math.MaxInt64)
	if configured > maximum-streamingSDKIdleGuardGrace {
		return maximum
	}
	return configured + streamingSDKIdleGuardGrace
}

func largeUnaryResponseFailure(bifrostContext *schemas.BifrostContext) *execution.AttemptResult {
	if bifrostContext == nil {
		return nil
	}
	large, _ := bifrostContext.Value(schemas.BifrostContextKeyLargeResponseMode).(bool)
	if !large {
		return nil
	}
	if reader, ok := bifrostContext.Value(schemas.BifrostContextKeyLargeResponseReader).(io.Closer); ok && reader != nil {
		_ = reader.Close()
	}
	headers := responseHeaders(nil, bifrostContext, false)
	result := startedUnaryFailure(
		http.StatusOK,
		headers,
		execution.ErrorKindInternal,
		"upstream response exceeds size limit",
	)
	return &result
}

func safeRequestHeaders(source http.Header) map[string][]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string][]string)
	for name, values := range source {
		canonical := http.CanonicalHeaderKey(name)
		if !safeUpstreamRequestHeader(canonical) {
			continue
		}
		result[canonical] = append([]string(nil), values...)
	}
	return result
}

func safePassthroughHeaders(source http.Header) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string)
	for name, values := range source {
		canonical := http.CanonicalHeaderKey(name)
		if !safeUpstreamRequestHeader(canonical) || len(values) == 0 {
			continue
		}
		result[canonical] = strings.Join(values, ", ")
	}
	return result
}

func safeUpstreamQuery(source url.Values) url.Values {
	if len(source) == 0 {
		return nil
	}
	result := make(url.Values)
	for key, values := range source {
		if unsafeUpstreamQueryKey(key) {
			continue
		}
		result[key] = append([]string(nil), values...)
	}
	return result
}

func safeRawUpstreamQuery(source string) string {
	if source == "" {
		return ""
	}
	segments := strings.Split(source, "&")
	kept := make([]string, 0, len(segments))
	for _, segment := range segments {
		key := segment
		if separator := strings.IndexByte(key, '='); separator >= 0 {
			key = key[:separator]
		}
		if decoded, err := url.QueryUnescape(key); err == nil {
			key = decoded
		}
		if unsafeUpstreamQueryKey(key) {
			continue
		}
		kept = append(kept, segment)
	}
	return strings.Join(kept, "&")
}

func safeAttemptQuery(spec execution.AttemptSpec) string {
	if spec.RawQuery != "" {
		return safeRawUpstreamQuery(spec.RawQuery)
	}
	return safeUpstreamQuery(spec.Query).Encode()
}

func unsafeUpstreamQueryKey(key string) bool {
	normalized := strings.ReplaceAll(strings.ToLower(key), "-", "_")
	switch normalized {
	case "provider", "fallback", "fallbacks", "authorization", "proxy_authorization",
		"api_key", "apikey", "x_api_key", "x_goog_api_key", "key", "access_token":
		return true
	default:
		return false
	}
}

func resolveTypedTargetURL(baseURL, resourcePath, rawQuery string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed == nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid target URL")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + resourcePath
	parsed.RawPath = ""
	if rawQuery != "" {
		parsed.RawQuery = rawQuery
	}
	return parsed.String(), nil
}

func setTypedRequestURL(ctx *schemas.BifrostContext, requestURL string) {
	if ctx != nil && requestURL != "" {
		ctx.SetValue(schemas.BifrostContextKeyURLPath, requestURL)
	}
}

func safeUpstreamRequestHeader(canonical string) bool {
	switch canonical {
	case "Authorization", "Proxy-Authorization", "Api-Key", "X-Api-Key", "X-Goog-Api-Key",
		"Host", "Content-Length", "Connection", "Proxy-Connection", "Keep-Alive", "Te", "Trailer",
		"Transfer-Encoding", "Upgrade", "Cookie", "Set-Cookie":
		return false
	}
	return !strings.HasPrefix(strings.ToLower(canonical), "x-bf-")
}

func boundedRequestContext(parent context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if budget > 0 {
		return context.WithTimeout(parent, budget)
	}
	if _, hasDeadline := parent.Deadline(); hasDeadline {
		return context.WithCancel(parent)
	}
	// Bifrost's fasthttp operation may outlive a canceled caller while it releases
	// pooled objects. A finite default keeps that SDK cleanup goroutine bounded.
	return context.WithTimeout(parent, defaultRequestBudget)
}

type responseGate struct {
	timer   *time.Timer
	fired   chan struct{}
	stopOne chan struct{}
}

func startPreResponseGate(cancel context.CancelFunc, timeouts execution.AttemptTimeouts) *responseGate {
	budget := timeouts.FirstByte
	gate := &responseGate{fired: make(chan struct{}), stopOne: make(chan struct{})}
	if budget <= 0 {
		return gate
	}
	// Core exposes neither an independent dial hook nor transport configuration
	// per attempt. This gate is therefore used only where the adapter can observe
	// a native response or complete client stream frame. Buffered converted unary
	// calls are bounded by Request instead of mislabeling body time as first byte.
	gate.timer = time.AfterFunc(budget, func() {
		select {
		case <-gate.stopOne:
			return
		default:
		}
		close(gate.fired)
		cancel()
	})
	return gate
}

func (g *responseGate) stop() {
	if g == nil {
		return
	}
	select {
	case <-g.stopOne:
		return
	default:
		close(g.stopOne)
	}
	if g.timer != nil {
		g.timer.Stop()
	}
}

func (g *responseGate) expired() bool {
	if g == nil {
		return false
	}
	select {
	case <-g.fired:
		return true
	default:
		return false
	}
}

func minimumPositive(values ...time.Duration) time.Duration {
	var result time.Duration
	for _, value := range values {
		if value > 0 && (result == 0 || value < result) {
			result = value
		}
	}
	return result
}

type streamIdleTimer struct {
	duration time.Duration
	timer    *time.Timer
	ch       <-chan time.Time
}

func newIdleTimer(duration time.Duration) *streamIdleTimer {
	timer := &streamIdleTimer{duration: duration}
	if duration > 0 {
		timer.timer = time.NewTimer(duration)
		timer.ch = timer.timer.C
	}
	return timer
}

func (t *streamIdleTimer) channel() <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.ch
}

func (t *streamIdleTimer) pause() {
	if t == nil || t.timer == nil {
		return
	}
	if !t.timer.Stop() {
		select {
		case <-t.timer.C:
		default:
		}
	}
	t.ch = nil
}

func (t *streamIdleTimer) resume() {
	if t == nil || t.timer == nil {
		return
	}
	t.timer.Reset(t.duration)
	t.ch = t.timer.C
}

func (t *streamIdleTimer) stop() {
	if t != nil {
		t.pause()
	}
}

func safeValidationReason(err error) string {
	if err == nil {
		return "validation failed"
	}
	message := strings.TrimSpace(err.Error())
	if message == "" || strings.ContainsAny(message, "\r\n\x00") {
		return "validation failed"
	}
	if len(message) > 192 {
		return message[:192]
	}
	return message
}

func unaryContextFailure(ctx context.Context, preResponseExpired bool) execution.AttemptResult {
	kind, summary := contextFailure(ctx, preResponseExpired)
	return attemptedUnaryFailure(kind, summary)
}

func streamContextFailure(
	ctx context.Context,
	preResponseExpired bool,
	started bool,
	headers http.Header,
	requestID string,
	model string,
	usageEvidence *execution.UsageEvidence,
) execution.StreamResult {
	kind, summary := contextFailure(ctx, preResponseExpired)
	if !started {
		return attemptedStreamFailure(kind, summary)
	}
	return execution.StreamResult{
		DispatchState:     execution.DispatchMaybeSent,
		ResponseStarted:   true,
		StatusCode:        http.StatusOK,
		Header:            headers.Clone(),
		Model:             model,
		UpstreamRequestID: requestID,
		Usage:             cloneUsage(usageEvidence),
		Error:             &execution.ErrorEvidence{Kind: kind, Summary: summary, RequestID: requestID},
	}
}

func contextFailure(ctx context.Context, preResponseExpired bool) (execution.ErrorKind, string) {
	if preResponseExpired || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return execution.ErrorKindTimeout, "upstream request timed out"
	}
	return execution.ErrorKindCanceled, "upstream request canceled"
}

func sdkStreamEndedNormally(ctx *schemas.BifrostContext) bool {
	if ctx == nil {
		return false
	}
	ended, _ := ctx.Value(schemas.BifrostContextKeyStreamEndIndicator).(bool)
	return ended
}

func terminatedStreamFailure(
	headers http.Header,
	requestID string,
	model string,
	usageEvidence *execution.UsageEvidence,
) execution.StreamResult {
	return execution.StreamResult{
		DispatchState:     execution.DispatchMaybeSent,
		ResponseStarted:   true,
		StatusCode:        http.StatusOK,
		Header:            headers.Clone(),
		Model:             model,
		UpstreamRequestID: requestID,
		Usage:             cloneUsage(usageEvidence),
		Error: &execution.ErrorEvidence{
			Kind:      execution.ErrorKindTransport,
			Summary:   "upstream stream terminated before completion",
			RequestID: requestID,
		},
	}
}
