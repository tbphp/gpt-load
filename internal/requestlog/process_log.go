package requestlog

import (
	"net/http"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/pricing"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
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

	row, err := mapEvent(redactor, event, prices)
	if err != nil {
		return logrus.InfoLevel, nil, false
	}
	groupID, keyID := attributedAttempt(event)

	level := logrus.WarnLevel
	if event.Status == telemetry.RequestStatusSuccess ||
		event.Status == telemetry.RequestStatusCanceled {
		level = logrus.InfoLevel
	}

	fields := logrus.Fields{
		"event":         "data_plane_request_completed",
		"request_id":    row.ID,
		"status":        row.Status,
		"protocol":      row.Protocol,
		"access_key_id": row.AccessKeyID,
		"duration_ms":   row.DurationMs,
	}
	if row.StatusCode != http.StatusOK {
		fields["status_code"] = row.StatusCode
	}
	if row.ClientModel != "" {
		fields["client_model"] = row.ClientModel
	}
	if row.UpstreamModel != "" && row.UpstreamModel != row.ClientModel {
		fields["upstream_model"] = row.UpstreamModel
	}
	if groupID > 0 {
		fields["group_id"] = groupID
	}
	if keyID > 0 {
		fields["key_id"] = keyID
	}
	if attemptCount := len(event.Attempts); attemptCount != 1 {
		fields["attempt_count"] = attemptCount
	}
	if row.UncachedInputTokens > 0 {
		fields["uncached_input_tokens"] = row.UncachedInputTokens
	}
	if row.CacheReadTokens > 0 {
		fields["cache_read_tokens"] = row.CacheReadTokens
	}
	cacheWriteTokens, cacheWriteOK := usage.CheckedAdd(
		row.CacheWrite5MTokens,
		row.CacheWrite1HTokens,
	)
	if cacheWriteOK && cacheWriteTokens > 0 {
		fields["cache_write_tokens"] = cacheWriteTokens
	}
	if row.OutputTokens > 0 {
		fields["output_tokens"] = row.OutputTokens
	}
	if row.UsageState != "" &&
		row.UsageState != string(usage.StateComplete) &&
		row.UsageState != string(usage.StateNotApplicable) {
		fields["usage_state"] = row.UsageState
	}
	if row.CostState != "" &&
		row.CostState != string(pricing.CostStatePriced) &&
		row.CostState != string(pricing.CostStateNotApplicable) {
		fields["cost_state"] = row.CostState
	}
	if row.CostState == string(pricing.CostStatePriced) {
		fields["estimated_cost_nano_usd"] = row.EstimatedCostNanoUSD
	}
	if event.Status != telemetry.RequestStatusSuccess {
		if row.ErrorCode != "" {
			fields["error_code"] = row.ErrorCode
		}
		if summary := sanitizeSummaryLimit(
			redactor,
			event.ErrorSummary,
			maxProcessSummaryBytes,
		); summary != "" {
			fields["error_summary"] = summary
		}
	}

	return level, fields, true
}

func attributedAttempt(event telemetry.RequestEvent) (uint, uint) {
	for _, attempt := range event.Attempts {
		if attempt.Sequence == event.Usage.AttemptSequence &&
			attempt.GroupID == event.Usage.GroupID &&
			attempt.KeyID == event.Usage.KeyID {
			return attempt.GroupID, attempt.KeyID
		}
	}
	return 0, 0
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
	utils.LogPlaneBestEffort(
		service.logger,
		level,
		utils.LogPlaneData,
		fields,
		"Request completed",
	)
}
