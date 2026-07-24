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
	"github.com/sirupsen/logrus"

	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/ratelimit"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
)

const (
	maxAttempts               = 3
	maxRequestBodyBytes       = int64(32 << 20)
	fixedCooldown             = time.Minute
	blacklistFailureThreshold = 3
	debugHeaderGroup          = "X-GPTLoad-Group"
	debugHeaderKey            = "X-GPTLoad-Key"
	debugHeaderAttempts       = "X-GPTLoad-Attempts"
)

var debugHeaderNames = []string{
	debugHeaderGroup,
	debugHeaderKey,
	debugHeaderAttempts,
	requestIDHeader,
}

var errRequestTooLarge = errors.New("request body is too large")

type AttemptForwarder interface {
	Forward(context.Context, ForwardInput) UpstreamResult
	ForwardStream(context.Context, ForwardInput, http.ResponseWriter) UpstreamResult
}

type AccessKeyRPMLimiter interface {
	Allow(accessKeyID uint, limit int64) ratelimit.LimitDecision
}

type runtimeKeyRegistry interface {
	scheduler.KeySource
	ActiveEncryptedValue(keyID, expectedGroupID uint) (string, bool)
	SetCooldown(keyID uint, until time.Time) bool
	IncrFailure(keyID uint) (int, bool)
	SetBlacklisted(keyID uint) bool
	ClearFailure(keyID uint) bool
}

type Handler struct {
	manager        *state.Manager
	registry       runtimeKeyRegistry
	encryption     encryption.Service
	forwarder      AttemptForwarder
	dialects       dialect.Set
	stats          *health.StatsStore
	limiter        AccessKeyRPMLimiter
	requestLogSink telemetry.RequestLogSink
	newRandom      func() *rand.Rand
	newRequestID   func() (string, error)
	requestNow     func() time.Time
	now            func() time.Time
	writeTimeout   time.Duration
	modelListLimit int64
}

func NewHandler(
	manager *state.Manager,
	registry *state.KeyRegistry,
	encryptionService encryption.Service,
	forwarder AttemptForwarder,
	dialects dialect.Set,
	stats *health.StatsStore,
	limiter AccessKeyRPMLimiter,
	requestLogSink telemetry.RequestLogSink,
) *Handler {
	if limiter == nil {
		limiter = unlimitedAccessKeyRPMLimiter{}
	}
	if requestLogSink == nil {
		requestLogSink = telemetry.NoopRequestLogSink{}
	}
	return &Handler{
		manager: manager, registry: registry, encryption: encryptionService,
		forwarder: forwarder, dialects: dialects, stats: stats,
		limiter: limiter, requestLogSink: requestLogSink,
		newRandom:      func() *rand.Rand { return rand.New(rand.NewSource(rand.Int63())) },
		newRequestID:   newRequestID,
		requestNow:     time.Now,
		now:            time.Now,
		writeTimeout:   downstreamWriteTimeout,
		modelListLimit: maxNonStreamingResponseBodyBytes,
	}
}

type unlimitedAccessKeyRPMLimiter struct{}

func (unlimitedAccessKeyRPMLimiter) Allow(uint, int64) ratelimit.LimitDecision {
	return ratelimit.LimitDecision{Allowed: true}
}

func (handler *Handler) applyKeyAction(
	keyID uint,
	decision health.Result,
	attemptNow time.Time,
) {
	switch decision.Action {
	case health.ActionCooldownKey:
		until := decision.CooldownUntil
		if decision.UseFixed {
			until = attemptNow.Add(fixedCooldown)
		}
		_ = handler.registry.SetCooldown(keyID, until)
	case health.ActionFailKey:
		handler.stats.Record(keyID, false, attemptNow)
		count, ok := handler.registry.IncrFailure(keyID)
		if ok && count >= blacklistFailureThreshold {
			_ = handler.registry.SetBlacklisted(keyID)
		}
	}
}

func (handler *Handler) RegisterRoutes(engine *gin.Engine) {
	engine.POST("/v1/chat/completions", handler.Handle)
	engine.NoRoute(handler.Handle)
}

func (handler *Handler) Handle(ginContext *gin.Context) {
	requestStarted := handler.requestNow()
	snapshot := handler.manager.Current()
	initializeDebugHeaders(ginContext.Writer.Header())
	accessKey, ok := authenticate(ginContext.Request, snapshot, handler.encryption)
	if !ok {
		_ = handler.writeReason(ginContext, reasonInvalidAccessKey)
		return
	}
	selectedRoute, ok := determineRoute(ginContext.Request.Method, ginContext.Request.URL.Path, ginContext.Request.Header)
	if !ok {
		_ = handler.writeReason(ginContext, reasonEndpointNotFound)
		return
	}

	requestID, err := handler.newRequestID()
	if err != nil {
		logrus.WithError(err).Warn("gateway request ID generation failed; request telemetry disabled")
		requestID = ""
	} else {
		ginContext.Writer.Header().Set(requestIDHeader, requestID)
	}

	var recorder *requestRecorder
	if selectedRoute.Kind == endpointChat && requestID != "" {
		recorder = newRequestRecorder(
			handler.requestLogSink,
			requestID,
			requestStarted,
			accessKey.ID,
			selectedRoute.Protocol,
			handler.requestNow,
		)
		defer recorder.emit()
	}

	limitDecision := handler.limiter.Allow(accessKey.ID, accessKey.RPMLimit)
	if !limitDecision.Allowed {
		ginContext.Writer.Header().Set(
			"Retry-After",
			strconv.Itoa(retryAfterSeconds(limitDecision.RetryAfter)),
		)
		handler.completeReason(ginContext, recorder, reasonAccessKeyRateLimited)
		return
	}
	if selectedRoute.Kind == endpointModels {
		handler.writeVisibleModelList(ginContext, snapshot, accessKey, selectedRoute.Protocol)
		return
	}

	selectedDialect, dialectReady := handler.dialects[selectedRoute.Protocol]
	if !dialectReady || selectedRoute.Kind != endpointChat {
		handler.completeReason(ginContext, recorder, reasonEndpointNotFound)
		return
	}

	body, err := readRequestBody(ginContext.Request.Body, maxRequestBodyBytes)
	if err != nil {
		if ginContext.Request.Context().Err() != nil {
			recorder.completeCanceled(0)
			return
		}
		if errors.Is(err, errRequestTooLarge) {
			handler.completeReason(ginContext, recorder, reasonRequestTooLarge)
			return
		}
		handler.completeReason(ginContext, recorder, reasonCannotExtractModel)
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
		if ginContext.Request.Context().Err() != nil {
			recorder.completeCanceled(0)
			return
		}
		handler.completeReason(ginContext, recorder, reasonCannotExtractModel)
		return
	}
	recorder.setClientModel(model)

	iterator := scheduler.New(snapshot, handler.registry, scheduler.Query{
		Protocol: selectedRoute.Protocol, ExternalModel: model, AccessKey: accessKey,
	}, handler.newRandom())
	handler.executeAttempts(ginContext, iterator, selectedDialect, parsed, model, stream, recorder)
}

func retryAfterSeconds(duration time.Duration) int {
	seconds := int((duration + time.Second - 1) / time.Second)
	if seconds < 1 {
		return 1
	}
	if seconds > 60 {
		return 60
	}
	return seconds
}

func (handler *Handler) completeReason(
	ginContext *gin.Context,
	recorder *requestRecorder,
	value reason,
) {
	recorder.completeReason(value)
	if err := handler.writeReason(ginContext, value); err != nil {
		handler.completeWriteTerminal(ginContext, recorder, value.Status)
	}
}

func (handler *Handler) completeWriteTerminal(
	ginContext *gin.Context,
	recorder *requestRecorder,
	selectedStatus int,
) {
	if ginContext != nil && ginContext.Request != nil &&
		ginContext.Request.Context().Err() != nil {
		status := 0
		if ginContext.Writer != nil && ginContext.Writer.Written() {
			status = ginContext.Writer.Status()
		}
		recorder.completeCanceled(status)
		return
	}
	recorder.completeDownstreamWrite(selectedStatus)
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
	externalModel string,
	stream bool,
	recorder *requestRecorder,
) {
	type deferredAttempt struct {
		result        UpstreamResult
		decision      health.Result
		upstreamModel string
	}
	var lastResponse *deferredAttempt
	var lastTransport *deferredAttempt
	attempts := 0
	for attempts < maxAttempts {
		if ginContext.Request.Context().Err() != nil {
			recorder.completeCanceled(0)
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
		selectedKeyID := selection.KeyID
		input := ForwardInput{
			Dialect: selectedDialect, Group: selection.Group, APIKey: apiKey, Request: parsed,
			ExternalModel:   externalModel,
			UpstreamModelID: selection.UpstreamModelID,
			OnStreamReady: func() {
				_ = handler.registry.ClearFailure(selectedKeyID)
				handler.stats.Record(selectedKeyID, true, handler.now())
			},
		}
		attemptStarted := recorder.beforeForward()
		var result UpstreamResult
		if stream {
			result = handler.forwarder.ForwardStream(ginContext.Request.Context(), input, ginContext.Writer)
		} else {
			result = handler.forwarder.Forward(ginContext.Request.Context(), input)
		}
		attemptCompleted := time.Time{}
		if recorder != nil {
			attemptCompleted = recorder.now()
		}
		requestCanceled := ginContext.Request.Context().Err() != nil
		if result.Committed {
			if recorder != nil {
				result.Stream = prioritizeStreamObservation(
					ginContext.Request.Context(),
					result.Err,
					result.Stream,
				)
				recorder.recordStreamAttempt(
					selection, apiKey, result, attemptStarted, attemptCompleted,
				)
				recorder.completeStream(result, selection.UpstreamModelID)
			}
			return
		}
		if requestCanceled {
			if recorder != nil {
				recorder.recordAttempt(
					selection,
					apiKey,
					result,
					health.Result{
						Category: health.FailureCategoryDownstreamCancel,
						Action:   health.ActionTerminate,
					},
					attemptStarted,
					attemptCompleted,
				)
				recorder.completeCanceled(0)
			}
			return
		}
		attemptNow := handler.now()
		if !stream && result.HasResponse() &&
			result.StatusCode >= http.StatusOK &&
			result.StatusCode < http.StatusMultipleChoices {
			_ = handler.registry.ClearFailure(selection.KeyID)
			handler.stats.Record(selection.KeyID, true, attemptNow)
		}
		decision := health.Judge(selectedDialect, health.Attempt{
			StatusCode: result.StatusCode, Body: result.ClassificationBody,
			Header: result.Header, Now: attemptNow,
			Err: result.Err, RequestWritten: result.RequestWritten,
			Committed: result.Committed, RetryableBeforeCommit: result.RetryableBeforeCommit,
		})
		recordedAttempt := recorder.recordAttempt(
			selection, apiKey, result, decision, attemptStarted, attemptCompleted,
		)
		handler.applyKeyAction(selection.KeyID, decision, attemptNow)
		if decision.Action == health.ActionSkipGroup {
			iterator.SkipGroup(selection.GroupID)
		}
		if result.HasResponse() {
			lastResponse = &deferredAttempt{
				result: result, decision: decision, upstreamModel: selection.UpstreamModelID,
			}
			if decision.ShouldRetry() {
				recorder.retryIfAnotherForward(recordedAttempt)
				continue
			}
			recorder.completeResponse(result, decision, selection.UpstreamModelID)
			if err := handler.writeUpstreamResponse(ginContext, result); err != nil {
				handler.completeWriteTerminal(ginContext, recorder, result.StatusCode)
				return
			}
			return
		}
		if errors.Is(result.Err, context.Canceled) {
			recorder.completeCanceled(0)
			return
		}
		if decision.ShouldRetry() {
			lastTransport = &deferredAttempt{
				result: result, decision: decision, upstreamModel: selection.UpstreamModelID,
			}
			recorder.retryIfAnotherForward(recordedAttempt)
			continue
		}
		value := transportReason(result)
		recorder.completeTransport(value, selection.UpstreamModelID)
		if err := handler.writeReason(ginContext, value); err != nil {
			handler.completeWriteTerminal(ginContext, recorder, value.Status)
		}
		return
	}

	if lastResponse != nil {
		recorder.completeResponse(
			lastResponse.result,
			lastResponse.decision,
			lastResponse.upstreamModel,
		)
		if err := handler.writeUpstreamResponse(ginContext, lastResponse.result); err != nil {
			handler.completeWriteTerminal(
				ginContext,
				recorder,
				lastResponse.result.StatusCode,
			)
			return
		}
		return
	}
	if lastTransport != nil {
		value := transportReason(lastTransport.result)
		recorder.completeTransport(value, lastTransport.upstreamModel)
		if err := handler.writeReason(ginContext, value); err != nil {
			handler.completeWriteTerminal(ginContext, recorder, value.Status)
		}
		return
	}
	handler.completeReason(ginContext, recorder, reasonNoCandidate)
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

func transportReason(result UpstreamResult) reason {
	switch {
	case errors.Is(result.Err, ErrUpstreamProtocol):
		return reasonUpstreamProtocol
	case isTimeoutError(result.Err):
		return reasonUpstreamTimeout
	default:
		return reasonUpstreamConnect
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
