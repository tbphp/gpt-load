package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/textproto"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/contentcoding"
	platformhttp "gpt-load/internal/platform/httpclient"
	platformheader "gpt-load/internal/platform/httpheader"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/state"
	"gpt-load/internal/usage"
)

type ForwardInput struct {
	Dialect         dialect.Dialect
	ObserveUsage    bool
	Group           state.GroupView
	APIKey          string
	Request         *dialect.ParsedRequest
	ExternalModel   string
	UpstreamModelID string
	OnStreamReady   func()
}

type UpstreamResult struct {
	StatusCode                int
	Header                    http.Header
	Body                      []byte
	ClassificationBody        []byte
	ErrorSummary              string
	Err                       error
	RequestWritten            bool
	Committed                 bool
	RetryableBeforeCommit     bool
	ProviderErrorBeforeCommit bool
	Stream                    StreamObservation
	Usage                     usage.Result
}

func (result UpstreamResult) HasResponse() bool {
	return result.StatusCode != 0 && result.Err == nil
}

type Forwarder struct {
	clients            *platformhttp.HTTPClientManager
	redactor           *redact.Redactor
	streamWriteTimeout time.Duration
	usageCapture       *usageCaptureBoundary
}

const (
	maxNonStreamingResponseBodyBytes = int64(32 << 20)
	maxErrorResponseBodyBytes        = int64(64 << 10)
	maxDecompressedErrorBodyBytes    = int64(1 << 20)
	maxStreamingErrorBodyBytes       = int(maxErrorResponseBodyBytes)
)

var ErrUpstreamProtocol = errors.New("upstream protocol error")

func NewForwarder(clients *platformhttp.HTTPClientManager, redactor *redact.Redactor) *Forwarder {
	return &Forwarder{
		clients: clients, redactor: redactor,
		streamWriteTimeout: downstreamWriteTimeout,
		usageCapture:       newUsageCaptureBoundary(),
	}
}

func (forwarder *Forwarder) Forward(ctx context.Context, input ForwardInput) UpstreamResult {
	if forwarder == nil || forwarder.clients == nil || forwarder.redactor == nil || input.Dialect == nil || input.Request == nil {
		return UpstreamResult{Err: fmt.Errorf("forward input is incomplete")}
	}
	request, wroteRequest, _, err := forwarder.newUpstreamRequest(ctx, input, false)
	if err != nil {
		return UpstreamResult{Err: err}
	}
	knownSecrets := resolvedCredentialSecretValues(input, request.Header)
	summarySecrets := resolvedErrorSummarySecretValues(
		input.APIKey,
		input.Group.HeaderRules,
		knownSecrets...,
	)
	response, err := forwarder.clients.GetClient(nonStreamingClientConfig(input.Group.Timeouts)).Do(request)
	if err != nil {
		return UpstreamResult{Err: fmt.Errorf("perform upstream request: %w", err), RequestWritten: wroteRequest.Load()}
	}
	defer response.Body.Close()

	representationHeaders := response.Header.Clone()
	headers := cloneEndToEndHeaders(response.Header)
	policy := classifyResponseBody(input.Request.Method, response.StatusCode)
	if !policy.readBody {
		encoding, parseErr := contentcoding.ParseContentEncoding(
			headerFieldValues(representationHeaders, "Content-Encoding"),
		)
		if parseErr != nil || encoding != contentcoding.Identity {
			return UpstreamResult{
				Err:            fmt.Errorf("%w: bodyless response has non-identity Content-Encoding", ErrUpstreamProtocol),
				RequestWritten: true,
			}
		}
		safeHeaders := sanitizeForwardResponseHeaders(representationHeaders, input, knownSecrets...)
		safeHeaders, _ = normalizeBufferedResponse(
			input.Request.Method,
			response.StatusCode,
			safeHeaders,
			nil,
		)
		return UpstreamResult{
			StatusCode:     response.StatusCode,
			Header:         safeHeaders,
			RequestWritten: true,
			Usage:          usage.Result{State: usage.StateNotApplicable},
		}
	}

	success := response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
	limit := maxErrorResponseBodyBytes
	if success {
		limit = maxNonStreamingResponseBodyBytes
	}
	overflow := response.ContentLength > limit
	var body []byte
	if !overflow {
		body, overflow, err = readBodyAtMost(response.Body, limit)
	}
	if err != nil {
		return UpstreamResult{Err: fmt.Errorf("read upstream response: %w", err), RequestWritten: true}
	}
	if overflow && success {
		return UpstreamResult{
			Err:            fmt.Errorf("%w: non-streaming response body exceeds limit", ErrUpstreamProtocol),
			RequestWritten: true,
		}
	}

	result := UpstreamResult{
		StatusCode:     response.StatusCode,
		Body:           body,
		RequestWritten: true,
		Usage:          usage.Result{State: usage.StateNotApplicable},
	}
	if success {
		prepared, prepareErr := forwarder.prepareSuccessRepresentation(
			input,
			response.StatusCode,
			representationHeaders,
			body,
			knownSecrets,
		)
		if prepareErr != nil {
			return UpstreamResult{Err: prepareErr, RequestWritten: true}
		}
		result.Body = prepared.downstream
		result.Header = prepared.headers
		if input.ObserveUsage && forwarder.usageCapture != nil {
			result.Usage = forwarder.usageCapture.extractNonStreamingPlain(
				input.Dialect,
				prepared.inspectable,
			)
		}
		return result
	}
	if !success {
		prepared := forwarder.prepareErrorRepresentation(
			input,
			representationHeaders,
			body,
			knownSecrets,
		)
		if overflow {
			prepared = forwarder.failClosedErrorRepresentation(input, headers, knownSecrets)
		}
		result.Body = prepared.downstream
		result.ClassificationBody = prepared.classification
		result.ErrorSummary = summarizeErrorBody(
			forwarder.redactor, prepared.classification, "", summarySecrets...,
		)
		result.Header = prepared.headers
		return result
	}
	return result
}

func (forwarder *Forwarder) ForwardStream(
	ctx context.Context,
	input ForwardInput,
	downstream http.ResponseWriter,
) (result UpstreamResult) {
	if forwarder == nil || forwarder.clients == nil || forwarder.redactor == nil ||
		input.Dialect == nil || input.Request == nil || downstream == nil {
		return UpstreamResult{Err: fmt.Errorf("stream forward input is incomplete")}
	}

	deadline := newFirstEventDeadline(ctx, input.Group.Timeouts.FirstByte)
	defer deadline.stop()
	request, wroteRequest, replay, err := forwarder.newUpstreamRequest(deadline.ctx, input, true)
	if err != nil {
		return UpstreamResult{Err: err}
	}
	knownSecrets := resolvedCredentialSecretValues(input, request.Header)
	summarySecrets := resolvedErrorSummarySecretValues(
		input.APIKey,
		input.Group.HeaderRules,
		knownSecrets...,
	)
	response, err := forwarder.clients.GetClient(streamingClientConfig(input.Group.Timeouts)).Do(request)
	if err != nil {
		requestWritten := wroteRequest.Load()
		return UpstreamResult{
			Err:            streamAttemptError(ctx, deadline.ctx, fmt.Errorf("perform upstream stream request: %w", err)),
			RequestWritten: requestWritten, RetryableBeforeCommit: retryableBeforeCommit(ctx, requestWritten),
		}
	}
	streamBody := response.Body
	defer func() { _ = streamBody.Close() }()

	representationHeaders := response.Header.Clone()
	headers := cloneEndToEndHeaders(response.Header)
	result = UpstreamResult{
		StatusCode:     response.StatusCode,
		RequestWritten: true,
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, overflow, readErr := readStreamingErrorBody(streamBody)
		if readErr != nil {
			result.Err = streamAttemptError(ctx, deadline.ctx, fmt.Errorf("read upstream stream error response: %w", readErr))
			result.RetryableBeforeCommit = retryableBeforeCommit(ctx, result.RequestWritten)
			return result
		}
		prepared := forwarder.prepareErrorRepresentation(input, representationHeaders, body, knownSecrets)
		if overflow {
			prepared = forwarder.failClosedErrorRepresentation(input, headers, knownSecrets)
		}
		result.Body = prepared.downstream
		result.ClassificationBody = prepared.classification
		result.ErrorSummary = summarizeErrorBody(
			forwarder.redactor,
			result.ClassificationBody,
			"",
			summarySecrets...,
		)
		result.Header = prepared.headers
		return result
	}
	streamEvents := newStreamEventObserver(
		input.Dialect,
		forwarder.usageCapture.newStreamForRequest(
			input.Dialect,
			input.ObserveUsage,
		),
	)
	defer func() { result.Usage = streamEvents.finalizeUsage() }()

	if err := validateStreamContentEncoding(representationHeaders); err != nil {
		result.Err = err
		result.RetryableBeforeCommit = retryableBeforeCommit(ctx, result.RequestWritten)
		return result
	}

	rewriteModel := needsModelRewrite(input)
	var rewriter dialect.ModelRewriter
	if rewriteModel {
		var ok bool
		rewriter, ok = input.Dialect.(dialect.ModelRewriter)
		if !ok {
			result.Err = fmt.Errorf("%w: dialect does not support model rewrite", ErrUpstreamProtocol)
			result.RetryableBeforeCommit = retryableBeforeCommit(ctx, result.RequestWritten)
			return result
		}
	}
	firstPayloadPending := true
	streamBody = newSSEEventRewriteStream(streamBody, func(
		event dialect.StreamEvent,
		errorEvent bool,
	) ([]byte, error) {
		firstPayload := firstPayloadPending
		firstPayloadPending = false
		safePayload := event.Payload
		for _, secret := range knownSecrets {
			var ok bool
			safePayload, ok = rewriteBoundedLiteral(
				safePayload,
				secret,
				redact.Placeholder,
				int64(maxSSEEventBytes),
			)
			if !ok {
				return nil, fmt.Errorf("%w: redact upstream SSE credential", ErrUpstreamProtocol)
			}
		}
		safeEvent := dialect.StreamEvent{
			Name:    event.Name,
			Payload: safePayload,
		}
		providerError, err := streamEvents.classify(
			safeEvent,
			errorEvent,
		)
		if err != nil {
			return nil, err
		}
		if !firstPayload {
			streamEvents.observeUsageEvent(safeEvent)
		}
		if providerError {
			observationPayload := forwarder.redactor.Bytes(safePayload)
			streamEvents.observeError(
				summarizeErrorBody(
					forwarder.redactor,
					observationPayload,
					fixedErrorSummary("upstream_sse_error"),
					summarySecrets...,
				),
			)
		}
		if !rewriteModel {
			return safePayload, nil
		}
		if providerError {
			var ok bool
			safePayload, ok = rewriteBoundedLiteral(
				safePayload,
				input.UpstreamModelID,
				input.ExternalModel,
				int64(maxSSEEventBytes),
			)
			if !ok {
				return nil, fmt.Errorf("%w: rewrite upstream SSE model literal", ErrUpstreamProtocol)
			}
			return safePayload, nil
		}
		rewritten, err := rewriter.RewriteResponseModel(safePayload, input.ExternalModel)
		if err != nil {
			return nil, fmt.Errorf("%w: rewrite upstream response model: %v", ErrUpstreamProtocol, err)
		}
		return rewritten, nil
	})
	invalidateRewrittenBodyHeaders(headers)

	firstEvent, err := bufferFirstSSEEvent(streamBody)
	if err != nil {
		if !rewriteModel && errors.Is(err, errSSEEventTooLarge) {
			err = errFirstSSEEventTooLarge
		}
		if errors.Is(err, errFirstSSEEventTooLarge) || errors.Is(err, errSSEEventTooLarge) {
			err = fmt.Errorf("%w: %w", ErrUpstreamProtocol, err)
		}
		result.Err = streamAttemptError(ctx, deadline.ctx, err)
		result.RetryableBeforeCommit = retryableBeforeCommit(ctx, result.RequestWritten)
		return result
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		result.Err = ctxErr
		return result
	}

	if firstEvent.IsProviderError || streamEvents.firstProviderError {
		streamEvents.observeUsageEvent(dialect.StreamEvent{
			Name:    firstEvent.Name,
			Payload: firstEvent.Payload,
		})
		result.ClassificationBody = forwarder.safeProviderErrorPayload(
			firstEvent.Payload,
			knownSecrets,
		)
		result.ErrorSummary = fixedErrorSummary("upstream_sse_error")
		result.Header = providerErrorRateLimitHeaders(
			sanitizeForwardResponseHeaders(headers, input, knownSecrets...),
		)
		result.ProviderErrorBeforeCommit = true
		result.Usage = usage.Result{State: usage.StateMissing}
		return result
	}
	if !deadline.disarm() {
		result.Err = streamAttemptError(ctx, deadline.ctx, context.DeadlineExceeded)
		result.RetryableBeforeCommit = retryableBeforeCommit(ctx, result.RequestWritten)
		return result
	}
	streamEvents.observeUsageEvent(dialect.StreamEvent{
		Name:    firstEvent.Name,
		Payload: firstEvent.Payload,
	})

	if input.OnStreamReady != nil {
		input.OnStreamReady()
	}

	result.Header = normalizeStreamResponseHeaders(
		sanitizeForwardResponseHeaders(headers, input, knownSecrets...),
	)
	streamWriter := newStreamWriteController(downstream, forwarder.streamWriteTimeout)
	defer func() { _ = streamWriter.clear() }()

	result.Committed = true
	releaseCommittedRequestReplay(input.Request, replay)
	if err := commitStream(streamWriter, response.StatusCode, result.Header, firstEvent.Prefix); err != nil {
		result.Err = err
		result.Stream = observeStreamTermination(ctx, err, streamEvents)
		return result
	}
	if err := pumpStream(deadline.ctx, streamBody, streamWriter, input.Group.Timeouts.StreamIdle); err != nil {
		result.Err = err
	}
	if result.Err == nil {
		result.Err = streamEvents.validateEOF()
	}
	result.Stream = observeStreamTermination(ctx, result.Err, streamEvents)
	return result
}

func retryableBeforeCommit(parent context.Context, requestWritten bool) bool {
	return !requestWritten && parent != nil && parent.Err() == nil
}

func providerErrorRateLimitHeaders(source http.Header) http.Header {
	filtered := make(http.Header)
	for name, values := range source {
		lowered := strings.ToLower(name)
		// Keep this allowlist synchronized with health.ParseRateLimitReset.
		if lowered != "retry-after" &&
			!(strings.HasPrefix(lowered, "anthropic-ratelimit-") &&
				strings.HasSuffix(lowered, "-reset")) &&
			lowered != "x-ratelimit-reset" &&
			!strings.HasPrefix(lowered, "x-ratelimit-reset-") {
			continue
		}
		filtered[name] = append([]string(nil), values...)
	}
	return filtered
}

func (forwarder *Forwarder) safeProviderErrorPayload(
	payload []byte,
	knownSecrets []string,
) []byte {
	safe := bytes.Clone(payload)
	for _, secret := range knownSecrets {
		var ok bool
		safe, ok = rewriteBoundedLiteral(
			safe,
			secret,
			redact.Placeholder,
			int64(maxFirstSSEEventBytes),
		)
		if !ok {
			return []byte(redact.Placeholder)
		}
	}
	safe = forwarder.redactor.Bytes(safe)
	if len(safe) > maxFirstSSEEventBytes {
		return []byte(redact.Placeholder)
	}
	return safe
}

func releaseCommittedRequestReplay(parsed *dialect.ParsedRequest, replay *requestReplay) {
	if parsed != nil {
		parsed.Body = nil
	}
	if replay != nil {
		replay.release()
	}
}

func (forwarder *Forwarder) newUpstreamRequest(
	ctx context.Context,
	input ForwardInput,
	stream bool,
) (*http.Request, *atomic.Bool, *requestReplay, error) {
	parsed := input.Request
	rewrite := needsModelRewrite(input)
	if rewrite {
		rewriter, ok := input.Dialect.(dialect.ModelRewriter)
		if !ok {
			return nil, nil, nil, fmt.Errorf("dialect does not support model rewriting")
		}
		derived, err := rewriter.RewriteRequestModel(input.Request, input.UpstreamModelID)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("rewrite upstream request model: %w", err)
		}
		if derived == nil {
			return nil, nil, nil, fmt.Errorf("rewrite upstream request model returned nil request")
		}
		if int64(len(derived.Body)) > maxRequestBodyBytes {
			return nil, nil, nil, fmt.Errorf("%w: rewritten request body exceeds limit", errRequestTooLarge)
		}
		parsed = derived
	}
	if stream && input.Group.InjectUsageOptions && forwarder != nil && forwarder.usageCapture != nil {
		parsed = forwarder.usageCapture.injectStreamUsage(input.Dialect, parsed)
	}
	upstreamURL, err := input.Dialect.BuildUpstreamURL(input.Group.UpstreamURL, parsed)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("build upstream URL: %w", err)
	}
	replay := newRequestReplay(parsed.Body)
	request, err := http.NewRequestWithContext(ctx, parsed.Method, upstreamURL, nil)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create upstream request: %w", err)
	}
	request.Body = replay.open()
	request.ContentLength = int64(len(parsed.Body))
	request.GetBody = nil
	request.Header = cloneEndToEndHeaders(parsed.Header)
	removeDownstreamCredentials(request.Header)
	dialect.ApplyCredential(input.Dialect, request.Header, input.APIKey, input.Group.HeaderRules)
	sanitizeUpstreamRequestHeaders(request.Header)
	platformheader.NormalizeUpstreamRequestRepresentation(request, int64(len(parsed.Body)))
	if _, exists := request.Header["User-Agent"]; !exists {
		request.Header["User-Agent"] = nil
	}

	wroteRequest := &atomic.Bool{}
	trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { wroteRequest.Store(true) }}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
	return request, wroteRequest, replay, nil
}

func needsModelRewrite(input ForwardInput) bool {
	return input.ExternalModel != "" &&
		input.UpstreamModelID != "" &&
		input.ExternalModel != input.UpstreamModelID
}

func isResolvedCredentialHeaderName(selected dialect.Dialect, name string) bool {
	if platformheader.IsCredentialName(name) {
		return true
	}
	namer, ok := selected.(dialect.CredentialHeaderNamer)
	if !ok {
		return false
	}
	identity := canonicalHeaderIdentity(name)
	if identity == "" {
		return false
	}
	for _, dialectName := range namer.CredentialHeaderNames() {
		if identity == canonicalHeaderIdentity(dialectName) {
			return true
		}
	}
	return false
}

func resolvedCredentialSecretValues(input ForwardInput, finalHeaders http.Header) []string {
	if finalHeaders == nil {
		finalHeaders = make(http.Header)
		if input.Request != nil {
			finalHeaders = cloneEndToEndHeaders(input.Request.Header)
		}
		removeDownstreamCredentials(finalHeaders)
		dialect.ApplyCredential(
			input.Dialect,
			finalHeaders,
			input.APIKey,
			input.Group.HeaderRules,
		)
		sanitizeUpstreamRequestHeaders(finalHeaders)
	}

	secrets := make([]string, 0, 8)
	seen := make(map[string]struct{})
	appendSecret := func(value string) {
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		secrets = append(secrets, value)
	}
	appendSecret(input.APIKey)
	for name, values := range finalHeaders {
		if !isResolvedCredentialHeaderName(input.Dialect, name) {
			continue
		}
		for _, value := range values {
			appendSecret(value)
		}
	}
	sort.SliceStable(secrets, func(left, right int) bool {
		return len(secrets[left]) > len(secrets[right])
	})
	return secrets
}

func rewriteBoundedLiteral(body []byte, literal, replacement string, limit int64) ([]byte, bool) {
	if literal == "" {
		return body, int64(len(body)) <= limit
	}
	if !json.Valid(body) {
		return replaceAllBounded(
			body, []byte(literal), []byte(replacement), limit,
		)
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	budget := int64(0)
	rewritten, changed, ok := rewriteJSONLiteralStrings(value, literal, replacement, limit, &budget)
	if !ok {
		return nil, false
	}
	if !changed {
		return body, true
	}
	encoded, err := json.Marshal(rewritten)
	if err != nil || int64(len(encoded)) > limit {
		return nil, false
	}
	return encoded, true
}

func rewriteJSONLiteralStrings(
	value any,
	literal string,
	replacement string,
	limit int64,
	budget *int64,
) (any, bool, bool) {
	switch typed := value.(type) {
	case string:
		rewritten, changed, ok := replaceStringBounded(
			typed, literal, replacement, limit, budget,
		)
		return rewritten, changed, ok
	case []any:
		changed := false
		for index, item := range typed {
			rewritten, itemChanged, ok := rewriteJSONLiteralStrings(
				item, literal, replacement, limit, budget,
			)
			if !ok {
				return nil, false, false
			}
			typed[index] = rewritten
			changed = changed || itemChanged
		}
		return typed, changed, true
	case map[string]any:
		rewrittenObject := make(map[string]any, len(typed))
		changed := false
		for key, item := range typed {
			rewrittenKey, keyChanged, ok := replaceStringBounded(
				key, literal, replacement, limit, budget,
			)
			if !ok {
				return nil, false, false
			}
			if _, exists := rewrittenObject[rewrittenKey]; exists {
				return nil, false, false
			}
			rewrittenItem, itemChanged, ok := rewriteJSONLiteralStrings(
				item, literal, replacement, limit, budget,
			)
			if !ok {
				return nil, false, false
			}
			rewrittenObject[rewrittenKey] = rewrittenItem
			changed = changed || keyChanged || itemChanged
		}
		return rewrittenObject, changed, true
	default:
		return value, false, true
	}
}

func replaceStringBounded(
	source string,
	old string,
	replacement string,
	limit int64,
	budget *int64,
) (string, bool, bool) {
	if old == "" {
		return source, false, true
	}
	count := strings.Count(source, old)
	if count == 0 {
		return source, false, true
	}
	resultLength, ok := replacementResultLength(
		int64(len(source)), int64(len(old)), int64(len(replacement)), int64(count), limit,
	)
	if !ok || budget == nil || *budget > limit-resultLength {
		return "", false, false
	}
	*budget += resultLength
	return strings.ReplaceAll(source, old, replacement), true, true
}

func replaceAllBounded(source, old, replacement []byte, limit int64) ([]byte, bool) {
	if len(old) == 0 {
		return source, int64(len(source)) <= limit
	}
	count := bytes.Count(source, old)
	if count == 0 {
		return source, int64(len(source)) <= limit
	}
	if _, ok := replacementResultLength(
		int64(len(source)), int64(len(old)), int64(len(replacement)), int64(count), limit,
	); !ok {
		return nil, false
	}
	return bytes.ReplaceAll(source, old, replacement), true
}

func replacementResultLength(sourceLength, oldLength, replacementLength, count, limit int64) (int64, bool) {
	if sourceLength < 0 || oldLength <= 0 || replacementLength < 0 || count < 0 || limit < 0 || sourceLength > limit {
		return 0, false
	}
	if replacementLength <= oldLength {
		return sourceLength - count*(oldLength-replacementLength), true
	}
	delta := replacementLength - oldLength
	if count > (limit-sourceLength)/delta {
		return 0, false
	}
	return sourceLength + count*delta, true
}

var representationMetadataHeaderNames = [...]string{
	"Content-Encoding",
	"Content-Length",
	"ETag",
	"Digest",
	"Content-MD5",
	"Content-Range",
	"Content-Digest",
	"Repr-Digest",
	"Signature",
	"Signature-Input",
}

func stripDownstreamResponseSignatures(headers http.Header) {
	deleteHeaderField(headers, "Signature")
	deleteHeaderField(headers, "Signature-Input")
}

func invalidateRewrittenBodyHeaders(headers http.Header) {
	for _, name := range representationMetadataHeaderNames {
		if strings.EqualFold(name, "Content-Encoding") {
			continue
		}
		deleteHeaderField(headers, name)
	}
}

func nonStreamingClientConfig(timeouts state.TimeoutConfig) *platformhttp.Config {
	return &platformhttp.Config{
		ConnectTimeout:        timeouts.Connect,
		RequestTimeout:        timeouts.Request,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		ResponseHeaderTimeout: timeouts.FirstByte,
		DisableCompression:    true,
		WriteBufferSize:       32 * 1024,
		ReadBufferSize:        32 * 1024,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   timeouts.Connect,
		ExpectContinueTimeout: time.Second,
		DisableRedirects:      true,
	}
}

func streamingClientConfig(timeouts state.TimeoutConfig) *platformhttp.Config {
	return &platformhttp.Config{
		ConnectTimeout:        timeouts.Connect,
		RequestTimeout:        0,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		ResponseHeaderTimeout: timeouts.FirstByte,
		DisableCompression:    true,
		WriteBufferSize:       32 * 1024,
		ReadBufferSize:        32 * 1024,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   timeouts.Connect,
		ExpectContinueTimeout: time.Second,
		DisableRedirects:      true,
	}
}

func validateStreamContentEncoding(headers http.Header) error {
	encoding, err := contentcoding.ParseContentEncoding(
		headerFieldValues(headers, "Content-Encoding"),
	)
	if err != nil || encoding != contentcoding.Identity {
		return fmt.Errorf("%w: non-identity stream Content-Encoding", ErrUpstreamProtocol)
	}
	return nil
}

func readStreamingErrorBody(body io.Reader) ([]byte, bool, error) {
	return readBodyAtMost(body, maxErrorResponseBodyBytes)
}

func readBodyAtMost(reader io.Reader, limit int64) ([]byte, bool, error) {
	if reader == nil || limit < 0 {
		return nil, false, fmt.Errorf("response reader/limit is invalid")
	}
	var limited io.Reader = reader
	if limit < math.MaxInt64 {
		limited = io.LimitReader(reader, limit+1)
	}
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) > limit {
		return nil, true, nil
	}
	return body, false, nil
}

func streamAttemptError(parent, attempt context.Context, fallback error) error {
	if err := parent.Err(); err != nil {
		return err
	}
	if cause := context.Cause(attempt); cause != nil {
		return cause
	}
	return fallback
}

type firstEventDeadline struct {
	ctx      context.Context
	cancel   context.CancelCauseFunc
	mu       sync.Mutex
	timer    *time.Timer
	disarmed bool
	expired  bool
}

func newFirstEventDeadline(parent context.Context, timeout time.Duration) *firstEventDeadline {
	ctx, cancel := context.WithCancelCause(parent)
	deadline := &firstEventDeadline{ctx: ctx, cancel: cancel}
	deadline.timer = time.AfterFunc(timeout, deadline.expire)
	return deadline
}

func (deadline *firstEventDeadline) expire() {
	deadline.mu.Lock()
	defer deadline.mu.Unlock()
	if deadline.disarmed {
		return
	}
	deadline.expired = true
	deadline.cancel(context.DeadlineExceeded)
}

func (deadline *firstEventDeadline) disarm() bool {
	deadline.mu.Lock()
	defer deadline.mu.Unlock()
	if deadline.expired {
		return false
	}
	deadline.disarmed = true
	if deadline.timer != nil {
		deadline.timer.Stop()
	}
	return true
}

func (deadline *firstEventDeadline) stop() {
	deadline.mu.Lock()
	defer deadline.mu.Unlock()
	deadline.disarmed = true
	if deadline.timer != nil {
		deadline.timer.Stop()
	}
	deadline.cancel(context.Canceled)
}

var hopByHopHeaders = map[string]struct{}{
	"Connection": {}, "Proxy-Connection": {}, "Keep-Alive": {},
	"Proxy-Authenticate": {}, "Proxy-Authorization": {}, "Te": {},
	"Trailer": {}, "Transfer-Encoding": {}, "Upgrade": {},
}

func cloneEndToEndHeaders(source http.Header) http.Header {
	cloned := source.Clone()
	if cloned == nil {
		cloned = make(http.Header)
	}
	for name, values := range source {
		if !strings.EqualFold(name, "Connection") {
			continue
		}
		for _, value := range values {
			for _, token := range strings.Split(value, ",") {
				if tokenName := strings.TrimSpace(token); tokenName != "" {
					deleteHeaderField(cloned, tokenName)
				}
			}
		}
	}
	for name := range cloned {
		if isHopByHopHeader(name) || isDebugHeader(name) {
			delete(cloned, name)
		}
	}
	return cloned
}

func isHopByHopHeader(name string) bool {
	for hopName := range hopByHopHeaders {
		if strings.EqualFold(name, hopName) {
			return true
		}
	}
	return false
}

func sanitizeUpstreamRequestHeaders(headers http.Header) {
	if headers == nil {
		return
	}
	for name, values := range headers {
		if !strings.EqualFold(name, "Connection") {
			continue
		}
		for _, value := range values {
			for _, token := range strings.Split(value, ",") {
				if tokenName := strings.TrimSpace(token); tokenName != "" {
					deleteHeaderField(headers, tokenName)
				}
			}
		}
	}
	for name := range headers {
		if isHopByHopHeader(name) ||
			platformheader.IsForbiddenRequestRuleName(name) ||
			isDebugHeader(name) {
			delete(headers, name)
		}
	}
}

func headerValuesContainLiteral(values []string, literal string) bool {
	if literal == "" {
		return false
	}
	for _, value := range values {
		if strings.Contains(value, literal) {
			return true
		}
	}
	return false
}

func sanitizeUpstreamResponseHeaders(source http.Header, apiKey string) http.Header {
	headers := cloneEndToEndHeaders(source)
	namesToDelete := make([]string, 0)
	for actualName, values := range headers {
		if platformheader.IsCredentialName(actualName) ||
			strings.EqualFold(actualName, "Set-Cookie") ||
			strings.EqualFold(actualName, "Set-Cookie2") ||
			headerValuesContainLiteral(values, apiKey) {
			namesToDelete = append(namesToDelete, actualName)
		}
	}
	for _, name := range namesToDelete {
		deleteHeaderField(headers, name)
	}
	return headers
}

func sanitizeForwardResponseHeaders(
	source http.Header,
	input ForwardInput,
	additionalSecrets ...string,
) http.Header {
	headers := cloneEndToEndHeaders(source)
	stripDownstreamResponseSignatures(headers)
	deleted := endToEndHeaderCleanupDeletedField(source, headers)
	namesToDelete := make([]string, 0)
	for actualName, values := range headers {
		deleteField := isResolvedCredentialHeaderName(input.Dialect, actualName) ||
			strings.EqualFold(actualName, "Set-Cookie") ||
			strings.EqualFold(actualName, "Set-Cookie2") ||
			headerValuesContainLiteral(values, input.APIKey)
		for _, secret := range additionalSecrets {
			if deleteField || secret == "" || secret == input.APIKey {
				continue
			}
			if headerValuesContainLiteral(values, secret) {
				deleteField = true
				break
			}
		}
		if deleteField {
			namesToDelete = append(namesToDelete, actualName)
		}
	}
	deleted = deleted || len(namesToDelete) > 0
	for _, name := range namesToDelete {
		deleteHeaderField(headers, name)
	}
	if !needsModelRewrite(input) {
		if deleted {
			deleteHeaderField(headers, "Signature")
			deleteHeaderField(headers, "Signature-Input")
		}
		return headers
	}
	for name, values := range headers {
		nameContainsModel := headerNameContainsLiteral(name, input.UpstreamModelID)
		valuesContainModel := headerValuesContainLiteral(values, input.UpstreamModelID)
		if !nameContainsModel && !valuesContainModel {
			continue
		}
		if isRequiredRepresentationFramingHeader(name) {
			continue
		}
		if strings.EqualFold(name, "Content-Type") {
			if valuesContainModel && contentTypeContainsDisallowedModel(values, input.UpstreamModelID) {
				deleteHeaderField(headers, name)
				deleted = true
			}
			continue
		}
		if nameContainsModel {
			deleteHeaderField(headers, name)
			deleted = true
			continue
		}
		deleteHeaderField(headers, name)
		deleted = true
	}
	if deleted {
		deleteHeaderField(headers, "Signature")
		deleteHeaderField(headers, "Signature-Input")
	}
	return headers
}

func endToEndHeaderCleanupDeletedField(source, headers http.Header) bool {
	for sourceName := range source {
		found := false
		for headerName := range headers {
			if strings.EqualFold(sourceName, headerName) {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}
	return false
}

func headerNameContainsLiteral(name, literal string) bool {
	return literal != "" && strings.Contains(strings.ToLower(name), strings.ToLower(literal))
}

func isRepresentationMetadataHeader(name string) bool {
	for _, metadataName := range representationMetadataHeaderNames {
		if strings.EqualFold(name, metadataName) {
			return true
		}
	}
	return false
}

func isRequiredRepresentationFramingHeader(name string) bool {
	return strings.EqualFold(name, "Content-Encoding") ||
		strings.EqualFold(name, "Content-Length")
}

func contentTypeContainsDisallowedModel(values []string, upstreamModel string) bool {
	allowedMediaTypes := map[string]struct{}{
		"application/json":         {},
		"application/problem+json": {},
		"application/x-ndjson":     {},
		"text/event-stream":        {},
		"text/plain":               {},
	}
	for _, value := range values {
		if !strings.Contains(value, upstreamModel) {
			continue
		}
		mediaType, _, err := mime.ParseMediaType(value)
		if err != nil {
			return true
		}
		if _, allowed := allowedMediaTypes[strings.ToLower(mediaType)]; !allowed {
			return true
		}

		mediaEnd := strings.IndexByte(value, ';')
		if mediaEnd < 0 {
			mediaEnd = len(value)
		}
		rawMediaType := value[:mediaEnd]
		trimmedMediaType := strings.TrimSpace(rawMediaType)
		mediaStart := strings.Index(rawMediaType, trimmedMediaType)
		mediaEnd = mediaStart + len(trimmedMediaType)
		for searchFrom := 0; searchFrom <= len(value)-len(upstreamModel); {
			index := strings.Index(value[searchFrom:], upstreamModel)
			if index < 0 {
				break
			}
			index += searchFrom
			if index < mediaStart || index+len(upstreamModel) > mediaEnd {
				return true
			}
			searchFrom = index + len(upstreamModel)
		}
	}
	return false
}

func deleteHeaderField(headers http.Header, name string) {
	for actualName := range headers {
		if strings.EqualFold(actualName, name) {
			delete(headers, actualName)
		}
	}
}

func canonicalHeaderIdentity(name string) string {
	if name == "" {
		return ""
	}
	return strings.ToLower(textproto.CanonicalMIMEHeaderKey(name))
}

func isDebugHeader(name string) bool {
	for _, reserved := range debugHeaderNames {
		if strings.EqualFold(name, reserved) {
			return true
		}
	}
	return false
}

func removeDownstreamCredentials(headers http.Header) {
	for name := range headers {
		if platformheader.IsCredentialName(name) {
			delete(headers, name)
		}
	}
}

func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}
