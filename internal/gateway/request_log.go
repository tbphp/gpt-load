package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/reasoning"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

const (
	maxRequestLogSummaryBytes = 4096
	requestLogTruncatedMarker = "...[truncated]"
)

type requestOutcome struct {
	status                telemetry.RequestStatus
	statusCode            int
	errorCode             string
	errorSummary          string
	upstreamModel         string
	upstreamReportedModel string
	responseModelObserved bool
	responseModelMismatch bool
}

type frozenAttemptPricing struct {
	channelID     string
	groupID       uint
	upstreamModel string
	table         *pricing.Table
	applicable    bool
}

type requestRecorder struct {
	sink             telemetry.RequestLogSink
	requestID        string
	startedAt        time.Time
	accessKeyID      uint
	protocol         protocol.Protocol
	operation        execution.Operation
	clientModel      string
	stream           bool
	firstResponseMs  *int64
	reasoning        reasoning.Config
	usageApplicable  bool
	usageDiagnostics usage.Diagnostics
	attempts         []telemetry.Attempt
	attemptPricing   []frozenAttemptPricing
	pendingPricing   frozenAttemptPricing
	pricingPending   bool
	outcome          requestOutcome
	usage            telemetry.UsageObservation
	now              func() time.Time
	redactor         *redact.Redactor
	emitted          bool

	pendingRetry int
}

func (recorder *requestRecorder) freezeNextAttemptPricing(frozen frozenAttemptPricing) {
	if recorder == nil {
		return
	}
	recorder.pendingPricing = frozen
	recorder.pricingPending = true
}

func newRequestRecorder(
	sink telemetry.RequestLogSink,
	requestID string,
	startedAt time.Time,
	accessKeyID uint,
	value protocol.Protocol,
	now func() time.Time,
) *requestRecorder {
	return &requestRecorder{
		sink: sink, requestID: requestID, startedAt: startedAt,
		accessKeyID: accessKeyID, protocol: value, now: now,
		pendingRetry:    -1,
		redactor:        redact.New(),
		usageApplicable: true,
		usage: telemetry.UsageObservation{
			Result: usage.Result{State: usage.StateNotApplicable},
			Pricing: telemetry.PricingObservation{
				CostState:           string(pricing.CostStateNotApplicable),
				PricingCompleteness: string(pricing.CompletenessNotApplicable),
			},
		},
	}
}

func (recorder *requestRecorder) emit() {
	if recorder == nil || recorder.emitted || recorder.requestID == "" ||
		recorder.sink == nil || recorder.now == nil {
		return
	}
	recorder.emitted = true
	completedAt := recorder.now()
	duration := completedAt.Sub(recorder.startedAt)
	if duration < 0 {
		duration = 0
	}
	reportedModel, modelConsistency := requestOutcomeModelConsistency(recorder.outcome)
	recorder.sink.Emit(telemetry.RequestEvent{
		RequestID:             recorder.requestID,
		CompletedAt:           completedAt.UTC(),
		AccessKeyID:           recorder.accessKeyID,
		Protocol:              recorder.protocol,
		ClientModel:           recorder.clientModel,
		UpstreamModel:         recorder.outcome.upstreamModel,
		UpstreamReportedModel: reportedModel,
		ModelConsistency:      modelConsistency,
		Status:                recorder.outcome.status,
		StatusCode:            recorder.outcome.statusCode,
		ErrorCode:             recorder.outcome.errorCode,
		ErrorSummary:          recorder.outcome.errorSummary,
		Stream:                recorder.stream,
		FirstResponseMs:       recorder.firstResponseMs,
		DurationMs:            duration.Milliseconds(),
		AffinityHit:           false,
		Reasoning:             recorder.reasoning,
		Operation:             recorder.operation,
		Attempts:              append([]telemetry.Attempt(nil), recorder.attempts...),
		Usage:                 recorder.usage,
	})
}

func requestOutcomeModelConsistency(
	outcome requestOutcome,
) (string, telemetry.ModelConsistency) {
	if outcome.status != telemetry.RequestStatusSuccess || outcome.upstreamModel == "" {
		return "", telemetry.ModelConsistencyNotApplicable
	}
	if !outcome.responseModelObserved || outcome.upstreamReportedModel == "" {
		return "", telemetry.ModelConsistencyUnknown
	}
	if outcome.responseModelMismatch || outcome.upstreamReportedModel != outcome.upstreamModel {
		return outcome.upstreamReportedModel, telemetry.ModelConsistencyMismatch
	}
	return outcome.upstreamReportedModel, telemetry.ModelConsistencyMatch
}

func (recorder *requestRecorder) setClientModel(model string) {
	if recorder != nil {
		recorder.clientModel = model
	}
}

func (recorder *requestRecorder) setOperation(operation execution.Operation) {
	if recorder != nil {
		recorder.operation = operation
	}
}

func (recorder *requestRecorder) setStream(stream bool) {
	if recorder != nil {
		recorder.stream = stream
	}
}

func (recorder *requestRecorder) setReasoning(config reasoning.Config) {
	if recorder == nil {
		return
	}
	if config.BudgetTokens != nil {
		budget := *config.BudgetTokens
		config.BudgetTokens = &budget
	}
	recorder.reasoning = config
}

func (recorder *requestRecorder) recordFirstResponse() {
	if recorder == nil || !recorder.stream || recorder.firstResponseMs != nil || recorder.now == nil {
		return
	}
	duration := recorder.now().Sub(recorder.startedAt)
	if duration < 0 {
		duration = 0
	}
	value := duration.Milliseconds()
	recorder.firstResponseMs = &value
}

func (recorder *requestRecorder) setUsageApplicable(applicable bool) {
	if recorder != nil {
		recorder.usageApplicable = applicable
	}
}

func (recorder *requestRecorder) setUsageDiagnostics(diagnostics usage.Diagnostics) {
	if recorder != nil {
		recorder.usageDiagnostics.Merge(diagnostics)
	}
}

func (recorder *requestRecorder) beforeForward() time.Time {
	if recorder == nil || recorder.now == nil {
		return time.Time{}
	}
	if recorder.pendingRetry >= 0 && recorder.pendingRetry < len(recorder.attempts) {
		recorder.attempts[recorder.pendingRetry].WillRetry = true
		recorder.pendingRetry = -1
	}
	return recorder.now()
}

func (recorder *requestRecorder) recordAttempt(
	selection scheduler.Selection,
	credentialSecrets []string,
	result UpstreamResult,
	decision health.Result,
	startedAt time.Time,
	completedAt time.Time,
) int {
	if recorder == nil {
		return -1
	}
	rules := state.HeaderRules{}
	if result.HasResponse() && !result.ProviderErrorBeforeCommit {
		rules = selection.Group.HeaderRules
	}
	summarySecrets := resolvedErrorSummarySecretValues(
		"",
		rules,
		credentialSecrets...,
	)
	return recorder.appendAttempt(
		selection,
		result,
		telemetryFailureCategory(decision.Category),
		telemetryAction(decision.Action),
		upstreamErrorCode(result, decision.Category),
		sanitizeErrorSummary(recorder.redactor, result.ErrorSummary, summarySecrets...),
		startedAt,
		completedAt,
	)
}

func (recorder *requestRecorder) recordStreamAttempt(
	selection scheduler.Selection,
	credentialSecrets []string,
	result UpstreamResult,
	startedAt time.Time,
	completedAt time.Time,
) int {
	if recorder == nil {
		return -1
	}
	category, action := streamAttemptObservation(result)
	rules := state.HeaderRules{}
	if result.Stream.EndReason == StreamEndSSEError {
		rules = selection.Group.HeaderRules
	}
	summarySecrets := resolvedErrorSummarySecretValues(
		"",
		rules,
		credentialSecrets...,
	)
	return recorder.appendAttempt(
		selection,
		result,
		category,
		action,
		streamErrorCode(result.Stream.EndReason),
		sanitizeErrorSummary(
			recorder.redactor,
			result.Stream.ErrorSummary,
			summarySecrets...,
		),
		startedAt,
		completedAt,
	)
}

func (recorder *requestRecorder) appendAttempt(
	selection scheduler.Selection,
	result UpstreamResult,
	category telemetry.FailureCategory,
	action telemetry.Action,
	errorCode string,
	errorSummary string,
	startedAt time.Time,
	completedAt time.Time,
) int {
	if recorder == nil {
		return -1
	}
	duration := completedAt.Sub(startedAt)
	if duration < 0 {
		duration = 0
	}
	attempt := telemetry.Attempt{
		Sequence:          len(recorder.attempts) + 1,
		GroupID:           selection.GroupID,
		GroupName:         selection.Group.Name,
		ChannelID:         selection.ChannelID,
		CredentialID:      selection.CredentialID,
		Operation:         recorder.operation,
		RouteMode:         selection.RouteMode,
		UpstreamModel:     optionalModelValue(selection.UpstreamModelID),
		UpstreamRequestID: result.UpstreamRequestID,
		DispatchState:     result.DispatchState,
		ResponseStarted:   result.ResponseStarted,
		UpstreamAPI:       result.UpstreamAPI,
		Reasoning:         result.AppliedReasoning.Clone(),
		StatusCode:        result.StatusCode,
		DurationMs:        duration.Milliseconds(),
		FailureCategory:   category,
		Action:            action,
		ErrorCode:         errorCode,
		ErrorSummary:      errorSummary,
		Committed:         result.Committed,
	}
	if attempt.ErrorCode != "" && attempt.ErrorSummary == "" {
		attempt.ErrorSummary = fixedErrorSummary(attempt.ErrorCode)
	}
	recorder.attempts = append(recorder.attempts, attempt)
	frozen := frozenAttemptPricing{}
	if recorder.pricingPending {
		frozen = recorder.pendingPricing
	}
	recorder.attemptPricing = append(recorder.attemptPricing, frozen)
	recorder.pendingPricing = frozenAttemptPricing{}
	recorder.pricingPending = false
	return len(recorder.attempts) - 1
}

func (recorder *requestRecorder) retryIfAnotherForward(index int) {
	if recorder != nil && index >= 0 && index < len(recorder.attempts) {
		recorder.pendingRetry = index
	}
}

func (recorder *requestRecorder) completeReason(value reason) {
	if recorder == nil {
		return
	}
	recorder.outcome = requestOutcome{
		status: telemetry.RequestStatusError, statusCode: value.Status,
		errorCode: value.Code, errorSummary: value.Message,
	}
}

func (recorder *requestRecorder) completeStream(
	result UpstreamResult,
	upstreamModel string,
	attemptIndex int,
) {
	if recorder == nil {
		return
	}
	code := streamErrorCode(result.Stream.EndReason)
	summary := result.Stream.ErrorSummary
	if attemptIndex >= 0 && attemptIndex < len(recorder.attempts) {
		summary = recorder.attempts[attemptIndex].ErrorSummary
	}
	if code != "" && summary == "" {
		summary = fixedErrorSummary(code)
	}
	outcome := requestOutcome{
		statusCode:            result.StatusCode,
		errorCode:             code,
		errorSummary:          summary,
		upstreamModel:         upstreamModel,
		upstreamReportedModel: result.UpstreamReportedModel,
		responseModelObserved: result.ResponseModelObserved,
		responseModelMismatch: result.ResponseModelMismatch,
	}
	switch result.Stream.EndReason {
	case StreamEndCleanEOF:
		outcome.status = telemetry.RequestStatusSuccess
	case StreamEndSSEError:
		outcome.status = telemetry.RequestStatusError
	case StreamEndClientCanceled, StreamEndServerShutdown:
		outcome.status = telemetry.RequestStatusCanceled
	default:
		outcome.status = telemetry.RequestStatusIncomplete
	}
	recorder.outcome = outcome
	recorder.bindUsage(attemptIndex, result.Usage, true)
}

func (recorder *requestRecorder) completeResponse(
	result UpstreamResult,
	decision health.Result,
	upstreamModel string,
	attemptIndex int,
) {
	if recorder == nil {
		return
	}
	if result.StatusCode >= 200 && result.StatusCode < 300 {
		recorder.outcome = requestOutcome{
			status:                telemetry.RequestStatusSuccess,
			statusCode:            result.StatusCode,
			upstreamModel:         upstreamModel,
			upstreamReportedModel: result.UpstreamReportedModel,
			responseModelObserved: result.ResponseModelObserved,
			responseModelMismatch: result.ResponseModelMismatch,
		}
		recorder.bindUsage(attemptIndex, result.Usage, true)
		return
	}
	code := upstreamErrorCode(result, decision.Category)
	summary := result.ErrorSummary
	if attemptIndex >= 0 && attemptIndex < len(recorder.attempts) {
		summary = recorder.attempts[attemptIndex].ErrorSummary
	}
	if summary == "" {
		summary = fixedErrorSummary(code)
	}
	recorder.outcome = requestOutcome{
		status: telemetry.RequestStatusError, statusCode: result.StatusCode,
		errorCode: code, errorSummary: summary, upstreamModel: upstreamModel,
	}
	recorder.bindUsage(attemptIndex, result.Usage, false)
}

func (recorder *requestRecorder) completeProviderError(
	result UpstreamResult,
	upstreamModel string,
	attemptIndex int,
) {
	if recorder == nil {
		return
	}
	summary := reasonUpstreamProtocol.Message
	if attemptIndex >= 0 && attemptIndex < len(recorder.attempts) && recorder.attempts[attemptIndex].ErrorSummary != "" {
		summary = recorder.attempts[attemptIndex].ErrorSummary
	}
	recorder.outcome = requestOutcome{
		status:        telemetry.RequestStatusError,
		statusCode:    reasonUpstreamProtocol.Status,
		errorCode:     reasonUpstreamProtocol.Code,
		errorSummary:  summary,
		upstreamModel: upstreamModel,
	}
	recorder.bindUsage(
		attemptIndex,
		result.Usage,
		true,
	)
}

func (recorder *requestRecorder) bindUsage(
	attemptIndex int,
	result usage.Result,
	applicable bool,
) {
	if recorder == nil || attemptIndex < 0 || attemptIndex >= len(recorder.attempts) {
		return
	}
	attempt := recorder.attempts[attemptIndex]
	if attempt.GroupID == 0 || attempt.ChannelID == "" || attempt.CredentialID == 0 || attempt.Sequence < 1 {
		return
	}
	if !applicable || !recorder.usageApplicable {
		result = usage.Result{State: usage.StateNotApplicable}
	} else if !validCapturedUsage(result) {
		result = usage.Result{State: usage.StateMissing}
	} else {
		result.Diagnostics.Merge(recorder.usageDiagnostics)
	}
	frozen := frozenAttemptPricing{}
	if attemptIndex < len(recorder.attemptPricing) {
		frozen = recorder.attemptPricing[attemptIndex]
	}
	pricingObservation := quoteFrozenAttempt(frozen, result)
	recorder.usage = telemetry.UsageObservation{
		Result:          result,
		GroupID:         attempt.GroupID,
		ChannelID:       attempt.ChannelID,
		CredentialID:    attempt.CredentialID,
		AttemptSequence: attempt.Sequence,
		Pricing:         pricingObservation,
	}
}

func quoteFrozenAttempt(
	frozen frozenAttemptPricing,
	result usage.Result,
) telemetry.PricingObservation {
	observation := telemetry.PricingObservation{
		UpstreamModel:       frozen.upstreamModel,
		CostState:           string(pricing.CostStateUnpriced),
		PricingCompleteness: string(pricing.CompletenessUnavailable),
	}
	if result.State == usage.StateNotApplicable || !frozen.applicable {
		observation.CostState = string(pricing.CostStateNotApplicable)
		observation.PricingCompleteness = string(pricing.CompletenessNotApplicable)
		return observation
	}
	if frozen.table == nil || frozen.upstreamModel == "" {
		return observation
	}
	quote, receipt := frozen.table.QuoteWithReceipt(pricing.Identity{
		ChannelID: frozen.channelID,
		ModelID:   frozen.upstreamModel,
	}, result)
	observation.CostState = string(quote.State)
	observation.PricingCompleteness = string(quote.Completeness)
	observation.EstimatedCostNanoUSD = int64(quote.EstimatedCostNanoUSD)
	if receipt != nil {
		if encoded, err := json.Marshal(receipt); err == nil {
			observation.ReceiptJSON = string(encoded)
		}
	}
	return observation
}

func (recorder *requestRecorder) completeTransport(
	value reason,
	upstreamModel string,
	attemptIndex int,
) {
	if recorder == nil {
		return
	}
	summary := value.Message
	if attemptIndex >= 0 && attemptIndex < len(recorder.attempts) && recorder.attempts[attemptIndex].ErrorSummary != "" {
		summary = recorder.attempts[attemptIndex].ErrorSummary
	}
	recorder.outcome = requestOutcome{
		status: telemetry.RequestStatusError, statusCode: value.Status,
		errorCode: value.Code, errorSummary: summary, upstreamModel: upstreamModel,
	}
	recorder.bindUsage(attemptIndex, usage.Result{}, false)
}

func (recorder *requestRecorder) completeCanceled(
	ctx context.Context,
	status int,
	attemptIndex int,
) {
	if recorder == nil {
		return
	}
	code := cancellationErrorCode(ctx)
	upstreamModel := recorder.outcome.upstreamModel
	if attemptIndex >= 0 && attemptIndex < len(recorder.attempts) {
		upstreamModel = recorder.attempts[attemptIndex].UpstreamModel
		recorder.attempts[attemptIndex].ErrorCode = code
		recorder.attempts[attemptIndex].ErrorSummary = fixedErrorSummary(code)
	}
	recorder.outcome = requestOutcome{
		status: telemetry.RequestStatusCanceled, statusCode: status,
		errorCode: code, errorSummary: fixedErrorSummary(code),
		upstreamModel: upstreamModel,
	}
	recorder.bindUsage(attemptIndex, usage.Result{}, false)
}

func (recorder *requestRecorder) completeDownstreamWrite(status int) {
	if recorder == nil {
		return
	}
	recorder.outcome.status = telemetry.RequestStatusIncomplete
	recorder.outcome.statusCode = status
	recorder.outcome.errorCode = "downstream_write_failed"
	recorder.outcome.errorSummary = fixedErrorSummary("downstream_write_failed")
}

func (recorder *requestRecorder) completeMissingOutcome(written bool, statusCode int) {
	if recorder == nil || recorder.outcome.status != "" {
		return
	}
	recorder.outcome = requestOutcome{
		status:       telemetry.RequestStatusError,
		statusCode:   http.StatusInternalServerError,
		errorCode:    "internal_error",
		errorSummary: fixedErrorSummary("internal_error"),
	}
	if written {
		recorder.outcome.status = telemetry.RequestStatusIncomplete
		recorder.outcome.statusCode = statusCode
	}
}

func telemetryFailureCategory(value health.FailureCategory) telemetry.FailureCategory {
	switch value {
	case health.FailureCategoryOK:
		return telemetry.FailureCategoryOK
	case health.FailureCategoryRateLimited:
		return telemetry.FailureCategoryRateLimited
	case health.FailureCategoryModelUnavailable:
		return telemetry.FailureCategoryModelUnavailable
	case health.FailureCategoryInvalidKey:
		return telemetry.FailureCategoryInvalidKey
	case health.FailureCategoryUpstreamHostError:
		return telemetry.FailureCategoryUpstreamHost
	case health.FailureCategoryClientError:
		return telemetry.FailureCategoryClientError
	case health.FailureCategoryDownstreamCancel:
		return telemetry.FailureCategoryDownstreamCancel
	default:
		return telemetry.FailureCategoryAmbiguous
	}
}

func telemetryAction(value health.Action) telemetry.Action {
	switch value {
	case health.ActionRetry:
		return telemetry.ActionRetry
	case health.ActionCooldownCredential:
		return telemetry.ActionCooldownCredential
	case health.ActionFailCredential:
		return telemetry.ActionFailCredential
	case health.ActionSkipGroup:
		return telemetry.ActionSkipGroup
	default:
		return telemetry.ActionTerminate
	}
}

func upstreamErrorCode(result UpstreamResult, category health.FailureCategory) string {
	switch {
	case errors.Is(result.Err, context.Canceled):
		return "client_canceled"
	case errors.Is(result.Err, ErrUpstreamProtocol):
		return "upstream_protocol_error"
	case isTimeoutError(result.Err):
		return "upstream_timeout"
	case result.Err != nil && !result.RequestWritten:
		return "upstream_connect_failed"
	case result.Err != nil:
		return "upstream_error"
	}
	switch category {
	case health.FailureCategoryOK:
		return ""
	case health.FailureCategoryRateLimited:
		return "upstream_rate_limited"
	case health.FailureCategoryModelUnavailable:
		return "upstream_model_unavailable"
	case health.FailureCategoryInvalidKey:
		return "upstream_invalid_key"
	case health.FailureCategoryUpstreamHostError:
		return "upstream_host_error"
	case health.FailureCategoryClientError:
		return "upstream_client_error"
	case health.FailureCategoryDownstreamCancel:
		return "client_canceled"
	default:
		return "upstream_error"
	}
}

func fixedErrorSummary(code string) string {
	switch code {
	case "upstream_rate_limited":
		return "Upstream rate limited the request."
	case "upstream_model_unavailable":
		return "The requested upstream model is unavailable."
	case "upstream_invalid_key":
		return "The upstream credential was rejected."
	case "upstream_host_error":
		return "The upstream service returned a server error."
	case "upstream_client_error":
		return "The upstream service rejected the request."
	case "upstream_connect_failed":
		return "Could not connect to an upstream service."
	case "upstream_timeout":
		return "Upstream request timed out."
	case "upstream_protocol_error":
		return "Upstream returned an unsupported response."
	case "upstream_sse_error":
		return "Upstream stream reported an error."
	case "upstream_stream_terminated":
		return "Upstream stream terminated before completion."
	case "upstream_stream_idle_timeout":
		return "Upstream stream timed out while idle."
	case "downstream_write_failed":
		return "The downstream response could not be completed."
	case "internal_error":
		return "The request failed due to an internal error."
	case "client_canceled":
		return "The client canceled the request."
	case "server_shutdown":
		return "The server canceled the request during shutdown."
	default:
		return "Upstream request failed."
	}
}

func summarizeErrorBody(
	redactor *redact.Redactor,
	body []byte,
	fallback string,
	knownSecrets ...string,
) string {
	summary := allowedErrorSummary(body)
	if summary == "" {
		summary = fallback
	}
	return sanitizeErrorSummary(redactor, summary, knownSecrets...)
}

func sanitizeErrorSummary(
	redactor *redact.Redactor,
	summary string,
	knownSecrets ...string,
) string {
	summary = strings.ToValidUTF8(summary, "\uFFFD")
	if redactor != nil {
		summary = redactor.String(summary, knownSecrets...)
	}
	summary = normalizeErrorSummaryWhitespace(summary)
	if redactor != nil {
		summary = redactor.String(summary, knownSecrets...)
	}
	if len(summary) <= maxRequestLogSummaryBytes {
		return summary
	}
	prefixBytes := maxRequestLogSummaryBytes - len(requestLogTruncatedMarker)
	for prefixBytes > 0 && !utf8.ValidString(summary[:prefixBytes]) {
		prefixBytes--
	}
	return summary[:prefixBytes] + requestLogTruncatedMarker
}

func resolvedErrorSummarySecretValues(
	apiKey string,
	rules state.HeaderRules,
	knownSecrets ...string,
) []string {
	secrets := make([]string, 0, len(knownSecrets)+len(rules.Set)*2+1)
	seen := make(map[string]struct{})
	appendSecret := func(secret string) {
		if secret == "" {
			return
		}
		if _, exists := seen[secret]; exists {
			return
		}
		seen[secret] = struct{}{}
		secrets = append(secrets, secret)
		normalized := normalizeErrorSummaryWhitespace(
			strings.ToValidUTF8(secret, "\uFFFD"),
		)
		if normalized == "" || normalized == secret {
			return
		}
		if _, exists := seen[normalized]; exists {
			return
		}
		seen[normalized] = struct{}{}
		secrets = append(secrets, normalized)
	}
	for _, secret := range knownSecrets {
		appendSecret(secret)
	}
	appendSecret(apiKey)
	for _, value := range rules.Set {
		appendSecret(value)
		appendSecret(strings.ReplaceAll(value, "${API_KEY}", apiKey))
	}
	sort.SliceStable(secrets, func(left, right int) bool {
		return len(secrets[left]) > len(secrets[right])
	})
	return secrets
}

func normalizeErrorSummaryWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func allowedErrorSummary(body []byte) string {
	return redact.ExtractErrorMessage(body)
}
