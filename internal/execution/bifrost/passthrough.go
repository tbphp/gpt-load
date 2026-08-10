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

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const maxStreamErrorEvidenceBytes = 64 << 10

type passthroughUnarySDKResult struct {
	response *schemas.BifrostPassthroughResponse
	err      *schemas.BifrostError
}

type passthroughStreamSDKResult struct {
	stream chan *schemas.BifrostStreamChunk
	err    *schemas.BifrostError
}

func sanitizeNativeChatBody(body []byte, upstreamModel string, stream ...bool) ([]byte, error) {
	object, err := decodeNativeJSONObject(body)
	if err != nil {
		return nil, err
	}
	changed := stripNativeControlFields(object)
	if !jsonStringEquals(object["model"], upstreamModel) {
		if err := forceJSONModel(object, upstreamModel); err != nil {
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
		return append([]byte(nil), body...), nil
	}
	return encodeNativeJSONObject(object)
}

func sanitizeNativeRequestBody(spec execution.AttemptSpec, stream bool) ([]byte, error) {
	switch {
	case spec.Operation == execution.OperationListModels:
		return nil, nil
	case (spec.ClientProtocol == protocol.OpenAICompletions || spec.ClientProtocol == protocol.Anthropic) &&
		(spec.Operation == execution.OperationChatCompletion || spec.Operation == execution.OperationProbe):
		body, err := sanitizeNativeChatBody(spec.Body, spec.UpstreamModel, stream)
		if err != nil {
			return nil, err
		}
		if spec.ClientProtocol == protocol.OpenAICompletions && stream && spec.IncludeUsage {
			body, err = includeOpenAIStreamUsage(body)
			if err != nil {
				return nil, err
			}
		}
		if spec.Operation != execution.OperationProbe {
			return body, nil
		}
		object, err := decodeNativeJSONObject(body)
		if err != nil {
			return nil, err
		}
		if _, exists := object["stream"]; !exists {
			object["stream"] = json.RawMessage("false")
			return encodeNativeJSONObject(object)
		}
		return body, nil
	case spec.ClientProtocol == protocol.Gemini &&
		(spec.Operation == execution.OperationChatCompletion || spec.Operation == execution.OperationProbe):
		object, err := decodeNativeJSONObject(spec.Body)
		if err != nil {
			// Preserve malformed/opaque provider payloads for the upstream to
			// validate, while valid JSON is scrubbed below.
			return append([]byte(nil), spec.Body...), nil
		}
		changed := stripNativeControlFields(object)
		if !changed {
			return append([]byte(nil), spec.Body...), nil
		}
		return encodeNativeJSONObject(object)
	case spec.ClientProtocol == protocol.OpenAIResponses &&
		(spec.Operation == execution.OperationResponsesCreate || spec.Operation == execution.OperationProbe):
		object, err := decodeNativeJSONObject(spec.Body)
		if err != nil {
			return nil, err
		}
		changed := stripNativeControlFields(object)
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
		(spec.Operation == execution.OperationResponsesCompact || spec.Operation == execution.OperationResponsesInputTokens):
		object, err := decodeNativeJSONObject(spec.Body)
		if err != nil {
			return nil, err
		}
		changed := stripNativeControlFields(object)
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
		changed := stripNativeControlFields(object)
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
		stripNativeControlFields(object)
		return encodeNativeJSONObject(object)
	}
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

func stripNativeControlFields(object map[string]json.RawMessage) bool {
	changed := false
	for key := range object {
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

func (r *Runtime) executeNative(
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

	bifrostContext := newSDKContext(callContext, spec, prepared.directKey)
	outcomeChannel := make(chan passthroughUnarySDKResult, 1)
	go func() {
		response, bifrostError := r.core.Passthrough(bifrostContext, prepared.provider, prepared.passthrough)
		outcomeChannel <- passthroughUnarySDKResult{response: response, err: bifrostError}
	}()

	var outcome passthroughUnarySDKResult
	select {
	case outcome = <-outcomeChannel:
		if preResponse.expired() || requestContext.Err() != nil {
			return unaryContextFailure(requestContext, preResponse.expired())
		}
	case <-callContext.Done():
		return unaryContextFailure(requestContext, preResponse.expired())
	}
	preResponse.stop()

	if outcome.err != nil {
		return unaryErrorResult(outcome.err, bifrostContext, prepared.secrets)
	}
	if outcome.response == nil {
		return attemptedUnaryFailure(execution.ErrorKindInternal, "execution runtime returned no response")
	}
	status := outcome.response.StatusCode
	if !validUpstreamStatus(status) {
		return attemptedUnaryFailure(execution.ErrorKindInternal, "execution runtime returned an invalid status")
	}
	headers := responseHeaders(outcome.response.Headers, bifrostContext, false)
	requestID := upstreamRequestID(headers)
	body := append([]byte(nil), outcome.response.Body...)
	model := openAIResponseModel(body, spec.UpstreamModel)
	usageEvidence, err := usageEvidenceFromPassthrough(outcome.response.PassthroughUsage)
	if err != nil {
		return startedUnaryFailure(status, headers, execution.ErrorKindInternal, "normalize upstream usage")
	}
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		if needsClientModelAlias(spec) {
			body, err = rewriteClientResponseModel(spec.ClientProtocol, body, spec.ClientModel)
			if err != nil {
				return startedUnaryFailure(status, headers, execution.ErrorKindInternal, "rewrite native response model")
			}
		}
		return execution.AttemptResult{
			DispatchState:     execution.DispatchMaybeSent,
			ResponseStarted:   true,
			StatusCode:        status,
			Header:            headers,
			Body:              body,
			Model:             model,
			UpstreamRequestID: requestID,
			Usage:             usageEvidence,
		}
	}
	evidence := passthroughHTTPError(status, headers, body, prepared.secrets)
	return execution.AttemptResult{
		DispatchState:     execution.DispatchMaybeSent,
		ResponseStarted:   true,
		StatusCode:        status,
		Header:            headers,
		Body:              redactSecrets(body, prepared.secrets),
		Model:             model,
		UpstreamRequestID: requestID,
		Error:             evidence,
	}
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
	bifrostContext := newSDKContext(callContext, spec, prepared.directKey)
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
	model := spec.UpstreamModel
	var usageEvidence *execution.UsageEvidence
	var errorBody bytes.Buffer
	firstEventGate := &nativeFirstSSEEventGate{}
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
				return finishNativeStream(status, headers, requestID, model, usageEvidence, errorBody.Bytes(), prepared.secrets)
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
				chunkUsage, err := usageEvidenceFromPassthrough(response.PassthroughUsage)
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
