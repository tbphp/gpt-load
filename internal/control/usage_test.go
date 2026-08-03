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
	}{
		{
			name:        "24 hours crosses the local and UTC day at an exact hour boundary",
			rawQuery:    "range=24h",
			observedAt:  time.Date(2026, time.July, 27, 0, 34, 56, 789, time.FixedZone("UTC+8", 8*60*60)),
			wantFrom:    time.Date(2026, time.July, 25, 17, 0, 0, 0, time.UTC),
			wantTo:      time.Date(2026, time.July, 26, 17, 0, 0, 0, time.UTC),
			granularity: requestlog.UsageGranularityHour,
		},
		{
			name:        "30 days crosses the local and UTC day at exact day boundaries",
			rawQuery:    "range=30d",
			observedAt:  time.Date(2026, time.July, 26, 20, 34, 56, 789, time.FixedZone("UTC-7", -7*60*60)),
			wantFrom:    time.Date(2026, time.June, 28, 0, 0, 0, 0, time.UTC),
			wantTo:      time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC),
			granularity: requestlog.UsageGranularityDay,
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
				query.Granularity != test.granularity {
				t.Fatalf(
					"parseUsageQuery() window = %d to %d (%s), want %d to %d (%s)",
					query.FromMS,
					query.ToMS,
					query.Granularity,
					test.wantFrom.UnixMilli(),
					test.wantTo.UnixMilli(),
					test.granularity,
				)
			}
		})
	}
}

func TestUsageAPIDefaultsToFixedUTCAligned24HoursAndReturnsZeroArrays(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 34, 56, 789, time.FixedZone("UTC+8", 8*60*60))
	reader := &recordingUsageStatReader{}
	reader.report.BreakdownGroupCount = 14
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
		query.GroupID != nil || query.UpstreamModel != "" || query.Limit != 100 ||
		query.BreakdownOrder != requestlog.UsageBreakdownOrderRequests {
		t.Fatalf("default UsageQuery = %#v", query)
	}
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Range        string                         `json:"range"`
			Granularity  requestlog.UsageGranularity    `json:"granularity"`
			FromMS       int64                          `json:"from_ms"`
			ToMS         int64                          `json:"to_ms"`
			ObservedAtMS int64                          `json:"observed_at_ms"`
			Series       []json.RawMessage              `json:"series"`
			Breakdown    []json.RawMessage              `json:"breakdown"`
			Order        requestlog.UsageBreakdownOrder `json:"breakdown_order"`
			GroupCount   int64                          `json:"breakdown_group_count"`
			Health       struct {
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
		envelope.Data.Breakdown == nil || len(envelope.Data.Breakdown) != 0 ||
		envelope.Data.Order != requestlog.UsageBreakdownOrderRequests ||
		envelope.Data.GroupCount != 14 ||
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
		Breakdown: []requestlog.UsageBreakdown{{
			GroupID: 9, Model: "upstream-model",
			UsageAggregate: requestlog.UsageAggregate{
				RequestCount: 1, UncachedInputTokens: 2, CacheWriteUnknownTokens: 4,
				OutputTokens: 3, PricingPartialCount: 1,
				EstimatedCostNanoUSD: 250_000_000,
			},
		}},
		BreakdownOrder:      requestlog.UsageBreakdownOrderCost,
		BreakdownGroupCount: 1,
	}}
	engine, _ := newUsageTestEngine(t, now, reader)
	recorder := performUsageRequest(
		engine,
		"test-auth-key",
		"range=30d&group_id=9&upstream_model=upstream-model&breakdown_order=cost",
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
		query.UpstreamModel != "upstream-model" || query.Limit != 100 ||
		query.BreakdownOrder != requestlog.UsageBreakdownOrderCost {
		t.Fatalf("30 day UsageQuery = %#v", query)
	}
	var envelope struct {
		Data struct {
			Range       string                         `json:"range"`
			Granularity requestlog.UsageGranularity    `json:"granularity"`
			FromMS      int64                          `json:"from_ms"`
			ToMS        int64                          `json:"to_ms"`
			Order       requestlog.UsageBreakdownOrder `json:"breakdown_order"`
			GroupCount  int64                          `json:"breakdown_group_count"`
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
			Breakdown []struct {
				GroupID                 uint   `json:"group_id"`
				Model                   string `json:"model"`
				CacheWriteUnknownTokens int64  `json:"cache_write_unknown_tokens"`
				PricingPartialCount     int64  `json:"pricing_partial_count"`
				EstimatedCostNanoUSD    string `json:"estimated_cost_nano_usd"`
			} `json:"breakdown"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.Range != "30d" ||
		envelope.Data.Granularity != requestlog.UsageGranularityDay ||
		envelope.Data.FromMS != time.Date(2026, time.June, 28, 0, 0, 0, 0, time.UTC).UnixMilli() ||
		envelope.Data.ToMS != time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC).UnixMilli() ||
		envelope.Data.Order != requestlog.UsageBreakdownOrderCost ||
		envelope.Data.GroupCount != 1 ||
		envelope.Data.Summary.TotalTokens != 9 ||
		envelope.Data.Summary.CacheWriteUnknownTokens != 4 ||
		envelope.Data.Summary.PricingPartialCount != 1 ||
		envelope.Data.Summary.EstimatedCostNanoUSD != "0" ||
		len(envelope.Data.Series) != 1 ||
		envelope.Data.Series[0].BucketStartMS != time.Date(2026, time.June, 28, 0, 0, 0, 0, time.UTC).UnixMilli() ||
		envelope.Data.Series[0].TotalTokens != 9 ||
		envelope.Data.Series[0].CacheWriteUnknownTokens != 4 ||
		envelope.Data.Series[0].PricingPartialCount != 1 ||
		envelope.Data.Series[0].EstimatedCostNanoUSD != "1123456789012" ||
		len(envelope.Data.Breakdown) != 1 || envelope.Data.Breakdown[0].GroupID != 9 ||
		envelope.Data.Breakdown[0].Model != "upstream-model" ||
		envelope.Data.Breakdown[0].CacheWriteUnknownTokens != 4 ||
		envelope.Data.Breakdown[0].PricingPartialCount != 1 ||
		envelope.Data.Breakdown[0].EstimatedCostNanoUSD != "250000000" {
		t.Fatalf("usage response = %#v", envelope.Data)
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
		{query: "range=24h&from_ms=1&to_ms=2", code: "VALIDATION_FAILED"},
		{query: "range=1h", code: "VALIDATION_FAILED"},
		{query: "group_id=0", code: "VALIDATION_FAILED"},
		{query: "group_id=01", code: "BAD_REQUEST"},
		{query: "group_id=%2B1", code: "BAD_REQUEST"},
		{query: "group_id=%201", code: "BAD_REQUEST"},
		{query: "group_id=1&group_id=2", code: "BAD_REQUEST"},
		{query: "group_id=9007199254740992", code: "VALIDATION_FAILED"},
		{query: "group_id=-1", code: "BAD_REQUEST"},
		{query: "model=legacy", code: "BAD_REQUEST"},
		{query: "upstream_model=", code: "VALIDATION_FAILED"},
		{query: "breakdown_order=", code: "VALIDATION_FAILED"},
		{query: "breakdown_order=unknown", code: "VALIDATION_FAILED"},
		{
			query: "breakdown_order=cost&breakdown_order=requests",
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

func TestMapUsageRejectsUnsafeOrMismatchedBreakdownMetadata(t *testing.T) {
	fixture := newServiceFixture(t)
	query := requestlog.UsageQuery{
		FromMS:         time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC).UnixMilli(),
		ToMS:           time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC).UnixMilli(),
		Granularity:    requestlog.UsageGranularityHour,
		Limit:          100,
		BreakdownOrder: requestlog.UsageBreakdownOrderRequests,
	}
	tests := []struct {
		name   string
		report requestlog.UsageReport
	}{
		{
			name: "negative group count",
			report: requestlog.UsageReport{
				BreakdownOrder:      requestlog.UsageBreakdownOrderRequests,
				BreakdownGroupCount: -1,
			},
		},
		{
			name: "unsafe group count",
			report: requestlog.UsageReport{
				BreakdownOrder:      requestlog.UsageBreakdownOrderRequests,
				BreakdownGroupCount: maxSafeInteger + 1,
			},
		},
		{
			name: "response order mismatch",
			report: requestlog.UsageReport{
				BreakdownOrder: requestlog.UsageBreakdownOrderCost,
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

func TestUsageAPIReturnsUnattributedAggregateFromSQLite(t *testing.T) {
	initControlI18n(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	fixture := newServiceFixture(t)
	if err := fixture.db.Create(&models.UsageStat{
		BucketStartMS: now.Add(-time.Hour).UnixMilli(),
		AccessKeyID:   0,
		GroupID:       0,
		Model:         "",
		RequestCount:  1,
		SuccessCount:  1,
	}).Error; err != nil {
		t.Fatalf("create unattributed UsageStat: %v", err)
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
			Breakdown []struct {
				GroupID      uint   `json:"group_id"`
				Model        string `json:"model"`
				RequestCount int64  `json:"request_count"`
			} `json:"breakdown"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(envelope.Data.Breakdown) != 1 ||
		envelope.Data.Breakdown[0].GroupID != 0 ||
		envelope.Data.Breakdown[0].Model != "" ||
		envelope.Data.Breakdown[0].RequestCount != 1 {
		t.Fatalf("unattributed breakdown = %#v", envelope.Data.Breakdown)
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
	if report.BreakdownOrder == "" {
		report.BreakdownOrder = query.BreakdownOrder
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
