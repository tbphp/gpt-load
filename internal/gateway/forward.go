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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gpt-load/internal/dialect"
	platformhttp "gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/state"
)

type ForwardInput struct {
	Dialect         dialect.Dialect
	Group           state.GroupView
	APIKey          string
	Request         *dialect.ParsedRequest
	ExternalModel   string
	UpstreamModelID string
	OnStreamReady   func()
}

type UpstreamResult struct {
	StatusCode            int
	Header                http.Header
	Body                  []byte
	ClassificationBody    []byte
	Err                   error
	RequestWritten        bool
	Committed             bool
	RetryableBeforeCommit bool
}

func (result UpstreamResult) HasResponse() bool {
	return result.StatusCode != 0 && result.Err == nil
}

type Forwarder struct {
	clients            *platformhttp.HTTPClientManager
	redactor           *redact.Redactor
	streamWriteTimeout time.Duration
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
	}
}

func (forwarder *Forwarder) Forward(ctx context.Context, input ForwardInput) UpstreamResult {
	if forwarder == nil || forwarder.clients == nil || forwarder.redactor == nil || input.Dialect == nil || input.Request == nil {
		return UpstreamResult{Err: fmt.Errorf("forward input is incomplete")}
	}
	request, wroteRequest, _, err := newUpstreamRequest(ctx, input, false)
	if err != nil {
		return UpstreamResult{Err: err}
	}
	response, err := forwarder.clients.GetClient(nonStreamingClientConfig(input.Group.Timeouts)).Do(request)
	if err != nil {
		return UpstreamResult{Err: fmt.Errorf("perform upstream request: %w", err), RequestWritten: wroteRequest.Load()}
	}
	defer response.Body.Close()

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

	headers := cloneEndToEndHeaders(response.Header)
	if success && needsModelRewrite(input) {
		if !inspectableStreamEncoding(response.Header) {
			return UpstreamResult{
				Err: fmt.Errorf(
					"%w: Content-Encoding %q",
					ErrUpstreamProtocol,
					response.Header.Values("Content-Encoding"),
				),
				RequestWritten: true,
			}
		}
		rewriter, ok := input.Dialect.(dialect.ModelRewriter)
		if !ok {
			return UpstreamResult{
				Err:            fmt.Errorf("%w: dialect does not support model rewrite", ErrUpstreamProtocol),
				RequestWritten: true,
			}
		}
		body, err = rewriter.RewriteResponseModel(body, input.ExternalModel)
		if err != nil {
			return UpstreamResult{
				Err:            fmt.Errorf("%w: rewrite upstream response model: %v", ErrUpstreamProtocol, err),
				RequestWritten: true,
			}
		}
		if int64(len(body)) > maxNonStreamingResponseBodyBytes {
			return UpstreamResult{
				Err:            fmt.Errorf("%w: rewritten non-streaming response body exceeds limit", ErrUpstreamProtocol),
				RequestWritten: true,
			}
		}
		updateRewrittenBodyHeaders(headers, len(body))
	}
	result := UpstreamResult{
		StatusCode:     response.StatusCode,
		Body:           body,
		RequestWritten: true,
	}
	if !success {
		var safeWire, safePlain []byte
		if overflow {
			safeWire, safePlain = failClosedErrorBody(headers)
		} else {
			safeWire, safePlain = forwarder.prepareErrorBody(headers, body, input)
		}
		if nonIdentityEncodingContainsKey(headers, input.APIKey) {
			safeWire, safePlain = failClosedErrorBody(headers)
		}
		result.Body = safeWire
		result.ClassificationBody = safePlain
	} else if nonIdentityEncodingContainsKey(headers, input.APIKey) {
		return UpstreamResult{
			Err:            fmt.Errorf("%w: credential collision in Content-Encoding", ErrUpstreamProtocol),
			RequestWritten: true,
		}
	}
	result.Header = sanitizeForwardResponseHeaders(headers, input)
	return result
}

func (forwarder *Forwarder) ForwardStream(
	ctx context.Context,
	input ForwardInput,
	downstream http.ResponseWriter,
) UpstreamResult {
	if forwarder == nil || forwarder.clients == nil || forwarder.redactor == nil ||
		input.Dialect == nil || input.Request == nil || downstream == nil {
		return UpstreamResult{Err: fmt.Errorf("stream forward input is incomplete")}
	}

	deadline := newFirstEventDeadline(ctx, input.Group.Timeouts.FirstByte)
	defer deadline.stop()
	request, wroteRequest, replay, err := newUpstreamRequest(deadline.ctx, input, true)
	if err != nil {
		return UpstreamResult{Err: err}
	}
	response, err := forwarder.clients.GetClient(streamingClientConfig(input.Group.Timeouts)).Do(request)
	if err != nil {
		return UpstreamResult{
			Err:            streamAttemptError(ctx, deadline.ctx, fmt.Errorf("perform upstream stream request: %w", err)),
			RequestWritten: wroteRequest.Load(), RetryableBeforeCommit: retryableBeforeCommit(ctx),
		}
	}
	streamBody := response.Body
	defer func() { _ = streamBody.Close() }()

	headers := cloneEndToEndHeaders(response.Header)
	result := UpstreamResult{
		StatusCode:     response.StatusCode,
		RequestWritten: true,
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, overflow, readErr := readStreamingErrorBody(streamBody)
		if readErr != nil {
			result.Err = streamAttemptError(ctx, deadline.ctx, fmt.Errorf("read upstream stream error response: %w", readErr))
			result.RetryableBeforeCommit = retryableBeforeCommit(ctx)
			return result
		}
		if overflow {
			result.Body, result.ClassificationBody = failClosedErrorBody(headers)
		} else {
			result.Body, result.ClassificationBody = forwarder.prepareErrorBody(headers, body, input)
		}
		if nonIdentityEncodingContainsKey(headers, input.APIKey) {
			result.Body, result.ClassificationBody = failClosedErrorBody(headers)
		}
		result.Header = sanitizeForwardResponseHeaders(headers, input)
		return result
	}

	if !inspectableStreamEncoding(response.Header) {
		result.Err = fmt.Errorf("%w: Content-Encoding %q", ErrUpstreamProtocol, response.Header.Values("Content-Encoding"))
		result.RetryableBeforeCommit = retryableBeforeCommit(ctx)
		return result
	}

	rewriteModel := needsModelRewrite(input)
	var rewriter dialect.ModelRewriter
	if rewriteModel {
		var ok bool
		rewriter, ok = input.Dialect.(dialect.ModelRewriter)
		if !ok {
			result.Err = fmt.Errorf("%w: dialect does not support model rewrite", ErrUpstreamProtocol)
			result.RetryableBeforeCommit = retryableBeforeCommit(ctx)
			return result
		}
	}
	streamBody = newSSERewriteStream(streamBody, func(data []byte) ([]byte, error) {
		safePayload, ok := rewriteBoundedLiteral(
			data,
			input.APIKey,
			redact.Placeholder,
			int64(maxSSEEventBytes),
		)
		if !ok {
			return nil, fmt.Errorf("%w: redact upstream SSE credential", ErrUpstreamProtocol)
		}
		if !rewriteModel {
			return safePayload, nil
		}
		safePayload, ok = rewriteBoundedLiteral(
			safePayload,
			input.UpstreamModelID,
			input.ExternalModel,
			int64(maxSSEEventBytes),
		)
		if !ok {
			return nil, fmt.Errorf("%w: rewrite upstream SSE model literal", ErrUpstreamProtocol)
		}
		rewritten, err := rewriter.RewriteResponseModel(safePayload, input.ExternalModel)
		if err != nil {
			return nil, fmt.Errorf("%w: rewrite upstream response model: %v", ErrUpstreamProtocol, err)
		}
		return rewritten, nil
	})
	invalidateRewrittenStreamHeaders(headers)

	prefix, err := bufferFirstSSEEvent(streamBody)
	if err != nil {
		if !rewriteModel && errors.Is(err, errSSEEventTooLarge) {
			err = errFirstSSEEventTooLarge
		}
		if errors.Is(err, errFirstSSEEventTooLarge) || errors.Is(err, errSSEEventTooLarge) {
			err = fmt.Errorf("%w: %w", ErrUpstreamProtocol, err)
		}
		result.Err = streamAttemptError(ctx, deadline.ctx, err)
		result.RetryableBeforeCommit = retryableBeforeCommit(ctx)
		return result
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		result.Err = ctxErr
		return result
	}
	if !deadline.disarm() {
		result.Err = streamAttemptError(ctx, deadline.ctx, context.DeadlineExceeded)
		result.RetryableBeforeCommit = retryableBeforeCommit(ctx)
		return result
	}

	if input.OnStreamReady != nil {
		input.OnStreamReady()
	}

	result.Header = sanitizeForwardResponseHeaders(headers, input)
	streamWriter := newStreamWriteController(downstream, forwarder.streamWriteTimeout)
	defer func() { _ = streamWriter.clear() }()

	result.Committed = true
	releaseCommittedRequestReplay(input.Request, replay)
	if err := commitStream(streamWriter, response.StatusCode, result.Header, prefix); err != nil {
		result.Err = err
		return result
	}
	if err := pumpStream(deadline.ctx, streamBody, streamWriter, input.Group.Timeouts.StreamIdle); err != nil {
		result.Err = err
	}
	return result
}

func retryableBeforeCommit(parent context.Context) bool {
	return parent != nil && parent.Err() == nil
}

func releaseCommittedRequestReplay(parsed *dialect.ParsedRequest, replay *requestReplay) {
	if parsed != nil {
		parsed.Body = nil
	}
	if replay != nil {
		replay.release()
	}
}

func newUpstreamRequest(
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
	upstreamURL, err := input.Dialect.BuildUpstreamURL(input.Group.UpstreamURL, parsed)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("build upstream URL: %w", err)
	}
	replay := newRequestReplay(parsed.Body)
	request, err := http.NewRequestWithContext(ctx, parsed.Method, upstreamURL, replay.open())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create upstream request: %w", err)
	}
	request.ContentLength = int64(len(parsed.Body))
	request.GetBody = func() (io.ReadCloser, error) { return replay.open(), nil }
	request.Header = cloneEndToEndHeaders(parsed.Header)
	removeDownstreamCredentials(request.Header)
	dialect.ApplyCredential(input.Dialect, request.Header, input.APIKey, input.Group.HeaderRules)
	if stream || rewrite {
		request.Header.Set("Accept-Encoding", "identity")
	}
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

func (forwarder *Forwarder) prepareErrorBody(headers http.Header, wire []byte, input ForwardInput) ([]byte, []byte) {
	if int64(len(wire)) > maxErrorResponseBodyBytes {
		return failClosedErrorBody(headers)
	}
	encoding, ok := inspectableErrorBodyEncoding(headers, wire)
	if !ok {
		return failClosedErrorBody(headers)
	}
	plain, err := utils.DecompressResponseLimited(encoding, wire, maxDecompressedErrorBodyBytes)
	if err != nil {
		return failClosedErrorBody(headers)
	}
	safePlain, ok := rewriteBoundedLiteral(
		plain, input.APIKey, redact.Placeholder, maxDecompressedErrorBodyBytes,
	)
	if !ok {
		return failClosedErrorBody(headers)
	}
	safePlain = forwarder.redactor.Bytes(safePlain)
	if int64(len(safePlain)) > maxDecompressedErrorBodyBytes {
		return failClosedErrorBody(headers)
	}
	downstreamPlain := safePlain
	if needsModelRewrite(input) {
		downstreamPlain, ok = rewriteBoundedLiteral(
			safePlain,
			input.UpstreamModelID,
			input.ExternalModel,
			maxDecompressedErrorBodyBytes,
		)
		if !ok {
			wire, _ := failClosedErrorBody(headers)
			return wire, safePlain
		}
	}
	if bytes.Equal(downstreamPlain, plain) {
		return bytes.Clone(wire), safePlain
	}
	safeWire, err := utils.CompressResponse(encoding, downstreamPlain)
	if err != nil || int64(len(safeWire)) > maxErrorResponseBodyBytes {
		if needsModelRewrite(input) {
			wire, _ := failClosedErrorBody(headers)
			return wire, safePlain
		}
		return failClosedErrorBody(headers)
	}
	updateRewrittenBodyHeaders(headers, len(safeWire))
	return safeWire, safePlain
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

func inspectableErrorBodyEncoding(headers http.Header, wire []byte) (string, bool) {
	values := headers.Values("Content-Encoding")
	if len(values) > 1 {
		return "", false
	}
	encoding := ""
	if len(values) == 1 {
		encoding = strings.ToLower(strings.TrimSpace(values[0]))
	}
	switch encoding {
	case "", "identity":
		return encoding, true
	case "gzip", "br", "deflate", "zstd":
		return encoding, len(wire) > 0
	default:
		return "", false
	}
}

func failClosedErrorBody(headers http.Header) ([]byte, []byte) {
	body := []byte(redact.Placeholder)
	deleteHeaderField(headers, "Content-Encoding")
	headers.Set("Content-Type", "text/plain; charset=utf-8")
	updateRewrittenBodyHeaders(headers, len(body))
	return bytes.Clone(body), bytes.Clone(body)
}

func updateRewrittenBodyHeaders(headers http.Header, bodyLength int) {
	for _, name := range []string{
		"ETag", "Digest", "Content-MD5", "Content-Range", "Content-Digest", "Repr-Digest",
		"Signature", "Signature-Input",
	} {
		headers.Del(name)
	}
	headers.Set("Content-Length", strconv.Itoa(bodyLength))
}

func invalidateRewrittenStreamHeaders(headers http.Header) {
	for _, name := range []string{
		"Content-Length", "ETag", "Digest", "Content-MD5", "Content-Range", "Content-Digest", "Repr-Digest",
		"Signature", "Signature-Input",
	} {
		headers.Del(name)
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
	}
}

func inspectableStreamEncoding(headers http.Header) bool {
	values := headers.Values("Content-Encoding")
	if len(values) == 0 {
		return true
	}
	if len(values) != 1 {
		return false
	}
	encoding := strings.TrimSpace(values[0])
	return encoding == "" || strings.EqualFold(encoding, "identity")
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
	for _, value := range source.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if name := strings.TrimSpace(token); name != "" {
				cloned.Del(name)
			}
		}
	}
	for name := range hopByHopHeaders {
		cloned.Del(name)
	}
	for name := range cloned {
		if isDebugHeader(name) {
			delete(cloned, name)
		}
	}
	return cloned
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

func nonIdentityEncodingContainsKey(headers http.Header, apiKey string) bool {
	var values []string
	for name, headerValues := range headers {
		if strings.EqualFold(name, "Content-Encoding") {
			values = append(values, headerValues...)
		}
	}
	if !headerValuesContainLiteral(values, apiKey) {
		return false
	}
	for _, value := range values {
		normalized := strings.TrimSpace(value)
		if normalized != "" && !strings.EqualFold(normalized, "identity") {
			return true
		}
	}
	return false
}

func sanitizeUpstreamResponseHeaders(source http.Header, apiKey string) http.Header {
	headers := cloneEndToEndHeaders(source)
	namesToDelete := []string{
		"Authorization", "Proxy-Authorization", "Api-Key",
		"X-Api-Key", "X-Goog-Api-Key",
	}
	for actualName, values := range headers {
		if headerValuesContainLiteral(values, apiKey) {
			namesToDelete = append(namesToDelete, actualName)
		}
	}
	for _, name := range namesToDelete {
		deleteHeaderField(headers, name)
	}
	return headers
}

func sanitizeForwardResponseHeaders(source http.Header, input ForwardInput) http.Header {
	headers := sanitizeUpstreamResponseHeaders(source, input.APIKey)
	if !needsModelRewrite(input) {
		return headers
	}
	deleted := false
	for name, values := range headers {
		nameContainsModel := headerNameContainsLiteral(name, input.UpstreamModelID)
		valuesContainModel := headerValuesContainLiteral(values, input.UpstreamModelID)
		if !nameContainsModel && !valuesContainModel {
			continue
		}
		if isRequiredRepresentationHeader(name) {
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

func headerNameContainsLiteral(name, literal string) bool {
	return literal != "" && strings.Contains(strings.ToLower(name), strings.ToLower(literal))
}

func isRequiredRepresentationHeader(name string) bool {
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

func isDebugHeader(name string) bool {
	for _, reserved := range debugHeaderNames {
		if strings.EqualFold(name, reserved) {
			return true
		}
	}
	return false
}

func removeDownstreamCredentials(headers http.Header) {
	for _, name := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
		headers.Del(name)
	}
}

func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}
