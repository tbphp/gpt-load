package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/httplifecycle"
	"gpt-load/internal/platform/contentcoding"
	"gpt-load/internal/platform/encryption"
	platformheader "gpt-load/internal/platform/httpheader"
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

type credentialMutationCoordinator interface {
	Do(uint, func())
}

type runtimeCredentialRegistry interface {
	scheduler.CredentialSource
	CaptureActiveCredentialRefs(groupIDs []uint) []state.CredentialRef
	ActiveEncryptedCredentialDataIfMatch(ref state.CredentialRef) (string, bool)
	SetCooldownWithChange(credentialID uint, until time.Time) (exists bool, changed bool)
	IncrFailure(credentialID uint) (int, bool)
	SetBlacklistedWithChange(credentialID uint) (exists bool, changed bool)
	ClearFailure(credentialID uint) bool
}

type Handler struct {
	manager             *state.Manager
	channels            *channel.Registry
	registry            runtimeCredentialRegistry
	encryption          encryption.Service
	forwarder           AttemptForwarder
	dialects            dialect.Set
	stats               *health.StatsStore
	mutations           credentialMutationCoordinator
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
	lifecycle           *httplifecycle.Coordinator
}

func (handler *Handler) freezeAttemptPricing(
	selection scheduler.Selection,
	applicable bool,
) frozenAttemptPricing {
	frozen := frozenAttemptPricing{
		channelID:     string(selection.ChannelID),
		groupID:       selection.GroupID,
		upstreamModel: optionalModelValue(selection.UpstreamModelID),
		applicable:    applicable,
	}
	if handler != nil && handler.priceTables != nil {
		frozen.table = handler.priceTables.Load()
	}
	return frozen
}

func NewHandler(
	manager *state.Manager,
	registry *state.CredentialRegistry,
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
		manager: manager, channels: channel.NewRegistry(), registry: registry, encryption: encryptionService,
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

// NewHandlerWithLifecycle wires the process HTTP lifecycle coordinator into
// the production gateway while preserving the compact constructor used by
// focused gateway tests.
func NewHandlerWithLifecycle(
	manager *state.Manager,
	registry *state.CredentialRegistry,
	channelRegistry *channel.Registry,
	encryptionService encryption.Service,
	forwarder AttemptForwarder,
	dialects dialect.Set,
	stats *health.StatsStore,
	mutations *health.MutationCoordinator,
	limiter AccessKeyRPMLimiter,
	requestLogSink telemetry.RequestLogSink,
	priceTables PriceTableProvider,
	lifecycle *httplifecycle.Coordinator,
) *Handler {
	handler := NewHandler(
		manager,
		registry,
		encryptionService,
		forwarder,
		dialects,
		stats,
		mutations,
		limiter,
		requestLogSink,
		priceTables,
	)
	if channelRegistry != nil {
		handler.channels = channelRegistry
	}
	handler.lifecycle = lifecycle
	return handler
}

type unlimitedAccessKeyRPMLimiter struct{}

func (unlimitedAccessKeyRPMLimiter) Allow(uint, int64) ratelimit.LimitDecision {
	return ratelimit.LimitDecision{Allowed: true}
}

func (handler *Handler) applyCredentialAction(
	credentialID uint,
	decision health.Result,
	statusCode int,
	attemptNow time.Time,
) {
	switch decision.Action {
	case health.ActionCooldownCredential:
		mutate := func() {
			until := decision.CooldownUntil
			if decision.UseFixed {
				until = attemptNow.Add(fixedCooldown)
			}
			exists, changed := handler.registry.SetCooldownWithChange(credentialID, until)
			if !exists {
				return
			}
			handler.stats.RecordProblem(credentialID, decision.Category, statusCode, attemptNow)
			if changed {
				handler.logCredentialCooldown(credentialID, decision.Category, statusCode)
			}
		}
		if handler.mutations == nil {
			mutate()
		} else {
			handler.mutations.Do(credentialID, mutate)
		}
	case health.ActionFailCredential:
		handler.mutations.Do(credentialID, func() {
			count, ok := handler.registry.IncrFailure(credentialID)
			if !ok {
				return
			}
			becameBlacklisted := false
			if count >= blacklistFailureThreshold {
				var exists bool
				exists, becameBlacklisted = handler.registry.SetBlacklistedWithChange(credentialID)
				if !exists {
					return
				}
			}
			handler.stats.RecordFailure(credentialID, decision.Category, statusCode, attemptNow)
			if becameBlacklisted {
				handler.logCredentialBlacklisted(credentialID, count, decision.Category, statusCode)
			}
		})
	}
}

func (handler *Handler) recordCredentialSuccess(credentialID uint, at time.Time) {
	handler.mutations.Do(credentialID, func() {
		if handler.registry.ClearFailure(credentialID) {
			handler.stats.RecordSuccess(credentialID, at)
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
	if selectedRoute.Kind == endpointModels {
		if !contentcoding.IdentityAcceptable(
			headerFieldValues(ginContext.Request.Header, "Accept-Encoding"),
		) {
			handler.completeReason(ginContext, recorder, reasonNotAcceptable)
			return
		}
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
	encoding, err := contentcoding.ParseContentEncoding(
		headerFieldValues(ginContext.Request.Header, "Content-Encoding"),
	)
	if err != nil {
		ginContext.Writer.Header().Set(
			"Accept-Encoding",
			contentcoding.SupportedRequestEncodings,
		)
		handler.completeReason(
			ginContext,
			recorder,
			reasonUnsupportedContentEncoding,
		)
		return
	}
	if !contentcoding.IdentityAcceptable(
		headerFieldValues(ginContext.Request.Header, "Accept-Encoding"),
	) {
		handler.completeReason(ginContext, recorder, reasonNotAcceptable)
		return
	}

	body, err := readDecodedRequestBody(
		ginContext.Request,
		encoding,
		maxRequestBodyBytes,
		maxRequestBodyBytes,
	)
	if err != nil {
		if ginContext.Request.Context().Err() != nil {
			recorder.completeCanceled(ginContext.Request.Context(), 0, -1)
			return
		}
		if errors.Is(err, errRequestTooLarge) {
			handler.completeReason(ginContext, recorder, reasonRequestTooLarge)
			return
		}
		if errors.Is(err, contentcoding.ErrInvalidContentEncoding) {
			handler.completeReason(ginContext, recorder, reasonInvalidContentEncoding)
			return
		}
		handler.completeReason(ginContext, recorder, reasonInvalidProtocolRequest)
		return
	}
	requestHeaders := ginContext.Request.Header.Clone()
	platformheader.StripRequestRepresentationMetadata(requestHeaders)
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
			recorder.completeCanceled(ginContext.Request.Context(), 0, -1)
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
	if ginContext.Request.Context().Err() != nil {
		recorder.completeCanceled(ginContext.Request.Context(), 0, -1)
		return
	}
	query := scheduler.Query{
		ClientProtocol:   selectedRoute.Protocol,
		Operation:        metadata.Operation,
		RouteRequirement: metadata.RouteRequirement,
		ExternalModel:    metadata.Model,
		AccessKey:        accessKey,
	}
	candidateGroupIDs := scheduler.CandidateGroupIDsForQuery(snapshot, query)
	capturedRefs := handler.registry.CaptureActiveCredentialRefs(candidateGroupIDs)
	allowedCredentialRefs := make(map[uint]state.CredentialRef, len(capturedRefs))
	for _, ref := range capturedRefs {
		allowedCredentialRefs[ref.ID] = ref
	}
	recorder.setClientModel(model)
	recorder.setOperation(metadata.Operation)
	recorder.setStream(metadata.Stream)
	recorder.setReasoning(metadata.Reasoning)
	recorder.setUsageApplicable(metadata.ObserveUsage)
	recorder.setUsageDiagnostics(metadata.UsageDiagnostics)

	allowedCredentialIDs := make(map[uint]struct{}, len(allowedCredentialRefs))
	for credentialID := range allowedCredentialRefs {
		allowedCredentialIDs[credentialID] = struct{}{}
	}
	query.AllowedCredentialIDs = allowedCredentialIDs
	iterator := scheduler.New(snapshot, handler.registry, query, handler.newRandom())
	handler.executeAttempts(
		ginContext,
		iterator,
		allowedCredentialRefs,
		selectedDialect,
		parsed,
		model,
		metadata.Stream,
		metadata.ObserveUsage,
		metadata.Operation,
		metadata.RouteRequirement,
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
		recorder.completeCanceled(ginContext.Request.Context(), status, -1)
		return
	}
	recorder.completeDownstreamWrite(selectedStatus)
}

func readDecodedRequestBody(
	request *http.Request,
	encoding contentcoding.Encoding,
	encodedLimit int64,
	decodedLimit int64,
) ([]byte, error) {
	if request == nil || request.Body == nil {
		return nil, fmt.Errorf("request body is required")
	}
	if encodedLimit < 0 {
		return nil, fmt.Errorf("encoded request body limit must not be negative")
	}
	if decodedLimit < 0 {
		return nil, fmt.Errorf("decoded request body limit must not be negative")
	}
	if request.ContentLength > 0 && request.ContentLength > encodedLimit {
		return nil, errRequestTooLarge
	}
	reader := io.Reader(request.Body)
	if encodedLimit < math.MaxInt64 {
		reader = io.LimitReader(request.Body, encodedLimit+1)
	}
	encoded, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	if int64(len(encoded)) > encodedLimit {
		return nil, errRequestTooLarge
	}
	body, err := contentcoding.DecodeLimited(encoding, encoded, decodedLimit)
	if errors.Is(err, contentcoding.ErrDecodedBodyTooLarge) {
		return nil, errRequestTooLarge
	}
	return body, err
}

func headerFieldValues(headers http.Header, name string) []string {
	var values []string
	for actualName, fieldValues := range headers {
		if strings.EqualFold(actualName, name) {
			values = append(values, fieldValues...)
		}
	}
	return values
}

func (handler *Handler) executeAttempts(
	ginContext *gin.Context,
	iterator *scheduler.Iterator,
	allowedCredentialRefs map[uint]state.CredentialRef,
	selectedDialect dialect.Dialect,
	parsed *dialect.ParsedRequest,
	externalModel string,
	stream bool,
	observeUsage bool,
	operation execution.Operation,
	routeRequirement execution.RouteRequirement,
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
	var lastConversion *deferredAttempt
	var lastProviderError *deferredAttempt
	lastAttemptIndex := -1
	attempts := 0
	for attempts < maxAttempts {
		if ginContext.Request.Context().Err() != nil {
			recorder.completeCanceled(ginContext.Request.Context(), 0, lastAttemptIndex)
			return
		}
		selection, err := iterator.Next()
		if errors.Is(err, scheduler.ErrExhausted) {
			break
		}
		if err != nil {
			break
		}
		ref, allowed := allowedCredentialRefs[selection.CredentialID]
		if !allowed || ref.GroupID != selection.GroupID {
			continue
		}
		encrypted, active := handler.registry.ActiveEncryptedCredentialDataIfMatch(ref)
		if !active {
			continue
		}
		decryptedCredential, err := handler.encryption.Decrypt(encrypted)
		if err != nil {
			continue
		}
		normalizedCredential, err := normalizeChannelCredential(
			handler.channels,
			selection.ChannelID,
			decryptedCredential,
		)
		if err != nil {
			continue
		}

		attempts++
		updateDebugHeaders(ginContext.Writer.Header(), selection.Group.Name, attempts)
		selectedCredentialID := selection.CredentialID
		executionRequestID := "untracked"
		if recorder != nil {
			executionRequestID = recorder.requestID
		}
		input := ForwardInput{
			Dialect: selectedDialect, ObserveUsage: observeUsage,
			Group: selection.Group, APIKey: normalizedCredential.apiKey,
			CredentialSecrets: normalizedCredential.secrets, Request: parsed,
			ExternalModel:    externalModel,
			UpstreamModelID:  optionalModelValue(selection.UpstreamModelID),
			RequestID:        executionRequestID,
			AttemptID:        executionRequestID + ":" + strconv.Itoa(attempts),
			AttemptSequence:  uint32(attempts),
			ClientProtocol:   selectedDialect.Protocol(),
			Operation:        operation,
			RouteRequirement: routeRequirement,
			ChannelID:        string(selection.ChannelID),
			TargetKind:       string(selection.ResolvedTarget.ProviderKind),
			RouteMode:        execution.RouteMode(selection.RouteMode),
			TargetConfig:     selection.ResolvedTarget.TargetConfig,
			Credential: execution.NewCredentialSnapshot(
				selection.CredentialID,
				ref.Version,
				ref.IdentityGeneration,
				normalizedCredential.payload,
			),
			OnStreamReady: func() {
				handler.recordCredentialSuccess(selectedCredentialID, handler.now())
			},
			OnFirstResponse: func() {
				recorder.recordFirstResponse()
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
		result = normalizeUpstreamResultEvidence(result)
		attemptCompleted := time.Time{}
		if recorder != nil {
			attemptCompleted = recorder.now()
		}
		requestCanceled := ginContext.Request.Context().Err() != nil
		if result.Committed {
			if stream && result.Stream.EndReason == StreamEndNone {
				result.Stream = prioritizeStreamObservation(
					ginContext.Request.Context(),
					result.Err,
					result.Stream,
				)
			}
			if recorder != nil {
				recordedAttempt := recorder.recordStreamAttempt(
					selection, normalizedCredential.secrets, result, attemptStarted, attemptCompleted,
				)
				recorder.completeStream(result, optionalModelValue(selection.UpstreamModelID), recordedAttempt)
			}
			return
		}
		if requestCanceled {
			if recorder != nil {
				recordedAttempt := recorder.recordAttempt(
					selection,
					normalizedCredential.secrets,
					result,
					health.Result{
						Category: health.FailureCategoryDownstreamCancel,
						Action:   health.ActionTerminate,
					},
					attemptStarted,
					attemptCompleted,
				)
				recorder.completeCanceled(ginContext.Request.Context(), 0, recordedAttempt)
			}
			return
		}
		attemptNow := handler.now()
		if !stream && !result.ProviderErrorBeforeCommit && result.HasResponse() &&
			result.StatusCode >= http.StatusOK &&
			result.StatusCode < http.StatusMultipleChoices {
			handler.recordCredentialSuccess(selection.CredentialID, attemptNow)
		}
		decision := judgeUpstreamResult(result, attemptNow)
		recordedAttempt := recorder.recordAttempt(
			selection, normalizedCredential.secrets, result, decision, attemptStarted, attemptCompleted,
		)
		lastAttemptIndex = recordedAttempt
		handler.applyCredentialAction(selection.CredentialID, decision, result.StatusCode, attemptNow)
		if decision.Action == health.ActionSkipGroup {
			iterator.SkipGroup(selection.GroupID)
		}
		if result.ProviderErrorBeforeCommit {
			if shouldRetryAcrossCandidates(input.Operation, input.Request.Method, result, decision) {
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
			if shouldRetryAcrossCandidates(input.Operation, input.Request.Method, result, decision) {
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
			recorder.completeCanceled(ginContext.Request.Context(), 0, recordedAttempt)
			return
		}
		if shouldRetryAcrossCandidates(input.Operation, input.Request.Method, result, decision) {
			deferred := &deferredAttempt{
				result: result, decision: decision, upstreamModel: optionalModelValue(selection.UpstreamModelID), attemptIndex: recordedAttempt,
			}
			if isConversionUnsupportedResult(result) {
				lastConversion = deferred
			} else {
				lastTransport = deferred
			}
			recorder.retryIfAnotherForward(recordedAttempt)
			continue
		}
		value := transportReason(result)
		recorder.completeTransport(value, optionalModelValue(selection.UpstreamModelID), recordedAttempt)
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
		recorder.completeTransport(value, lastTransport.upstreamModel, lastTransport.attemptIndex)
		if err := handler.writeReason(ginContext, value); err != nil {
			handler.completeWriteTerminal(ginContext, recorder, value.Status)
		}
		return
	}
	if lastConversion != nil {
		value := reasonProtocolConversionUnsupported
		recorder.completeTransport(value, lastConversion.upstreamModel, lastConversion.attemptIndex)
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

func shouldRetryAcrossCandidates(
	operation execution.Operation,
	method string,
	result UpstreamResult,
	decision health.Result,
) bool {
	if !decision.ShouldRetry() || result.Committed {
		return false
	}
	if result.DispatchState == execution.DispatchNotSent {
		return true
	}
	if result.DispatchState != execution.DispatchMaybeSent {
		return false
	}
	switch result.StatusCode {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden, http.StatusTooManyRequests:
		return true
	}
	if decision.Category == health.FailureCategoryInvalidKey ||
		decision.Category == health.FailureCategoryRateLimited ||
		decision.Category == health.FailureCategoryModelUnavailable {
		return true
	}
	if method == http.MethodGet || method == http.MethodHead {
		return true
	}
	switch operation {
	case execution.OperationResponsesRetrieve,
		execution.OperationResponsesInputItems,
		execution.OperationResponsesInputTokens,
		execution.OperationListModels:
		return true
	default:
		return false
	}
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
	case isConversionUnsupportedResult(result):
		return reasonProtocolConversionUnsupported
	case result.DispatchState == execution.DispatchNotSent && result.ExecutionError != nil &&
		result.ExecutionError.Kind == execution.ErrorKindInvalidRequest:
		return reasonInvalidProtocolRequest
	case errors.Is(result.Err, ErrUpstreamProtocol):
		return reasonUpstreamProtocol
	case isTimeoutError(result.Err):
		return reasonUpstreamTimeout
	default:
		return reasonUpstreamConnect
	}
}

func isConversionUnsupportedResult(result UpstreamResult) bool {
	return result.DispatchState == execution.DispatchNotSent && result.ExecutionError != nil &&
		result.ExecutionError.Kind == execution.ErrorKindConversionUnsupported
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

	method := ""
	if ginContext.Request != nil {
		method = ginContext.Request.Method
	}
	normalizedHeaders, writeBody := normalizeBufferedResponse(method, status, headers, body)
	for name, values := range normalizedHeaders {
		for _, value := range values {
			ginContext.Writer.Header().Add(name, value)
		}
	}
	if err := controlled.writeHeader(status); err != nil {
		return fmt.Errorf("write downstream response headers: %w", err)
	}
	ginContext.Writer.WriteHeaderNow()
	if writeBody && len(body) > 0 {
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
