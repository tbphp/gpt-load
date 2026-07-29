package requestlog

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

const (
	maxSummaryBytes = 1024
	maxModelBytes   = 255
	truncatedMarker = "...[truncated]"
)

func mapEvent(
	redactor *redact.Redactor,
	event telemetry.RequestEvent,
	prices *pricing.Table,
) models.RequestLog {
	attempts := make([]Attempt, 0, len(event.Attempts))
	for _, attempt := range event.Attempts {
		attempts = append(attempts, Attempt{
			Sequence:        attempt.Sequence,
			GroupID:         attempt.GroupID,
			GroupName:       attempt.GroupName,
			KeyID:           attempt.KeyID,
			UpstreamModel:   projectModel(attempt.UpstreamModel),
			StatusCode:      attempt.StatusCode,
			DurationMs:      attempt.DurationMs,
			FailureCategory: attempt.FailureCategory,
			Action:          attempt.Action,
			WillRetry:       attempt.WillRetry,
			ErrorCode:       attempt.ErrorCode,
			ErrorSummary:    sanitizeSummary(redactor, attempt.ErrorSummary),
			Committed:       attempt.Committed,
		})
	}
	encodedAttempts, err := json.Marshal(attempts)
	if err != nil {
		encodedAttempts = []byte("[]")
	}

	result := event.Usage.Result
	quote := pricing.Quote{State: pricing.CostStateUnpriced}
	if prices != nil {
		quote = prices.Quote(event.UpstreamModel, result)
	} else if result.State == usage.StateNotApplicable {
		quote.State = pricing.CostStateNotApplicable
	}

	return models.RequestLog{
		ID:                 event.RequestID,
		CreatedAt:          event.CompletedAt.UTC(),
		AccessKeyID:        event.AccessKeyID,
		GroupID:            event.Usage.GroupID,
		Protocol:           string(event.Protocol),
		ClientModel:        projectModel(event.ClientModel),
		UpstreamModel:      projectModel(event.UpstreamModel),
		Status:             string(event.Status),
		StatusCode:         event.StatusCode,
		DurationMs:         event.DurationMs,
		ErrorCode:          event.ErrorCode,
		ErrorSummary:       sanitizeSummary(redactor, event.ErrorSummary),
		AffinityHit:        false,
		InputTokens:        result.Tokens.UncachedInput,
		OutputTokens:       result.Tokens.Output,
		CacheReadTokens:    result.Tokens.CacheRead,
		CacheWrite5MTokens: result.Tokens.CacheWrite5M,
		CacheWrite1HTokens: result.Tokens.CacheWrite1H,
		Cost:               quote.Cost,
		UsageState:         string(result.State),
		CostState:          string(quote.State),
		Attempts:           models.JSON(encodedAttempts),
	}
}

func projectModel(model string) string {
	model = strings.ToValidUTF8(model, "\uFFFD")
	if len(model) <= maxModelBytes {
		return model
	}

	prefixBytes := maxModelBytes - len(truncatedMarker)
	for prefixBytes > 0 && !utf8.ValidString(model[:prefixBytes]) {
		prefixBytes--
	}
	return model[:prefixBytes] + truncatedMarker
}

func sanitizeSummary(redactor *redact.Redactor, summary string) string {
	summary = strings.ToValidUTF8(summary, "\uFFFD")
	summary = strings.Join(strings.Fields(summary), " ")
	if redactor != nil {
		summary = redactor.String(summary)
	}
	if len(summary) <= maxSummaryBytes {
		return summary
	}

	prefixBytes := maxSummaryBytes - len(truncatedMarker)
	for prefixBytes > 0 && !utf8.ValidString(summary[:prefixBytes]) {
		prefixBytes--
	}
	return summary[:prefixBytes] + truncatedMarker
}
