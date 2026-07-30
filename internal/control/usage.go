package control

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/requestlog"
)

const (
	usageRange24Hours = "24h"
	usageRange30Days  = "30d"
	usageBreakdownMax = 100
	maxSafeInteger    = int64(9007199254740991)
)

type UsageStatReader interface {
	QueryUsage(context.Context, requestlog.UsageQuery) (requestlog.UsageReport, error)
}

type usageAggregateResponse struct {
	RequestCount         int64       `json:"request_count"`
	SuccessCount         int64       `json:"success_count"`
	FailureCount         int64       `json:"failure_count"`
	UncachedInputTokens  int64       `json:"uncached_input_tokens"`
	CacheReadTokens      int64       `json:"cache_read_tokens"`
	CacheWrite5MTokens   int64       `json:"cache_write_5m_tokens"`
	CacheWrite1HTokens   int64       `json:"cache_write_1h_tokens"`
	OutputTokens         int64       `json:"output_tokens"`
	TotalTokens          int64       `json:"total_tokens"`
	EstimatedCostUSD     json.Number `json:"estimated_cost_usd"`
	UsageMissingCount    int64       `json:"usage_missing_count"`
	PartialCount         int64       `json:"partial_count"`
	UnpricedRequestCount int64       `json:"unpriced_request_count"`
}

type usageSeriesResponse struct {
	BucketStart string `json:"bucket_start"`
	BucketEnd   string `json:"bucket_end"`
	usageAggregateResponse
}

type usageBreakdownResponse struct {
	GroupID uint   `json:"group_id"`
	Model   string `json:"model"`
	usageAggregateResponse
}

type usageCollectionHealthResponse struct {
	Scope              string     `json:"scope"`
	DroppedTotal       uint64     `json:"dropped_total"`
	WriteFailureTotal  uint64     `json:"write_failure_total"`
	LastWriteFailureAt *time.Time `json:"last_write_failure_at"`
}

type usageResponse struct {
	Range               string                         `json:"range"`
	Granularity         requestlog.UsageGranularity    `json:"granularity"`
	Timezone            string                         `json:"timezone"`
	From                string                         `json:"from"`
	To                  string                         `json:"to"`
	ObservedAt          string                         `json:"observed_at"`
	Summary             usageAggregateResponse         `json:"summary"`
	Series              []usageSeriesResponse          `json:"series"`
	Breakdown           []usageBreakdownResponse       `json:"breakdown"`
	BreakdownTruncated  bool                           `json:"breakdown_truncated"`
	BreakdownOrder      requestlog.UsageBreakdownOrder `json:"breakdown_order"`
	BreakdownGroupCount int64                          `json:"breakdown_group_count"`
	CollectionHealth    usageCollectionHealthResponse  `json:"collection_health"`
}

func (service *Service) QueryUsage(
	ctx context.Context,
	query requestlog.UsageQuery,
) (requestlog.UsageReport, error) {
	if service.usageStats == nil {
		return requestlog.UsageReport{}, app_errors.ErrInternalServer
	}
	report, err := service.usageStats.QueryUsage(ctx, query)
	if err != nil {
		return requestlog.UsageReport{}, app_errors.ParseDBError(err)
	}
	return report, nil
}

func (server *Server) handleUsage(c *gin.Context) {
	observedAt := server.service.now().UTC()
	query, apiErr := parseUsageQuery(c.Request.URL.RawQuery, observedAt)
	if apiErr != nil {
		writeServiceError(c, "usage", apiErr)
		return
	}
	report, err := server.service.QueryUsage(c.Request.Context(), query)
	if err != nil {
		writeServiceError(c, "usage", err)
		return
	}
	result, err := server.service.mapUsageResponse(observedAt, query, report)
	if err != nil {
		writeServiceError(c, "usage", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func parseUsageQuery(rawQuery string, observedAt time.Time) (requestlog.UsageQuery, *app_errors.APIError) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return requestlog.UsageQuery{}, app_errors.ErrBadRequest
	}
	allowed := map[string]struct{}{
		"range":           {},
		"group_id":        {},
		"model":           {},
		"breakdown_order": {},
	}
	for key, value := range values {
		if _, ok := allowed[key]; !ok || len(value) != 1 {
			return requestlog.UsageQuery{}, app_errors.ErrBadRequest
		}
	}

	observedAt = observedAt.UTC()
	query := requestlog.UsageQuery{
		Limit:          usageBreakdownMax,
		BreakdownOrder: requestlog.UsageBreakdownOrderRequests,
	}
	rangeValue := usageRange24Hours
	if value, ok := singleQueryValue(values, "range"); ok {
		rangeValue = value
	}
	switch rangeValue {
	case usageRange24Hours:
		currentHour := observedAt.Truncate(time.Hour)
		query.From = currentHour.Add(-23 * time.Hour)
		query.To = currentHour.Add(time.Hour)
		query.Granularity = requestlog.UsageGranularityHour
	case usageRange30Days:
		currentDay := time.Date(observedAt.Year(), observedAt.Month(), observedAt.Day(), 0, 0, 0, 0, time.UTC)
		query.From = currentDay.AddDate(0, 0, -29)
		query.To = currentDay.AddDate(0, 0, 1)
		query.Granularity = requestlog.UsageGranularityDay
	default:
		return requestlog.UsageQuery{}, app_errors.ErrValidation
	}
	if value, ok := singleQueryValue(values, "group_id"); ok {
		groupID, apiErr := parseUsageGroupID(value)
		if apiErr != nil {
			return requestlog.UsageQuery{}, apiErr
		}
		query.GroupID = &groupID
	}
	if value, ok := singleQueryValue(values, "model"); ok {
		if !validUsageModel(value) {
			return requestlog.UsageQuery{}, app_errors.ErrValidation
		}
		query.Model = value
	}
	if value, ok := singleQueryValue(values, "breakdown_order"); ok {
		switch requestlog.UsageBreakdownOrder(value) {
		case requestlog.UsageBreakdownOrderRequests:
			query.BreakdownOrder = requestlog.UsageBreakdownOrderRequests
		case requestlog.UsageBreakdownOrderCost:
			query.BreakdownOrder = requestlog.UsageBreakdownOrderCost
		default:
			return requestlog.UsageQuery{}, app_errors.ErrValidation
		}
	}
	return query, nil
}

func validUsageModel(value string) bool {
	if !utf8.ValidString(value) || len(value) < 1 || len(value) > 255 ||
		strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func parseUsageGroupID(value string) (uint, *app_errors.APIError) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, app_errors.ErrBadRequest
	}
	if parsed == 0 || parsed > uint64(maxSafeInteger) || uint64(uint(parsed)) != parsed {
		return 0, app_errors.ErrValidation
	}
	return uint(parsed), nil
}

func (service *Service) mapUsageResponse(
	observedAt time.Time,
	query requestlog.UsageQuery,
	report requestlog.UsageReport,
) (usageResponse, error) {
	if service.requestLogStats == nil {
		return usageResponse{}, app_errors.ErrInternalServer
	}
	switch query.BreakdownOrder {
	case requestlog.UsageBreakdownOrderRequests, requestlog.UsageBreakdownOrderCost:
	default:
		return usageResponse{}, fmt.Errorf("map usage response: invalid query breakdown order")
	}
	if report.BreakdownOrder != query.BreakdownOrder {
		return usageResponse{}, fmt.Errorf("map usage response: breakdown order mismatch")
	}
	if report.BreakdownGroupCount < 0 || report.BreakdownGroupCount > maxSafeInteger {
		return usageResponse{}, fmt.Errorf("map usage response: unsafe breakdown group count")
	}
	summary, err := mapUsageAggregate(report.Summary)
	if err != nil {
		return usageResponse{}, err
	}
	stats := service.requestLogStats.Stats()
	if stats.DroppedTotal > uint64(maxSafeInteger) ||
		stats.WriteFailureTotal > uint64(maxSafeInteger) {
		return usageResponse{}, fmt.Errorf("map usage collection health: unsafe counter")
	}
	rangeValue := usageRange24Hours
	switch query.Granularity {
	case requestlog.UsageGranularityHour:
	case requestlog.UsageGranularityDay:
		rangeValue = usageRange30Days
	default:
		return usageResponse{}, fmt.Errorf("map usage response: invalid granularity")
	}
	result := usageResponse{
		Range:       rangeValue,
		Granularity: query.Granularity,
		Timezone:    "UTC",
		From:        query.From.UTC().Format(time.RFC3339Nano),
		To:          query.To.UTC().Format(time.RFC3339Nano),
		ObservedAt:  observedAt.UTC().Format(time.RFC3339Nano),
		CollectionHealth: usageCollectionHealthResponse{
			Scope:              "current_process",
			DroppedTotal:       stats.DroppedTotal,
			WriteFailureTotal:  stats.WriteFailureTotal,
			LastWriteFailureAt: optionalUTC(stats.LastWriteFailureAt),
		},
		Summary:             summary,
		Series:              make([]usageSeriesResponse, 0, len(report.Series)),
		Breakdown:           make([]usageBreakdownResponse, 0, len(report.Breakdown)),
		BreakdownTruncated:  report.BreakdownTruncated,
		BreakdownOrder:      report.BreakdownOrder,
		BreakdownGroupCount: report.BreakdownGroupCount,
	}
	for _, point := range report.Series {
		aggregate, err := mapUsageAggregate(point.UsageAggregate)
		if err != nil {
			return usageResponse{}, err
		}
		result.Series = append(result.Series, usageSeriesResponse{
			BucketStart:            point.BucketStart.UTC().Format(time.RFC3339Nano),
			BucketEnd:              point.BucketEnd.UTC().Format(time.RFC3339Nano),
			usageAggregateResponse: aggregate,
		})
	}
	for _, row := range report.Breakdown {
		if row.GroupID == 0 || uint64(row.GroupID) > uint64(maxSafeInteger) || row.Model == "" {
			return usageResponse{}, fmt.Errorf("map usage breakdown: invalid group or model")
		}
		aggregate, err := mapUsageAggregate(row.UsageAggregate)
		if err != nil {
			return usageResponse{}, err
		}
		result.Breakdown = append(result.Breakdown, usageBreakdownResponse{
			GroupID: row.GroupID, Model: row.Model, usageAggregateResponse: aggregate,
		})
	}
	return result, nil
}

func mapUsageAggregate(source requestlog.UsageAggregate) (usageAggregateResponse, error) {
	values := []int64{
		source.RequestCount, source.SuccessCount, source.FailureCount,
		source.UncachedInputTokens, source.CacheReadTokens, source.CacheWrite5MTokens,
		source.CacheWrite1HTokens, source.OutputTokens, source.UsageMissingCount,
		source.PartialCount, source.UnpricedRequestCount,
	}
	for _, value := range values {
		if value < 0 || value > maxSafeInteger {
			return usageAggregateResponse{}, fmt.Errorf("map usage aggregate: unsafe integer")
		}
	}
	totalTokens, err := checkedUsageTokenTotal(
		source.UncachedInputTokens,
		source.CacheReadTokens,
		source.CacheWrite5MTokens,
		source.CacheWrite1HTokens,
		source.OutputTokens,
	)
	if err != nil {
		return usageAggregateResponse{}, err
	}
	cost, err := mapEstimatedCostUSD(source.Cost)
	if err != nil {
		return usageAggregateResponse{}, err
	}
	return usageAggregateResponse{
		RequestCount: source.RequestCount, SuccessCount: source.SuccessCount, FailureCount: source.FailureCount,
		UncachedInputTokens: source.UncachedInputTokens, CacheReadTokens: source.CacheReadTokens,
		CacheWrite5MTokens: source.CacheWrite5MTokens, CacheWrite1HTokens: source.CacheWrite1HTokens,
		OutputTokens: source.OutputTokens, TotalTokens: totalTokens, EstimatedCostUSD: cost,
		UsageMissingCount: source.UsageMissingCount, PartialCount: source.PartialCount,
		UnpricedRequestCount: source.UnpricedRequestCount,
	}, nil
}

func checkedUsageTokenTotal(tokens ...int64) (int64, error) {
	var total int64
	for _, value := range tokens {
		if value < 0 || value > maxSafeInteger-total {
			return 0, fmt.Errorf("map usage aggregate: unsafe total tokens")
		}
		total += value
	}
	return total, nil
}

func mapEstimatedCostUSD(value float64) (json.Number, error) {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return "", fmt.Errorf("map usage cost: invalid value")
	}
	if value == 0 {
		value = 0
	}
	return json.Number(strconv.FormatFloat(value, 'g', 12, 64)), nil
}
