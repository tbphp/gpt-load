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

	"gpt-load/internal/accessquota"
	"gpt-load/internal/affinity"
	"gpt-load/internal/channel"
	"gpt-load/internal/connection"
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
	subscriptionproviders "gpt-load/internal/subscription/providers"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
	"gpt-load/internal/telemetry"
)

const (
	maxRequestBodyBytes    = int64(128 << 20)
	maxDataPlaneModelBytes = 255
	fixedCooldown          = time.Minute
	debugHeaderGroup       = "X-GPTLoad-Group"
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
	CredentialRef(credentialID uint) (state.CredentialRef, bool)
	ActiveEncryptedCredentialDataIfMatch(ref state.CredentialRef) (string, bool)
	SetCooldownWithChange(credentialID uint, until time.Time) (exists bool, changed bool)
	SetCooldownWithChangeIfVersion(credentialID uint, expectedVersion uint64, until time.Time) (matched bool, changed bool)
	IncrFailure(credentialID uint) (int, bool)
	SetBlacklistedWithChange(credentialID uint) (exists bool, changed bool)
	ClearFailure(credentialID uint) bool
}

type Handler struct {
	manager             *state.Manager
	channels            *channel.Registry
	subscriptions       *subscriptionruntime.Runtime
	registry            runtimeCredentialRegistry
	encryption          encryption.Service
	forwarder           AttemptForwarder
	dialects            dialect.Set
	stats               *health.StatsStore
	mutations           credentialMutationCoordinator
	limiter             AccessKeyRPMLimiter
	requestLogSink      telemetry.RequestLogSink
	priceTables         PriceTableProvider
	accessQuota         *accessquota.Runtime
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
	affinityCache       *affinity.Cache
}

func (handler *Handler) freezeAttemptPricing(
	selection scheduler.Selection,
	metadata dialect.RequestMetadata,
) frozenAttemptPricing {
	frozen := frozenAttemptPricing{
		channelID:        string(selection.ChannelID),
		groupID:          selection.GroupID,
		upstreamModel:    optionalModelValue(selection.UpstreamModelID),
		applicable:       metadata.ObserveUsage,
		metadataSet:      true,
		pricingMode:      metadata.PricingMode,
		usageDiagnostics: metadata.UsageDiagnostics,
		reasoning:        metadata.Reasoning.Clone(),
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
	accessQuotas ...*accessquota.Runtime,
) *Handler {
	if limiter == nil {
		limiter = unlimitedAccessKeyRPMLimiter{}
	}
	if requestLogSink == nil {
		requestLogSink = telemetry.NoopRequestLogSink{}
	}
	channels := channel.NewRegistry()
	subscriptions, _ := subscriptionruntime.NewRuntime(channels, subscriptionproviders.Implementations()...)
	handler := &Handler{
		manager: manager, channels: channels, subscriptions: subscriptions, registry: registry, encryption: encryptionService,
		forwarder: forwarder, dialects: dialects, stats: stats, mutations: mutations,
		limiter: limiter, requestLogSink: requestLogSink, priceTables: priceTables,
		affinityCache:  affinity.NewCache(),
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
	for _, runtime := range accessQuotas {
		if runtime != nil {
			handler.accessQuota = runtime
			break
		}
	}
	return handler
}

// NewHandlerWithLifecycle wires the process HTTP lifecycle coordinator into
// the production gateway while preserving the compact constructor used by
// focused gateway tests.
func NewHandlerWithLifecycle(
	manager *state.Manager,
	registry *state.CredentialRegistry,
	channelRegistry *channel.Registry,
	subscriptions *subscriptionruntime.Runtime,
	encryptionService encryption.Service,
	forwarder AttemptForwarder,
	dialects dialect.Set,
	stats *health.StatsStore,
	mutations *health.MutationCoordinator,
	limiter AccessKeyRPMLimiter,
	requestLogSink telemetry.RequestLogSink,
	priceTables PriceTableProvider,
	accessQuota *accessquota.Runtime,
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
		accessQuota,
	)
	if channelRegistry != nil {
		handler.channels = channelRegistry
	}
	if subscriptions != nil {
		handler.subscriptions = subscriptions
	}
	handler.lifecycle = lifecycle
	return handler
}

type unlimitedAccessKeyRPMLimiter struct{}

func (unlimitedAccessKeyRPMLimiter) Allow(uint, int64) ratelimit.LimitDecision {
	return ratelimit.LimitDecision{Allowed: true}
}

type requestAccessQuotaAdmission struct {
	accessKeyID uint
	snapshot    *state.ConfigSnapshot
	ticket      accessquota.Ticket
	admitted    bool
}

func (handler *Handler) applyDecisionEffect(
	credentialID uint,
	decision health.Decision,
	statusCode int,
	attemptNow time.Time,
) {
	defaults := state.DefaultRuntimeSettings()
	handler.applyDecisionEffectWithBlacklistPolicy(
		credentialID,
		0,
		decision,
		statusCode,
		attemptNow,
		defaults.BlacklistThreshold,
	)
}

func (handler *Handler) applyGroupDecisionEffect(
	group state.GroupView,
	credentialID uint,
	credentialVersion uint64,
	decision health.Decision,
	statusCode int,
	attemptNow time.Time,
) {
	handler.applyDecisionEffectWithBlacklistPolicy(
		credentialID,
		credentialVersion,
		decision,
		statusCode,
		attemptNow,
		group.BlacklistThreshold,
	)
}

func refreshCooldownCredentialVersion(result UpstreamResult, credentialVersion uint64) uint64 {
	if result.DispatchState == execution.DispatchNotSent && result.ExecutionError != nil &&
		result.ExecutionError.Hint == execution.FailureHintRefreshUnavailable {
		return credentialVersion
	}
	return 0
}

func (handler *Handler) applyDecisionEffectWithBlacklistPolicy(
	credentialID uint,
	credentialVersion uint64,
	decision health.Decision,
	statusCode int,
	attemptNow time.Time,
	blacklistThreshold int,
) {
	switch decision.Effect {
	case health.EffectCooldownCredential:
		mutate := func() {
			until := decision.CooldownUntil
			exists, changed := false, false
			if credentialVersion == 0 {
				exists, changed = handler.registry.SetCooldownWithChange(credentialID, until)
			} else {
				exists, changed = handler.registry.SetCooldownWithChangeIfVersion(
					credentialID,
					credentialVersion,
					until,
				)
			}
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
	case health.EffectRecordCredentialFailure:
		handler.mutations.Do(credentialID, func() {
			count, ok := handler.registry.IncrFailure(credentialID)
			if !ok {
				return
			}
			becameBlacklisted := false
			if blacklistThreshold > 0 && count >= blacklistThreshold {
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

func retryAttemptLimit(group state.GroupView) int {
	if group.RetryCount <= 0 {
		return 1
	}
	maximum := int(^uint(0) >> 1)
	if group.RetryCount >= maximum {
		return maximum
	}
	return group.RetryCount + 1
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

	quotaAdmission := requestAccessQuotaAdmission{accessKeyID: accessKey.ID}
	if len(accessKey.CostLimitRules) > 0 {
		quotaAdmission.snapshot = snapshot
	}
	var recorder *requestRecorder
	if selectedRoute.Kind == endpointForward {
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
			if quotaAdmission.admitted && handler.accessQuota != nil {
				completion := handler.accessQuota.Complete(
					quotaAdmission.ticket,
					recorder.estimatedCostNanoUSD(),
				)
				handler.logAccessQuotaCompletionFault(accessKey.ID, completion)
			}
			recorder.emit()
		}()
	}

	if handler.accessQuota != nil {
		quotaDecision := accessquota.Decision{}
		if quotaAdmission.snapshot == nil {
			quotaDecision = handler.accessQuota.Check(accessKey.ID, handler.quotaNow())
		} else {
			var current bool
			quotaDecision, current = handler.checkAccessQuotaForSnapshot(
				quotaAdmission.snapshot,
				accessKey.ID,
				handler.quotaNow(),
			)
			if !current {
				handler.completeConfigurationChanged(ginContext, recorder)
				return
			}
		}
		if !quotaDecision.Allowed {
			handler.completeAccessQuotaReason(ginContext, recorder, quotaDecision)
			return
		}
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
		ClientProtocol:           selectedRoute.Protocol,
		Operation:                metadata.Operation,
		RouteRequirement:         metadata.RouteRequirement,
		ResponsesStorePreference: metadata.ResponsesStorePreference,
		ExternalModel:            metadata.Model,
		AccessKey:                accessKey,
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
	recorder.setPricingMode(metadata.PricingMode)
	recorder.setUsageDiagnostics(metadata.UsageDiagnostics)

	allowedCredentialIDs := make(map[uint]struct{}, len(allowedCredentialRefs))
	for credentialID := range allowedCredentialRefs {
		allowedCredentialIDs[credentialID] = struct{}{}
	}
	query.AllowedCredentialIDs = allowedCredentialIDs
	requestAffinity := handler.resolveRequestAffinity(
		snapshot,
		accessKey.ID,
		selectedRoute.Protocol,
		metadata.AffinityPrefix,
		allowedCredentialRefs,
	)
	query.PreferredCredentialID = requestAffinity.preferredCredentialID
	iterator := scheduler.New(snapshot, handler.registry, query, handler.newRandom())
	handler.executeAttempts(
		ginContext,
		iterator,
		allowedCredentialRefs,
		selectedDialect,
		parsed,
		model,
		metadata,
		requestAffinity,
		recorder,
		&quotaAdmission,
	)
}

func (handler *Handler) quotaNow() time.Time {
	if handler != nil && handler.now != nil {
		return handler.now()
	}
	return time.Now()
}

func (handler *Handler) checkAccessQuotaForSnapshot(
	snapshot *state.ConfigSnapshot,
	accessKeyID uint,
	now time.Time,
) (accessquota.Decision, bool) {
	var decision accessquota.Decision
	if handler == nil || handler.manager == nil || handler.accessQuota == nil || snapshot == nil {
		return decision, false
	}
	current := handler.manager.WithCurrentSnapshotRead(func(currentSnapshot *state.ConfigSnapshot) bool {
		if currentSnapshot != snapshot {
			return false
		}
		decision = handler.accessQuota.Check(accessKeyID, now)
		return true
	})
	return decision, current
}

func (handler *Handler) admitAccessQuotaForSnapshot(
	snapshot *state.ConfigSnapshot,
	accessKeyID uint,
	now time.Time,
) (accessquota.Ticket, accessquota.Decision, bool) {
	var ticket accessquota.Ticket
	var decision accessquota.Decision
	if handler == nil || handler.manager == nil || handler.accessQuota == nil || snapshot == nil {
		return ticket, decision, false
	}
	current := handler.manager.WithCurrentSnapshotRead(func(currentSnapshot *state.ConfigSnapshot) bool {
		if currentSnapshot != snapshot {
			return false
		}
		ticket, decision = handler.accessQuota.Admit(accessKeyID, now)
		return true
	})
	return ticket, decision, current
}

func (handler *Handler) logAccessQuotaCompletionFault(
	accessKeyID uint,
	completion accessquota.CompletionResult,
) {
	if completion.Fault == "" {
		return
	}
	utils.LogPlaneBestEffort(
		handler.logger,
		logrus.ErrorLevel,
		utils.LogPlaneData,
		logrus.Fields{
			"access_key_id": accessKeyID,
			"failure_type":  string(completion.Fault),
		},
		"Access key cost limit accounting saturated",
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
	originalMetadata dialect.RequestMetadata,
	requestAffinity requestAffinity,
	recorder *requestRecorder,
	quotaAdmission *requestAccessQuotaAdmission,
) {
	stream := originalMetadata.Stream
	operation := originalMetadata.Operation
	type deferredAttempt struct {
		result        UpstreamResult
		decision      health.Decision
		upstreamModel string
		attemptIndex  int
	}
	var lastResponse *deferredAttempt
	var lastTransport *deferredAttempt
	var lastConversion *deferredAttempt
	var lastProviderError *deferredAttempt
	lastAttemptIndex := -1
	attemptSequence := 0
	forwardAttempts := 0
	forwardAttemptLimit := 1
	retryPolicyResolved := false
	type credentialRefreshRetry struct {
		selection scheduler.Selection
		ref       state.CredentialRef
	}
	var refreshRetry *credentialRefreshRetry
	authRefreshReplayUsed := false
	type preparedRequest struct {
		request  *dialect.ParsedRequest
		metadata dialect.RequestMetadata
		applied  bool
		err      error
	}
	preparedByGroup := make(map[uint]preparedRequest)
	loggedOverrideRejections := make(map[uint]struct{})
	parameterOverrideRejected := false
	prepareRequest := func(selection scheduler.Selection) preparedRequest {
		if prepared, exists := preparedByGroup[selection.GroupID]; exists {
			return prepared
		}
		prepared := preparedRequest{request: parsed, metadata: originalMetadata}
		body, applied, err := selection.Group.ParameterOverrides.Apply(
			selectedDialect.Protocol(),
			originalMetadata.Operation,
			externalModel,
			parsed.Body,
		)
		if err != nil {
			prepared.err = err
			preparedByGroup[selection.GroupID] = prepared
			return prepared
		}
		if !applied {
			preparedByGroup[selection.GroupID] = prepared
			return prepared
		}
		prepared.applied = true
		if int64(len(body)) > maxRequestBodyBytes {
			prepared.err = errRequestTooLarge
			preparedByGroup[selection.GroupID] = prepared
			return prepared
		}
		request := &dialect.ParsedRequest{
			Method: parsed.Method, Path: parsed.Path, RawQuery: parsed.RawQuery,
			Header: parsed.Header.Clone(), Body: body,
		}
		metadata, err := selectedDialect.InspectRequest(request)
		if err != nil || metadata.Operation != originalMetadata.Operation ||
			metadata.Stream != originalMetadata.Stream ||
			metadata.ResponsesStorePreference != originalMetadata.ResponsesStorePreference ||
			!sameOptionalString(metadata.Model, originalMetadata.Model) {
			prepared.err = fmt.Errorf("inspect overridden request")
			preparedByGroup[selection.GroupID] = prepared
			return prepared
		}
		prepared.request = request
		prepared.metadata = metadata
		preparedByGroup[selection.GroupID] = prepared
		return prepared
	}
	decisionContextForSelection := func(selection scheduler.Selection) health.DecisionContext {
		defaultRateLimitCooldown := fixedCooldown
		credentialRefreshable := false
		if connection.Normalize(selection.Group.ConnectionType) == connection.Subscription {
			defaultRateLimitCooldown = subscriptionruntime.DefaultRefreshFailureCooldown
			credentialRefreshable = true
		}
		method := ""
		if parsed != nil {
			method = parsed.Method
		}
		return health.DecisionContext{
			DefaultRateLimitCooldown: defaultRateLimitCooldown,
			CredentialRefreshable:    credentialRefreshable,
			Method:                   method,
			Operation:                operation,
		}
	}
	recordCandidatePreparationFailure := func(
		selection scheduler.Selection,
		attemptMetadata dialect.RequestMetadata,
		code string,
		summary string,
		scope execution.ErrorScope,
	) bool {
		attemptSequence++
		if attemptSequence == 1 && requestAffinity.preferredCredentialID != 0 &&
			selection.CredentialID == requestAffinity.preferredCredentialID {
			recorder.setAffinityHit(true)
		}
		updateDebugHeaders(ginContext.Writer.Header(), selection.Group.Name, attemptSequence)
		if recorder != nil {
			recorder.freezeNextAttemptPricing(
				handler.freezeAttemptPricing(selection, attemptMetadata),
			)
		}
		attemptStarted := recorder.beforeForward()
		evidence := execution.ErrorEvidence{
			Kind: execution.ErrorKindInternal, OriginHint: execution.ErrorOriginInternal,
			ScopeHint: scope, Code: code, Summary: summary,
		}
		result := UpstreamResult{
			Err:           fmt.Errorf("%w: candidate preparation failed", ErrUpstreamProtocol),
			DispatchState: execution.DispatchNotSent, ExecutionError: &evidence,
			ErrorSummary: summary,
		}
		attemptCompleted := time.Time{}
		if recorder != nil {
			attemptCompleted = recorder.now()
		}
		attemptNow := handler.now()
		decision := judgeUpstreamResult(
			result,
			attemptNow,
			decisionContextForSelection(selection),
		)
		recordedAttempt := recorder.recordAttempt(
			selection, nil, result, decision, attemptStarted, attemptCompleted,
		)
		lastAttemptIndex = recordedAttempt
		handler.applyGroupDecisionEffect(selection.Group, selection.CredentialID, 0, decision, 0, attemptNow)
		if decision.Effect == health.EffectSkipGroup {
			iterator.SkipGroup(selection.GroupID)
		}
		lastTransport = &deferredAttempt{
			result: result, decision: decision,
			upstreamModel: optionalModelValue(selection.UpstreamModelID),
			attemptIndex:  recordedAttempt,
		}
		if decision.Retry != health.RetryNone {
			recorder.retryIfAnotherForward(recordedAttempt)
		}
		return decision.Retry != health.RetryNone
	}
	for forwardAttempts < forwardAttemptLimit {
		if ginContext.Request.Context().Err() != nil {
			recorder.completeCanceled(ginContext.Request.Context(), 0, lastAttemptIndex)
			return
		}
		forceCredentialRefresh := false
		var selection scheduler.Selection
		var ref state.CredentialRef
		if refreshRetry != nil {
			selection = refreshRetry.selection
			currentRef, exists := handler.registry.CredentialRef(selection.CredentialID)
			if !exists || currentRef.GroupID != selection.GroupID ||
				currentRef.IdentityGeneration != refreshRetry.ref.IdentityGeneration ||
				currentRef.EncryptedProxy != refreshRetry.ref.EncryptedProxy ||
				currentRef.ProxyFingerprint != refreshRetry.ref.ProxyFingerprint {
				refreshRetry = nil
				continue
			}
			ref = currentRef
			authRefreshReplayUsed = true
			forceCredentialRefresh = currentRef.Version <= refreshRetry.ref.Version
			refreshRetry = nil
		} else {
			var err error
			selection, err = iterator.Next()
			if errors.Is(err, scheduler.ErrExhausted) {
				break
			}
			if err != nil {
				break
			}
			candidateRef, allowed := allowedCredentialRefs[selection.CredentialID]
			if !allowed || candidateRef.GroupID != selection.GroupID {
				continue
			}
			ref = candidateRef
		}
		encrypted, active := handler.registry.ActiveEncryptedCredentialDataIfMatch(ref)
		if !active {
			continue
		}
		prepared := prepareRequest(selection)
		if prepared.err != nil ||
			(prepared.applied &&
				!prepared.metadata.RouteRequirement.Allows(execution.RouteMode(selection.RouteMode))) {
			parameterOverrideRejected = true
			if _, logged := loggedOverrideRejections[selection.GroupID]; !logged {
				reason := "invalid_final_request"
				if prepared.err == nil {
					reason = "route_incompatible"
				}
				utils.LogPlaneBestEffort(
					handler.logger,
					logrus.WarnLevel,
					utils.LogPlaneData,
					logrus.Fields{"group_id": selection.GroupID, "reason": reason},
					"Parameter override rejected an upstream Group",
				)
				loggedOverrideRejections[selection.GroupID] = struct{}{}
			}
			iterator.SkipGroup(selection.GroupID)
			continue
		}
		attemptMetadata := prepared.metadata
		if !retryPolicyResolved {
			// A request can fail over across Groups. Freeze the first active
			// candidate's effective Group policy for the whole retry chain.
			forwardAttemptLimit = retryAttemptLimit(selection.Group)
			retryPolicyResolved = true
		}
		decryptedCredential, err := handler.encryption.Decrypt(encrypted)
		if err != nil {
			if !recordCandidatePreparationFailure(
				selection,
				attemptMetadata,
				"credential_decrypt_failed",
				"Stored credential could not be decrypted.",
				execution.ErrorScopeCredential,
			) {
				break
			}
			continue
		}
		normalizedCredential, err := normalizeChannelCredential(
			handler.channels,
			handler.subscriptions,
			selection.ChannelID,
			selection.Group.ConnectionType,
			decryptedCredential,
		)
		if err != nil {
			if !recordCandidatePreparationFailure(
				selection,
				attemptMetadata,
				"credential_normalization_failed",
				"Stored credential could not be prepared.",
				execution.ErrorScopeCredential,
			) {
				break
			}
			continue
		}
		effectiveProxy, proxyFingerprint, err := resolveAttemptProxy(
			handler.encryption,
			selection.Group.Proxy,
			ref,
		)
		if err != nil {
			code := "group_proxy_prepare_failed"
			summary := "Group proxy configuration could not be prepared."
			scope := execution.ErrorScopeGroup
			if ref.EncryptedProxy != "" || ref.ProxyFingerprint != "" {
				code = "credential_proxy_prepare_failed"
				summary = "Credential proxy configuration could not be prepared."
				scope = execution.ErrorScopeCredential
			}
			if !recordCandidatePreparationFailure(selection, attemptMetadata, code, summary, scope) {
				break
			}
			continue
		}
		if quotaAdmission != nil && !quotaAdmission.admitted && handler.accessQuota != nil {
			var ticket accessquota.Ticket
			var decision accessquota.Decision
			if quotaAdmission.snapshot == nil {
				ticket, decision = handler.accessQuota.Admit(
					quotaAdmission.accessKeyID,
					handler.quotaNow(),
				)
			} else {
				var current bool
				ticket, decision, current = handler.admitAccessQuotaForSnapshot(
					quotaAdmission.snapshot,
					quotaAdmission.accessKeyID,
					handler.quotaNow(),
				)
				if !current {
					handler.completeConfigurationChanged(ginContext, recorder)
					return
				}
			}
			if !decision.Allowed {
				handler.completeAccessQuotaReason(ginContext, recorder, decision)
				return
			}
			quotaAdmission.ticket = ticket
			quotaAdmission.admitted = true
		}

		attemptSequence++
		forwardAttempts++
		if attemptSequence == 1 && requestAffinity.preferredCredentialID != 0 &&
			selection.CredentialID == requestAffinity.preferredCredentialID {
			recorder.setAffinityHit(true)
		}
		updateDebugHeaders(ginContext.Writer.Header(), selection.Group.Name, attemptSequence)
		executionRequestID := "untracked"
		if recorder != nil && recorder.requestID != "" {
			executionRequestID = recorder.requestID
		}
		input := ForwardInput{
			Dialect: selectedDialect, ObserveUsage: attemptMetadata.ObserveUsage,
			Group: selection.Group, APIKey: normalizedCredential.apiKey,
			CredentialSecrets: normalizedCredential.secrets, Request: prepared.request,
			ExternalModel:            externalModel,
			UpstreamModelID:          optionalModelValue(selection.UpstreamModelID),
			RequestID:                executionRequestID,
			AttemptID:                executionRequestID + ":" + strconv.Itoa(attemptSequence),
			AttemptSequence:          uint32(attemptSequence),
			ClientProtocol:           selectedDialect.Protocol(),
			Operation:                attemptMetadata.Operation,
			RouteRequirement:         attemptMetadata.RouteRequirement,
			ResponsesStoreDowngraded: selection.ResponsesStoreDowngraded,
			ChannelID:                string(selection.ChannelID),
			RouteMode:                execution.RouteMode(selection.RouteMode),
			TargetConfig:             selection.ResolvedTarget.TargetConfig,
			Credential: execution.NewCredentialSnapshot(
				selection.CredentialID,
				ref.Version,
				ref.IdentityGeneration,
				normalizedCredential.payload,
			),
			Proxy:                  effectiveProxy,
			ProxyFingerprint:       proxyFingerprint,
			ForceCredentialRefresh: forceCredentialRefresh,
			ContinuityKey:          requestAffinity.continuityKey,
			OnFirstResponse: func() {
				recorder.recordFirstResponse()
			},
		}
		if recorder != nil {
			recorder.freezeNextAttemptPricing(
				handler.freezeAttemptPricing(selection, attemptMetadata),
			)
		}
		attemptStarted := recorder.beforeForward()
		var result UpstreamResult
		if stream {
			result = handler.forwarder.ForwardStream(ginContext.Request.Context(), input, ginContext.Writer)
		} else {
			result = handler.forwarder.Forward(ginContext.Request.Context(), input)
		}
		result = normalizeUpstreamResultContract(result)
		attemptCompleted := time.Time{}
		if recorder != nil {
			attemptCompleted = recorder.now()
		}
		requestCanceled := ginContext.Request.Context().Err() != nil
		if stream && result.Committed && result.Stream.EndReason == StreamEndNone {
			result.Stream = prioritizeStreamObservation(
				ginContext.Request.Context(),
				result.Err,
				result.Stream,
			)
		}
		attemptNow := handler.now()
		resultForDecision := result
		if requestCanceled {
			resultForDecision.Err = ginContext.Request.Context().Err()
		}
		decision := judgeUpstreamResult(
			resultForDecision,
			attemptNow,
			decisionContextForSelection(selection),
		)
		if result.Committed {
			if recorder != nil {
				recordedAttempt := recorder.recordStreamAttempt(
					selection, normalizedCredential.secrets, result, decision, attemptStarted, attemptCompleted,
				)
				recorder.completeStream(result, optionalModelValue(selection.UpstreamModelID), recordedAttempt)
			}
			handler.applyGroupDecisionEffect(
				selection.Group,
				selection.CredentialID,
				0,
				decision,
				result.StatusCode,
				attemptNow,
			)
			if stream && result.Stream.EndReason == StreamEndCleanEOF {
				handler.recordCredentialSuccess(selection.CredentialID, attemptNow)
				handler.recordAffinitySuccess(requestAffinity, selection, ref)
			}
			return
		}
		if requestCanceled {
			if recorder != nil {
				recordedAttempt := recorder.recordAttempt(
					selection,
					normalizedCredential.secrets,
					result,
					decision,
					attemptStarted,
					attemptCompleted,
				)
				recorder.completeCanceled(ginContext.Request.Context(), 0, recordedAttempt)
			}
			return
		}
		if !stream && result.DispatchState != execution.DispatchLocal &&
			!result.ProviderErrorBeforeCommit && result.HasResponse() &&
			result.StatusCode >= http.StatusOK &&
			result.StatusCode < http.StatusMultipleChoices {
			handler.recordCredentialSuccess(selection.CredentialID, attemptNow)
		}
		recordedAttempt := recorder.recordAttempt(
			selection, normalizedCredential.secrets, result, decision, attemptStarted, attemptCompleted,
		)
		lastAttemptIndex = recordedAttempt
		handler.applyGroupDecisionEffect(
			selection.Group,
			selection.CredentialID,
			refreshCooldownCredentialVersion(result, ref.Version),
			decision,
			result.StatusCode,
			attemptNow,
		)
		if decision.Retry == health.RetryRefreshCredential &&
			!authRefreshReplayUsed && forwardAttempts < forwardAttemptLimit {
			refreshRetry = &credentialRefreshRetry{selection: selection, ref: ref}
		}
		if decision.Effect == health.EffectSkipGroup {
			iterator.SkipGroup(selection.GroupID)
		}
		if result.StatusCode >= http.StatusContinue && result.StatusCode < http.StatusOK {
			recorder.completeTransport(
				reasonUpstreamProtocol,
				optionalModelValue(selection.UpstreamModelID),
				recordedAttempt,
			)
			if err := handler.writeReason(ginContext, reasonUpstreamProtocol); err != nil {
				handler.completeWriteTerminal(ginContext, recorder, reasonUpstreamProtocol.Status)
			}
			return
		}
		if result.ProviderErrorBeforeCommit {
			if decision.Retry != health.RetryNone {
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
			if decision.Retry != health.RetryNone {
				recorder.retryIfAnotherForward(recordedAttempt)
				continue
			}
			recorder.completeResponse(result, decision, optionalModelValue(selection.UpstreamModelID), recordedAttempt)
			if err := handler.writeUpstreamResponse(ginContext, result); err != nil {
				handler.completeWriteTerminal(ginContext, recorder, result.StatusCode)
				return
			}
			if result.DispatchState != execution.DispatchLocal &&
				result.StatusCode >= http.StatusOK && result.StatusCode < http.StatusMultipleChoices {
				handler.recordAffinitySuccess(requestAffinity, selection, ref)
			}
			return
		}
		if errors.Is(result.Err, context.Canceled) {
			recorder.completeCanceled(ginContext.Request.Context(), 0, recordedAttempt)
			return
		}
		if decision.Retry != health.RetryNone {
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
	if parameterOverrideRejected && lastAttemptIndex < 0 {
		handler.completeReason(ginContext, recorder, reasonParameterOverrideUnavailable)
		return
	}
	handler.completeReason(ginContext, recorder, reasonNoCandidate)
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
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
