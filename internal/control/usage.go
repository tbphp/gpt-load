package control

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/requestlog"
)

const (
	usageRange24Hours = "24h"
	usageRange30Days  = "30d"
	usageBreakdownMax = 100
)

type UsageStatReader interface {
	QueryUsage(context.Context, requestlog.UsageQuery) (requestlog.UsageReport, error)
}

type usageRangeResponse struct {
	From        string                      `json:"from"`
	To          string                      `json:"to"`
	Granularity requestlog.UsageGranularity `json:"granularity"`
}

type usageFiltersResponse struct {
	GroupID *uint  `json:"group_id"`
	Model   string `json:"model"`
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

type usageResponse struct {
	ObservedAt         string                   `json:"observed_at"`
	Range              usageRangeResponse       `json:"range"`
	Filters            usageFiltersResponse     `json:"filters"`
	Summary            usageAggregateResponse   `json:"summary"`
	Series             []usageSeriesResponse    `json:"series"`
	Breakdown          []usageBreakdownResponse `json:"breakdown"`
	BreakdownTruncated bool                     `json:"breakdown_truncated"`
	RequestLog         requestLogHealthResponse `json:"request_log"`
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
	allowed := map[string]struct{}{"range": {}, "group_id": {}, "model": {}}
	for key, value := range values {
		if _, ok := allowed[key]; !ok || len(value) != 1 {
			return requestlog.UsageQuery{}, app_errors.ErrBadRequest
		}
	}

	query := requestlog.UsageQuery{To: observedAt.UTC(), Limit: usageBreakdownMax}
	rangeValue := usageRange24Hours
	if value, ok := singleQueryValue(values, "range"); ok {
		rangeValue = value
	}
	switch rangeValue {
	case usageRange24Hours:
		query.From = query.To.Add(-24 * time.Hour)
		query.Granularity = requestlog.UsageGranularityHour
	case usageRange30Days:
		query.From = query.To.AddDate(0, 0, -30)
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
		if value == "" {
			return requestlog.UsageQuery{}, app_errors.ErrValidation
		}
		query.Model = value
	}
	return query, nil
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
	summary, err := mapUsageAggregate(report.Summary)
	if err != nil {
		return usageResponse{}, err
	}
	result := usageResponse{
		ObservedAt: observedAt.UTC().Format(time.RFC3339Nano),
		Range: usageRangeResponse{
			From: query.From.UTC().Format(time.RFC3339Nano), To: query.To.UTC().Format(time.RFC3339Nano),
			Granularity: query.Granularity,
		},
		Filters:            usageFiltersResponse{GroupID: query.GroupID, Model: query.Model},
		Summary:            summary,
		Series:             make([]usageSeriesResponse, 0, len(report.Series)),
		Breakdown:          make([]usageBreakdownResponse, 0, len(report.Breakdown)),
		BreakdownTruncated: report.BreakdownTruncated,
		RequestLog:         mapRequestLogHealth(service.requestLogStats.Stats()),
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
		if uint64(row.GroupID) > uint64(maxSafeInteger) || row.Model == "" {
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
