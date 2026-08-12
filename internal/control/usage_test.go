package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/storage/models"
)

func TestUsageAPIRouteUsesManagementAuthentication(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.now = func() time.Time {
		return time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	}
	fixture.service.usageStats = &recordingUsageStatReader{}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	request := httptest.NewRequest(http.MethodGet, "/api/usage", nil)
	request.Header.Set("Authorization", "Bearer test-auth-key")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/usage = %d %s, want 200", recorder.Code, recorder.Body.String())
	}
}

func TestParseUsageQueryUsesFixedUTCAlignedWindows(t *testing.T) {
	tests := []struct {
		name        string
		rawQuery    string
		observedAt  time.Time
		wantFrom    time.Time
		wantTo      time.Time
		granularity requestlog.UsageGranularity
		bucketWidth int64
	}{
		{
			name:        "1 hour uses one complete UTC hour",
			rawQuery:    "range=1h",
			observedAt:  time.Date(2026, time.July, 27, 12, 34, 56, 789, time.UTC),
			wantFrom:    time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC),
			wantTo:      time.Date(2026, time.July, 27, 13, 0, 0, 0, time.UTC),
			granularity: requestlog.UsageGranularityHour,
			bucketWidth: int64(time.Hour / time.Millisecond),
		},
		{
			name:        "24 hours crosses the local and UTC day at an exact hour boundary",
			rawQuery:    "range=24h",
			observedAt:  time.Date(2026, time.July, 27, 0, 34, 56, 789, time.FixedZone("UTC+8", 8*60*60)),
			wantFrom:    time.Date(2026, time.July, 25, 17, 0, 0, 0, time.UTC),
			wantTo:      time.Date(2026, time.July, 26, 17, 0, 0, 0, time.UTC),
			granularity: requestlog.UsageGranularityHour,
			bucketWidth: int64(time.Hour / time.Millisecond),
		},
		{
			name:        "30 days crosses the local and UTC day at exact day boundaries",
			rawQuery:    "range=30d",
			observedAt:  time.Date(2026, time.July, 26, 20, 34, 56, 789, time.FixedZone("UTC-7", -7*60*60)),
			wantFrom:    time.Date(2026, time.June, 28, 0, 0, 0, 0, time.UTC),
			wantTo:      time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC),
			granularity: requestlog.UsageGranularityDay,
			bucketWidth: int64(24 * time.Hour / time.Millisecond),
		},
		{
			name:        "3 days uses three hour buckets",
			rawQuery:    "range=3d",
			observedAt:  time.Date(2026, time.July, 27, 12, 34, 56, 789, time.UTC),
			wantFrom:    time.Date(2026, time.July, 24, 15, 0, 0, 0, time.UTC),
			wantTo:      time.Date(2026, time.July, 27, 15, 0, 0, 0, time.UTC),
			granularity: requestlog.UsageGranularityHour,
			bucketWidth: int64(3 * time.Hour / time.Millisecond),
		},
		{
			name:        "7 days uses six hour buckets",
			rawQuery:    "range=7d",
			observedAt:  time.Date(2026, time.July, 27, 12, 34, 56, 789, time.UTC),
			wantFrom:    time.Date(2026, time.July, 20, 18, 0, 0, 0, time.UTC),
			wantTo:      time.Date(2026, time.July, 27, 18, 0, 0, 0, time.UTC),
			granularity: requestlog.UsageGranularityHour,
			bucketWidth: int64(6 * time.Hour / time.Millisecond),
		},
		{
			name:        "15 days uses twelve hour buckets",
			rawQuery:    "range=15d",
			observedAt:  time.Date(2026, time.July, 27, 12, 34, 56, 789, time.UTC),
			wantFrom:    time.Date(2026, time.July, 13, 0, 0, 0, 0, time.UTC),
			wantTo:      time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC),
			granularity: requestlog.UsageGranularityHour,
			bucketWidth: int64(12 * time.Hour / time.Millisecond),
		},
		{
			name:        "custom 24 hours accepts exact UTC hour boundaries",
			rawQuery:    "from_ms=1785042000000&to_ms=1785128400000",
			observedAt:  time.Date(2026, time.July, 27, 12, 34, 56, 789, time.UTC),
			wantFrom:    time.Date(2026, time.July, 26, 5, 0, 0, 0, time.UTC),
			wantTo:      time.Date(2026, time.July, 27, 5, 0, 0, 0, time.UTC),
			granularity: requestlog.UsageGranularityHour,
			bucketWidth: int64(time.Hour / time.Millisecond),
		},
		{
			name:        "custom 30 days accepts exact UTC day boundaries",
			rawQuery:    "from_ms=1782604800000&to_ms=1785196800000",
			observedAt:  time.Date(2026, time.July, 27, 12, 34, 56, 789, time.UTC),
			wantFrom:    time.Date(2026, time.June, 28, 0, 0, 0, 0, time.UTC),
			wantTo:      time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC),
			granularity: requestlog.UsageGranularityDay,
			bucketWidth: int64(24 * time.Hour / time.Millisecond),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query, apiErr := parseUsageQuery(test.rawQuery, test.observedAt.UnixMilli())
			if apiErr != nil {
				t.Fatalf("parseUsageQuery() error = %v", apiErr)
			}
			if query.FromMS != test.wantFrom.UnixMilli() ||
				query.ToMS != test.wantTo.UnixMilli() ||
				query.Granularity != test.granularity ||
				query.BucketWidthMS != test.bucketWidth {
				t.Fatalf(
					"parseUsageQuery() window = %d to %d (%s, %dms), want %d to %d (%s, %dms)",
					query.FromMS,
					query.ToMS,
					query.Granularity,
					query.BucketWidthMS,
					test.wantFrom.UnixMilli(),
					test.wantTo.UnixMilli(),
					test.granularity,
					test.bucketWidth,
				)
			}
		})
	}
}

func TestUsageAPIReturnsExactPresetRange(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 34, 56, 789, time.UTC)
	tests := []struct {
		rangeValue    string
		bucketWidthMS int64
	}{
		{rangeValue: "1h", bucketWidthMS: int64(time.Hour / time.Millisecond)},
		{rangeValue: "24h", bucketWidthMS: int64(time.Hour / time.Millisecond)},
		{rangeValue: "3d", bucketWidthMS: int64(3 * time.Hour / time.Millisecond)},
		{rangeValue: "7d", bucketWidthMS: int64(6 * time.Hour / time.Millisecond)},
		{rangeValue: "15d", bucketWidthMS: int64(12 * time.Hour / time.Millisecond)},
		{rangeValue: "30d", bucketWidthMS: int64(24 * time.Hour / time.Millisecond)},
	}
	for _, test := range tests {
		t.Run(test.rangeValue, func(t *testing.T) {
			engine, _ := newUsageTestEngine(t, now, &recordingUsageStatReader{})
			recorder := performUsageRequest(engine, "test-auth-key", "range="+test.rangeValue)
			if recorder.Code != http.StatusOK {
				t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
			}
			var envelope struct {
				Data struct {
					Range         string `json:"range"`
					BucketWidthMS int64  `json:"bucket_width_ms"`
				} `json:"data"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if envelope.Data.Range != test.rangeValue ||
				envelope.Data.BucketWidthMS != test.bucketWidthMS {
				t.Fatalf("range/bucket width = %q/%d, want %q/%d", envelope.Data.Range, envelope.Data.BucketWidthMS, test.rangeValue, test.bucketWidthMS)
			}
		})
	}
}

func TestUsageAPIReturnsDistributionWithoutCredentialIdentity(t *testing.T) {
	now := time.Date(2026, time.August, 12, 8, 0, 0, 0, time.UTC)
	other := requestlog.UsageDistributionAggregate{
		RequestCount:         3,
		EstimatedCostNanoUSD: 300_000_000,
	}
	reader := &recordingUsageStatReader{report: requestlog.UsageReport{
		Summary: requestlog.UsageAggregate{
			RequestCount: 13, SuccessCount: 11, FailureCount: 2,
			EstimatedCostNanoUSD: 2_300_000_000,
		},
		Distribution: requestlog.UsageDistribution{
			Dimension: requestlog.UsageDistributionDimensionGroup,
			Metric:    requestlog.UsageDistributionMetricCost,
			Items: []requestlog.UsageDistributionItem{{
				GroupID: 7,
				UsageDistributionAggregate: requestlog.UsageDistributionAggregate{
					RequestCount:         10,
					EstimatedCostNanoUSD: 2_000_000_000,
				},
			}},
			Other: &other,
		},
	}}
	engine, _ := newUsageTestEngine(t, now, reader)
	recorder := performUsageRequest(
		engine,
		"test-auth-key",
		"distribution=group&distribution_metric=cost",
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	if len(reader.queries) != 1 ||
		reader.queries[0].Distribution != requestlog.UsageDistributionDimensionGroup ||
		reader.queries[0].DistributionMetric != requestlog.UsageDistributionMetricCost {
		t.Fatalf("usage query = %#v", reader.queries)
	}
	var envelope struct {
		Data struct {
			Distribution struct {
				Dimension string `json:"dimension"`
				Metric    string `json:"metric"`
				Items     []struct {
					GroupID              uint   `json:"group_id"`
					RequestCount         int64  `json:"request_count"`
					EstimatedCostNanoUSD string `json:"estimated_cost_nano_usd"`
				} `json:"items"`
				Other *struct {
					RequestCount         int64  `json:"request_count"`
					EstimatedCostNanoUSD string `json:"estimated_cost_nano_usd"`
				} `json:"other"`
			} `json:"distribution"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.Distribution.Dimension != "group" ||
		envelope.Data.Distribution.Metric != "cost" ||
		len(envelope.Data.Distribution.Items) != 1 ||
		envelope.Data.Distribution.Items[0].GroupID != 7 ||
		envelope.Data.Distribution.Items[0].RequestCount != 10 ||
		envelope.Data.Distribution.Items[0].EstimatedCostNanoUSD != "2000000000" ||
		envelope.Data.Distribution.Other == nil ||
		envelope.Data.Distribution.Other.RequestCount != 3 {
		t.Fatalf("distribution response = %#v", envelope.Data.Distribution)
	}
	for _, forbidden := range []string{`"credential_id"`, `"channel_id"`} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("usage response exposes removed field %s: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestUsageAPIDefaultsToFixedUTCAligned24HoursAndReturnsZeroArrays(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 34, 56, 789, time.FixedZone("UTC+8", 8*60*60))
	reader := &recordingUsageStatReader{}
	engine, fixture := newUsageTestEngine(t, now, reader)
	fixture.requestLogStats.value.DroppedTotal = 2
	fixture.requestLogStats.value.WriteFailureTotal = 1
	fixture.requestLogStats.value.LastWriteFailureAt = time.Date(
		2026, time.July, 27, 3, 0, 0, 0, time.UTC,
	)

	recorder := performUsageRequest(engine, "test-auth-key", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	if len(reader.queries) != 1 {
		t.Fatalf("QueryUsage calls = %d, want one", len(reader.queries))
	}
	query := reader.queries[0]
	if query.Granularity != requestlog.UsageGranularityHour ||
		query.FromMS != time.Date(2026, time.July, 26, 5, 0, 0, 0, time.UTC).UnixMilli() ||
		query.ToMS != time.Date(2026, time.July, 27, 5, 0, 0, 0, time.UTC).UnixMilli() ||
		query.GroupID != nil || query.UpstreamModel != "" ||
		query.Distribution != requestlog.UsageDistributionDimensionGroup ||
		query.DistributionMetric != requestlog.UsageDistributionMetricRequests {
		t.Fatalf("default UsageQuery = %#v", query)
	}
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Range        string                      `json:"range"`
			Granularity  requestlog.UsageGranularity `json:"granularity"`
			FromMS       int64                       `json:"from_ms"`
			ToMS         int64                       `json:"to_ms"`
			ObservedAtMS int64                       `json:"observed_at_ms"`
			Series       []json.RawMessage           `json:"series"`
			Distribution struct {
				Dimension string            `json:"dimension"`
				Metric    string            `json:"metric"`
				Items     []json.RawMessage `json:"items"`
				Other     json.RawMessage   `json:"other"`
			} `json:"distribution"`
			Health struct {
				Scope                string `json:"scope"`
				DroppedTotal         uint64 `json:"dropped_total"`
				WriteFailureTotal    uint64 `json:"write_failure_total"`
				LastWriteFailureAtMS *int64 `json:"last_write_failure_at_ms"`
			} `json:"collection_health"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Code != 0 || envelope.Data.Range != "24h" ||
		envelope.Data.Granularity != requestlog.UsageGranularityHour ||
		envelope.Data.FromMS != time.Date(2026, time.July, 26, 5, 0, 0, 0, time.UTC).UnixMilli() ||
		envelope.Data.ToMS != time.Date(2026, time.July, 27, 5, 0, 0, 0, time.UTC).UnixMilli() ||
		envelope.Data.ObservedAtMS != now.UnixMilli() ||
		envelope.Data.Series == nil || len(envelope.Data.Series) != 0 ||
		envelope.Data.Distribution.Dimension != "group" ||
		envelope.Data.Distribution.Metric != "requests" ||
		envelope.Data.Distribution.Items == nil || len(envelope.Data.Distribution.Items) != 0 ||
		string(envelope.Data.Distribution.Other) != "null" ||
		envelope.Data.Health.Scope != "current_process" ||
		envelope.Data.Health.DroppedTotal != 2 ||
		envelope.Data.Health.WriteFailureTotal != 1 ||
		envelope.Data.Health.LastWriteFailureAtMS == nil ||
		*envelope.Data.Health.LastWriteFailureAtMS != time.Date(2026, time.July, 27, 3, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("default usage envelope = %#v", envelope)
	}
	var rawEnvelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &rawEnvelope); err != nil {
		t.Fatalf("decode raw response: %v", err)
	}
	for _, forbidden := range []string{
		"filters", "request_log", "timezone", "from", "to", "observed_at",
		"estimated_cost" + "_usd",
	} {
		if _, exists := rawEnvelope.Data[forbidden]; exists {
			t.Fatalf("usage response exposes forbidden %q field: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestUsageAPISelectsThirtyUTCDaysAndAppliesFilters(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	reader := &recordingUsageStatReader{report: requestlog.UsageReport{
		Summary: requestlog.UsageAggregate{
			RequestCount: 1, UncachedInputTokens: 2, CacheWriteUnknownTokens: 4,
			OutputTokens: 3, PricingPartialCount: 1,
			EstimatedCostNanoUSD: 250_000_000,
		},
		Series: []requestlog.UsageSeriesPoint{{
			BucketStartMS: time.Date(2026, time.June, 28, 0, 0, 0, 0, time.UTC).UnixMilli(),
			BucketEndMS:   time.Date(2026, time.June, 29, 0, 0, 0, 0, time.UTC).UnixMilli(),
			UsageAggregate: requestlog.UsageAggregate{
				RequestCount: 1, UncachedInputTokens: 2, CacheWriteUnknownTokens: 4,
				OutputTokens: 3, PricingPartialCount: 1,
				EstimatedCostNanoUSD: 1_123_456_789_012,
			},
		}},
		Distribution: requestlog.UsageDistribution{
			Dimension: requestlog.UsageDistributionDimensionModel,
			Metric:    requestlog.UsageDistributionMetricCost,
			Items: []requestlog.UsageDistributionItem{{
				Model: "upstream-model",
				UsageDistributionAggregate: requestlog.UsageDistributionAggregate{
					RequestCount:         1,
					EstimatedCostNanoUSD: 250_000_000,
				},
			}},
		},
	}}
	engine, _ := newUsageTestEngine(t, now, reader)
	recorder := performUsageRequest(
		engine,
		"test-auth-key",
		"range=30d&group_id=9&channel_id=openai&credential_id=13&upstream_model=upstream-model&distribution=model&distribution_metric=cost",
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	if len(reader.queries) != 1 {
		t.Fatalf("QueryUsage calls = %d, want one", len(reader.queries))
	}
	query := reader.queries[0]
	if query.Granularity != requestlog.UsageGranularityDay ||
		query.FromMS != time.Date(2026, time.June, 28, 0, 0, 0, 0, time.UTC).UnixMilli() ||
		query.ToMS != time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC).UnixMilli() ||
		query.GroupID == nil || *query.GroupID != 9 ||
		query.ChannelID != channel.OpenAI ||
		query.CredentialID == nil || *query.CredentialID != 13 ||
		query.UpstreamModel != "upstream-model" ||
		query.Distribution != requestlog.UsageDistributionDimensionModel ||
		query.DistributionMetric != requestlog.UsageDistributionMetricCost {
		t.Fatalf("30 day UsageQuery = %#v", query)
	}
	var envelope struct {
		Data struct {
			Range       string                      `json:"range"`
			Granularity requestlog.UsageGranularity `json:"granularity"`
			FromMS      int64                       `json:"from_ms"`
			ToMS        int64                       `json:"to_ms"`
			Summary     struct {
				TotalTokens             int64  `json:"total_tokens"`
				CacheWriteUnknownTokens int64  `json:"cache_write_unknown_tokens"`
				PricingPartialCount     int64  `json:"pricing_partial_count"`
				EstimatedCostNanoUSD    string `json:"estimated_cost_nano_usd"`
			} `json:"summary"`
			Series []struct {
				BucketStartMS           int64  `json:"bucket_start_ms"`
				TotalTokens             int64  `json:"total_tokens"`
				CacheWriteUnknownTokens int64  `json:"cache_write_unknown_tokens"`
				PricingPartialCount     int64  `json:"pricing_partial_count"`
				EstimatedCostNanoUSD    string `json:"estimated_cost_nano_usd"`
			} `json:"series"`
			Distribution struct {
				Dimension string `json:"dimension"`
				Metric    string `json:"metric"`
				Items     []struct {
					Model                string `json:"model"`
					RequestCount         int64  `json:"request_count"`
					EstimatedCostNanoUSD string `json:"estimated_cost_nano_usd"`
				} `json:"items"`
			} `json:"distribution"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.Range != "30d" ||
		envelope.Data.Granularity != requestlog.UsageGranularityDay ||
		envelope.Data.FromMS != time.Date(2026, time.June, 28, 0, 0, 0, 0, time.UTC).UnixMilli() ||
		envelope.Data.ToMS != time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC).UnixMilli() ||
		envelope.Data.Summary.TotalTokens != 9 ||
		envelope.Data.Summary.CacheWriteUnknownTokens != 4 ||
		envelope.Data.Summary.PricingPartialCount != 1 ||
		envelope.Data.Summary.EstimatedCostNanoUSD != "250000000" ||
		len(envelope.Data.Series) != 1 ||
		envelope.Data.Series[0].BucketStartMS != time.Date(2026, time.June, 28, 0, 0, 0, 0, time.UTC).UnixMilli() ||
		envelope.Data.Series[0].TotalTokens != 9 ||
		envelope.Data.Series[0].CacheWriteUnknownTokens != 4 ||
		envelope.Data.Series[0].PricingPartialCount != 1 ||
		envelope.Data.Series[0].EstimatedCostNanoUSD != "1123456789012" ||
		envelope.Data.Distribution.Dimension != "model" ||
		envelope.Data.Distribution.Metric != "cost" ||
		len(envelope.Data.Distribution.Items) != 1 ||
		envelope.Data.Distribution.Items[0].Model != "upstream-model" ||
		envelope.Data.Distribution.Items[0].RequestCount != 1 ||
		envelope.Data.Distribution.Items[0].EstimatedCostNanoUSD != "250000000" {
		t.Fatalf("usage response = %#v", envelope.Data)
	}
	for _, legacyField := range []string{`"key_id"`, `"upstream_key_id"`} {
		if strings.Contains(recorder.Body.String(), legacyField) {
			t.Fatalf("usage response exposes legacy field %s: %s", legacyField, recorder.Body.String())
		}
	}
}

func TestUsageAPIValidatesModelAsUTF8BytesWithoutBoundaryWhitespaceOrControls(t *testing.T) {
	reader := &recordingUsageStatReader{}
	engine, _ := newUsageTestEngine(
		t,
		time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC),
		reader,
	)
	validModels := []string{
		"a",
		strings.Repeat("a", 252) + "猫",
		"model variant",
	}
	for _, model := range validModels {
		recorder := performUsageRequest(engine, "test-auth-key", "upstream_model="+url.QueryEscape(model))
		if recorder.Code != http.StatusOK {
			t.Fatalf("valid model %q response = %d %s", model, recorder.Code, recorder.Body.String())
		}
		if got := reader.queries[len(reader.queries)-1].UpstreamModel; got != model {
			t.Fatalf("reader model = %q, want %q", got, model)
		}
	}
	validCalls := len(reader.queries)

	invalidModels := []struct {
		name  string
		query string
	}{
		{name: "invalid UTF-8", query: "upstream_model=%FF"},
		{name: "leading whitespace", query: "upstream_model=" + url.QueryEscape(" model")},
		{name: "trailing whitespace", query: "upstream_model=" + url.QueryEscape("model\u3000")},
		{name: "embedded control", query: "upstream_model=" + url.QueryEscape("model\x00id")},
		{name: "DEL control", query: "upstream_model=" + url.QueryEscape("model\x7fid")},
		{name: "256 UTF-8 bytes", query: "upstream_model=" + url.QueryEscape(strings.Repeat("a", 253)+"猫")},
	}
	for _, test := range invalidModels {
		t.Run(test.name, func(t *testing.T) {
			recorder := performUsageRequest(engine, "test-auth-key", test.query)
			assertUsageErrorCode(t, recorder, "VALIDATION_FAILED")
		})
	}
	if len(reader.queries) != validCalls {
		t.Fatalf("reader calls = %d, want %d after invalid models", len(reader.queries), validCalls)
	}
}

func TestUsageAPIRejectsStrictInvalidQueriesWithoutCallingReader(t *testing.T) {
	reader := &recordingUsageStatReader{}
	engine, _ := newUsageTestEngine(t, time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC), reader)
	tests := []struct {
		query string
		code  string
	}{
		{query: "unknown=1", code: "BAD_REQUEST"},
		{query: "range=24h&range=30d", code: "BAD_REQUEST"},
		{query: "from=1&to=2", code: "BAD_REQUEST"},
		{query: "from_ms=1", code: "VALIDATION_FAILED"},
		{query: "from_ms=01&to_ms=2", code: "BAD_REQUEST"},
		{query: "from_ms=-1&to_ms=2", code: "BAD_REQUEST"},
		{query: "from_ms=1&to_ms=1", code: "VALIDATION_FAILED"},
		{query: "from_ms=1&to_ms=3600001", code: "VALIDATION_FAILED"},
		{query: "from_ms=1&to_ms=90000001", code: "VALIDATION_FAILED"},
		{query: "from_ms=0&to_ms=2678400000", code: "VALIDATION_FAILED"},
		{query: "range=24h&from_ms=1&to_ms=2", code: "VALIDATION_FAILED"},
		{query: "group_id=0", code: "VALIDATION_FAILED"},
		{query: "group_id=01", code: "BAD_REQUEST"},
		{query: "group_id=%2B1", code: "BAD_REQUEST"},
		{query: "group_id=%201", code: "BAD_REQUEST"},
		{query: "group_id=1&group_id=2", code: "BAD_REQUEST"},
		{query: "group_id=9007199254740992", code: "VALIDATION_FAILED"},
		{query: "group_id=-1", code: "BAD_REQUEST"},
		{query: "channel_id=", code: "VALIDATION_FAILED"},
		{query: "channel_id=unknown", code: "VALIDATION_FAILED"},
		{query: "channel_id=openai&channel_id=anthropic", code: "BAD_REQUEST"},
		{query: "credential_id=0", code: "VALIDATION_FAILED"},
		{query: "credential_id=01", code: "BAD_REQUEST"},
		{query: "credential_id=9007199254740992", code: "VALIDATION_FAILED"},
		{query: "credential_id=1&credential_id=2", code: "BAD_REQUEST"},
		{query: "key_id=1", code: "BAD_REQUEST"},
		{query: "upstream_key_id=1", code: "BAD_REQUEST"},
		{query: "model=legacy", code: "BAD_REQUEST"},
		{query: "upstream_model=", code: "VALIDATION_FAILED"},
		{query: "distribution=", code: "VALIDATION_FAILED"},
		{query: "distribution=credential", code: "VALIDATION_FAILED"},
		{query: "distribution_metric=", code: "VALIDATION_FAILED"},
		{query: "distribution_metric=tokens", code: "VALIDATION_FAILED"},
		{
			query: "distribution_metric=cost&distribution_metric=requests",
			code:  "BAD_REQUEST",
		},
	}
	for _, test := range tests {
		t.Run(test.query, func(t *testing.T) {
			recorder := performUsageRequest(engine, "test-auth-key", test.query)
			assertUsageErrorCode(t, recorder, test.code)
		})
	}
	if len(reader.queries) != 0 {
		t.Fatalf("QueryUsage calls = %d, want zero", len(reader.queries))
	}
}

func TestMapUsageRejectsUnsafeOrMismatchedDistribution(t *testing.T) {
	fixture := newServiceFixture(t)
	query := requestlog.UsageQuery{
		FromMS:             time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC).UnixMilli(),
		ToMS:               time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC).UnixMilli(),
		Granularity:        requestlog.UsageGranularityHour,
		Distribution:       requestlog.UsageDistributionDimensionGroup,
		DistributionMetric: requestlog.UsageDistributionMetricRequests,
	}
	tests := []struct {
		name   string
		report requestlog.UsageReport
	}{
		{
			name: "response dimension mismatch",
			report: requestlog.UsageReport{
				Distribution: requestlog.UsageDistribution{
					Dimension: requestlog.UsageDistributionDimensionModel,
					Metric:    requestlog.UsageDistributionMetricRequests,
				},
			},
		},
		{
			name: "response metric mismatch",
			report: requestlog.UsageReport{
				Distribution: requestlog.UsageDistribution{
					Dimension: requestlog.UsageDistributionDimensionGroup,
					Metric:    requestlog.UsageDistributionMetricCost,
				},
			},
		},
		{
			name: "unsafe group identity",
			report: requestlog.UsageReport{
				Distribution: requestlog.UsageDistribution{
					Dimension: requestlog.UsageDistributionDimensionGroup,
					Metric:    requestlog.UsageDistributionMetricRequests,
					Items: []requestlog.UsageDistributionItem{{
						GroupID: uint(maxSafeInteger) + 1,
					}},
				},
			},
		},
		{
			name: "more than five items",
			report: requestlog.UsageReport{
				Distribution: requestlog.UsageDistribution{
					Dimension: requestlog.UsageDistributionDimensionGroup,
					Metric:    requestlog.UsageDistributionMetricRequests,
					Items: []requestlog.UsageDistributionItem{
						{GroupID: 1}, {GroupID: 2}, {GroupID: 3},
						{GroupID: 4}, {GroupID: 5}, {GroupID: 6},
					},
				},
			},
		},
		{
			name: "duplicate group identity",
			report: requestlog.UsageReport{
				Distribution: requestlog.UsageDistribution{
					Dimension: requestlog.UsageDistributionDimensionGroup,
					Metric:    requestlog.UsageDistributionMetricRequests,
					Items:     []requestlog.UsageDistributionItem{{GroupID: 1}, {GroupID: 1}},
				},
			},
		},
		{
			name: "distribution total mismatch",
			report: requestlog.UsageReport{
				Summary: requestlog.UsageAggregate{RequestCount: 2, SuccessCount: 2},
				Distribution: requestlog.UsageDistribution{
					Dimension: requestlog.UsageDistributionDimensionGroup,
					Metric:    requestlog.UsageDistributionMetricRequests,
					Items: []requestlog.UsageDistributionItem{{
						GroupID: 1,
						UsageDistributionAggregate: requestlog.UsageDistributionAggregate{
							RequestCount: 1,
						},
					}},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := fixture.service.mapUsageResponse(
				query.ToMS,
				query,
				test.report,
			); err == nil {
				t.Fatal("mapUsageResponse() error = nil, want invalid metadata rejection")
			}
		})
	}
}

func TestUsageAPIRejectsUnsafeAggregateAndKeepsErrorsSecret(t *testing.T) {
	reader := &recordingUsageStatReader{report: requestlog.UsageReport{
		Summary: requestlog.UsageAggregate{RequestCount: 9_007_199_254_740_992},
	}}
	engine, _ := newUsageTestEngine(t, time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC), reader)
	recorder := performUsageRequest(engine, "test-auth-key", "")
	if recorder.Code != http.StatusInternalServerError ||
		!strings.Contains(recorder.Body.String(), "INTERNAL_SERVER_ERROR") ||
		strings.Contains(recorder.Body.String(), "unsafe") {
		t.Fatalf("unsafe response = %d %s", recorder.Code, recorder.Body.String())
	}

	reader.err = errors.New("usage database secret")
	reader.report = requestlog.UsageReport{}
	recorder = performUsageRequest(engine, "test-auth-key", "")
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "usage database secret") {
		t.Fatalf("reader error response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestUsageAPIRejectsUnsafeProcessStatsWithoutLeakingCause(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*requestlog.Stats)
	}{
		{name: "dropped total", mutate: func(stats *requestlog.Stats) { stats.DroppedTotal = uint64(maxSafeInteger) + 1 }},
		{name: "write failure", mutate: func(stats *requestlog.Stats) { stats.WriteFailureTotal = uint64(maxSafeInteger) + 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine, fixture := newUsageTestEngine(
				t,
				time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC),
				&recordingUsageStatReader{},
			)
			test.mutate(&fixture.requestLogStats.value)
			recorder := performUsageRequest(engine, "test-auth-key", "")
			if recorder.Code != http.StatusInternalServerError ||
				!strings.Contains(recorder.Body.String(), "INTERNAL_SERVER_ERROR") ||
				strings.Contains(strings.ToLower(recorder.Body.String()), "queue") ||
				strings.Contains(strings.ToLower(recorder.Body.String()), "safe") {
				t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestUsageAPIExcludesLegacyZeroAttemptAggregateFromSQLite(t *testing.T) {
	initControlI18n(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	fixture := newServiceFixture(t)
	if err := fixture.db.Create(&models.UsageStat{
		BucketStartMS: now.Add(-time.Hour).UnixMilli(),
		AccessKeyID:   1,
		GroupID:       0,
		Model:         "",
		RequestCount:  1,
		FailureCount:  1,
	}).Error; err != nil {
		t.Fatalf("create legacy zero-attempt UsageStat: %v", err)
	}
	fixture.service.now = func() time.Time { return now }
	fixture.service.usageStats = requestlog.NewService(fixture.db, nil, nil)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	recorder := performUsageRequest(engine, "test-auth-key", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s, want 200", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data struct {
			Summary struct {
				RequestCount int64 `json:"request_count"`
				FailureCount int64 `json:"failure_count"`
			} `json:"summary"`
			Distribution struct {
				Items []json.RawMessage `json:"items"`
				Other json.RawMessage   `json:"other"`
			} `json:"distribution"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.Summary.RequestCount != 0 || envelope.Data.Summary.FailureCount != 0 ||
		len(envelope.Data.Distribution.Items) != 0 ||
		string(envelope.Data.Distribution.Other) != "null" {
		t.Fatalf("usage data = %#v", envelope.Data)
	}
}

func TestUsageAPIAcceptsMaximumSafeCanonicalGroupID(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("maximum JavaScript safe integer does not fit uint on this architecture")
	}
	reader := &recordingUsageStatReader{}
	engine, _ := newUsageTestEngine(
		t,
		time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC),
		reader,
	)
	recorder := performUsageRequest(engine, "test-auth-key", "group_id=9007199254740991")
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s, want 200", recorder.Code, recorder.Body.String())
	}
	if len(reader.queries) != 1 || reader.queries[0].GroupID == nil ||
		uint64(*reader.queries[0].GroupID) != uint64(maxSafeInteger) {
		t.Fatalf("QueryUsage calls = %#v", reader.queries)
	}
}

func TestUsageAPIRequiresManagementAuthentication(t *testing.T) {
	engine, _ := newUsageTestEngine(t, time.Now(), &recordingUsageStatReader{})
	recorder := performUsageRequest(engine, "", "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("response = %d %s, want 401", recorder.Code, recorder.Body.String())
	}
}

func TestUsageAPIBindsAccessKeyScopeAndRedactsProcessHealth(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{
		Name: "usage viewer",
	})
	if err != nil {
		t.Fatalf("CreateAccessKey() error = %v", err)
	}
	now := time.Date(2026, time.August, 8, 17, 30, 0, 0, time.UTC)
	reader := &recordingUsageStatReader{report: requestlog.UsageReport{
		Distribution: requestlog.UsageDistribution{
			Dimension: requestlog.UsageDistributionDimensionModel,
			Metric:    requestlog.UsageDistributionMetricRequests,
			Items: []requestlog.UsageDistributionItem{{
				Model: "allowed-model",
			}},
		},
	}}
	fixture.service.now = func() time.Time { return now }
	fixture.service.usageStats = reader
	fixture.requestLogStats.value.DroppedTotal = 100
	fixture.requestLogStats.value.WriteFailureTotal = 50
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	recorder := performUsageRequest(engine, created.Key, "range=7d&upstream_model=allowed-model")
	if recorder.Code != http.StatusOK {
		t.Fatalf("AccessKey usage response = %d %s, want 200", recorder.Code, recorder.Body.String())
	}
	if len(reader.queries) != 1 || reader.queries[0].AccessKeyID == nil ||
		*reader.queries[0].AccessKeyID != created.ID || reader.queries[0].GroupID != nil ||
		reader.queries[0].Distribution != requestlog.UsageDistributionDimensionModel {
		t.Fatalf("AccessKey UsageQuery = %#v", reader.queries)
	}
	var envelope struct {
		Data struct {
			Distribution struct {
				Dimension string `json:"dimension"`
				Items     []struct {
					Model string `json:"model"`
				} `json:"items"`
			} `json:"distribution"`
			CollectionHealth usageCollectionHealthResponse `json:"collection_health"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode AccessKey usage response: %v", err)
	}
	if envelope.Data.Distribution.Dimension != "model" ||
		len(envelope.Data.Distribution.Items) != 1 ||
		envelope.Data.Distribution.Items[0].Model != "allowed-model" ||
		envelope.Data.CollectionHealth.Scope != "access_key" ||
		envelope.Data.CollectionHealth.DroppedTotal != 0 ||
		envelope.Data.CollectionHealth.WriteFailureTotal != 0 ||
		envelope.Data.CollectionHealth.LastWriteFailureAtMS != nil {
		t.Fatalf("AccessKey usage redaction = %#v", envelope.Data)
	}

	for _, filter := range []string{"group_id=1", "channel_id=openai", "credential_id=101"} {
		forbidden := performUsageRequest(engine, created.Key, filter)
		if forbidden.Code != http.StatusBadRequest || len(reader.queries) != 1 {
			t.Fatalf(
				"AccessKey internal filter %q = %d %s, calls=%d, want 400/no query",
				filter,
				forbidden.Code,
				forbidden.Body.String(),
				len(reader.queries),
			)
		}
	}
}

type recordingUsageStatReader struct {
	queries []requestlog.UsageQuery
	report  requestlog.UsageReport
	err     error
}

func (reader *recordingUsageStatReader) QueryUsage(
	_ context.Context,
	query requestlog.UsageQuery,
) (requestlog.UsageReport, error) {
	reader.queries = append(reader.queries, query)
	if reader.err != nil {
		return requestlog.UsageReport{}, reader.err
	}
	report := reader.report
	if report.Distribution.Dimension == "" {
		report.Distribution.Dimension = query.Distribution
	}
	if report.Distribution.Metric == "" {
		report.Distribution.Metric = query.DistributionMetric
	}
	return report, nil
}

func newUsageTestEngine(
	t *testing.T,
	now time.Time,
	reader *recordingUsageStatReader,
) (*gin.Engine, serviceFixture) {
	t.Helper()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.now = func() time.Time { return now }
	fixture.service.usageStats = reader
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	return engine, fixture
}

func performUsageRequest(engine *gin.Engine, authKey, query string) *httptest.ResponseRecorder {
	target := "/api/usage"
	if query != "" {
		target += "?" + query
	}
	request := httptest.NewRequest(http.MethodGet, target, nil)
	if authKey != "" {
		request.Header.Set("Authorization", "Bearer "+authKey)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func assertUsageErrorCode(t *testing.T, recorder *httptest.ResponseRecorder, want string) {
	t.Helper()
	var envelope struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, recorder.Body.String())
	}
	if recorder.Code != http.StatusBadRequest || envelope.Code != want {
		t.Fatalf("error response = %d/%q, want 400/%q; body=%s", recorder.Code, envelope.Code, want, recorder.Body.String())
	}
}
