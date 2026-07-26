package control

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/requestlog"
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

func TestUsageAPIDefaultsToExactTrailing24HoursAndReturnsZeroArrays(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 34, 56, 789, time.FixedZone("UTC+8", 8*60*60))
	reader := &recordingUsageStatReader{}
	engine, fixture := newUsageTestEngine(t, now, reader)
	fixture.requestLogStats.value.PersistedTotal = 8

	recorder := performUsageRequest(engine, "test-auth-key", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	if len(reader.queries) != 1 {
		t.Fatalf("QueryUsage calls = %d, want one", len(reader.queries))
	}
	query := reader.queries[0]
	if query.Granularity != requestlog.UsageGranularityHour ||
		query.From.Format(time.RFC3339Nano) != "2026-07-26T04:34:56.000000789Z" ||
		query.To.Format(time.RFC3339Nano) != "2026-07-27T04:34:56.000000789Z" ||
		query.GroupID != nil || query.Model != "" || query.Limit != 100 {
		t.Fatalf("default UsageQuery = %#v", query)
	}
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			ObservedAt string            `json:"observed_at"`
			Series     []json.RawMessage `json:"series"`
			Breakdown  []json.RawMessage `json:"breakdown"`
			RequestLog struct {
				PersistedTotal uint64 `json:"persisted_total"`
			} `json:"request_log"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Code != 0 || envelope.Data.ObservedAt != "2026-07-27T04:34:56.000000789Z" ||
		envelope.Data.Series == nil || len(envelope.Data.Series) != 0 ||
		envelope.Data.Breakdown == nil || len(envelope.Data.Breakdown) != 0 ||
		envelope.Data.RequestLog.PersistedTotal != 8 {
		t.Fatalf("default usage envelope = %#v", envelope)
	}
}

func TestUsageAPISelectsThirtyUTCDaysAndAppliesFilters(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	reader := &recordingUsageStatReader{report: requestlog.UsageReport{
		Summary: requestlog.UsageAggregate{RequestCount: 1, UncachedInputTokens: 2, OutputTokens: 3, Cost: math.Copysign(0, -1)},
		Series: []requestlog.UsageSeriesPoint{{
			BucketStart: time.Date(2026, time.June, 27, 0, 0, 0, 0, time.UTC),
			BucketEnd:   time.Date(2026, time.June, 28, 0, 0, 0, 0, time.UTC),
			UsageAggregate: requestlog.UsageAggregate{
				RequestCount: 1, UncachedInputTokens: 2, OutputTokens: 3, Cost: 1.1234567890123,
			},
		}},
		Breakdown: []requestlog.UsageBreakdown{{
			GroupID: 9, Model: "upstream-model",
			UsageAggregate: requestlog.UsageAggregate{RequestCount: 1, UncachedInputTokens: 2, OutputTokens: 3, Cost: 0.25},
		}},
	}}
	engine, _ := newUsageTestEngine(t, now, reader)
	recorder := performUsageRequest(engine, "test-auth-key", "range=30d&group_id=9&model=upstream-model")
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	if len(reader.queries) != 1 {
		t.Fatalf("QueryUsage calls = %d, want one", len(reader.queries))
	}
	query := reader.queries[0]
	if query.Granularity != requestlog.UsageGranularityDay ||
		query.From.Format(time.RFC3339Nano) != "2026-06-27T12:00:00Z" ||
		!query.To.Equal(now) || query.GroupID == nil || *query.GroupID != 9 ||
		query.Model != "upstream-model" || query.Limit != 100 {
		t.Fatalf("30 day UsageQuery = %#v", query)
	}
	var envelope struct {
		Data struct {
			Summary struct {
				TotalTokens      int64       `json:"total_tokens"`
				EstimatedCostUSD json.Number `json:"estimated_cost_usd"`
			} `json:"summary"`
			Series []struct {
				BucketStart      string      `json:"bucket_start"`
				TotalTokens      int64       `json:"total_tokens"`
				EstimatedCostUSD json.Number `json:"estimated_cost_usd"`
			} `json:"series"`
			Breakdown []struct {
				GroupID uint        `json:"group_id"`
				Model   string      `json:"model"`
				Cost    json.Number `json:"estimated_cost_usd"`
			} `json:"breakdown"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.Summary.TotalTokens != 5 || envelope.Data.Summary.EstimatedCostUSD.String() != "0" ||
		len(envelope.Data.Series) != 1 || envelope.Data.Series[0].BucketStart != "2026-06-27T00:00:00Z" ||
		envelope.Data.Series[0].TotalTokens != 5 || envelope.Data.Series[0].EstimatedCostUSD.String() != "1.12345678901" ||
		len(envelope.Data.Breakdown) != 1 || envelope.Data.Breakdown[0].GroupID != 9 ||
		envelope.Data.Breakdown[0].Model != "upstream-model" || envelope.Data.Breakdown[0].Cost.String() != "0.25" {
		t.Fatalf("usage response = %#v", envelope.Data)
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
		{query: "range=1h", code: "VALIDATION_FAILED"},
		{query: "group_id=0", code: "VALIDATION_FAILED"},
		{query: "group_id=9007199254740992", code: "VALIDATION_FAILED"},
		{query: "group_id=-1", code: "BAD_REQUEST"},
		{query: "model=", code: "VALIDATION_FAILED"},
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
		{name: "enqueued", mutate: func(stats *requestlog.Stats) { stats.EnqueuedTotal = uint64(maxSafeInteger) + 1 }},
		{name: "persisted", mutate: func(stats *requestlog.Stats) { stats.PersistedTotal = uint64(maxSafeInteger) + 1 }},
		{name: "dropped not running", mutate: func(stats *requestlog.Stats) { stats.DroppedNotRunningTotal = uint64(maxSafeInteger) + 1 }},
		{name: "dropped queue full", mutate: func(stats *requestlog.Stats) { stats.DroppedQueueFullTotal = uint64(maxSafeInteger) + 1 }},
		{name: "dropped stopping", mutate: func(stats *requestlog.Stats) { stats.DroppedStoppingTotal = uint64(maxSafeInteger) + 1 }},
		{name: "dropped persist failed", mutate: func(stats *requestlog.Stats) { stats.DroppedPersistFailedTotal = uint64(maxSafeInteger) + 1 }},
		{name: "dropped shutdown", mutate: func(stats *requestlog.Stats) { stats.DroppedShutdownTotal = uint64(maxSafeInteger) + 1 }},
		{name: "dropped total", mutate: func(stats *requestlog.Stats) { stats.DroppedTotal = uint64(maxSafeInteger) + 1 }},
		{name: "write failure", mutate: func(stats *requestlog.Stats) { stats.WriteFailureTotal = uint64(maxSafeInteger) + 1 }},
		{name: "retention delete failure", mutate: func(stats *requestlog.Stats) { stats.RetentionDeleteFailureTotal = uint64(maxSafeInteger) + 1 }},
		{name: "negative queue depth", mutate: func(stats *requestlog.Stats) { stats.QueueDepth = -1 }},
	}
	if strconv.IntSize == 64 {
		unsafeQueueCapacity := maxSafeInteger + 1
		tests = append(tests, struct {
			name   string
			mutate func(*requestlog.Stats)
		}{
			name: "unsafe queue capacity",
			mutate: func(stats *requestlog.Stats) {
				stats.QueueCapacity = int(unsafeQueueCapacity)
			},
		})
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

func TestUsageAPIRejectsUnattributedBreakdownWithoutLeakingCause(t *testing.T) {
	reader := &recordingUsageStatReader{report: requestlog.UsageReport{
		Breakdown: []requestlog.UsageBreakdown{{GroupID: 0, Model: "unattributed"}},
	}}
	engine, _ := newUsageTestEngine(t, time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC), reader)
	recorder := performUsageRequest(engine, "test-auth-key", "")
	if recorder.Code != http.StatusInternalServerError ||
		!strings.Contains(recorder.Body.String(), "INTERNAL_SERVER_ERROR") ||
		strings.Contains(strings.ToLower(recorder.Body.String()), "group") {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
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
	return reader.report, nil
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
