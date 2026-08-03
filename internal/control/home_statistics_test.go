package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/epochms"
	"gpt-load/internal/platform/version"
	"gpt-load/internal/requestlog"
)

func TestHomeBaseHTTPUsesAuthenticationEnvelopeAndServerClock(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	server := NewServer(
		&config.Config{AuthKey: "test-auth-key"},
		fixture.service,
	)
	now := time.Date(2026, time.July, 30, 12, 34, 56, 789, time.UTC)
	startedAt := time.Date(2026, time.July, 29, 1, 2, 3, 456, time.UTC)
	server.now = func() time.Time { return now }
	server.startedAt = startedAt
	engine := gin.New()
	server.RegisterRoutes(engine)

	unauthorized := performHomeRequest(engine, "/api/home", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf(
			"unauthorized GET /api/home = %d %s",
			unauthorized.Code,
			unauthorized.Body.String(),
		)
	}

	recorder := performHomeRequest(engine, "/api/home", "test-auth-key")
	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"GET /api/home = %d %s, want 200",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			ServerNowMS int64           `json:"server_now_ms"`
			StartedAtMS int64           `json:"started_at_ms"`
			Version     string          `json:"version"`
			Inventory   HomeInventory   `json:"inventory"`
			AccessKeys  []HomeAccessKey `json:"access_keys"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode home response: %v", err)
	}
	if envelope.Code != 0 || envelope.Message == "" ||
		envelope.Data.ServerNowMS != now.UnixMilli() ||
		envelope.Data.StartedAtMS != startedAt.UnixMilli() ||
		envelope.Data.Version != version.Version ||
		envelope.Data.Inventory != (HomeInventory{}) ||
		envelope.Data.AccessKeys == nil ||
		len(envelope.Data.AccessKeys) != 0 {
		t.Fatalf("home response = %#v", envelope)
	}
	assertManagementWireObject(
		t,
		envelope.Data,
		[]string{
			"server_now_ms",
			"started_at_ms",
			"version",
			"inventory",
			"access_keys",
		},
	)
}

func TestHomeBaseHTTPRejectsEveryQueryBeforeReading(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.db = nil
	server := NewServer(
		&config.Config{AuthKey: "test-auth-key"},
		fixture.service,
	)
	engine := gin.New()
	server.RegisterRoutes(engine)

	for _, target := range []string{
		"/api/home?unknown=1",
		"/api/home?range=24h",
		"/api/home?unknown=",
	} {
		recorder := performHomeRequest(engine, target, "test-auth-key")
		assertHomeHTTPError(t, recorder, http.StatusBadRequest, "BAD_REQUEST")
	}
}

func TestParseHomeStatisticsQueryIsStrictAndDefaultsTo24Hours(t *testing.T) {
	const observedAtMS = int64(1_785_412_345_678)
	tests := []struct {
		rawQuery string
		want     requestlog.HomeStatisticsRange
		code     string
	}{
		{rawQuery: "", want: requestlog.HomeStatistics24H},
		{rawQuery: "range=24h", want: requestlog.HomeStatistics24H},
		{rawQuery: "range=30d", want: requestlog.HomeStatistics30D},
		{rawQuery: "range=", code: "VALIDATION_FAILED"},
		{rawQuery: "range=1h", code: "VALIDATION_FAILED"},
		{rawQuery: "range=24h&range=30d", code: "BAD_REQUEST"},
		{rawQuery: "unknown=1", code: "BAD_REQUEST"},
		{rawQuery: "range=24h&unknown=1", code: "BAD_REQUEST"},
	}
	for _, test := range tests {
		t.Run(test.rawQuery, func(t *testing.T) {
			query, apiErr := parseHomeStatisticsQuery(
				test.rawQuery,
				observedAtMS,
			)
			if test.code != "" {
				if apiErr == nil || apiErr.Code != test.code {
					t.Fatalf(
						"parseHomeStatisticsQuery() error = %v, want %s",
						apiErr,
						test.code,
					)
				}
				return
			}
			if apiErr != nil {
				t.Fatalf("parseHomeStatisticsQuery() error = %v", apiErr)
			}
			if query.Range != test.want ||
				query.ObservedAtMS != observedAtMS {
				t.Fatalf("query = %#v", query)
			}
		})
	}

	for _, observedAtMS := range []int64{-1, maxSafeInteger + 1} {
		if _, apiErr := parseHomeStatisticsQuery("", observedAtMS); apiErr == nil ||
			apiErr.Code != "INTERNAL_SERVER_ERROR" {
			t.Fatalf(
				"parseHomeStatisticsQuery(observed=%d) error = %v",
				observedAtMS,
				apiErr,
			)
		}
	}
}

func TestHomeStatisticsHTTPDefaultsToDense24HoursAndMapsExactWire(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 34, 56, 789, time.UTC)
	currentAccessKeyName := "current-access"
	reader := &recordingHomeStatisticsReader{
		fn: func(query requestlog.HomeStatisticsQuery) requestlog.HomeStatisticsReport {
			report := homeStatisticsReportForQuery(t, query)
			report.Summary = requestlog.UsageAggregate{
				RequestCount: 8, SuccessCount: 6, FailureCount: 2,
				UncachedInputTokens: 11, CacheReadTokens: 12,
				CacheWrite5MTokens: 13, CacheWrite1HTokens: 14,
				CacheWriteUnknownTokens: 16,
				OutputTokens:            15, EstimatedCostNanoUSD: 58_200_000_000,
				UsageMissingCount: 1, PartialCount: 2,
				UnpricedRequestCount: 3, PricingPartialCount: 4,
			}
			report.Series[0].UsageAggregate = requestlog.UsageAggregate{
				RequestCount: 4,
				SuccessCount: 3,
				FailureCount: 1,
			}
			report.TopModels = []requestlog.HomeModelRanking{{
				Model: "sonnet-4.5",
				UsageAggregate: requestlog.UsageAggregate{
					RequestCount: 3, SuccessCount: 3, UncachedInputTokens: 20,
					OutputTokens: 30, EstimatedCostNanoUSD: 18_400_000_000,
				},
			}}
			report.TopGroups = []requestlog.HomeGroupRanking{{
				Group: requestlog.HomeStatisticsRef{
					ID: 9, Deleted: true,
				},
				UsageAggregate: requestlog.UsageAggregate{
					RequestCount: 4, SuccessCount: 4, UncachedInputTokens: 40,
					OutputTokens: 50, EstimatedCostNanoUSD: 24_100_000_000,
				},
			}}
			report.TopAccessKeys = []requestlog.HomeAccessKeyRanking{{
				AccessKey: requestlog.HomeStatisticsRef{
					ID: 3, Name: &currentAccessKeyName,
				},
				UsageAggregate: requestlog.UsageAggregate{
					RequestCount: 5, SuccessCount: 5, UncachedInputTokens: 60,
					OutputTokens: 70, EstimatedCostNanoUSD: 28_440_000_000,
				},
			}}
			return report
		},
	}
	engine := newHomeStatisticsTestEngine(t, now, reader)
	recorder := performHomeRequest(
		engine,
		"/api/home/statistics",
		"test-auth-key",
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"GET /api/home/statistics = %d %s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	if len(reader.queries) != 1 ||
		reader.queries[0].Range != requestlog.HomeStatistics24H ||
		reader.queries[0].ObservedAtMS != now.UnixMilli() {
		t.Fatalf("QueryHomeStatistics calls = %#v", reader.queries)
	}

	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Range        string                      `json:"range"`
			Granularity  requestlog.UsageGranularity `json:"granularity"`
			FromMS       int64                       `json:"from_ms"`
			ToMS         int64                       `json:"to_ms"`
			ObservedAtMS int64                       `json:"observed_at_ms"`
			Summary      struct {
				RequestCount            int64  `json:"request_count"`
				SuccessCount            int64  `json:"success_count"`
				FailureCount            int64  `json:"failure_count"`
				TotalTokens             int64  `json:"total_tokens"`
				CacheWriteUnknownTokens int64  `json:"cache_write_unknown_tokens"`
				EstimatedCostNanoUSD    string `json:"estimated_cost_nano_usd"`
				UsageMissingCount       int64  `json:"usage_missing_count"`
				PartialCount            int64  `json:"partial_count"`
				UnpricedRequestCount    int64  `json:"unpriced_request_count"`
				PricingPartialCount     int64  `json:"pricing_partial_count"`
			} `json:"summary"`
			Series []struct {
				BucketStartMS int64 `json:"bucket_start_ms"`
				BucketEndMS   int64 `json:"bucket_end_ms"`
				RequestCount  int64 `json:"request_count"`
				FailureCount  int64 `json:"failure_count"`
			} `json:"series"`
			Rankings struct {
				Models []struct {
					Model                string `json:"model"`
					RequestCount         int64  `json:"request_count"`
					TotalTokens          int64  `json:"total_tokens"`
					EstimatedCostNanoUSD string `json:"estimated_cost_nano_usd"`
				} `json:"models"`
				Groups []struct {
					Group struct {
						ID      uint    `json:"id"`
						Name    *string `json:"name"`
						Deleted bool    `json:"deleted"`
					} `json:"group"`
					EstimatedCostNanoUSD string `json:"estimated_cost_nano_usd"`
				} `json:"groups"`
				AccessKeys []struct {
					AccessKey struct {
						ID      uint    `json:"id"`
						Name    *string `json:"name"`
						Deleted bool    `json:"deleted"`
					} `json:"access_key"`
					EstimatedCostNanoUSD string `json:"estimated_cost_nano_usd"`
				} `json:"access_keys"`
			} `json:"rankings"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode home statistics response: %v", err)
	}
	if envelope.Code != 0 ||
		envelope.Data.Range != "24h" ||
		envelope.Data.Granularity != requestlog.UsageGranularityHour ||
		envelope.Data.ObservedAtMS != now.UnixMilli() ||
		envelope.Data.ToMS-envelope.Data.FromMS !=
			24*epochms.MillisecondsPerHour ||
		envelope.Data.Summary.RequestCount != 8 ||
		envelope.Data.Summary.SuccessCount != 6 ||
		envelope.Data.Summary.FailureCount != 2 ||
		envelope.Data.Summary.TotalTokens != 81 ||
		envelope.Data.Summary.CacheWriteUnknownTokens != 16 ||
		envelope.Data.Summary.EstimatedCostNanoUSD != "58200000000" ||
		envelope.Data.Summary.UsageMissingCount != 1 ||
		envelope.Data.Summary.PartialCount != 2 ||
		envelope.Data.Summary.UnpricedRequestCount != 3 ||
		envelope.Data.Summary.PricingPartialCount != 4 ||
		len(envelope.Data.Series) != 24 ||
		envelope.Data.Series[0].RequestCount != 4 ||
		envelope.Data.Series[0].FailureCount != 1 ||
		len(envelope.Data.Rankings.Models) != 1 ||
		envelope.Data.Rankings.Models[0].Model != "sonnet-4.5" ||
		envelope.Data.Rankings.Models[0].EstimatedCostNanoUSD != "18400000000" ||
		len(envelope.Data.Rankings.Groups) != 1 ||
		envelope.Data.Rankings.Groups[0].Group.ID != 9 ||
		envelope.Data.Rankings.Groups[0].Group.Name != nil ||
		!envelope.Data.Rankings.Groups[0].Group.Deleted ||
		envelope.Data.Rankings.Groups[0].EstimatedCostNanoUSD != "24100000000" ||
		len(envelope.Data.Rankings.AccessKeys) != 1 ||
		envelope.Data.Rankings.AccessKeys[0].AccessKey.Name == nil ||
		*envelope.Data.Rankings.AccessKeys[0].AccessKey.Name != currentAccessKeyName ||
		envelope.Data.Rankings.AccessKeys[0].EstimatedCostNanoUSD != "28440000000" {
		t.Fatalf("home statistics response = %#v", envelope.Data)
	}
	if strings.Contains(recorder.Body.String(), "2026-") {
		t.Fatalf("response exposes RFC 3339 timestamp: %s", recorder.Body.String())
	}
	var rawEnvelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &rawEnvelope); err != nil {
		t.Fatalf("decode raw home statistics response: %v", err)
	}
	var rankingWire struct {
		Data struct {
			Rankings struct {
				Models []map[string]json.RawMessage `json:"models"`
			} `json:"rankings"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &rankingWire); err != nil {
		t.Fatalf("decode raw model ranking: %v", err)
	}
	if len(rankingWire.Data.Rankings.Models) != 1 {
		t.Fatalf("model ranking wire = %s", recorder.Body.String())
	}
	modelRanking := rankingWire.Data.Rankings.Models[0]
	for _, key := range []string{
		"model",
		"request_count",
		"total_tokens",
		"estimated_cost_nano_usd",
	} {
		if _, exists := modelRanking[key]; !exists {
			t.Fatalf("model ranking missing %q: %s", key, recorder.Body.String())
		}
	}
	if len(modelRanking) != 4 {
		t.Fatalf("model ranking exposes non-model dimension: %s", recorder.Body.String())
	}
	for _, forbidden := range []string{"health", "timezone", "link"} {
		if _, exists := rawEnvelope.Data[forbidden]; exists {
			t.Fatalf(
				"home statistics response exposes %q: %s",
				forbidden,
				recorder.Body.String(),
			)
		}
	}
	assertManagementWireObject(
		t,
		envelope.Data,
		[]string{
			"range",
			"granularity",
			"from_ms",
			"to_ms",
			"observed_at_ms",
			"summary",
			"series",
			"rankings",
		},
	)
}

func TestHomeStatisticsHTTPAcceptsOnlyOneSupportedRange(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	reader := &recordingHomeStatisticsReader{}
	engine := newHomeStatisticsTestEngine(t, now, reader)

	recorder := performHomeRequest(
		engine,
		"/api/home/statistics?range=30d",
		"test-auth-key",
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("30d response = %d %s", recorder.Code, recorder.Body.String())
	}
	if len(reader.queries) != 1 ||
		reader.queries[0].Range != requestlog.HomeStatistics30D {
		t.Fatalf("30d queries = %#v", reader.queries)
	}
	var response struct {
		Data struct {
			Granularity requestlog.UsageGranularity `json:"granularity"`
			Series      []json.RawMessage           `json:"series"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode 30d response: %v", err)
	}
	if response.Data.Granularity != requestlog.UsageGranularityDay ||
		len(response.Data.Series) != 30 {
		t.Fatalf("30d response = %#v", response.Data)
	}

	validCalls := len(reader.queries)
	for _, target := range []string{
		"/api/home/statistics?unknown=1",
		"/api/home/statistics?range=24h&range=30d",
		"/api/home/statistics?range=1h",
		"/api/home/statistics?range=",
	} {
		recorder := performHomeRequest(engine, target, "test-auth-key")
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf(
				"%s response = %d %s",
				target,
				recorder.Code,
				recorder.Body.String(),
			)
		}
	}
	if len(reader.queries) != validCalls {
		t.Fatalf(
			"QueryHomeStatistics calls = %d after invalid queries, want %d",
			len(reader.queries),
			validCalls,
		)
	}
}

func TestHomeStatisticsHTTPFailsClosedOnUnsafeDataAndReadError(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	t.Run("unsafe aggregate", func(t *testing.T) {
		reader := &recordingHomeStatisticsReader{
			fn: func(query requestlog.HomeStatisticsQuery) requestlog.HomeStatisticsReport {
				report := homeStatisticsReportForQuery(t, query)
				report.Summary.RequestCount = maxSafeInteger + 1
				return report
			},
		}
		recorder := performHomeRequest(
			newHomeStatisticsTestEngine(t, now, reader),
			"/api/home/statistics",
			"test-auth-key",
		)
		assertHomeHTTPError(
			t,
			recorder,
			http.StatusInternalServerError,
			"INTERNAL_SERVER_ERROR",
		)
	})

	t.Run("too many rankings", func(t *testing.T) {
		reader := &recordingHomeStatisticsReader{
			fn: func(query requestlog.HomeStatisticsQuery) requestlog.HomeStatisticsReport {
				report := homeStatisticsReportForQuery(t, query)
				report.TopGroups = make([]requestlog.HomeGroupRanking, 6)
				return report
			},
		}
		recorder := performHomeRequest(
			newHomeStatisticsTestEngine(t, now, reader),
			"/api/home/statistics",
			"test-auth-key",
		)
		assertHomeHTTPError(
			t,
			recorder,
			http.StatusInternalServerError,
			"INTERNAL_SERVER_ERROR",
		)
	})

	t.Run("reader error", func(t *testing.T) {
		reader := &recordingHomeStatisticsReader{
			err: errors.New("private database detail"),
		}
		recorder := performHomeRequest(
			newHomeStatisticsTestEngine(t, now, reader),
			"/api/home/statistics",
			"test-auth-key",
		)
		assertHomeHTTPError(
			t,
			recorder,
			http.StatusInternalServerError,
			"DATABASE_ERROR",
		)
		if strings.Contains(recorder.Body.String(), "private database detail") {
			t.Fatalf("response leaks reader error: %s", recorder.Body.String())
		}
	})
}

type recordingHomeStatisticsReader struct {
	queries []requestlog.HomeStatisticsQuery
	report  requestlog.HomeStatisticsReport
	fn      func(requestlog.HomeStatisticsQuery) requestlog.HomeStatisticsReport
	err     error
}

func (reader *recordingHomeStatisticsReader) QueryHomeStatistics(
	_ context.Context,
	query requestlog.HomeStatisticsQuery,
) (requestlog.HomeStatisticsReport, error) {
	reader.queries = append(reader.queries, query)
	if reader.err != nil {
		return requestlog.HomeStatisticsReport{}, reader.err
	}
	if reader.fn != nil {
		return reader.fn(query), nil
	}
	if reader.report.Range != "" {
		return reader.report, nil
	}
	return homeStatisticsReportForQuery(nil, query), nil
}

func homeStatisticsReportForQuery(
	t *testing.T,
	query requestlog.HomeStatisticsQuery,
) requestlog.HomeStatisticsReport {
	if t != nil {
		t.Helper()
	}
	var width int64
	var count int
	var granularity requestlog.UsageGranularity
	switch query.Range {
	case requestlog.HomeStatistics24H:
		width = epochms.MillisecondsPerHour
		count = 24
		granularity = requestlog.UsageGranularityHour
	case requestlog.HomeStatistics30D:
		width = epochms.MillisecondsPerDay
		count = 30
		granularity = requestlog.UsageGranularityDay
	default:
		if t != nil {
			t.Fatalf("unsupported test range %q", query.Range)
		}
		return requestlog.HomeStatisticsReport{}
	}
	fromMS, toMS, err := epochms.WindowEndingAt(
		query.ObservedAtMS,
		width,
		count,
	)
	if err != nil {
		if t != nil {
			t.Fatalf("WindowEndingAt() error = %v", err)
		}
		return requestlog.HomeStatisticsReport{}
	}
	report := requestlog.HomeStatisticsReport{
		Range:         query.Range,
		ObservedAtMS:  query.ObservedAtMS,
		FromMS:        fromMS,
		ToMS:          toMS,
		Granularity:   granularity,
		Series:        make([]requestlog.UsageSeriesPoint, 0, count),
		TopModels:     []requestlog.HomeModelRanking{},
		TopGroups:     []requestlog.HomeGroupRanking{},
		TopAccessKeys: []requestlog.HomeAccessKeyRanking{},
	}
	for startMS := fromMS; startMS < toMS; startMS += width {
		report.Series = append(report.Series, requestlog.UsageSeriesPoint{
			BucketStartMS: startMS,
			BucketEndMS:   startMS + width,
		})
	}
	return report
}

func newHomeStatisticsTestEngine(
	t *testing.T,
	now time.Time,
	reader *recordingHomeStatisticsReader,
) *gin.Engine {
	t.Helper()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.homeStatistics = reader
	server := NewServer(
		&config.Config{AuthKey: "test-auth-key"},
		fixture.service,
	)
	server.now = func() time.Time { return now }
	engine := gin.New()
	server.RegisterRoutes(engine)
	return engine
}

func performHomeRequest(
	engine *gin.Engine,
	target string,
	authKey string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	if authKey != "" {
		request.Header.Set("Authorization", "Bearer "+authKey)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func assertHomeHTTPError(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	status int,
	code string,
) {
	t.Helper()
	var envelope struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, recorder.Body.String())
	}
	if recorder.Code != status || envelope.Code != code {
		t.Fatalf(
			"error response = %d/%q, want %d/%q; body=%s",
			recorder.Code,
			envelope.Code,
			status,
			code,
			recorder.Body.String(),
		)
	}
}
