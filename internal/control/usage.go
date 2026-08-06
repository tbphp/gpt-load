package control

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/epochms"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/requestlog"
)

const (
	usageRange24Hours = "24h"
	usageRange3Days   = "3d"
	usageRange7Days   = "7d"
	usageRange15Days  = "15d"
	usageRange30Days  = "30d"
	usageRangeCustom  = "custom"
	usageBreakdownMax = 100
)

type UsageStatReader interface {
	QueryUsage(context.Context, requestlog.UsageQuery) (requestlog.UsageReport, error)
}

type usageAggregateResponse struct {
	RequestCount            int64  `json:"request_count"`
	SuccessCount            int64  `json:"success_count"`
	FailureCount            int64  `json:"failure_count"`
	UncachedInputTokens     int64  `json:"uncached_input_tokens"`
	CacheReadTokens         int64  `json:"cache_read_tokens"`
	CacheWrite5MTokens      int64  `json:"cache_write_5m_tokens"`
	CacheWrite1HTokens      int64  `json:"cache_write_1h_tokens"`
	CacheWriteUnknownTokens int64  `json:"cache_write_unknown_tokens"`
	OutputTokens            int64  `json:"output_tokens"`
	TotalTokens             int64  `json:"total_tokens"`
	EstimatedCostNanoUSD    string `json:"estimated_cost_nano_usd"`
	UsageMissingCount       int64  `json:"usage_missing_count"`
	PartialCount            int64  `json:"partial_count"`
	UnpricedRequestCount    int64  `json:"unpriced_request_count"`
	PricingPartialCount     int64  `json:"pricing_partial_count"`
}

type usageSeriesResponse struct {
	BucketStartMS int64 `json:"bucket_start_ms"`
	BucketEndMS   int64 `json:"bucket_end_ms"`
	usageAggregateResponse
}

type usageBreakdownResponse struct {
	GroupID uint   `json:"group_id"`
	Model   string `json:"model"`
	usageAggregateResponse
}

type usageCollectionHealthResponse struct {
	Scope                string `json:"scope"`
	DroppedTotal         uint64 `json:"dropped_total"`
	WriteFailureTotal    uint64 `json:"write_failure_total"`
	LastWriteFailureAtMS *int64 `json:"last_write_failure_at_ms"`
}

type usageResponse struct {
	Range              string                         `json:"range"`
	Granularity        requestlog.UsageGranularity    `json:"granularity"`
	FromMS             int64                          `json:"from_ms"`
	ToMS               int64                          `json:"to_ms"`
	ObservedAtMS       int64                          `json:"observed_at_ms"`
	Summary            usageAggregateResponse         `json:"summary"`
	Series             []usageSeriesResponse          `json:"series"`
	Breakdown          []usageBreakdownResponse       `json:"breakdown"`
	BreakdownTruncated bool                           `json:"breakdown_truncated"`
	BreakdownOrder     requestlog.UsageBreakdownOrder `json:"breakdown_order"`
	BreakdownCount     int64                          `json:"breakdown_count"`
	CollectionHealth   usageCollectionHealthResponse  `json:"collection_health"`
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
	observedAtMS, err := safeEpochMilliseconds(server.service.now())
	if err != nil {
		writeServiceError(c, "usage", err)
		return
	}
	query, apiErr := parseUsageQuery(c.Request.URL.RawQuery, observedAtMS)
	if apiErr != nil {
		writeServiceError(c, "usage", apiErr)
		return
	}
	report, err := server.service.QueryUsage(c.Request.Context(), query)
	if err != nil {
		writeServiceError(c, "usage", err)
		return
	}
	result, err := server.service.mapUsageResponse(observedAtMS, query, report)
	if err != nil {
		writeServiceError(c, "usage", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func parseUsageQuery(rawQuery string, observedAtMS int64) (requestlog.UsageQuery, *app_errors.APIError) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return requestlog.UsageQuery{}, app_errors.ErrBadRequest
	}
	allowed := map[string]struct{}{
		"range":           {},
		"from_ms":         {},
		"to_ms":           {},
		"group_id":        {},
		"upstream_model":  {},
		"breakdown_order": {},
	}
	for key, value := range values {
		if _, ok := allowed[key]; !ok || len(value) != 1 {
			return requestlog.UsageQuery{}, app_errors.ErrBadRequest
		}
	}

	if err := validateSafeMilliseconds(observedAtMS); err != nil {
		return requestlog.UsageQuery{}, app_errors.ErrInternalServer
	}
	query := requestlog.UsageQuery{
		Limit:          usageBreakdownMax,
		BreakdownOrder: requestlog.UsageBreakdownOrderRequests,
	}
	rangeValue := usageRange24Hours
	if value, ok := singleQueryValue(values, "range"); ok {
		rangeValue = value
	}
	fromValue, hasFrom := singleQueryValue(values, "from_ms")
	toValue, hasTo := singleQueryValue(values, "to_ms")
	if hasFrom != hasTo {
		return requestlog.UsageQuery{}, app_errors.ErrValidation
	}
	if hasFrom {
		if _, hasRange := singleQueryValue(values, "range"); hasRange {
			return requestlog.UsageQuery{}, app_errors.ErrValidation
		}
		fromMS, err := parseCanonicalSafeMilliseconds(fromValue)
		if err != nil {
			return requestlog.UsageQuery{}, app_errors.ErrBadRequest
		}
		toMS, err := parseCanonicalSafeMilliseconds(toValue)
		if err != nil {
			return requestlog.UsageQuery{}, app_errors.ErrBadRequest
		}
		if fromMS >= toMS {
			return requestlog.UsageQuery{}, app_errors.ErrValidation
		}
		query.FromMS = fromMS
		query.ToMS = toMS
		if toMS-fromMS <= epochms.MillisecondsPerDay {
			query.Granularity = requestlog.UsageGranularityHour
		} else {
			query.Granularity = requestlog.UsageGranularityDay
		}
		rangeValue = ""
	}
	if !hasFrom {
		switch rangeValue {
		case usageRange24Hours:
			fromMS, toMS, err := epochms.WindowEndingAt(
				observedAtMS,
				epochms.MillisecondsPerHour,
				24,
			)
			if err != nil {
				return requestlog.UsageQuery{}, app_errors.ErrInternalServer
			}
			query.FromMS = fromMS
			query.ToMS = toMS
			query.Granularity = requestlog.UsageGranularityHour
		case usageRange3Days, usageRange7Days, usageRange15Days, usageRange30Days:
			days := map[string]int{
				usageRange3Days:  3,
				usageRange7Days:  7,
				usageRange15Days: 15,
				usageRange30Days: 30,
			}[rangeValue]
			fromMS, toMS, err := epochms.WindowEndingAt(
				observedAtMS,
				epochms.MillisecondsPerDay,
				days,
			)
			if err != nil {
				return requestlog.UsageQuery{}, app_errors.ErrInternalServer
			}
			query.FromMS = fromMS
			query.ToMS = toMS
			query.Granularity = requestlog.UsageGranularityDay
		default:
			return requestlog.UsageQuery{}, app_errors.ErrValidation
		}
	}
	if value, ok := singleQueryValue(values, "group_id"); ok {
		groupID, apiErr := parseUsageGroupID(value)
		if apiErr != nil {
			return requestlog.UsageQuery{}, apiErr
		}
		query.GroupID = &groupID
	}
	if value, ok := singleQueryValue(values, "upstream_model"); ok {
		if !validUsageModel(value) {
			return requestlog.UsageQuery{}, app_errors.ErrValidation
		}
		query.UpstreamModel = value
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
	parsed, err := parseCanonicalSafeUint(value)
	if err != nil {
		if errors.Is(err, errUnsafeCanonicalUint) {
			return 0, app_errors.ErrValidation
		}
		return 0, app_errors.ErrBadRequest
	}
	if parsed == 0 || uint64(uint(parsed)) != parsed {
		return 0, app_errors.ErrValidation
	}
	return uint(parsed), nil
}

func (service *Service) mapUsageResponse(
	observedAtMS int64,
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
	if report.BreakdownCount < 0 || report.BreakdownCount > maxSafeInteger {
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
	rangeValue, err := usageResponseRange(query)
	if err != nil {
		return usageResponse{}, err
	}
	lastWriteFailureAtMS, err := optionalSafeEpochMilliseconds(stats.LastWriteFailureAt)
	if err != nil {
		return usageResponse{}, fmt.Errorf("map usage collection health: %w", err)
	}
	if err := validateSafeMilliseconds(query.FromMS); err != nil {
		return usageResponse{}, fmt.Errorf("map usage from_ms: %w", err)
	}
	if err := validateSafeMilliseconds(query.ToMS); err != nil {
		return usageResponse{}, fmt.Errorf("map usage to_ms: %w", err)
	}
	if err := validateSafeMilliseconds(observedAtMS); err != nil {
		return usageResponse{}, fmt.Errorf("map usage observed_at_ms: %w", err)
	}
	result := usageResponse{
		Range:        rangeValue,
		Granularity:  query.Granularity,
		FromMS:       query.FromMS,
		ToMS:         query.ToMS,
		ObservedAtMS: observedAtMS,
		CollectionHealth: usageCollectionHealthResponse{
			Scope:                "current_process",
			DroppedTotal:         stats.DroppedTotal,
			WriteFailureTotal:    stats.WriteFailureTotal,
			LastWriteFailureAtMS: lastWriteFailureAtMS,
		},
		Summary:            summary,
		Series:             make([]usageSeriesResponse, 0, len(report.Series)),
		Breakdown:          make([]usageBreakdownResponse, 0, len(report.Breakdown)),
		BreakdownTruncated: report.BreakdownTruncated,
		BreakdownOrder:     report.BreakdownOrder,
		BreakdownCount:     report.BreakdownCount,
	}
	for _, point := range report.Series {
		if err := validateSafeMilliseconds(point.BucketStartMS); err != nil {
			return usageResponse{}, fmt.Errorf("map usage bucket_start_ms: %w", err)
		}
		if err := validateSafeMilliseconds(point.BucketEndMS); err != nil {
			return usageResponse{}, fmt.Errorf("map usage bucket_end_ms: %w", err)
		}
		aggregate, err := mapUsageAggregate(point.UsageAggregate)
		if err != nil {
			return usageResponse{}, err
		}
		result.Series = append(result.Series, usageSeriesResponse{
			BucketStartMS:          point.BucketStartMS,
			BucketEndMS:            point.BucketEndMS,
			usageAggregateResponse: aggregate,
		})
	}
	for _, row := range report.Breakdown {
		if uint64(row.GroupID) > uint64(maxSafeInteger) {
			return usageResponse{}, fmt.Errorf("map usage breakdown: unsafe group")
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

func usageResponseRange(query requestlog.UsageQuery) (string, error) {
	duration := query.ToMS - query.FromMS
	switch query.Granularity {
	case requestlog.UsageGranularityHour:
		if duration == 24*epochms.MillisecondsPerHour {
			return usageRange24Hours, nil
		}
	case requestlog.UsageGranularityDay:
		for rangeValue, days := range map[string]int64{
			usageRange3Days:  3,
			usageRange7Days:  7,
			usageRange15Days: 15,
			usageRange30Days: 30,
		} {
			if duration == days*epochms.MillisecondsPerDay {
				return rangeValue, nil
			}
		}
	default:
		return "", fmt.Errorf("map usage response: invalid granularity")
	}
	return usageRangeCustom, nil
}

func mapUsageAggregate(source requestlog.UsageAggregate) (usageAggregateResponse, error) {
	values := []int64{
		source.RequestCount, source.SuccessCount, source.FailureCount,
		source.UncachedInputTokens, source.CacheReadTokens, source.CacheWrite5MTokens,
		source.CacheWrite1HTokens, source.OutputTokens, source.UsageMissingCount,
		source.CacheWriteUnknownTokens, source.PartialCount, source.UnpricedRequestCount,
		source.PricingPartialCount,
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
		source.CacheWriteUnknownTokens,
		source.OutputTokens,
	)
	if err != nil {
		return usageAggregateResponse{}, err
	}
	if source.EstimatedCostNanoUSD < 0 {
		return usageAggregateResponse{}, fmt.Errorf("map usage cost: negative value")
	}
	return usageAggregateResponse{
		RequestCount: source.RequestCount, SuccessCount: source.SuccessCount, FailureCount: source.FailureCount,
		UncachedInputTokens: source.UncachedInputTokens, CacheReadTokens: source.CacheReadTokens,
		CacheWrite5MTokens: source.CacheWrite5MTokens, CacheWrite1HTokens: source.CacheWrite1HTokens,
		CacheWriteUnknownTokens: source.CacheWriteUnknownTokens,
		OutputTokens:            source.OutputTokens, TotalTokens: totalTokens,
		EstimatedCostNanoUSD: strconv.FormatInt(source.EstimatedCostNanoUSD, 10),
		UsageMissingCount:    source.UsageMissingCount, PartialCount: source.PartialCount,
		UnpricedRequestCount: source.UnpricedRequestCount,
		PricingPartialCount:  source.PricingPartialCount,
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
