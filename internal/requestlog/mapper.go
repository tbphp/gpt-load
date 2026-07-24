package requestlog

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
)

const (
	maxSummaryBytes = 1024
	truncatedMarker = "...[truncated]"
)

func mapEvent(redactor *redact.Redactor, event telemetry.RequestEvent) models.RequestLog {
	attempts := make([]Attempt, 0, len(event.Attempts))
	for _, attempt := range event.Attempts {
		attempts = append(attempts, Attempt{
			Sequence:        attempt.Sequence,
			GroupID:         attempt.GroupID,
			GroupName:       attempt.GroupName,
			KeyID:           attempt.KeyID,
			KeyMask:         attempt.KeyMask,
			UpstreamModel:   attempt.UpstreamModel,
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

	return models.RequestLog{
		ID:                 event.RequestID,
		CreatedAt:          event.CompletedAt.UTC(),
		AccessKeyID:        event.AccessKeyID,
		Protocol:           string(event.Protocol),
		ClientModel:        event.ClientModel,
		UpstreamModel:      event.UpstreamModel,
		Status:             string(event.Status),
		StatusCode:         event.StatusCode,
		DurationMs:         event.DurationMs,
		ErrorCode:          event.ErrorCode,
		ErrorSummary:       sanitizeSummary(redactor, event.ErrorSummary),
		AffinityHit:        false,
		InputTokens:        0,
		OutputTokens:       0,
		CacheReadTokens:    0,
		CacheWrite5MTokens: 0,
		CacheWrite1HTokens: 0,
		Cost:               0,
		Attempts:           models.JSON(encodedAttempts),
	}
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
