package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/execution/geminiembedding"
	"gpt-load/internal/execution/geminiimage"
	"gpt-load/internal/execution/responsemodel"
	"gpt-load/internal/platform/contentcoding"
	"gpt-load/internal/platform/httpheader"
	"gpt-load/internal/protocol"
)

const maxStreamErrorEvidenceBytes = 64 << 10

type passthroughStreamSDKResult struct {
	stream chan *schemas.BifrostStreamChunk
	err    *schemas.BifrostError
}

// nativeMessageProvider 为缺少透传接口的渠道复用相同 wire 协议，保留原有渠道身份。
func nativeMessageProvider(providerKind channel.ProviderKind, spec execution.AttemptSpec) (schemas.ModelProvider, bool) {
	if spec.RouteMode != execution.RouteNative {
		return "", false
	}
	if providerKind == channel.ProviderAlibaba {
		// 百炼 Anthropic 端点不在 OpenAI 兼容前缀下，凭据测试也要走同一端点。
		if spec.ClientProtocol == protocol.Anthropic &&
			(spec.Operation == execution.OperationChatCompletion || spec.Operation == execution.OperationProbe) {
			return schemas.Anthropic, true
		}
		return "", false
	}
	if spec.Operation != execution.OperationChatCompletion && spec.Operation != execution.OperationResponsesCreate {
		return "", false
	}
	switch providerKind {
	case channel.ProviderDeepSeek, channel.ProviderOpenRouter, channel.ProviderGroq, channel.ProviderXAI:
		if spec.ClientProtocol == protocol.OpenAICompletions || spec.ClientProtocol == protocol.OpenAIResponses {
			return schemas.OpenAI, true
		}
		if providerKind == channel.ProviderDeepSeek && spec.ClientProtocol == protocol.Anthropic {
			return schemas.Anthropic, true
		}
	}
	return "", false
}

func sanitizeNativeChatBody(spec execution.AttemptSpec, stream ...bool) ([]byte, error) {
	object, err := decodeNativeJSONObject(spec.Body)
	if err != nil {
		return nil, err
	}
	changed := stripNativeControlFields(object, spec.ChannelID == string(channel.OpenRouter))
	if !jsonStringEquals(object["model"], spec.UpstreamModel) {
		if err := forceJSONModel(object, spec.UpstreamModel); err != nil {
			return nil, err
		}
		changed = true
	}
	if len(stream) > 0 {
		_, exists := object["stream"]
		if (exists && !jsonBoolEquals(object["stream"], stream[0])) || (!exists && stream[0]) {
			object["stream"] = json.RawMessage(strconv.FormatBool(stream[0]))
			changed = true
		}
	}
	if !changed {
		return append([]byte(nil), spec.Body...), nil
	}
	return encodeNativeJSONObject(object)
}

func sanitizeNativeRequestBody(spec execution.AttemptSpec, stream bool) ([]byte, error) {
	switch {
	case spec.Operation == execution.OperationListModels:
		return nil, nil
	case (spec.ClientProtocol == protocol.OpenAICompletions || spec.ClientProtocol == protocol.Anthropic) &&
		spec.Operation == execution.OperationChatCompletion:
		body, err := sanitizeNativeChatBody(spec, stream)
		if err != nil {
			return nil, err
		}
		if spec.ClientProtocol == protocol.OpenAICompletions && stream && spec.IncludeUsage {
			body, err = includeOpenAIStreamUsage(body)
			if err != nil {
				return nil, err
			}
		}
		return body, nil
	case spec.ClientProtocol == protocol.Anthropic && spec.Operation == execution.OperationCountTokens:
		return sanitizeNativeChatBody(spec)
	case spec.ClientProtocol == protocol.Gemini && spec.Operation == execution.OperationChatCompletion:
		object, err := decodeNativeJSONObject(spec.Body)
		if err != nil {
			// Preserve malformed/opaque provider payloads for the upstream to
			// validate, while valid JSON is scrubbed below.
			return append([]byte(nil), spec.Body...), nil
		}
		changed := stripNativeControlFields(object, false)
		if !changed {
			return append([]byte(nil), spec.Body...), nil
		}
		return encodeNativeJSONObject(object)
	case spec.ClientProtocol == protocol.Gemini && spec.Operation == execution.OperationCountTokens:
		object, err := decodeNativeJSONObject(spec.Body)
		if err != nil {
			return append([]byte(nil), spec.Body...), nil
		}
		if !stripNativeControlFields(object, false) {
			return append([]byte(nil), spec.Body...), nil
		}
		return encodeNativeJSONObject(object)
	case spec.ClientProtocol == protocol.OpenAIResponses &&
		spec.Operation == execution.OperationResponsesCreate:
		object, err := decodeNativeJSONObject(spec.Body)
		if err != nil {
			return nil, err
		}
		changed := stripNativeControlFields(object, spec.ChannelID == string(channel.OpenRouter))
		if !jsonStringEquals(object["model"], spec.UpstreamModel) {
			if err := forceJSONModel(object, spec.UpstreamModel); err != nil {
				return nil, err
			}
			changed = true
		}
		_, exists := object["stream"]
		if (exists && !jsonBoolEquals(object["stream"], stream)) || (!exists && stream) {
			object["stream"] = json.RawMessage(strconv.FormatBool(stream))
			changed = true
		}
		if !changed {
			return append([]byte(nil), spec.Body...), nil
		}
		return encodeNativeJSONObject(object)
	case spec.ClientProtocol == protocol.OpenAIResponses &&
		(spec.Operation == execution.OperationResponsesCompact || spec.Operation == execution.OperationResponsesInputTokens || spec.Operation == execution.OperationWebSearch):
		object, err := decodeNativeJSONObject(spec.Body)
		if err != nil {
			return nil, err
		}
		changed := stripNativeControlFields(object, false)
		if !jsonStringEquals(object["model"], spec.UpstreamModel) {
			if err := forceJSONModel(object, spec.UpstreamModel); err != nil {
				return nil, err
			}
			changed = true
		}
		if _, exists := object["stream"]; exists {
			delete(object, "stream")
			changed = true
		}
		if !changed {
			return append([]byte(nil), spec.Body...), nil
		}
		return encodeNativeJSONObject(object)
	case spec.ClientProtocol == protocol.OpenAIResponses && spec.Operation == execution.OperationResponsesPassthrough:
		if len(bytes.TrimSpace(spec.Body)) == 0 {
			return nil, nil
		}
		object, err := decodeNativeJSONObject(spec.Body)
		if err != nil {
			return nil, err
		}
		changed := stripNativeControlFields(object, false)
		if _, hasModel := object["model"]; hasModel && strings.TrimSpace(spec.UpstreamModel) != "" {
			if !jsonStringEquals(object["model"], spec.UpstreamModel) {
				if err := forceJSONModel(object, spec.UpstreamModel); err != nil {
					return nil, err
				}
				changed = true
			}
		}
		if !changed {
			return append([]byte(nil), spec.Body...), nil
		}
		return encodeNativeJSONObject(object)
	default:
		if len(bytes.TrimSpace(spec.Body)) == 0 {
			return nil, nil
		}
		object, err := decodeNativeJSONObject(spec.Body)
		if err != nil {
			return nil, err
		}
		stripNativeControlFields(object, false)
		return encodeNativeJSONObject(object)
	}
}

func sanitizeNativePassthroughRequest(
	spec execution.AttemptSpec,
	stream bool,
) ([]byte, http.Header, error) {
	if spec.ClientProtocol == protocol.OpenAIImages &&
		(spec.Operation == execution.OperationImagesGenerate || spec.Operation == execution.OperationImagesEdit) {
		request, err := dialect.NewOpenAIImages().SanitizeRequestForAttempt(&dialect.ParsedRequest{
			Method: spec.Method, Path: spec.Path, RawQuery: spec.RawQuery,
			Header: spec.Header.Clone(), Body: bytes.Clone(spec.Body),
		}, spec.UpstreamModel, stream)
		if err != nil {
			return nil, nil, err
		}
		return bytes.Clone(request.Body), request.Header.Clone(), nil
	}
	if spec.ClientProtocol == protocol.GeminiEmbeddings && spec.Operation == execution.OperationEmbeddingsCreate {
		model := spec.UpstreamModel
		if spec.ChannelID == string(channel.GoogleVertex) {
			var ok bool
			model, ok = channel.NormalizeVertexEmbeddingsModel(model)
			if !ok {
				return nil, nil, fmt.Errorf("invalid Vertex Embeddings model")
			}
		}
		request, err := dialect.NewGeminiEmbeddings().SanitizeRequestForAttempt(&dialect.ParsedRequest{
			Method: spec.Method, Path: spec.Path, RawQuery: spec.RawQuery,
			Header: spec.Header.Clone(), Body: spec.Body,
		}, model)
		if err != nil {
			return nil, nil, err
		}
		return request.Body, request.Header, nil
	}
	body, err := sanitizeNativeRequestBody(spec, stream)
	if err != nil {
		return nil, nil, err
	}
	return body, spec.Header.Clone(), nil
}

func decodeNativeJSONObject(body []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, fmt.Errorf("decode request object")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("request body has trailing data")
	}
	return object, nil
}

func stripNativeControlFields(object map[string]json.RawMessage, allowProviderPreferences bool) bool {
	changed := false
	for key, value := range object {
		// OpenRouter's native provider object selects its upstream providers.
		// Scalar gateway controls and case variants remain filtered.
		if allowProviderPreferences && key == "provider" && bytes.HasPrefix(bytes.TrimSpace(value), []byte("{")) {
			continue
		}
		normalized := strings.ReplaceAll(strings.ToLower(key), "-", "_")
		switch normalized {
		case "provider", "fallback", "fallbacks", "authorization", "proxy_authorization",
			"api_key", "apikey", "x_api_key", "x_goog_api_key":
			delete(object, key)
			changed = true
		}
	}
	return changed
}

func jsonStringEquals(raw json.RawMessage, expected string) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && value == expected
}

func jsonBoolEquals(raw json.RawMessage, expected bool) bool {
	var value bool
	return json.Unmarshal(raw, &value) == nil && value == expected
}

func includeOpenAIStreamUsage(body []byte) ([]byte, error) {
	object, err := decodeNativeJSONObject(body)
	if err != nil {
		return nil, err
	}
	options := make(map[string]json.RawMessage)
	if raw, exists := object["stream_options"]; exists &&
		!bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := json.Unmarshal(raw, &options); err != nil || options == nil {
			return nil, fmt.Errorf("stream_options must be an object or null")
		}
	}
	options["include_usage"] = json.RawMessage("true")
	encoded, err := json.Marshal(options)
	if err != nil {
		return nil, fmt.Errorf("encode stream_options")
	}
	object["stream_options"] = encoded
	return encodeNativeJSONObject(object)
}

func forceJSONModel(object map[string]json.RawMessage, upstreamModel string) error {
	model, err := json.Marshal(upstreamModel)
	if err != nil {
		return fmt.Errorf("encode selected model")
	}
	object["model"] = model
	return nil
}

func encodeNativeJSONObject(object map[string]json.RawMessage) ([]byte, error) {
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode request object")
	}
	return encoded, nil
}

func (r *Runtime) executePassthrough(
	parent context.Context,
	spec execution.AttemptSpec,
	prepared preparedAttempt,
) execution.AttemptResult {
	if spec.Method == http.MethodHead {
		return r.executeNativeHead(parent, spec, prepared)
	}
	requestContext, requestCancel := boundedRequestContext(parent, spec.Timeouts.Request)
	defer requestCancel()
	callContext, callCancel := context.WithCancel(requestContext)
	defer callCancel()
	preResponse := startPreResponseGate(callCancel, spec.Timeouts)
	defer preResponse.stop()

	bifrostContext := r.newSDKContext(callContext, spec, prepared.directKey)
	cancelCall := func() {
		callCancel()
		bifrostContext.Cancel()
	}
	outcomeChannel := make(chan passthroughStreamSDKResult, 1)
	go func() {
		stream, bifrostError := r.core.PassthroughStream(bifrostContext, prepared.provider, prepared.passthrough)
		outcomeChannel <- passthroughStreamSDKResult{stream: stream, err: bifrostError}
	}()

	var outcome passthroughStreamSDKResult
	select {
	case outcome = <-outcomeChannel:
		if preResponse.expired() || requestContext.Err() != nil {
			return unaryContextFailure(requestContext, preResponse.expired())
		}
	case <-callContext.Done():
		return unaryContextFailure(requestContext, preResponse.expired())
	}
	if outcome.err != nil {
		return unaryErrorResult(outcome.err, bifrostContext, prepared.secrets)
	}
	if outcome.stream == nil {
		return attemptedUnaryFailure(execution.ErrorKindInternal, "execution runtime returned no response stream")
	}

	responseObserved := false
	status := 0
	var headers http.Header
	requestID := ""
	var body bytes.Buffer
	var usageEvidence *execution.UsageEvidence
	for {
		select {
		case <-callContext.Done():
			return unaryContextFailure(requestContext, preResponse.expired())
		case chunk, open := <-outcome.stream:
			if !open {
				if !responseObserved {
					return attemptedUnaryFailure(execution.ErrorKindInternal, "execution runtime returned an empty response stream")
				}
				goto complete
			}
			if chunk == nil || chunk.BifrostError != nil {
				cancelCall()
				if chunk != nil && chunk.BifrostError != nil {
					return unaryErrorResult(chunk.BifrostError, bifrostContext, prepared.secrets)
				}
				return attemptedUnaryFailure(execution.ErrorKindInternal, "execution runtime returned an invalid response chunk")
			}
			response := chunk.BifrostPassthroughResponse
			if response == nil || !validUpstreamStatus(response.StatusCode) {
				cancelCall()
				return attemptedUnaryFailure(execution.ErrorKindInternal, "execution runtime returned an invalid response")
			}
			if !responseObserved {
				responseObserved = true
				preResponse.stop()
				status = response.StatusCode
				headers = responseHeaders(response.Headers, bifrostContext, false)
				requestID = upstreamRequestID(headers)
			} else if response.StatusCode != status {
				cancelCall()
				return startedUnaryFailure(status, headers, execution.ErrorKindInternal, "execution runtime changed response status")
			}
			limit := r.unaryResponseBodyLimit(spec)
			if status < http.StatusOK || status >= http.StatusMultipleChoices {
				limit = min(limit, int64(maxStreamErrorEvidenceBytes))
			}
			if int64(len(response.Body)) > limit-int64(body.Len()) {
				cancelCall()
				return startedUnaryFailure(status, headers, execution.ErrorKindInternal, "upstream response exceeds size limit")
			}
			_, _ = body.Write(response.Body)
			if response.PassthroughUsage != nil {
				chunkUsage, err := usageEvidenceFromPassthroughForSpec(spec, response.PassthroughUsage)
				if err != nil {
					cancelCall()
					return startedUnaryFailure(status, headers, execution.ErrorKindInternal, "normalize upstream usage")
				}
				usageEvidence = cloneUsage(chunkUsage)
			}
		}
	}

complete:
	bodyBytes := body.Bytes()
	if spec.ClientProtocol != protocol.OpenAIImages {
		bodyBytes = bytes.Clone(bodyBytes)
	}
	restoredEncoding := contentcoding.Identity
	if encoding, encoded := sniffContentCoding(bodyBytes); encoded && headers.Get("Content-Encoding") == "" &&
		(!audioSpeechAudio(spec, headers) || encodedAudio(encoding, bodyBytes, r.unaryResponseBodyLimit(spec))) {
		if prepared.mode != channel.RouteNative {
			return startedUnaryFailure(status, headers, execution.ErrorKindInternal, "encoded upstream response cannot be safely forwarded")
		}
		// 上游忽略 Accept-Encoding: identity 时，SDK 透传会丢掉 Content-Encoding 但保留压缩正文；
		// 补回标记，由网关在体积上限内解码后照常脱敏、改写模型并补提用量。
		headers.Set("Content-Encoding", string(encoding))
		restoredEncoding = encoding
	}
	model := openAIResponseModel(bodyBytes, spec.UpstreamModel)
	if responsemodel.IsConversation(spec.ClientProtocol, spec.Operation) {
		modelObserver := responsemodel.New(spec.ClientProtocol, spec.UpstreamModel)
		modelObserver.ObserveJSON(bodyBytes)
		model = modelObserver.Model
	}
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		if prepared.mode == channel.RouteConverted && spec.ClientProtocol == protocol.OpenAIImages {
			var err error
			bodyBytes, usageEvidence, err = geminiimage.ConvertResponse(bodyBytes)
			httpheader.StripRepresentationMetadata(headers)
			headers.Set("Content-Type", "application/json")
			if err != nil {
				failure := startedUnaryFailure(http.StatusBadGateway, headers, execution.ErrorKindProvider, geminiimage.ErrInvalidResponse.Error())
				failure.Error.Code = "invalid_image_response"
				failure.Error.StatusCode = http.StatusBadGateway
				failure.Error.Hint = execution.FailureHintRequestRejected
				failure.Error.OriginHint, failure.Error.ScopeHint = execution.ErrorOriginUpstream, execution.ErrorScopeRequest
				return failure
			}
			model = openAIResponseModel(bodyBytes, "")
		}
		if prepared.mode == channel.RouteConverted && spec.ClientProtocol == protocol.OpenAIEmbeddings && prepared.embeddingConversion != nil {
			var err error
			bodyBytes, usageEvidence, err = geminiembedding.ConvertResponse(bodyBytes, *prepared.embeddingConversion)
			httpheader.StripRepresentationMetadata(headers)
			headers.Set("Content-Type", "application/json")
			if err != nil {
				failure := startedUnaryFailure(http.StatusBadGateway, headers, execution.ErrorKindProvider, geminiembedding.ErrInvalidResponse.Error())
				failure.Error.Code = "invalid_embedding_response"
				failure.Error.StatusCode = http.StatusBadGateway
				failure.Error.Hint = execution.FailureHintRequestRejected
				failure.Error.OriginHint, failure.Error.ScopeHint = execution.ErrorOriginUpstream, execution.ErrorScopeRequest
				return failure
			}
			model = openAIResponseModel(bodyBytes, "")
		}
		if needsClientModelAlias(spec) && headers.Get("Content-Encoding") == "" {
			var err error
			bodyBytes, err = rewriteClientResponseModel(spec.ClientProtocol, bodyBytes, spec.ClientModel)
			if err != nil {
				return startedUnaryFailure(status, headers, execution.ErrorKindInternal, "rewrite native response model")
			}
		}
		return execution.AttemptResult{
			DispatchState:     execution.DispatchMaybeSent,
			ResponseStarted:   true,
			StatusCode:        status,
			Header:            headers,
			Body:              bodyBytes,
			Model:             model,
			UpstreamRequestID: requestID,
			Usage:             usageEvidence,
		}
	}
	evidenceBody := bodyBytes
	if restoredEncoding != contentcoding.Identity {
		// 错误码决定是否重试与套用分组错误规则，需从解压后的正文读取。
		if plain, err := contentcoding.DecodeLimited(restoredEncoding, bodyBytes, maxStreamErrorEvidenceBytes); err == nil {
			evidenceBody = plain
		}
	}
	evidence := passthroughHTTPError(status, headers, evidenceBody, prepared.secrets)
	return execution.AttemptResult{
		DispatchState:     execution.DispatchMaybeSent,
		ResponseStarted:   true,
		StatusCode:        status,
		Header:            headers,
		Body:              redactSecrets(bodyBytes, prepared.secrets),
		Model:             model,
		UpstreamRequestID: requestID,
		Error:             evidence,
	}
}

func (r *Runtime) executeNativeHead(
	parent context.Context,
	spec execution.AttemptSpec,
	prepared preparedAttempt,
) execution.AttemptResult {
	requestContext, requestCancel := boundedRequestContext(parent, spec.Timeouts.Request)
	defer requestCancel()
	callContext, callCancel := context.WithCancel(requestContext)
	defer callCancel()
	preResponse := startPreResponseGate(callCancel, spec.Timeouts)
	defer preResponse.stop()

	bifrostContext := r.newSDKContext(callContext, spec, prepared.directKey)
	outcomeChannel := make(chan struct {
		response *schemas.BifrostPassthroughResponse
		err      *schemas.BifrostError
	}, 1)
	go func() {
		response, bifrostError := r.core.Passthrough(bifrostContext, prepared.provider, prepared.passthrough)
		outcomeChannel <- struct {
			response *schemas.BifrostPassthroughResponse
			err      *schemas.BifrostError
		}{response: response, err: bifrostError}
	}()

	select {
	case outcome := <-outcomeChannel:
		if preResponse.expired() || requestContext.Err() != nil {
			return unaryContextFailure(requestContext, preResponse.expired())
		}
		preResponse.stop()
		if outcome.err != nil {
			return unaryErrorResult(outcome.err, bifrostContext, prepared.secrets)
		}
		if outcome.response == nil || !validUpstreamStatus(outcome.response.StatusCode) {
			return attemptedUnaryFailure(execution.ErrorKindInternal, "execution runtime returned an invalid response")
		}
		headers := responseHeaders(outcome.response.Headers, bifrostContext, false)
		if len(outcome.response.Body) > 0 {
			return startedUnaryFailure(outcome.response.StatusCode, headers, execution.ErrorKindInternal, "HEAD response contained an unexpected body")
		}
		result := execution.AttemptResult{
			DispatchState:     execution.DispatchMaybeSent,
			ResponseStarted:   true,
			StatusCode:        outcome.response.StatusCode,
			Header:            headers,
			UpstreamRequestID: upstreamRequestID(headers),
		}
		if outcome.response.StatusCode < http.StatusOK || outcome.response.StatusCode >= http.StatusMultipleChoices {
			result.Error = passthroughHTTPError(outcome.response.StatusCode, headers, nil, prepared.secrets)
		}
		return result
	case <-callContext.Done():
		return unaryContextFailure(requestContext, preResponse.expired())
	}
}

// sniffContentCoding 按魔数识别正文是否为 gzip 或 zstd 压缩数据。
func sniffContentCoding(body []byte) (contentcoding.Encoding, bool) {
	switch {
	case len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b:
		return contentcoding.Gzip, true
	case len(body) >= 4 && body[0] == 0x28 && body[1] == 0xb5 && body[2] == 0x2f && body[3] == 0xfd:
		return contentcoding.Zstd, true
	}
	return contentcoding.Identity, false
}

func (r *Runtime) executeNativeStream(
	parent context.Context,
	spec execution.AttemptSpec,
	prepared preparedAttempt,
	sink execution.StreamSink,
) execution.StreamResult {
	requestContext, requestCancel := boundedRequestContext(parent, spec.Timeouts.Request)
	defer requestCancel()
	callContext, callCancel := context.WithCancel(requestContext)
	bifrostContext := r.newStreamingSDKContext(callContext, spec, prepared.directKey)
	cancelCall := func() {
		callCancel()
		bifrostContext.Cancel()
	}
	defer cancelCall()
	preResponse := startPreResponseGate(cancelCall, spec.Timeouts)
	defer preResponse.stop()

	outcomeChannel := make(chan passthroughStreamSDKResult, 1)
	go func() {
		stream, bifrostError := r.core.PassthroughStream(bifrostContext, prepared.provider, prepared.passthrough)
		outcomeChannel <- passthroughStreamSDKResult{stream: stream, err: bifrostError}
	}()

	var outcome passthroughStreamSDKResult
	select {
	case outcome = <-outcomeChannel:
		if preResponse.expired() || requestContext.Err() != nil {
			return streamContextFailure(requestContext, preResponse.expired(), false, nil, "", "", nil)
		}
	case <-callContext.Done():
		return streamContextFailure(requestContext, preResponse.expired(), false, nil, "", "", nil)
	}
	if outcome.err != nil {
		return streamErrorResult(outcome.err, bifrostContext, prepared.secrets, false, 0, nil, "", nil)
	}
	if outcome.stream == nil {
		return attemptedStreamFailure(execution.ErrorKindInternal, "execution runtime returned no stream")
	}

	sequence := uint64(0)
	responseObserved := false
	started := false
	status := 0
	var headers http.Header
	requestID := ""
	modelObserver := responsemodel.New(spec.ClientProtocol, spec.UpstreamModel)
	model := ""
	var usageEvidence *execution.UsageEvidence
	var errorBody bytes.Buffer
	firstData := execution.NewFirstResponseSSEObserver(parent)
	firstEventGate := newNativeFirstSSEEventGate(spec)
	aliasRewriter := newNativeAliasSSERewriter(spec)
	idleTimer := newIdleTimer(spec.Timeouts.StreamIdle)
	defer idleTimer.stop()
	emitReady := func() error {
		if started {
			return nil
		}
		preResponse.stop()
		started = true
		sequence = 1
		return sink(execution.StreamEvent{
			Sequence:          sequence,
			Kind:              execution.StreamEventReady,
			StatusCode:        status,
			Header:            headers.Clone(),
			UpstreamRequestID: requestID,
		})
	}

	for {
		select {
		case <-callContext.Done():
			return nativeStreamContextFailure(requestContext, preResponse.expired(), started, status, headers, requestID, model, usageEvidence)
		case <-requestContext.Done():
			cancelCall()
			return nativeStreamContextFailure(requestContext, false, started, status, headers, requestID, model, usageEvidence)
		case <-idleTimer.channel():
			cancelCall()
			return nativeStreamContextFailure(requestContext, true, started, status, headers, requestID, model, usageEvidence)
		case chunk, open := <-outcome.stream:
			idleTimer.pause()
			if callContext.Err() != nil || requestContext.Err() != nil || preResponse.expired() {
				return nativeStreamContextFailure(requestContext, preResponse.expired(), started, status, headers, requestID, model, usageEvidence)
			}
			if !open {
				if !responseObserved {
					return attemptedStreamFailure(execution.ErrorKindInternal, "execution runtime returned an empty stream")
				}
				if status >= http.StatusOK && status < http.StatusMultipleChoices {
					if err := firstEventGate.finish(); err != nil {
						return attemptedStreamFailure(execution.ErrorKindInternal, "invalid upstream SSE stream")
					}
				}
				if aliasRewriter != nil && status >= http.StatusOK && status < http.StatusMultipleChoices {
					data, err := aliasRewriter.finish()
					if err != nil {
						return streamAliasFailure(status, headers, requestID, model, usageEvidence)
					}
					if len(data) > 0 {
						sequence++
						if err := sink(execution.StreamEvent{Sequence: sequence, Kind: execution.StreamEventData, Data: data}); err != nil {
							return nativeStreamSinkFailure(status, headers, requestID, model, usageEvidence)
						}
					}
				}
				modelObserver.FinishSSE()
				return finishNativeStream(status, headers, requestID, modelObserver.Model, usageEvidence, errorBody.Bytes(), prepared.secrets)
			}
			if chunk == nil {
				cancelCall()
				return nativeStreamRuntimeFailure(bifrostContext, prepared.secrets, started, status, headers, model, usageEvidence)
			}
			if chunk.BifrostError != nil {
				cancelCall()
				return streamErrorResult(chunk.BifrostError, bifrostContext, prepared.secrets, started, status, headers, model, usageEvidence)
			}
			response := chunk.BifrostPassthroughResponse
			if response == nil || !validUpstreamStatus(response.StatusCode) {
				cancelCall()
				return nativeStreamRuntimeFailure(bifrostContext, prepared.secrets, started, status, headers, model, usageEvidence)
			}
			if !responseObserved {
				responseObserved = true
				status = response.StatusCode
				headers = responseHeaders(response.Headers, bifrostContext, true)
				requestID = upstreamRequestID(headers)
				if status < http.StatusOK || status >= http.StatusMultipleChoices {
					if err := emitReady(); err != nil {
						callCancel()
						return nativeStreamSinkFailure(status, headers, requestID, model, usageEvidence)
					}
				}
			} else if response.StatusCode != status {
				cancelCall()
				return nativeStreamRuntimeFailure(bifrostContext, prepared.secrets, started, status, headers, model, usageEvidence)
			}

			if len(response.Body) > 0 {
				data := append([]byte(nil), response.Body...)
				if status < http.StatusOK || status >= http.StatusMultipleChoices {
					appendBounded(&errorBody, data, maxStreamErrorEvidenceBytes)
					data = redactSecrets(data, prepared.secrets)
				} else {
					var err error
					firstData(data)
					modelObserver.ObserveSSE(data)
					model = modelObserver.Model
					data, err = firstEventGate.push(data)
					if err != nil {
						cancelCall()
						if !started {
							return attemptedStreamFailure(execution.ErrorKindInternal, "invalid upstream SSE stream")
						}
						return nativeStreamProtocolFailure(status, headers, requestID, model, usageEvidence)
					}
					if aliasRewriter != nil && len(data) > 0 {
						data, err = aliasRewriter.push(data)
						if err != nil {
							cancelCall()
							if !started {
								return attemptedStreamFailure(execution.ErrorKindInternal, "rewrite client response model")
							}
							return streamAliasFailure(status, headers, requestID, model, usageEvidence)
						}
					}
				}
				if len(data) > 0 {
					if err := emitReady(); err != nil {
						cancelCall()
						return nativeStreamSinkFailure(status, headers, requestID, model, usageEvidence)
					}
					sequence++
					if err := sink(execution.StreamEvent{Sequence: sequence, Kind: execution.StreamEventData, Data: data}); err != nil {
						cancelCall()
						return nativeStreamSinkFailure(status, headers, requestID, model, usageEvidence)
					}
				}
			}
			if response.PassthroughUsage != nil {
				chunkUsage, err := usageEvidenceFromPassthroughForSpec(spec, response.PassthroughUsage)
				if err != nil {
					cancelCall()
					return nativeStreamRuntimeFailure(bifrostContext, prepared.secrets, started, status, headers, model, usageEvidence)
				}
				if chunkUsage != nil {
					usageEvidence = cloneUsage(chunkUsage)
					if !started {
						idleTimer.resume()
						continue
					}
					sequence++
					if err := sink(execution.StreamEvent{
						Sequence: sequence,
						Kind:     execution.StreamEventUsage,
						Usage:    cloneUsage(chunkUsage),
					}); err != nil {
						cancelCall()
						return nativeStreamSinkFailure(status, headers, requestID, model, usageEvidence)
					}
				}
			}
			idleTimer.resume()
		}
	}
}

func usageEvidenceFromPassthroughForSpec(
	spec execution.AttemptSpec,
	source *schemas.BifrostPassthroughUsage,
) (*execution.UsageEvidence, error) {
	// 搜索不计模型用量；转换路由由转换器提供证据，原生 OpenAI Embeddings 不经过透传。
	if spec.Operation == execution.OperationWebSearch || spec.ClientProtocol == protocol.OpenAIImages || spec.ClientProtocol == protocol.OpenAIEmbeddings ||
		spec.ClientProtocol == protocol.Rerank || spec.ClientProtocol == protocol.Decisions ||
		spec.ClientProtocol == protocol.GeminiDecisions {
		return nil, nil
	}
	return usageEvidenceFromPassthrough(source)
}

func nativeStreamRuntimeFailure(
	bifrostContext *schemas.BifrostContext,
	secrets []string,
	started bool,
	status int,
	headers http.Header,
	model string,
	usageEvidence *execution.UsageEvidence,
) execution.StreamResult {
	return streamErrorResult(nil, bifrostContext, secrets, started, status, headers, model, usageEvidence)
}

func nativeStreamContextFailure(
	ctx context.Context,
	preResponseExpired bool,
	started bool,
	status int,
	headers http.Header,
	requestID string,
	model string,
	usageEvidence *execution.UsageEvidence,
) execution.StreamResult {
	if !started {
		return streamContextFailure(ctx, preResponseExpired, false, nil, "", model, usageEvidence)
	}
	kind, summary := contextFailure(ctx, preResponseExpired)
	return execution.StreamResult{
		DispatchState:     execution.DispatchMaybeSent,
		ResponseStarted:   true,
		StatusCode:        status,
		Header:            headers.Clone(),
		Model:             model,
		UpstreamRequestID: requestID,
		Usage:             cloneUsage(usageEvidence),
		Error: &execution.ErrorEvidence{
			Kind:      kind,
			Summary:   summary,
			RequestID: requestID,
		},
	}
}

func nativeStreamSinkFailure(
	status int,
	headers http.Header,
	requestID string,
	model string,
	usageEvidence *execution.UsageEvidence,
) execution.StreamResult {
	return execution.StreamResult{
		DispatchState:     execution.DispatchMaybeSent,
		ResponseStarted:   true,
		StatusCode:        status,
		Header:            headers.Clone(),
		Model:             model,
		UpstreamRequestID: requestID,
		Usage:             cloneUsage(usageEvidence),
		Error: &execution.ErrorEvidence{
			Kind:      execution.ErrorKindCanceled,
			Summary:   "stream consumer stopped",
			RequestID: requestID,
		},
	}
}

func streamAliasFailure(
	status int,
	headers http.Header,
	requestID string,
	model string,
	usageEvidence *execution.UsageEvidence,
) execution.StreamResult {
	return execution.StreamResult{
		DispatchState:     execution.DispatchMaybeSent,
		ResponseStarted:   true,
		StatusCode:        status,
		Header:            headers.Clone(),
		Model:             model,
		UpstreamRequestID: requestID,
		Usage:             cloneUsage(usageEvidence),
		Error: &execution.ErrorEvidence{
			Kind:      execution.ErrorKindInternal,
			Summary:   "rewrite client response model",
			RequestID: requestID,
		},
	}
}

func nativeStreamProtocolFailure(
	status int,
	headers http.Header,
	requestID string,
	model string,
	usageEvidence *execution.UsageEvidence,
) execution.StreamResult {
	return execution.StreamResult{
		DispatchState:     execution.DispatchMaybeSent,
		ResponseStarted:   true,
		StatusCode:        status,
		Header:            headers.Clone(),
		Model:             model,
		UpstreamRequestID: requestID,
		Usage:             cloneUsage(usageEvidence),
		Error: &execution.ErrorEvidence{
			Kind:      execution.ErrorKindInternal,
			Summary:   "invalid upstream SSE stream",
			RequestID: requestID,
		},
	}
}

func finishNativeStream(
	status int,
	headers http.Header,
	requestID string,
	model string,
	usageEvidence *execution.UsageEvidence,
	errorBody []byte,
	secrets []string,
) execution.StreamResult {
	result := execution.StreamResult{
		DispatchState:     execution.DispatchMaybeSent,
		ResponseStarted:   true,
		StatusCode:        status,
		Header:            headers.Clone(),
		Model:             model,
		UpstreamRequestID: requestID,
		Usage:             cloneUsage(usageEvidence),
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		result.Error = passthroughHTTPError(status, headers, errorBody, secrets)
	}
	return result
}

func appendBounded(destination *bytes.Buffer, source []byte, limit int) {
	if destination == nil || limit <= 0 || destination.Len() >= limit {
		return
	}
	remaining := limit - destination.Len()
	if len(source) > remaining {
		source = source[:remaining]
	}
	_, _ = destination.Write(source)
}

func validUpstreamStatus(status int) bool { return status >= 100 && status <= 599 }
