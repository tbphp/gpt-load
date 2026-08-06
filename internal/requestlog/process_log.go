package requestlog

import (
	"net/http"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

const maxProcessSummaryBytes = 200

func projectProcessLog(
	redactor *redact.Redactor,
	event telemetry.RequestEvent,
) (logrus.Level, logrus.Fields, bool) {
	if event.RequestID == "" {
		return logrus.InfoLevel, nil, false
	}

	row, err := mapEvent(redactor, event)
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
		"event":    "request_completed",
		"status":   row.Status,
		"proto":    row.Protocol,
		"ak_id":    row.AccessKeyID,
		"duration": utils.FormatDurationMS(row.DurationMs),
	}
	if row.StatusCode != http.StatusOK {
		fields["http"] = row.StatusCode
	}
	if row.ClientModel != "" {
		fields["model"] = row.ClientModel
	}
	if row.UpstreamModel != "" && row.UpstreamModel != row.ClientModel {
		fields["up_model"] = row.UpstreamModel
	}
	if groupID > 0 {
		fields["group"] = groupID
	}
	if keyID > 0 {
		fields["kid"] = keyID
	}
	if attemptCount := len(event.Attempts); attemptCount != 1 {
		fields["attempts"] = attemptCount
	}
	if inputTokens, ok := processInputTokens(row); ok && inputTokens > 0 {
		fields["in_tokens"] = inputTokens
	}
	if row.OutputTokens > 0 {
		fields["out_tokens"] = row.OutputTokens
	}
	if row.UsageState != "" &&
		row.UsageState != string(usage.StateComplete) &&
		row.UsageState != string(usage.StateNotApplicable) {
		fields["usage"] = row.UsageState
	}
	if row.CostState != "" &&
		row.CostState != string(pricing.CostStatePriced) &&
		row.CostState != string(pricing.CostStateNotApplicable) {
		fields["cost_state"] = row.CostState
	}
	if row.CostState == string(pricing.CostStatePriced) {
		fields["cost_usd"] = utils.FormatNanoUSD(row.EstimatedCostNanoUSD)
	}
	if event.Status != telemetry.RequestStatusSuccess {
		if row.ErrorCode != "" {
			fields["err"] = row.ErrorCode
		}
		if summary := sanitizeSummaryLimit(
			redactor,
			event.ErrorSummary,
			maxProcessSummaryBytes,
		); summary != "" {
			fields["err_msg"] = summary
		}
	}

	return level, fields, true
}

func processInputTokens(row models.RequestLog) (int64, bool) {
	total := int64(0)
	for _, value := range [...]int64{
		row.UncachedInputTokens,
		row.CacheReadTokens,
		row.CacheWrite5MTokens,
		row.CacheWrite1HTokens,
		row.CacheWriteUnknownTokens,
	} {
		var ok bool
		total, ok = usage.CheckedAdd(total, value)
		if !ok {
			return 0, false
		}
	}
	return total, true
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
) {
	defer func() {
		_ = recover()
	}()
	level, fields, ok := projectProcessLog(service.redactor, event)
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
