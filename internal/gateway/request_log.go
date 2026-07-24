package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"gpt-load/internal/health"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/telemetry"
)

const (
	maxRequestLogSummaryBytes = 1024
	requestLogTruncatedMarker = "...[truncated]"
)

type requestOutcome struct {
	status        telemetry.RequestStatus
	statusCode    int
	errorCode     string
	errorSummary  string
	upstreamModel string
}

type requestRecorder struct {
	sink        telemetry.RequestLogSink
	requestID   string
	startedAt   time.Time
	accessKeyID uint
	protocol    protocol.Protocol
	clientModel string
	attempts    []telemetry.Attempt
	outcome     requestOutcome
	now         func() time.Time
	emitted     bool

	pendingRetry int
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
		pendingRetry: -1,
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
	recorder.sink.Emit(telemetry.RequestEvent{
		RequestID:     recorder.requestID,
		CompletedAt:   completedAt.UTC(),
		AccessKeyID:   recorder.accessKeyID,
		Protocol:      recorder.protocol,
		ClientModel:   recorder.clientModel,
		UpstreamModel: recorder.outcome.upstreamModel,
		Status:        recorder.outcome.status,
		StatusCode:    recorder.outcome.statusCode,
		ErrorCode:     recorder.outcome.errorCode,
		ErrorSummary:  recorder.outcome.errorSummary,
		DurationMs:    duration.Milliseconds(),
		AffinityHit:   false,
		Attempts:      append([]telemetry.Attempt(nil), recorder.attempts...),
	})
}

func (recorder *requestRecorder) setClientModel(model string) {
	if recorder != nil {
		recorder.clientModel = model
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
	apiKey string,
	result UpstreamResult,
	decision health.Result,
	startedAt time.Time,
	completedAt time.Time,
) int {
	if recorder == nil {
		return -1
	}
	return recorder.appendAttempt(
		selection,
		apiKey,
		result,
		telemetryFailureCategory(decision.Category),
		telemetryAction(decision.Action),
		upstreamErrorCode(result, decision.Category),
		result.ErrorSummary,
		startedAt,
		completedAt,
	)
}

func (recorder *requestRecorder) recordStreamAttempt(
	selection scheduler.Selection,
	apiKey string,
	result UpstreamResult,
	startedAt time.Time,
	completedAt time.Time,
) int {
	if recorder == nil {
		return -1
	}
	category, action := streamAttemptObservation(result)
	return recorder.appendAttempt(
		selection,
		apiKey,
		result,
		category,
		action,
		streamErrorCode(result.Stream.EndReason),
		result.Stream.ErrorSummary,
		startedAt,
		completedAt,
	)
}

func (recorder *requestRecorder) appendAttempt(
	selection scheduler.Selection,
	apiKey string,
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
		Sequence:        len(recorder.attempts) + 1,
		GroupID:         selection.GroupID,
		GroupName:       selection.Group.Name,
		KeyID:           selection.KeyID,
		KeyMask:         utils.MaskAPIKey(apiKey),
		UpstreamModel:   selection.UpstreamModelID,
		StatusCode:      result.StatusCode,
		DurationMs:      duration.Milliseconds(),
		FailureCategory: category,
		Action:          action,
		ErrorCode:       errorCode,
		ErrorSummary:    errorSummary,
		Committed:       result.Committed,
	}
	if attempt.ErrorCode != "" && attempt.ErrorSummary == "" {
		attempt.ErrorSummary = fixedErrorSummary(attempt.ErrorCode)
	}
	recorder.attempts = append(recorder.attempts, attempt)
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
) {
	if recorder == nil {
		return
	}
	code := streamErrorCode(result.Stream.EndReason)
	summary := result.Stream.ErrorSummary
	if code != "" && summary == "" {
		summary = fixedErrorSummary(code)
	}
	outcome := requestOutcome{
		statusCode:    result.StatusCode,
		errorCode:     code,
		errorSummary:  summary,
		upstreamModel: upstreamModel,
	}
	switch result.Stream.EndReason {
	case StreamEndCleanEOF:
		outcome.status = telemetry.RequestStatusSuccess
	case StreamEndSSEError:
		outcome.status = telemetry.RequestStatusError
	case StreamEndClientCanceled:
		outcome.status = telemetry.RequestStatusCanceled
	default:
		outcome.status = telemetry.RequestStatusIncomplete
	}
	recorder.outcome = outcome
}

func (recorder *requestRecorder) completeResponse(
	result UpstreamResult,
	decision health.Result,
	upstreamModel string,
) {
	if recorder == nil {
		return
	}
	if result.StatusCode >= 200 && result.StatusCode < 300 {
		recorder.outcome = requestOutcome{
			status: telemetry.RequestStatusSuccess, statusCode: result.StatusCode,
			upstreamModel: upstreamModel,
		}
		return
	}
	code := upstreamErrorCode(result, decision.Category)
	summary := result.ErrorSummary
	if summary == "" {
		summary = fixedErrorSummary(code)
	}
	recorder.outcome = requestOutcome{
		status: telemetry.RequestStatusError, statusCode: result.StatusCode,
		errorCode: code, errorSummary: summary, upstreamModel: upstreamModel,
	}
}

func (recorder *requestRecorder) completeTransport(value reason, upstreamModel string) {
	if recorder == nil {
		return
	}
	recorder.outcome = requestOutcome{
		status: telemetry.RequestStatusError, statusCode: value.Status,
		errorCode: value.Code, errorSummary: value.Message, upstreamModel: upstreamModel,
	}
}

func (recorder *requestRecorder) completeCanceled(status int) {
	if recorder == nil {
		return
	}
	recorder.outcome = requestOutcome{
		status: telemetry.RequestStatusCanceled, statusCode: status,
		errorCode: "client_canceled", errorSummary: fixedErrorSummary("client_canceled"),
	}
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
	case health.ActionCooldownKey:
		return telemetry.ActionCooldownKey
	case health.ActionFailKey:
		return telemetry.ActionFailKey
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
	case "client_canceled":
		return "The client canceled the request."
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
	summary = strings.ToValidUTF8(summary, "\uFFFD")
	summary = strings.Join(strings.Fields(summary), " ")
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

func allowedErrorSummary(body []byte) string {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return ""
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ""
	}
	if nested, ok := payload["error"].(map[string]any); ok {
		if value, ok := nested["message"].(string); ok && value != "" {
			return value
		}
		if value, ok := nested["detail"].(string); ok && value != "" {
			return value
		}
	}
	if value, ok := payload["message"].(string); ok && value != "" {
		return value
	}
	if value, ok := payload["detail"].(string); ok && value != "" {
		return value
	}
	if value, ok := payload["error"].(string); ok && value != "" {
		return value
	}
	return ""
}
