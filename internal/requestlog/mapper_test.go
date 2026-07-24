package requestlog

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
	"gpt-load/internal/telemetry"
)

func TestMapEventPersistsCompletedAtZeroUsageAndJSONArray(t *testing.T) {
	location := time.FixedZone("test-offset", 8*60*60)
	completedAt := time.Date(2026, time.July, 24, 20, 30, 0, 123, location)
	event := telemetry.RequestEvent{
		RequestID:     "00000000-0000-4000-8000-000000000101",
		CompletedAt:   completedAt,
		AccessKeyID:   17,
		Protocol:      protocol.OpenAI,
		ClientModel:   "client-model",
		UpstreamModel: "upstream-model",
		Status:        telemetry.RequestStatusError,
		StatusCode:    429,
		ErrorCode:     "upstream_rate_limited",
		ErrorSummary:  "Rate limit exceeded.",
		DurationMs:    845,
		AffinityHit:   true,
		Attempts: []telemetry.Attempt{{
			Sequence:        1,
			GroupID:         12,
			GroupName:       "Anthropic Primary",
			KeyID:           34,
			KeyMask:         "sk-ant-...wxyz",
			UpstreamModel:   "claude-sonnet-5",
			StatusCode:      429,
			DurationMs:      800,
			FailureCategory: telemetry.FailureCategoryRateLimited,
			Action:          telemetry.ActionCooldownKey,
			WillRetry:       true,
			ErrorCode:       "upstream_rate_limited",
			ErrorSummary:    "Rate limit exceeded.",
		}},
	}

	row := mapEvent(redact.New(), event)
	if row.ID != event.RequestID || !row.CreatedAt.Equal(completedAt.UTC()) || row.CreatedAt.Location() != time.UTC {
		t.Fatalf("identity/completed_at = %q/%v, want %q/%v UTC", row.ID, row.CreatedAt, event.RequestID, completedAt.UTC())
	}
	if row.AccessKeyID != 17 || row.Protocol != "openai" || row.ClientModel != "client-model" ||
		row.UpstreamModel != "upstream-model" || row.Status != "error" || row.StatusCode != 429 ||
		row.DurationMs != 845 || row.ErrorCode != "upstream_rate_limited" ||
		row.ErrorSummary != "Rate limit exceeded." {
		t.Fatalf("mapped request row = %+v", row)
	}
	if row.AffinityHit {
		t.Fatal("AffinityHit = true, want false in M3")
	}
	if row.InputTokens != 0 || row.OutputTokens != 0 || row.CacheReadTokens != 0 ||
		row.CacheWrite5MTokens != 0 || row.CacheWrite1HTokens != 0 || row.Cost != 0 {
		t.Fatalf("usage fields are non-zero: %+v", row)
	}

	var attempts []Attempt
	if err := json.Unmarshal(row.Attempts, &attempts); err != nil {
		t.Fatalf("unmarshal attempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].GroupID != 12 || attempts[0].FailureCategory != telemetry.FailureCategoryRateLimited ||
		attempts[0].Action != telemetry.ActionCooldownKey || !attempts[0].WillRetry {
		t.Fatalf("attempts = %+v", attempts)
	}

	zeroAttempts := mapEvent(redact.New(), telemetry.RequestEvent{})
	if string(zeroAttempts.Attempts) != "[]" {
		t.Fatalf("zero attempts JSON = %q, want []", zeroAttempts.Attempts)
	}
}

func TestMapEventDefensivelyRedactsAndBoundsSummaries(t *testing.T) {
	const secret = "sk-this-is-a-secret-value"
	unsafeSummary := string([]byte{0xff}) + "\r\n\t " + secret + "   " + strings.Repeat("界", 500)
	event := telemetry.RequestEvent{
		ErrorSummary: unsafeSummary,
		Attempts: []telemetry.Attempt{{
			ErrorSummary: unsafeSummary,
		}},
	}

	row := mapEvent(redact.New(), event)
	if len(row.ErrorSummary) > maxSummaryBytes || !utf8.ValidString(row.ErrorSummary) {
		t.Fatalf("request summary bytes/UTF-8 = %d/%t", len(row.ErrorSummary), utf8.ValidString(row.ErrorSummary))
	}
	if !strings.HasSuffix(row.ErrorSummary, truncatedMarker) {
		t.Fatalf("request summary does not end with %q: %q", truncatedMarker, row.ErrorSummary)
	}
	if strings.Contains(row.ErrorSummary, secret) || strings.ContainsAny(row.ErrorSummary, "\r\n\t") ||
		strings.Contains(row.ErrorSummary, "  ") {
		t.Fatalf("request summary was not defensively sanitized: %q", row.ErrorSummary)
	}

	var attempts []Attempt
	if err := json.Unmarshal(row.Attempts, &attempts); err != nil {
		t.Fatalf("unmarshal attempts: %v", err)
	}
	if len(attempts) != 1 || len(attempts[0].ErrorSummary) > maxSummaryBytes ||
		!utf8.ValidString(attempts[0].ErrorSummary) ||
		strings.Contains(attempts[0].ErrorSummary, secret) ||
		!strings.HasSuffix(attempts[0].ErrorSummary, truncatedMarker) {
		t.Fatalf("attempt summary was not defensively sanitized: %+v", attempts)
	}
}
