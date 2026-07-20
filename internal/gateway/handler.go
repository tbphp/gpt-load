package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
)

const (
	maxAttempts         = 3
	maxRequestBodyBytes = int64(32 << 20)
	debugHeaderGroup    = "X-GPTLoad-Group"
	debugHeaderKey      = "X-GPTLoad-Key"
	debugHeaderAttempts = "X-GPTLoad-Attempts"
)

var debugHeaderNames = []string{debugHeaderGroup, debugHeaderKey, debugHeaderAttempts}

var errRequestTooLarge = errors.New("request body is too large")

type AttemptForwarder interface {
	Forward(context.Context, ForwardInput) UpstreamResult
	ForwardStream(context.Context, ForwardInput, http.ResponseWriter) UpstreamResult
}

type runtimeKeyRegistry interface {
	scheduler.KeySource
	ActiveEncryptedValue(keyID, expectedGroupID uint) (string, bool)
}

type Handler struct {
	manager        *state.Manager
	registry       runtimeKeyRegistry
	encryption     encryption.Service
	forwarder      AttemptForwarder
	dialects       dialect.Set
	newRandom      func() *rand.Rand
	writeTimeout   time.Duration
	modelListLimit int64
}

func NewHandler(
	manager *state.Manager,
	registry *state.KeyRegistry,
	encryptionService encryption.Service,
	forwarder AttemptForwarder,
	dialects dialect.Set,
) *Handler {
	return &Handler{
		manager: manager, registry: registry, encryption: encryptionService,
		forwarder: forwarder, dialects: dialects,
		newRandom:      func() *rand.Rand { return rand.New(rand.NewSource(rand.Int63())) },
		writeTimeout:   downstreamWriteTimeout,
		modelListLimit: maxNonStreamingResponseBodyBytes,
	}
}

func (handler *Handler) RegisterRoutes(engine *gin.Engine) {
	engine.POST("/v1/chat/completions", handler.Handle)
	engine.NoRoute(handler.Handle)
}

func (handler *Handler) Handle(ginContext *gin.Context) {
	initializeDebugHeaders(ginContext.Writer.Header())
	snapshot := handler.manager.Current()
	accessKey, ok := authenticate(ginContext.Request, snapshot, handler.encryption)
	if !ok {
		writeReason(ginContext, reasonInvalidAccessKey)
		return
	}
	selectedRoute, ok := determineRoute(ginContext.Request.Method, ginContext.Request.URL.Path, ginContext.Request.Header)
	if !ok {
		writeReason(ginContext, reasonEndpointNotFound)
		return
	}
	if selectedRoute.Kind == endpointModels {
		handler.writeVisibleModelList(ginContext, snapshot, accessKey, selectedRoute.Protocol)
		return
	}

	selectedDialect, dialectReady := handler.dialects[selectedRoute.Protocol]
	if !dialectReady || selectedRoute.Kind != endpointChat {
		writeReason(ginContext, reasonEndpointNotFound)
		return
	}

	body, err := readRequestBody(ginContext.Request.Body, maxRequestBodyBytes)
	if err != nil {
		if errors.Is(err, errRequestTooLarge) {
			writeReason(ginContext, reasonRequestTooLarge)
			return
		}
		writeReason(ginContext, reasonCannotExtractModel)
		return
	}
	parsed := &dialect.ParsedRequest{
		Method:   ginContext.Request.Method,
		Path:     ginContext.Request.URL.Path,
		RawQuery: ginContext.Request.URL.RawQuery,
		Header:   ginContext.Request.Header.Clone(),
		Body:     body,
	}
	model, stream, err := selectedDialect.ExtractModel(parsed)
	if err != nil {
		writeReason(ginContext, reasonCannotExtractModel)
		return
	}

	iterator := scheduler.New(snapshot, handler.registry, scheduler.Query{
		Protocol: selectedRoute.Protocol, ExternalModel: model, AccessKey: accessKey,
	}, handler.newRandom())
	handler.executeAttempts(ginContext, iterator, selectedDialect, parsed, stream)
}

func readRequestBody(reader io.Reader, limit int64) ([]byte, error) {
	if reader == nil {
		return nil, fmt.Errorf("request body is required")
	}
	if limit < 0 {
		return nil, fmt.Errorf("request body limit must not be negative")
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, errRequestTooLarge
	}
	return body, nil
}

func (handler *Handler) executeAttempts(
	ginContext *gin.Context,
	iterator *scheduler.Iterator,
	selectedDialect dialect.Dialect,
	parsed *dialect.ParsedRequest,
	stream bool,
) {
	var lastResponse *UpstreamResult
	var lastTransport *UpstreamResult
	attempts := 0
	for attempts < maxAttempts {
		if ginContext.Request.Context().Err() != nil {
			return
		}
		selection, err := iterator.Next()
		if errors.Is(err, scheduler.ErrExhausted) {
			break
		}
		if err != nil {
			break
		}
		encrypted, ok := handler.registry.ActiveEncryptedValue(selection.KeyID, selection.GroupID)
		if !ok {
			continue
		}
		apiKey, err := handler.encryption.Decrypt(encrypted)
		if err != nil {
			continue
		}

		attempts++
		updateDebugHeaders(ginContext.Writer.Header(), selection.Group.Name, apiKey, attempts)
		input := ForwardInput{
			Dialect: selectedDialect, Group: selection.Group, APIKey: apiKey, Request: parsed,
		}
		var result UpstreamResult
		if stream {
			result = handler.forwarder.ForwardStream(ginContext.Request.Context(), input, ginContext.Writer)
		} else {
			result = handler.forwarder.Forward(ginContext.Request.Context(), input)
		}
		if result.Committed || ginContext.Request.Context().Err() != nil {
			return
		}
		verdict := health.Judge(selectedDialect, health.Attempt{
			StatusCode: result.StatusCode, Body: result.ClassificationBody,
			Err: result.Err, RequestWritten: result.RequestWritten,
			Committed: result.Committed, RetryableBeforeCommit: result.RetryableBeforeCommit,
		})
		if result.HasResponse() {
			copied := result
			lastResponse = &copied
			if verdict.Retryable {
				continue
			}
			if err := handler.writeUpstreamResponse(ginContext, result); err != nil {
				return
			}
			return
		}
		if errors.Is(result.Err, context.Canceled) {
			return
		}
		if verdict.Retryable {
			copied := result
			lastTransport = &copied
			continue
		}
		writeTransportReason(ginContext, result)
		return
	}

	if lastResponse != nil {
		if err := handler.writeUpstreamResponse(ginContext, *lastResponse); err != nil {
			return
		}
		return
	}
	if lastTransport != nil {
		writeTransportReason(ginContext, *lastTransport)
		return
	}
	writeReason(ginContext, reasonNoCandidate)
}

func initializeDebugHeaders(headers http.Header) {
	headers.Set(debugHeaderGroup, "")
	headers.Set(debugHeaderKey, "")
	headers.Set(debugHeaderAttempts, "0")
}

func updateDebugHeaders(headers http.Header, group, apiKey string, attempts int) {
	headers.Set(debugHeaderGroup, group)
	headers.Set(debugHeaderKey, utils.MaskAPIKey(apiKey))
	headers.Set(debugHeaderAttempts, strconv.Itoa(attempts))
}

func writeTransportReason(ginContext *gin.Context, result UpstreamResult) {
	switch {
	case errors.Is(result.Err, ErrUpstreamProtocol):
		writeReason(ginContext, reasonUpstreamProtocol)
	case isTimeoutError(result.Err):
		writeReason(ginContext, reasonUpstreamTimeout)
	default:
		writeReason(ginContext, reasonUpstreamConnect)
	}
}

func (handler *Handler) writeUpstreamResponse(ginContext *gin.Context, result UpstreamResult) error {
	return handler.writeBufferedResponse(ginContext, result.StatusCode, result.Header, result.Body)
}

func (handler *Handler) writeBufferedResponse(
	ginContext *gin.Context,
	status int,
	headers http.Header,
	body []byte,
) (err error) {
	if handler == nil || ginContext == nil {
		return fmt.Errorf("downstream response writer is required")
	}
	controlled := newStreamWriteController(ginContext.Writer, handler.writeTimeout)
	defer func() {
		if clearErr := controlled.clear(); err == nil && clearErr != nil {
			err = fmt.Errorf("clear downstream write deadline: %w", clearErr)
		}
	}()

	for name, values := range cloneEndToEndHeaders(headers) {
		for _, value := range values {
			ginContext.Writer.Header().Add(name, value)
		}
	}
	if err := controlled.writeHeader(status); err != nil {
		return fmt.Errorf("write downstream response headers: %w", err)
	}
	ginContext.Writer.WriteHeaderNow()
	if len(body) > 0 {
		written, writeErr := controlled.write(body)
		if writeErr != nil {
			return fmt.Errorf("write downstream response: %w", writeErr)
		}
		if written != len(body) {
			return fmt.Errorf("write downstream response: %w", io.ErrShortWrite)
		}
	}
	if err := controlled.flush(); err != nil {
		return fmt.Errorf("flush downstream response: %w", err)
	}
	return nil
}
