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

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const (
	// Bifrost 1.8.4 compares a compressed Content-Length before decoding. Force
	// every Embeddings success body through its decoded streaming reader, then
	// enforce GPT-Load's protocol limit while materializing the raw JSON once.
	embeddingDecodedResponseThresholdBytes = int64(1)
	embeddingDecodedResponsePrefetchBytes  = 2
)

func buildEmbeddingRequest(
	spec execution.AttemptSpec,
	provider schemas.ModelProvider,
) (*schemas.BifrostEmbeddingRequest, error) {
	sanitized, err := dialect.NewOpenAIEmbeddings().SanitizeRequestForAttempt(
		&dialect.ParsedRequest{
			Method: spec.Method, Path: spec.Path, RawQuery: spec.RawQuery,
			Header: spec.Header.Clone(), Body: bytes.Clone(spec.Body),
		},
		spec.UpstreamModel,
	)
	if err != nil {
		return nil, err
	}
	input, err := embeddingInputMarker(sanitized.Body)
	if err != nil {
		return nil, err
	}
	return &schemas.BifrostEmbeddingRequest{
		Provider:       provider,
		Model:          spec.UpstreamModel,
		Input:          input,
		Fallbacks:      nil,
		RawRequestBody: bytes.Clone(sanitized.Body),
	}, nil
}

func newEmbeddingProbeRequest(
	provider schemas.ModelProvider,
	model string,
) *schemas.BifrostEmbeddingRequest {
	input := "ping"
	body, _ := json.Marshal(struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}{Model: model, Input: input})
	return &schemas.BifrostEmbeddingRequest{
		Provider:       provider,
		Model:          model,
		Input:          &schemas.EmbeddingInput{Text: &input},
		RawRequestBody: body,
	}
}

// embeddingInputMarker preserves only the validated input shape needed by
// Bifrost's typed preflight. The selected provider receives RawRequestBody, so
// GPT-Load does not impose Go integer bounds on token IDs or rebuild the wire.
func embeddingInputMarker(body []byte) (*schemas.EmbeddingInput, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return nil, fmt.Errorf("decode Embeddings request")
	}
	raw := bytes.TrimSpace(object["input"])
	if len(raw) == 0 {
		return nil, fmt.Errorf("Embeddings input is required")
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, fmt.Errorf("decode Embeddings text input")
		}
		return &schemas.EmbeddingInput{Text: &text}, nil
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return nil, fmt.Errorf("decode Embeddings array input")
	}
	if len(values) == 0 {
		return &schemas.EmbeddingInput{Texts: []string{}}, nil
	}
	first := bytes.TrimSpace(values[0])
	if len(first) == 0 {
		return nil, fmt.Errorf("decode Embeddings array input")
	}
	switch first[0] {
	case '"':
		var texts []string
		if err := json.Unmarshal(raw, &texts); err != nil {
			return nil, fmt.Errorf("decode Embeddings text array")
		}
		return &schemas.EmbeddingInput{Texts: texts}, nil
	case '[':
		return &schemas.EmbeddingInput{Embeddings: [][]int{}}, nil
	default:
		return &schemas.EmbeddingInput{Embedding: []int{}}, nil
	}
}

func embeddingTypedTarget(
	providerKind channel.ProviderKind,
	targetConfig json.RawMessage,
	rawQuery string,
) (string, error) {
	resourcePath := "/v1/embeddings"
	baseURL, configured, err := targetBaseURL(targetConfig)
	if err != nil {
		return "", err
	}
	switch providerKind {
	case channel.ProviderOpenAI:
	case channel.ProviderMultiProtocolGateway:
		if !configured {
			return "", fmt.Errorf("multi-protocol gateway Embeddings target is required")
		}
		return resolveMultiProtocolGatewayTargetURL(baseURL, resourcePath, rawQuery)
	case channel.ProviderOpenRouter:
		// OpenRouter concatenates the configured provider base URL with the
		// context path, so the per-request override must remain relative.
		return appendTypedQuery(resourcePath, rawQuery), nil
	case channel.ProviderOpenAICompatible:
		if !configured {
			return "", fmt.Errorf("compatible Embeddings target is required")
		}
		resourcePath = "/embeddings"
	default:
		return "", fmt.Errorf("unsupported Embeddings provider")
	}
	if configured {
		return resolveTypedTargetURL(baseURL, resourcePath, rawQuery)
	}
	return appendTypedQuery(resourcePath, rawQuery), nil
}

func enableEmbeddingWireFidelity(ctx *schemas.BifrostContext) {
	if ctx == nil {
		return
	}
	ctx.SetValue(schemas.BifrostContextKeyAllowPerRequestRawOverride, true)
	ctx.SetValue(schemas.BifrostContextKeyUseRawRequestBody, true)
	ctx.SetValue(schemas.BifrostContextKeySendBackRawResponse, true)
	ctx.SetValue(schemas.BifrostContextKeyPassthroughExtraParams, true)
	ctx.SetValue(
		schemas.BifrostContextKeyLargeResponseThreshold,
		embeddingDecodedResponseThresholdBytes,
	)
	ctx.SetValue(
		schemas.BifrostContextKeyLargePayloadPrefetchSize,
		embeddingDecodedResponsePrefetchBytes,
	)
}

func (r *Runtime) executeEmbedding(
	parent context.Context,
	spec execution.AttemptSpec,
	prepared preparedAttempt,
) execution.AttemptResult {
	requestContext, requestCancel := boundedRequestContext(parent, spec.Timeouts.Request)
	defer requestCancel()
	callContext, callCancel := context.WithCancel(requestContext)
	defer callCancel()

	bifrostContext := r.newSDKContext(callContext, spec, prepared.directKey)
	enableEmbeddingWireFidelity(bifrostContext)
	setTypedRequestURL(bifrostContext, prepared.typedURL)
	outcomeChannel := make(chan embeddingSDKResult, 1)
	go func() {
		response, bifrostError := r.core.EmbeddingRequest(bifrostContext, prepared.embeddingRequest)
		outcomeChannel <- embeddingSDKResult{response: response, err: bifrostError}
	}()

	var outcome embeddingSDKResult
	select {
	case outcome = <-outcomeChannel:
		if requestContext.Err() != nil {
			return unaryContextFailure(requestContext, false)
		}
	case <-callContext.Done():
		return unaryContextFailure(requestContext, false)
	}
	if outcome.err != nil {
		result := unaryErrorResult(outcome.err, bifrostContext, prepared.secrets)
		clearEmbeddingErrorRaw(outcome.err)
		return result
	}
	var materializedHeaders http.Header
	if large, _ := bifrostContext.Value(schemas.BifrostContextKeyLargeResponseMode).(bool); large {
		materializedHeaders = responseHeaders(nil, bifrostContext, false)
		body, tooLarge, materializeErr := materializeEmbeddingLargeResponse(
			bifrostContext,
			r.unaryResponseBodyLimit(spec),
		)
		if requestContext.Err() != nil {
			return unaryContextFailure(requestContext, false)
		}
		if tooLarge {
			return startedUnaryFailure(
				http.StatusOK,
				materializedHeaders,
				execution.ErrorKindInternal,
				"upstream response exceeds size limit",
			)
		}
		if materializeErr != nil {
			return startedUnaryFailure(
				http.StatusOK,
				materializedHeaders,
				execution.ErrorKindProvider,
				"read upstream Embeddings response",
			)
		}
		var headerErr error
		materializedHeaders, headerErr = normalizeMaterializedEmbeddingHeaders(
			materializedHeaders,
			len(body),
		)
		if headerErr != nil {
			return startedUnaryFailure(
				http.StatusOK,
				materializedHeaders,
				execution.ErrorKindProvider,
				"unsupported Embeddings response encoding",
			)
		}
		decoded, err := materializedEmbeddingResponse(body)
		if err != nil {
			return startedUnaryFailure(
				http.StatusOK,
				materializedHeaders,
				execution.ErrorKindProvider,
				"decode upstream Embeddings response",
			)
		}
		outcome.response = decoded
	}
	if outcome.response == nil {
		return attemptedUnaryFailure(execution.ErrorKindInternal, "execution runtime returned no Embeddings response")
	}
	body, ok := takeRawEmbeddingResponse(outcome.response)
	headers := materializedHeaders
	if headers == nil {
		headers = responseHeaders(outcome.response.ExtraFields.ProviderResponseHeaders, bifrostContext, false)
	}
	if !ok {
		return startedUnaryFailure(
			http.StatusOK,
			headers,
			execution.ErrorKindInternal,
			"execution runtime returned no raw Embeddings response",
		)
	}
	// The raw body is the response authority. Clear typed data defensively before
	// alias projection and gateway processing.
	outcome.response.Data = nil
	if spec.Operation == execution.OperationProbe && !validEmbeddingProbeResponse(body) {
		return startedUnaryFailure(
			http.StatusOK,
			headers,
			execution.ErrorKindProvider,
			"upstream returned an invalid Embeddings probe response",
		)
	}
	model := openAIResponseModel(body, "")
	if spec.Operation != execution.OperationProbe && needsClientModelAlias(spec) {
		var err error
		body, err = rewriteClientResponseModel(spec.ClientProtocol, body, spec.ClientModel)
		if err != nil {
			return startedUnaryFailure(http.StatusOK, headers, execution.ErrorKindInternal, "rewrite Embeddings response model")
		}
	}
	return execution.AttemptResult{
		DispatchState:     execution.DispatchMaybeSent,
		ResponseStarted:   true,
		StatusCode:        http.StatusOK,
		Header:            headers,
		Body:              body,
		Model:             model,
		UpstreamRequestID: upstreamRequestID(headers),
	}
}

func materializedEmbeddingResponse(body []byte) (*schemas.BifrostEmbeddingResponse, error) {
	if !json.Valid(body) {
		return nil, fmt.Errorf("invalid Embeddings response JSON")
	}
	response := &schemas.BifrostEmbeddingResponse{}
	response.ExtraFields.RawResponse = json.RawMessage(body)
	return response, nil
}

func materializeEmbeddingLargeResponse(
	ctx *schemas.BifrostContext,
	limit int64,
) (body []byte, tooLarge bool, err error) {
	if ctx == nil || limit <= 0 {
		return nil, false, fmt.Errorf("invalid Embeddings response limit")
	}
	reader, ok := ctx.Value(schemas.BifrostContextKeyLargeResponseReader).(*providerUtils.LargeResponseReader)
	if !ok || reader == nil || reader.Resp == nil {
		return nil, false, fmt.Errorf("missing Embeddings response reader")
	}
	stopCancellation := providerUtils.SetupStreamCancellation(ctx, reader.Resp.BodyStream(), nil)
	defer func() {
		stopCancellation()
		if closeErr := reader.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
		clearEmbeddingLargeResponseContext(ctx)
	}()

	body, err = io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		abortEmbeddingLargeResponse(ctx, reader, err)
		return nil, false, err
	}
	if int64(len(body)) > limit {
		abortEmbeddingLargeResponse(
			ctx,
			reader,
			fmt.Errorf("Embeddings response exceeds size limit"),
		)
		return nil, true, nil
	}
	return body, false, nil
}

func abortEmbeddingLargeResponse(
	ctx *schemas.BifrostContext,
	reader *providerUtils.LargeResponseReader,
	cause error,
) {
	if ctx == nil || reader == nil || reader.Resp == nil {
		return
	}
	if closed, _ := ctx.GetAndSetValue(
		schemas.BifrostContextKeyConnectionClosed,
		true,
	).(bool); closed {
		return
	}
	bodyStream := reader.Resp.BodyStream()
	if closer, ok := bodyStream.(interface{ CloseWithError(error) error }); ok {
		_ = closer.CloseWithError(cause)
		return
	}
	if closer, ok := bodyStream.(io.Closer); ok {
		_ = closer.Close()
	}
}

func clearEmbeddingLargeResponseContext(ctx *schemas.BifrostContext) {
	if ctx == nil {
		return
	}
	for _, key := range []schemas.BifrostContextKey{
		schemas.BifrostContextKeyLargeResponseMode,
		schemas.BifrostContextKeyLargeResponseReader,
		schemas.BifrostContextKeyLargeResponseContentLength,
		schemas.BifrostContextKeyLargeResponseContentType,
		schemas.BifrostContextKeyLargePayloadResponsePreview,
	} {
		ctx.ClearValue(key)
	}
}

func normalizeMaterializedEmbeddingHeaders(
	headers http.Header,
	bodyLength int,
) (http.Header, error) {
	result := headers.Clone()
	if result == nil {
		result = make(http.Header)
	}
	// The Bifrost decoded reader does not retain a reliable signal that the
	// upstream representation was compressed. Treat every materialized body as
	// changed so stale validators can never describe different wire bytes.
	for _, name := range []string{
		"ETag", "Digest", "Content-MD5", "Content-Range",
		"Content-Digest", "Repr-Digest", "Signature", "Signature-Input",
	} {
		result.Del(name)
	}
	encoding := strings.ToLower(strings.TrimSpace(result.Get("Content-Encoding")))
	switch encoding {
	case "", "identity":
		result.Del("Content-Encoding")
	case "gzip":
		result.Del("Content-Encoding")
	default:
		return result, fmt.Errorf("unsupported Content-Encoding %q", encoding)
	}
	result.Del("Content-Length")
	result.Set("Content-Length", strconv.Itoa(bodyLength))
	return result, nil
}

func validEmbeddingProbeResponse(body []byte) bool {
	var envelope struct {
		Data []struct {
			Embedding json.RawMessage `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Data) == 0 {
		return false
	}
	embedding := bytes.TrimSpace(envelope.Data[0].Embedding)
	return len(embedding) > 0 && !bytes.Equal(embedding, []byte("null"))
}

func takeRawEmbeddingResponse(response *schemas.BifrostEmbeddingResponse) ([]byte, bool) {
	if response == nil {
		return nil, false
	}
	raw := response.ExtraFields.RawResponse
	response.ExtraFields.RawResponse = nil
	switch value := raw.(type) {
	case json.RawMessage:
		return value, len(value) > 0
	case []byte:
		return value, len(value) > 0
	case string:
		return []byte(value), value != ""
	default:
		return nil, false
	}
}

func clearEmbeddingErrorRaw(bifrostError *schemas.BifrostError) {
	if bifrostError == nil {
		return
	}
	bifrostError.ExtraFields.RawRequest = nil
	bifrostError.ExtraFields.RawResponse = nil
}

func normalizeEmbeddingsAttemptResult(spec execution.AttemptSpec, result *execution.AttemptResult) {
	if result == nil || spec.ClientProtocol != protocol.OpenAIEmbeddings {
		return
	}
	if result.Error != nil && result.Error.ReplaySafety == "" {
		result.Error.ReplaySafety = execution.ReplaySafetyUnknown
	}
}
