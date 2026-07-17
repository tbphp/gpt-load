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
	StatusCode         int
	Header             http.Header
	Body               []byte
	ClassificationBody []byte
	Err                error
	RequestWritten     bool
}

func (result UpstreamResult) HasResponse() bool {
	return result.StatusCode != 0 && result.Err == nil
}

type Forwarder struct {
	clients  *platformhttp.HTTPClientManager
	redactor *redact.Redactor
}

func NewForwarder(clients *platformhttp.HTTPClientManager, redactor *redact.Redactor) *Forwarder {
	return &Forwarder{clients: clients, redactor: redactor}
}

func (forwarder *Forwarder) Forward(ctx context.Context, input ForwardInput) UpstreamResult {
	if forwarder == nil || forwarder.clients == nil || forwarder.redactor == nil || input.Dialect == nil || input.Request == nil {
		return UpstreamResult{Err: fmt.Errorf("forward input is incomplete")}
	}
	upstreamURL, err := input.Dialect.BuildUpstreamURL(input.Group.UpstreamURL, input.Request)
	if err != nil {
		return UpstreamResult{Err: fmt.Errorf("build upstream URL: %w", err)}
	}
	request, err := http.NewRequestWithContext(ctx, input.Request.Method, upstreamURL, bytes.NewReader(input.Request.Body))
	if err != nil {
		return UpstreamResult{Err: fmt.Errorf("create upstream request: %w", err)}
	}
	request.Header = cloneEndToEndHeaders(input.Request.Header)
	removeDownstreamCredentials(request.Header)
	dialect.ApplyCredential(input.Dialect, request.Header, input.APIKey, input.Group.HeaderRules)
	if _, exists := request.Header["User-Agent"]; !exists {
		request.Header["User-Agent"] = nil
	}

	var wroteRequest atomic.Bool
	trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { wroteRequest.Store(true) }}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
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
	return cloned
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
