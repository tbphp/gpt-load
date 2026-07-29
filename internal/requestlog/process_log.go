package requestlog

import (
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/pricing"
	"gpt-load/internal/telemetry"
)

const maxProcessSummaryBytes = 200

func projectProcessLog(
	redactor *redact.Redactor,
	event telemetry.RequestEvent,
	prices *pricing.Table,
) (logrus.Level, logrus.Fields, bool) {
	if event.RequestID == "" {
		return logrus.InfoLevel, nil, false
	}

	row := mapEvent(redactor, event, prices)
	groupID, keyID, groupName := attributedAttempt(event)
	groupName = redactIdentityValue(redactor, groupName)
	retryCount := 0
	for _, attempt := range event.Attempts {
		if attempt.WillRetry {
			retryCount++
		}
	}

	estimatedCost := ""
	if row.CostState == string(pricing.CostStatePriced) {
		estimatedCost = strconv.FormatFloat(row.Cost, 'g', 12, 64)
	}
	errorCode := ""
	errorSummary := ""
	if event.Status != telemetry.RequestStatusSuccess {
		errorCode = row.ErrorCode
		errorSummary = sanitizeSummaryLimit(
			redactor,
			event.ErrorSummary,
			maxProcessSummaryBytes,
		)
	}

	level := logrus.WarnLevel
	if event.Status == telemetry.RequestStatusSuccess ||
		event.Status == telemetry.RequestStatusCanceled {
		level = logrus.InfoLevel
	}

	return level, logrus.Fields{
		"event":                 "data_plane_request_completed",
		"request_id":            row.ID,
		"completed_at":          row.CreatedAt.UTC().Format(time.RFC3339Nano),
		"status":                row.Status,
		"status_code":           row.StatusCode,
		"protocol":              row.Protocol,
		"access_key_id":         row.AccessKeyID,
		"client_model":          row.ClientModel,
		"upstream_model":        row.UpstreamModel,
		"group_id":              groupID,
		"key_id":                keyID,
		"group_name":            groupName,
		"duration_ms":           row.DurationMs,
		"attempt_count":         len(event.Attempts),
		"retry_count":           retryCount,
		"affinity_hit":          row.AffinityHit,
		"uncached_input_tokens": row.InputTokens,
		"cache_read_tokens":     row.CacheReadTokens,
		"cache_write_5m_tokens": row.CacheWrite5MTokens,
		"cache_write_1h_tokens": row.CacheWrite1HTokens,
		"output_tokens":         row.OutputTokens,
		"usage_state":           row.UsageState,
		"cost_state":            row.CostState,
		"estimated_cost_usd":    estimatedCost,
		"error_code":            errorCode,
		"error_summary":         errorSummary,
	}, true
}

func attributedAttempt(event telemetry.RequestEvent) (uint, uint, string) {
	for _, attempt := range event.Attempts {
		if attempt.Sequence == event.Usage.AttemptSequence &&
			attempt.GroupID == event.Usage.GroupID &&
			attempt.KeyID == event.Usage.KeyID {
			return attempt.GroupID, attempt.KeyID, attempt.GroupName
		}
	}
	return 0, 0, ""
}

func (service *Service) logCompletedRequest(
	event telemetry.RequestEvent,
	prices *pricing.Table,
) {
	defer func() {
		_ = recover()
	}()
	level, fields, ok := projectProcessLog(service.redactor, event, prices)
	if !ok {
		return
	}
	utils.LogBestEffort(
		service.logger,
		level,
		fields,
		"Data plane request completed",
	)
}
