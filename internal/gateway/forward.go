package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
	Dialect dialect.Dialect
	Group   state.GroupView
	APIKey  string
	Request *dialect.ParsedRequest
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
	clients  *platformhttp.HTTPClientManager
	redactor *redact.Redactor
}

const maxStreamingErrorBodyBytes = 64 << 10

var ErrUpstreamProtocol = errors.New("upstream protocol error")

func NewForwarder(clients *platformhttp.HTTPClientManager, redactor *redact.Redactor) *Forwarder {
	return &Forwarder{clients: clients, redactor: redactor}
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
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return UpstreamResult{Err: fmt.Errorf("read upstream response: %w", err), RequestWritten: true}
	}

	result := UpstreamResult{
		StatusCode:     response.StatusCode,
		Header:         cloneEndToEndHeaders(response.Header),
		Body:           body,
		RequestWritten: true,
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		safeWire, safePlain := forwarder.prepareErrorBody(result.Header, body, input.APIKey)
		result.Body = safeWire
		result.ClassificationBody = safePlain
	}
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
	defer response.Body.Close()

	result := UpstreamResult{
		StatusCode:     response.StatusCode,
		Header:         cloneEndToEndHeaders(response.Header),
		RequestWritten: true,
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, overflow, readErr := readStreamingErrorBody(response.Body)
		if readErr != nil {
			result.Err = streamAttemptError(ctx, deadline.ctx, fmt.Errorf("read upstream stream error response: %w", readErr))
			result.RetryableBeforeCommit = retryableBeforeCommit(ctx)
			return result
		}
		if overflow {
			result.Body, result.ClassificationBody = failClosedErrorBody(result.Header)
			return result
		}
		result.Body, result.ClassificationBody = forwarder.prepareErrorBody(result.Header, body, input.APIKey)
		return result
	}

	if !inspectableStreamEncoding(response.Header) {
		result.Err = fmt.Errorf("%w: Content-Encoding %q", ErrUpstreamProtocol, response.Header.Values("Content-Encoding"))
		result.RetryableBeforeCommit = retryableBeforeCommit(ctx)
		return result
	}

	prefix, err := bufferFirstSSEEvent(response.Body)
	if err != nil {
		if errors.Is(err, errFirstSSEEventTooLarge) {
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

	result.Committed = true
	releaseCommittedRequestReplay(input.Request, replay)
	if err := commitStream(downstream, response.StatusCode, result.Header, prefix); err != nil {
		result.Err = err
		return result
	}
	if err := pumpStream(deadline.ctx, response.Body, downstream, input.Group.Timeouts.StreamIdle); err != nil {
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
	upstreamURL, err := input.Dialect.BuildUpstreamURL(input.Group.UpstreamURL, input.Request)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("build upstream URL: %w", err)
	}
	replay := newRequestReplay(input.Request.Body)
	request, err := http.NewRequestWithContext(ctx, input.Request.Method, upstreamURL, replay.open())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create upstream request: %w", err)
	}
	request.ContentLength = int64(len(input.Request.Body))
	request.GetBody = func() (io.ReadCloser, error) { return replay.open(), nil }
	request.Header = cloneEndToEndHeaders(input.Request.Header)
	removeDownstreamCredentials(request.Header)
	dialect.ApplyCredential(input.Dialect, request.Header, input.APIKey, input.Group.HeaderRules)
	if stream {
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

func (forwarder *Forwarder) prepareErrorBody(headers http.Header, wire []byte, apiKey string) ([]byte, []byte) {
	encoding, ok := inspectableErrorBodyEncoding(headers, wire)
	if !ok {
		return failClosedErrorBody(headers)
	}
	plain, err := utils.DecompressResponse(encoding, wire)
	if err != nil {
		return failClosedErrorBody(headers)
	}
	safePlain := forwarder.redactor.Bytes(plain, apiKey)
	if bytes.Equal(safePlain, plain) {
		return bytes.Clone(wire), safePlain
	}
	safeWire, err := utils.CompressResponse(encoding, safePlain)
	if err != nil {
		return failClosedErrorBody(headers)
	}
	updateRewrittenBodyHeaders(headers, len(safeWire))
	return safeWire, safePlain
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
	headers.Del("Content-Encoding")
	headers.Set("Content-Type", "text/plain; charset=utf-8")
	updateRewrittenBodyHeaders(headers, len(body))
	return bytes.Clone(body), bytes.Clone(body)
}

func updateRewrittenBodyHeaders(headers http.Header, bodyLength int) {
	for _, name := range []string{
		"ETag", "Digest", "Content-MD5", "Content-Range", "Content-Digest", "Repr-Digest",
	} {
		headers.Del(name)
	}
	headers.Set("Content-Length", strconv.Itoa(bodyLength))
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
	wire, err := io.ReadAll(io.LimitReader(body, maxStreamingErrorBodyBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(wire) > maxStreamingErrorBodyBytes {
		return nil, true, nil
	}
	return wire, false, nil
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
