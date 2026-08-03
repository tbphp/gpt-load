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
	"gpt-load/internal/platform/contentcoding"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/pricing"
	"gpt-load/internal/ratelimit"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
)

const (
	maxAttempts               = 3
	maxRequestBodyBytes       = int64(32 << 20)
	maxDataPlaneModelBytes    = 255
	fixedCooldown             = time.Minute
	blacklistFailureThreshold = 3
	debugHeaderGroup          = "X-GPTLoad-Group"
	// debugHeaderKey remains reserved so an upstream cannot inject it downstream.
	debugHeaderKey      = "X-GPTLoad-Key"
	debugHeaderAttempts = "X-GPTLoad-Attempts"
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

// PriceTableProvider exposes the currently published immutable price table.
type PriceTableProvider interface {
	Load() *pricing.Table
}

type keyMutationCoordinator interface {
	Do(uint, func())
}

type runtimeKeyRegistry interface {
	scheduler.KeySource
	CaptureActiveKeyRefs(groupIDs []uint) []state.KeyRef
	ActiveEncryptedValueIfMatch(ref state.KeyRef) (string, bool)
	SetCooldown(keyID uint, until time.Time) bool
	IncrFailure(keyID uint) (int, bool)
	SetBlacklisted(keyID uint) bool
	ClearFailure(keyID uint) bool
}

type Handler struct {
	manager             *state.Manager
	registry            runtimeKeyRegistry
	encryption          encryption.Service
	forwarder           AttemptForwarder
	dialects            dialect.Set
	stats               *health.StatsStore
	mutations           keyMutationCoordinator
	limiter             AccessKeyRPMLimiter
	requestLogSink      telemetry.RequestLogSink
	priceTables         PriceTableProvider
	newRandom           func() *rand.Rand
	newRequestID        func() (string, error)
	requestNow          func() time.Time
	now                 func() time.Time
	writeTimeout        time.Duration
	modelListLimit      int64
	logger              *logrus.Logger
	authFailureEvents   *utils.RateLimitedEventCounter
	routeNotFoundEvents *utils.RateLimitedEventCounter
}

func (handler *Handler) freezeAttemptPricing(
	selection scheduler.Selection,
	applicable bool,
) frozenAttemptPricing {
	frozen := frozenAttemptPricing{
		groupID:       selection.GroupID,
		upstreamModel: optionalModelValue(selection.UpstreamModelID),
		applicable:    applicable,
	}
	if handler != nil && handler.priceTables != nil {
		frozen.table = handler.priceTables.Load()
	}
	var err error
	if selection.Group.ProviderID != nil {
		frozen.scopeKey, err = pricing.ProviderScopeKey(*selection.Group.ProviderID)
	} else {
		frozen.scopeKey, err = pricing.GroupScopeKey(selection.GroupID)
	}
	if err != nil {
		frozen.scopeKey = ""
	}
	return frozen
}

func NewHandler(
	manager *state.Manager,
	registry *state.KeyRegistry,
	encryptionService encryption.Service,
	forwarder AttemptForwarder,
	dialects dialect.Set,
	stats *health.StatsStore,
	mutations *health.MutationCoordinator,
	limiter AccessKeyRPMLimiter,
	requestLogSink telemetry.RequestLogSink,
	priceTables PriceTableProvider,
) *Handler {
	if limiter == nil {
		limiter = unlimitedAccessKeyRPMLimiter{}
	}
	if requestLogSink == nil {
		requestLogSink = telemetry.NoopRequestLogSink{}
	}
	return &Handler{
		manager: manager, registry: registry, encryption: encryptionService,
		forwarder: forwarder, dialects: dialects, stats: stats, mutations: mutations,
		limiter: limiter, requestLogSink: requestLogSink, priceTables: priceTables,
		newRandom:      func() *rand.Rand { return rand.New(rand.NewSource(rand.Int63())) },
		newRequestID:   newRequestID,
		requestNow:     time.Now,
		now:            time.Now,
		writeTimeout:   downstreamWriteTimeout,
		modelListLimit: maxNonStreamingResponseBodyBytes,
		logger:         logrus.StandardLogger(),
		authFailureEvents: utils.NewRateLimitedEventCounter(
			time.Minute,
			time.Now,
		),
		routeNotFoundEvents: utils.NewRateLimitedEventCounter(
			time.Minute,
			time.Now,
		),
	}
}

type unlimitedAccessKeyRPMLimiter struct{}

func (unlimitedAccessKeyRPMLimiter) Allow(uint, int64) ratelimit.LimitDecision {
	return ratelimit.LimitDecision{Allowed: true}
}

func (handler *Handler) applyKeyAction(
	keyID uint,
	decision health.Result,
	statusCode int,
	attemptNow time.Time,
) {
	switch decision.Action {
	case health.ActionCooldownKey:
		mutate := func() {
			until := decision.CooldownUntil
			if decision.UseFixed {
				until = attemptNow.Add(fixedCooldown)
			}
			if handler.registry.SetCooldown(keyID, until) {
				handler.stats.RecordProblem(keyID, decision.Category, statusCode, attemptNow)
			}
		}
		if handler.mutations == nil {
			mutate()
		} else {
			handler.mutations.Do(keyID, mutate)
		}
	case health.ActionFailKey:
		handler.mutations.Do(keyID, func() {
			count, ok := handler.registry.IncrFailure(keyID)
			if !ok {
				return
			}
			if count >= blacklistFailureThreshold &&
				!handler.registry.SetBlacklisted(keyID) {
				return
			}
			handler.stats.RecordFailure(keyID, decision.Category, statusCode, attemptNow)
		})
	}
}

func (handler *Handler) recordSuccess(keyID uint, at time.Time) {
	handler.mutations.Do(keyID, func() {
		if handler.registry.ClearFailure(keyID) {
			handler.stats.RecordSuccess(keyID, at)
		}
	})
}

func (handler *Handler) Handle(ginContext *gin.Context) {
	requestContext, ok := dataPlaneRequestContextFrom(ginContext)
	if !ok ||
		!requestContext.authenticated ||
		requestContext.snapshot == nil {
		handler.dataPlaneRouteNotFound(ginContext)
		return
	}
	if requestContext.locallyRejected {
		handler.dataPlaneRouteNotFound(ginContext)
		return
	}
	requestStarted := requestContext.requestStarted
	snapshot := requestContext.snapshot
	accessKey := requestContext.accessKey
	selectedRoute := requestContext.selectedRoute

	requestID, err := handler.newRequestID()
	if err != nil {
		utils.LogPlaneBestEffort(
			logrus.StandardLogger(),
			logrus.WarnLevel,
			utils.LogPlaneData,
			logrus.Fields{"error": err},
			"Request telemetry disabled",
		)
		requestID = ""
	} else {
		ginContext.Writer.Header().Set(requestIDHeader, requestID)
	}

	var recorder *requestRecorder
	if selectedRoute.Kind == endpointForward && requestID != "" {
		recorder = newRequestRecorder(
			handler.requestLogSink,
			requestID,
			requestStarted,
			accessKey.ID,
			selectedRoute.Protocol,
			handler.requestNow,
		)
		defer func() {
			recorder.completeMissingOutcome(
				ginContext.Writer.Written(),
				ginContext.Writer.Status(),
			)
			recorder.emit()
		}()
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
	if !contentcoding.AcceptsIdentity(ginContext.Request.Header.Values("Accept-Encoding")) {
		handler.completeReason(ginContext, recorder, reasonNotAcceptable)
		return
	}
	if selectedRoute.Kind == endpointModels {
		handler.writeVisibleModelList(ginContext, snapshot, accessKey, selectedRoute.Protocol)
		return
	}

	selectedDialect, dialectReady := handler.dialects[selectedRoute.Protocol]
	if !dialectReady || selectedRoute.Kind != endpointForward {
		handler.logDataPlaneRouteNotFound(
			ginContext.Request,
			accessKey.ID,
		)
		handler.completeReason(ginContext, recorder, reasonEndpointNotFound)
		return
	}

	candidateGroupIDs := scheduler.CandidateGroupIDs(
		snapshot,
		selectedRoute.Protocol,
		accessKey,
	)
	capturedRefs := handler.registry.CaptureActiveKeyRefs(candidateGroupIDs)
	allowedKeyRefs := make(map[uint]state.KeyRef, len(capturedRefs))
	for _, ref := range capturedRefs {
		allowedKeyRefs[ref.ID] = ref
	}

	body, requestHeaders, err := readDecodedRequestBody(
		ginContext.Request,
		maxRequestBodyBytes,
		maxRequestBodyBytes,
	)
	if err != nil {
		if ginContext.Request.Context().Err() != nil {
			recorder.completeCanceled(0)
			return
		}
		switch {
		case errors.Is(err, contentcoding.ErrEncodedTooLarge),
			errors.Is(err, contentcoding.ErrDecodedTooLarge),
			errors.Is(err, errRequestTooLarge):
			handler.completeReason(ginContext, recorder, reasonRequestTooLarge)
		case errors.Is(err, contentcoding.ErrUnsupportedEncoding):
			handler.completeReason(ginContext, recorder, reasonUnsupportedContentEncoding)
		case errors.Is(err, contentcoding.ErrInvalidEncoding):
			handler.completeReason(ginContext, recorder, reasonInvalidContentEncoding)
		default:
			handler.completeReason(ginContext, recorder, reasonInvalidProtocolRequest)
		}
		return
	}
	parsed := &dialect.ParsedRequest{
		Method:   ginContext.Request.Method,
		Path:     ginContext.Request.URL.Path,
		RawQuery: ginContext.Request.URL.RawQuery,
		Header:   requestHeaders,
		Body:     body,
	}
	metadata, err := selectedDialect.InspectRequest(parsed)
	if err != nil {
		if ginContext.Request.Context().Err() != nil {
			recorder.completeCanceled(0)
			return
		}
		handler.completeReason(ginContext, recorder, reasonInvalidProtocolRequest)
		return
	}
	model := ""
	if metadata.Model != nil {
		model = *metadata.Model
	}
	if len(model) > maxDataPlaneModelBytes {
		handler.completeReason(ginContext, recorder, reasonInvalidProtocolRequest)
		return
	}
	recorder.setClientModel(model)
	recorder.setUsageApplicable(metadata.ObserveUsage)
	recorder.setUsageDiagnostics(metadata.UsageDiagnostics)

	allowedKeyIDs := make(map[uint]struct{}, len(allowedKeyRefs))
	for keyID := range allowedKeyRefs {
		allowedKeyIDs[keyID] = struct{}{}
	}
	iterator := scheduler.New(snapshot, handler.registry, scheduler.Query{
		Protocol: selectedRoute.Protocol, ExternalModel: metadata.Model, AccessKey: accessKey,
		AllowedKeyIDs: allowedKeyIDs,
	}, handler.newRandom())
	handler.executeAttempts(
		ginContext,
		iterator,
		allowedKeyRefs,
		selectedDialect,
		parsed,
		model,
		metadata.Stream,
		metadata.ObserveUsage,
		recorder,
	)
}

func optionalModelValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
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
	allowedKeyRefs map[uint]state.KeyRef,
	selectedDialect dialect.Dialect,
	parsed *dialect.ParsedRequest,
	externalModel string,
	stream bool,
	observeUsage bool,
	recorder *requestRecorder,
) {
	type deferredAttempt struct {
		result        UpstreamResult
		decision      health.Result
		upstreamModel string
		attemptIndex  int
	}
	var lastResponse *deferredAttempt
	var lastTransport *deferredAttempt
	var lastProviderError *deferredAttempt
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
		ref, allowed := allowedKeyRefs[selection.KeyID]
		if !allowed || ref.GroupID != selection.GroupID {
			continue
		}
		encrypted, active := handler.registry.ActiveEncryptedValueIfMatch(ref)
		if !active {
			continue
		}
		apiKey, err := handler.encryption.Decrypt(encrypted)
		if err != nil {
			continue
		}

		attempts++
		updateDebugHeaders(ginContext.Writer.Header(), selection.Group.Name, attempts)
		selectedKeyID := selection.KeyID
		input := ForwardInput{
			Dialect: selectedDialect, ObserveUsage: observeUsage,
			Group: selection.Group, APIKey: apiKey, Request: parsed,
			ExternalModel:   externalModel,
			UpstreamModelID: optionalModelValue(selection.UpstreamModelID),
			OnStreamReady: func() {
				handler.recordSuccess(selectedKeyID, handler.now())
			},
		}
		if recorder != nil {
			recorder.freezeNextAttemptPricing(
				handler.freezeAttemptPricing(selection, observeUsage),
			)
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
				recordedAttempt := recorder.recordStreamAttempt(
					selection, apiKey, result, attemptStarted, attemptCompleted,
				)
				recorder.completeStream(result, optionalModelValue(selection.UpstreamModelID), recordedAttempt)
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
		if !stream && !result.ProviderErrorBeforeCommit && result.HasResponse() &&
			result.StatusCode >= http.StatusOK &&
			result.StatusCode < http.StatusMultipleChoices {
			handler.recordSuccess(selection.KeyID, attemptNow)
		}
		decision := health.Judge(selectedDialect, health.Attempt{
			StatusCode: result.StatusCode, Body: result.ClassificationBody,
			Header: result.Header, Now: attemptNow,
			Err: result.Err, RequestWritten: result.RequestWritten,
			Committed: result.Committed, RetryableBeforeCommit: result.RetryableBeforeCommit,
			ProviderErrorBeforeCommit: result.ProviderErrorBeforeCommit,
		})
		recordedAttempt := recorder.recordAttempt(
			selection, apiKey, result, decision, attemptStarted, attemptCompleted,
		)
		handler.applyKeyAction(selection.KeyID, decision, result.StatusCode, attemptNow)
		if decision.Action == health.ActionSkipGroup {
			iterator.SkipGroup(selection.GroupID)
		}
		if result.ProviderErrorBeforeCommit {
			if decision.ShouldRetry() {
				lastProviderError = &deferredAttempt{
					result:        result,
					decision:      decision,
					upstreamModel: optionalModelValue(selection.UpstreamModelID),
					attemptIndex:  recordedAttempt,
				}
				recorder.retryIfAnotherForward(recordedAttempt)
				continue
			}
			recorder.completeProviderError(
				result,
				optionalModelValue(selection.UpstreamModelID),
				recordedAttempt,
			)
			if err := handler.writeReason(ginContext, reasonUpstreamProtocol); err != nil {
				handler.completeWriteTerminal(ginContext, recorder, reasonUpstreamProtocol.Status)
			}
			return
		}
		if result.HasResponse() {
			lastResponse = &deferredAttempt{
				result: result, decision: decision, upstreamModel: optionalModelValue(selection.UpstreamModelID), attemptIndex: recordedAttempt,
			}
			if decision.ShouldRetry() {
				recorder.retryIfAnotherForward(recordedAttempt)
				continue
			}
			recorder.completeResponse(result, decision, optionalModelValue(selection.UpstreamModelID), recordedAttempt)
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
				result: result, decision: decision, upstreamModel: optionalModelValue(selection.UpstreamModelID),
			}
			recorder.retryIfAnotherForward(recordedAttempt)
			continue
		}
		value := transportReason(result)
		recorder.completeTransport(value, optionalModelValue(selection.UpstreamModelID))
		if err := handler.writeReason(ginContext, value); err != nil {
			handler.completeWriteTerminal(ginContext, recorder, value.Status)
		}
		return
	}

	if lastProviderError != nil {
		recorder.completeProviderError(
			lastProviderError.result,
			lastProviderError.upstreamModel,
			lastProviderError.attemptIndex,
		)
		if err := handler.writeReason(ginContext, reasonUpstreamProtocol); err != nil {
			handler.completeWriteTerminal(ginContext, recorder, reasonUpstreamProtocol.Status)
		}
		return
	}
	if lastResponse != nil {
		recorder.completeResponse(
			lastResponse.result,
			lastResponse.decision,
			lastResponse.upstreamModel,
			lastResponse.attemptIndex,
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
	if iterator.StaticReason() == scheduler.ReasonModelRequiredByFilter {
		handler.completeReason(ginContext, recorder, reasonModelRequiredByFilter)
		return
	}
	handler.completeReason(ginContext, recorder, reasonNoCandidate)
}

func initializeDebugHeaders(headers http.Header) {
	headers.Set(debugHeaderGroup, "")
	headers.Set(debugHeaderAttempts, "0")
}

func updateDebugHeaders(headers http.Header, group string, attempts int) {
	headers.Set(debugHeaderGroup, group)
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
