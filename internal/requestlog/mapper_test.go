package requestlog

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func TestMapEventPersistsCompletedAtZeroUsageAndJSONArray(t *testing.T) {
	location := time.FixedZone("test-offset", 8*60*60)
	completedAt := time.Date(2026, time.July, 24, 20, 30, 0, 123, location)
	event := telemetry.RequestEvent{
		RequestID:     "00000000-0000-4000-8000-000000000101",
		CompletedAt:   completedAt,
		AccessKeyID:   17,
		Protocol:      protocol.OpenAICompletions,
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

	row := mapEvent(redact.New(), event, nil)
	if row.ID != event.RequestID || !row.CreatedAt.Equal(completedAt.UTC()) || row.CreatedAt.Location() != time.UTC {
		t.Fatalf("identity/completed_at = %q/%v, want %q/%v UTC", row.ID, row.CreatedAt, event.RequestID, completedAt.UTC())
	}
	if row.AccessKeyID != 17 || row.Protocol != string(protocol.OpenAICompletions) || row.ClientModel != "client-model" ||
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

	zeroAttempts := mapEvent(redact.New(), telemetry.RequestEvent{}, nil)
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

	row := mapEvent(redact.New(), event, nil)
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

func TestMapEventBoundsModelsAtUTF8Boundary(t *testing.T) {
	const wantMaxModelBytes = 255
	exactASCII := strings.Repeat("a", 255)
	exactMultibyte := strings.Repeat("界", 85)
	overlongASCII := strings.Repeat("b", 256)
	overlongMultibyte := strings.Repeat("模", 86)
	invalidAndOverlong := string([]byte{0xff}) + strings.Repeat("c", 255)
	event := telemetry.RequestEvent{
		ClientModel:   invalidAndOverlong,
		UpstreamModel: overlongASCII,
		Attempts: []telemetry.Attempt{
			{Sequence: 1, UpstreamModel: overlongMultibyte},
			{Sequence: 2, UpstreamModel: exactASCII},
			{Sequence: 3, UpstreamModel: exactMultibyte},
		},
	}

	projectedPriceTable := compileRequestLogTestPriceTable(
		t,
		strings.Repeat("b", wantMaxModelBytes-len(truncatedMarker))+truncatedMarker,
		pricing.Prices{Output: pricing.Price{Value: 1, Set: true}},
	)
	row := mapEvent(redact.New(), event, projectedPriceTable)

	assertProjectedModel := func(name, projected, original string, wantTruncated bool) {
		t.Helper()
		if len(projected) > wantMaxModelBytes || !utf8.ValidString(projected) {
			t.Fatalf(
				"%s bytes/UTF-8 = %d/%t, want <= %d/true",
				name,
				len(projected),
				utf8.ValidString(projected),
				wantMaxModelBytes,
			)
		}
		if strings.Contains(projected, string([]byte{0xff})) {
			t.Fatalf("%s retained invalid UTF-8: %q", name, projected)
		}
		if wantTruncated != strings.HasSuffix(projected, truncatedMarker) {
			t.Fatalf(
				"%s truncated marker = %t, want %t: %q",
				name,
				strings.HasSuffix(projected, truncatedMarker),
				wantTruncated,
				projected,
			)
		}
		if !wantTruncated && projected != original {
			t.Fatalf("%s = %q, want unchanged %q", name, projected, original)
		}
	}

	assertProjectedModel("client model", row.ClientModel, invalidAndOverlong, true)
	assertProjectedModel("overall upstream model", row.UpstreamModel, overlongASCII, true)
	var attempts []Attempt
	if err := json.Unmarshal(row.Attempts, &attempts); err != nil {
		t.Fatalf("unmarshal attempts: %v", err)
	}
	if len(attempts) != 3 {
		t.Fatalf("attempts = %d, want 3", len(attempts))
	}
	assertProjectedModel("attempt 1 upstream model", attempts[0].UpstreamModel, overlongMultibyte, true)
	assertProjectedModel("attempt 2 upstream model", attempts[1].UpstreamModel, exactASCII, false)
	assertProjectedModel("attempt 3 upstream model", attempts[2].UpstreamModel, exactMultibyte, false)
	if row.CostState != string(pricing.CostStateUnpriced) {
		t.Fatalf(
			"cost state = %q, want unpriced because Quote must use untruncated telemetry model",
			row.CostState,
		)
	}

	rawInvalidModel := "billable-" + string([]byte{0xff}) + "-model"
	rawPriceTable := compileRequestLogTestPriceTable(
		t,
		rawInvalidModel,
		pricing.Prices{Output: pricing.Price{Value: 2, Set: true}},
	)
	pricedEvent := telemetry.RequestEvent{
		UpstreamModel: rawInvalidModel,
		Usage: telemetry.UsageObservation{Result: usage.Result{
			State:  usage.StateComplete,
			Tokens: usage.Tokens{Output: 1_000_000},
		}},
	}
	pricedRow := mapEvent(redact.New(), pricedEvent, rawPriceTable)
	if pricedRow.UpstreamModel == rawInvalidModel || !utf8.ValidString(pricedRow.UpstreamModel) {
		t.Fatalf("priced row upstream model was not projected safely: %q", pricedRow.UpstreamModel)
	}
	if pricedRow.CostState != string(pricing.CostStatePriced) || pricedRow.Cost != 2 {
		t.Fatalf(
			"raw telemetry model quote = %q/%v, want priced/2",
			pricedRow.CostState,
			pricedRow.Cost,
		)
	}
}

func TestMapEventPersistsUsageAttributionAndQuote(t *testing.T) {
	table := compileRequestLogTestPriceTable(t, "actual-upstream", pricing.Prices{
		UncachedInput: pricing.Price{Value: 1, Set: true},
		CacheRead:     pricing.Price{Value: 2, Set: true},
		CacheWrite5M:  pricing.Price{Value: 3, Set: true},
		CacheWrite1H:  pricing.Price{Value: 4, Set: true},
		Output:        pricing.Price{Value: 5, Set: true},
	})

	t.Run("complete usage uses final attribution and all five token prices", func(t *testing.T) {
		event := testEvent("usage-complete")
		event.UpstreamModel = "actual-upstream"
		event.Attempts[0].GroupID = 91
		event.Attempts[0].UpstreamModel = "attempt-model-must-not-price"
		event.Usage = telemetry.UsageObservation{
			GroupID: 23,
			Result: usage.Result{
				State: usage.StateComplete,
				Tokens: usage.Tokens{
					UncachedInput: 1_000_000,
					CacheRead:     2_000_000,
					CacheWrite5M:  3_000_000,
					CacheWrite1H:  4_000_000,
					Output:        5_000_000,
				},
			},
		}

		row := mapEvent(redact.New(), event, table)

		if row.GroupID != 23 || row.UpstreamModel != "actual-upstream" {
			t.Fatalf("attribution = group:%d model:%q, want group:23 model:actual-upstream", row.GroupID, row.UpstreamModel)
		}
		if row.InputTokens != 1_000_000 || row.CacheReadTokens != 2_000_000 ||
			row.CacheWrite5MTokens != 3_000_000 || row.CacheWrite1HTokens != 4_000_000 ||
			row.OutputTokens != 5_000_000 {
			t.Fatalf("persisted tokens = %+v", row)
		}
		if row.UsageState != string(usage.StateComplete) ||
			row.CostState != string(pricing.CostStatePriced) || row.Cost != 55 {
			t.Fatalf("usage/cost = %q/%q/%v, want complete/priced/55", row.UsageState, row.CostState, row.Cost)
		}
	})

	t.Run("missing tokens are preserved and remain unpriced", func(t *testing.T) {
		event := testEvent("usage-missing")
		event.UpstreamModel = "actual-upstream"
		event.Usage = telemetry.UsageObservation{
			GroupID: 24,
			Result: usage.Result{
				State: usage.StateMissing,
				Tokens: usage.Tokens{
					UncachedInput: 6,
					CacheRead:     7,
					CacheWrite5M:  8,
					CacheWrite1H:  9,
					Output:        10,
				},
			},
		}

		row := mapEvent(redact.New(), event, table)

		if row.InputTokens != 6 || row.CacheReadTokens != 7 ||
			row.CacheWrite5MTokens != 8 || row.CacheWrite1HTokens != 9 ||
			row.OutputTokens != 10 {
			t.Fatalf("missing tokens were rewritten: %+v", row)
		}
		if row.UsageState != string(usage.StateMissing) ||
			row.CostState != string(pricing.CostStateUnpriced) || row.Cost != 0 {
			t.Fatalf("missing usage/cost = %q/%q/%v", row.UsageState, row.CostState, row.Cost)
		}
	})

	t.Run("non-2xx not applicable preserves final attribution and tokens", func(t *testing.T) {
		event := testEvent("usage-not-applicable")
		event.Status = telemetry.RequestStatusError
		event.StatusCode = 429
		event.UpstreamModel = "actual-upstream"
		event.Usage = telemetry.UsageObservation{
			GroupID: 25,
			Result: usage.Result{
				State: usage.StateNotApplicable,
				Tokens: usage.Tokens{
					UncachedInput: 11,
					Output:        12,
				},
			},
		}

		row := mapEvent(redact.New(), event, table)

		if row.GroupID != 25 || row.UpstreamModel != "actual-upstream" ||
			row.InputTokens != 11 || row.OutputTokens != 12 {
			t.Fatalf("not-applicable attribution/tokens = %+v", row)
		}
		if row.UsageState != string(usage.StateNotApplicable) ||
			row.CostState != string(pricing.CostStateNotApplicable) || row.Cost != 0 {
			t.Fatalf("not-applicable usage/cost = %q/%q/%v", row.UsageState, row.CostState, row.Cost)
		}
	})
}

func TestMapEventHandlesNilPriceTableFailOpen(t *testing.T) {
	for _, test := range []struct {
		name      string
		usage     usage.State
		wantState pricing.CostState
	}{
		{name: "complete", usage: usage.StateComplete, wantState: pricing.CostStateUnpriced},
		{name: "partial", usage: usage.StatePartial, wantState: pricing.CostStateUnpriced},
		{name: "missing", usage: usage.StateMissing, wantState: pricing.CostStateUnpriced},
		{name: "not applicable", usage: usage.StateNotApplicable, wantState: pricing.CostStateNotApplicable},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := testEvent("nil-price-" + test.name)
			event.Usage.Result = usage.Result{
				State: test.usage,
				Tokens: usage.Tokens{
					UncachedInput: 13,
					CacheRead:     14,
					CacheWrite5M:  15,
					CacheWrite1H:  16,
					Output:        17,
				},
			}

			row := mapEvent(redact.New(), event, nil)

			if row.UsageState != string(test.usage) || row.CostState != string(test.wantState) ||
				row.Cost != 0 || row.InputTokens != 13 || row.CacheReadTokens != 14 ||
				row.CacheWrite5MTokens != 15 || row.CacheWrite1HTokens != 16 ||
				row.OutputTokens != 17 {
				t.Fatalf("nil-table mapping = %+v", row)
			}
		})
	}
}
