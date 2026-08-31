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
	"gpt-load/internal/pricing"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/usage"
)

const (
	usageRange1Hour        = "1h"
	usageRange24Hours      = "24h"
	usageRange3Days        = "3d"
	usageRange7Days        = "7d"
	usageRange15Days       = "15d"
	usageRange30Days       = "30d"
	usageRangeCustom       = "custom"
	usageDistributionLimit = 5
	usageCustomMaxMS       = 30 * epochms.MillisecondsPerDay
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

type usageDistributionItemResponse struct {
	GroupID              *uint   `json:"group_id,omitempty"`
	AccessKeyID          *uint   `json:"access_key_id,omitempty"`
	Model                *string `json:"model,omitempty"`
	RequestCount         int64   `json:"request_count"`
	TotalTokens          int64   `json:"total_tokens"`
	EstimatedCostNanoUSD string  `json:"estimated_cost_nano_usd"`
}

type usageDistributionResponse struct {
	Dimension requestlog.UsageDistributionDimension `json:"dimension"`
	Metric    requestlog.UsageDistributionMetric    `json:"metric"`
	Items     []usageDistributionItemResponse       `json:"items"`
	Other     *usageDistributionAggregateResponse   `json:"other"`
}

type usageDistributionViewsResponse struct {
	Group     map[requestlog.UsageDistributionMetric]usageDistributionResponse `json:"group,omitempty"`
	Model     map[requestlog.UsageDistributionMetric]usageDistributionResponse `json:"model"`
	AccessKey map[requestlog.UsageDistributionMetric]usageDistributionResponse `json:"access_key,omitempty"`
}

type usageDistributionAggregateResponse struct {
	RequestCount         int64  `json:"request_count"`
	TotalTokens          int64  `json:"total_tokens"`
	EstimatedCostNanoUSD string `json:"estimated_cost_nano_usd"`
}

type usageCollectionHealthResponse struct {
	Scope                string `json:"scope"`
	DroppedTotal         uint64 `json:"dropped_total"`
	WriteFailureTotal    uint64 `json:"write_failure_total"`
	LastWriteFailureAtMS *int64 `json:"last_write_failure_at_ms"`
}

type usageResponse struct {
	Range            string                         `json:"range"`
	Granularity      requestlog.UsageGranularity    `json:"granularity"`
	BucketWidthMS    int64                          `json:"bucket_width_ms"`
	FromMS           int64                          `json:"from_ms"`
	ToMS             int64                          `json:"to_ms"`
	ObservedAtMS     int64                          `json:"observed_at_ms"`
	Summary          usageAggregateResponse         `json:"summary"`
	Series           []usageSeriesResponse          `json:"series"`
	Distributions    usageDistributionViewsResponse `json:"distributions"`
	CollectionHealth usageCollectionHealthResponse  `json:"collection_health"`
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
	if accessKeyID, scoped := currentAccessKeyID(c); scoped {
		if query.GroupID != nil || query.ChannelID != "" || query.CredentialID != nil {
			writeServiceError(c, "usage", app_errors.ErrBadRequest)
			return
		}
		query.AccessKeyID = &accessKeyID
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
		"range":          {},
		"from_ms":        {},
		"to_ms":          {},
		"group_id":       {},
		"channel_id":     {},
		"credential_id":  {},
		"upstream_model": {},
	}
	for key, value := range values {
		if _, ok := allowed[key]; !ok || len(value) != 1 {
			return requestlog.UsageQuery{}, app_errors.ErrBadRequest
		}
	}

	if err := validateSafeMilliseconds(observedAtMS); err != nil {
		return requestlog.UsageQuery{}, app_errors.ErrInternalServer
	}
	query := requestlog.UsageQuery{}
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
		spanMS := toMS - fromMS
		if spanMS > usageCustomMaxMS {
			return requestlog.UsageQuery{}, app_errors.ErrValidation
		}
		query.FromMS = fromMS
		query.ToMS = toMS
		bucketWidth := epochms.MillisecondsPerDay
		if spanMS <= epochms.MillisecondsPerDay {
			query.Granularity = requestlog.UsageGranularityHour
			bucketWidth = epochms.MillisecondsPerHour
		} else {
			query.Granularity = requestlog.UsageGranularityDay
		}
		query.BucketWidthMS = bucketWidth
		if fromMS%bucketWidth != 0 || toMS%bucketWidth != 0 {
			return requestlog.UsageQuery{}, app_errors.ErrValidation
		}
		rangeValue = ""
	}
	if !hasFrom {
		preset, ok := usageRangePreset(rangeValue)
		if !ok {
			return requestlog.UsageQuery{}, app_errors.ErrValidation
		}
		fromMS, toMS, err := epochms.WindowEndingAt(
			observedAtMS,
			preset.bucketWidthMS,
			preset.bucketCount,
		)
		if err != nil {
			return requestlog.UsageQuery{}, app_errors.ErrInternalServer
		}
		query.FromMS = fromMS
		query.ToMS = toMS
		query.Granularity = preset.granularity
		query.BucketWidthMS = preset.bucketWidthMS
	}
	if value, ok := singleQueryValue(values, "group_id"); ok {
		groupID, apiErr := parseUsageGroupID(value)
		if apiErr != nil {
			return requestlog.UsageQuery{}, apiErr
		}
		query.GroupID = &groupID
	}
	if value, ok := singleQueryValue(values, "channel_id"); ok {
		channelID, apiErr := parseRequestLogChannelID(value)
		if apiErr != nil {
			return requestlog.UsageQuery{}, apiErr
		}
		query.ChannelID = channelID
	}
	if value, ok := singleQueryValue(values, "credential_id"); ok {
		credentialID, apiErr := parseUsageGroupID(value)
		if apiErr != nil {
			return requestlog.UsageQuery{}, apiErr
		}
		query.CredentialID = &credentialID
	}
	if value, ok := singleQueryValue(values, "upstream_model"); ok {
		if !validUsageModel(value) {
			return requestlog.UsageQuery{}, app_errors.ErrValidation
		}
		query.UpstreamModel = value
	}
	return query, nil
}

type usagePreset struct {
	bucketWidthMS int64
	bucketCount   int
	granularity   requestlog.UsageGranularity
}

func usageRangePreset(value string) (usagePreset, bool) {
	switch value {
	case usageRange1Hour:
		return usagePreset{epochms.MillisecondsPerHour, 1, requestlog.UsageGranularityHour}, true
	case usageRange24Hours:
		return usagePreset{epochms.MillisecondsPerHour, 24, requestlog.UsageGranularityHour}, true
	case usageRange3Days:
		return usagePreset{3 * epochms.MillisecondsPerHour, 24, requestlog.UsageGranularityHour}, true
	case usageRange7Days:
		return usagePreset{6 * epochms.MillisecondsPerHour, 28, requestlog.UsageGranularityHour}, true
	case usageRange15Days:
		return usagePreset{12 * epochms.MillisecondsPerHour, 30, requestlog.UsageGranularityHour}, true
	case usageRange30Days:
		return usagePreset{epochms.MillisecondsPerDay, 30, requestlog.UsageGranularityDay}, true
	default:
		return usagePreset{}, false
	}
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
	parsed, err := parseCanonicalSafePlatformUint(value)
	if err != nil {
		if errors.Is(err, errUnsafeCanonicalUint) {
			return 0, app_errors.ErrValidation
		}
		return 0, app_errors.ErrBadRequest
	}
	if parsed == 0 {
		return 0, app_errors.ErrValidation
	}
	return parsed, nil
}

func (service *Service) mapUsageResponse(
	observedAtMS int64,
	query requestlog.UsageQuery,
	report requestlog.UsageReport,
) (usageResponse, error) {
	accessKeyScoped := query.AccessKeyID != nil
	if !accessKeyScoped && service.requestLogStats == nil {
		return usageResponse{}, app_errors.ErrInternalServer
	}
	summary, err := mapUsageAggregate(report.Summary)
	if err != nil {
		return usageResponse{}, err
	}
	stats := requestlog.Stats{}
	if !accessKeyScoped {
		stats = service.requestLogStats.Stats()
	}
	if stats.DroppedTotal > uint64(maxSafeInteger) ||
		stats.WriteFailureTotal > uint64(maxSafeInteger) {
		return usageResponse{}, fmt.Errorf("map usage collection health: unsafe counter")
	}
	bucketWidthMS, err := usageResponseBucketWidth(query)
	if err != nil {
		return usageResponse{}, err
	}
	rangeValue, err := usageResponseRange(query, bucketWidthMS)
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
		Range:         rangeValue,
		Granularity:   query.Granularity,
		BucketWidthMS: bucketWidthMS,
		FromMS:        query.FromMS,
		ToMS:          query.ToMS,
		ObservedAtMS:  observedAtMS,
		CollectionHealth: usageCollectionHealthResponse{
			Scope:                "current_process",
			DroppedTotal:         stats.DroppedTotal,
			WriteFailureTotal:    stats.WriteFailureTotal,
			LastWriteFailureAtMS: lastWriteFailureAtMS,
		},
		Summary: summary,
		Series:  make([]usageSeriesResponse, 0, len(report.Series)),
		Distributions: usageDistributionViewsResponse{
			Model: make(map[requestlog.UsageDistributionMetric]usageDistributionResponse, 3),
		},
	}
	if accessKeyScoped {
		result.CollectionHealth = usageCollectionHealthResponse{Scope: "access_key"}
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
	if !accessKeyScoped {
		result.Distributions.Group = make(map[requestlog.UsageDistributionMetric]usageDistributionResponse, 3)
		result.Distributions.AccessKey = make(map[requestlog.UsageDistributionMetric]usageDistributionResponse, 3)
	}
	for _, dimension := range []requestlog.UsageDistributionDimension{
		requestlog.UsageDistributionDimensionGroup,
		requestlog.UsageDistributionDimensionModel,
		requestlog.UsageDistributionDimensionAccessKey,
	} {
		if accessKeyScoped && dimension != requestlog.UsageDistributionDimensionModel {
			continue
		}
		for _, metric := range []requestlog.UsageDistributionMetric{
			requestlog.UsageDistributionMetricRequests,
			requestlog.UsageDistributionMetricTokens,
			requestlog.UsageDistributionMetricCost,
		} {
			distribution, ok := report.Distributions.Get(dimension, metric)
			if !ok {
				return usageResponse{}, fmt.Errorf("map usage response: distribution view unavailable")
			}
			if distribution.Dimension != dimension || distribution.Metric != metric {
				return usageResponse{}, fmt.Errorf("map usage response: distribution metadata mismatch")
			}
			mapped, err := mapUsageDistribution(distribution)
			if err != nil {
				return usageResponse{}, err
			}
			if err := validateMappedUsageDistribution(result.Summary, mapped); err != nil {
				return usageResponse{}, err
			}
			switch dimension {
			case requestlog.UsageDistributionDimensionGroup:
				result.Distributions.Group[metric] = mapped
			case requestlog.UsageDistributionDimensionModel:
				result.Distributions.Model[metric] = mapped
			case requestlog.UsageDistributionDimensionAccessKey:
				result.Distributions.AccessKey[metric] = mapped
			}
		}
	}
	return result, nil
}

func mapUsageDistribution(
	distribution requestlog.UsageDistribution,
) (usageDistributionResponse, error) {
	if len(distribution.Items) > usageDistributionLimit {
		return usageDistributionResponse{}, fmt.Errorf("map usage response: distribution exceeds limit")
	}
	result := usageDistributionResponse{
		Dimension: distribution.Dimension,
		Metric:    distribution.Metric,
		Items:     make([]usageDistributionItemResponse, 0, len(distribution.Items)),
	}
	seenDistributionIdentities := make(map[string]struct{}, len(distribution.Items))
	for _, row := range distribution.Items {
		if uint64(row.GroupID) > uint64(maxSafeInteger) {
			return usageDistributionResponse{}, fmt.Errorf("map usage distribution: unsafe group")
		}
		if uint64(row.AccessKeyID) > uint64(maxSafeInteger) {
			return usageDistributionResponse{}, fmt.Errorf("map usage distribution: unsafe access key")
		}
		identity := row.Model
		switch distribution.Dimension {
		case requestlog.UsageDistributionDimensionGroup:
			if row.GroupID == 0 || row.AccessKeyID != 0 || row.Model != "" {
				return usageDistributionResponse{}, fmt.Errorf("map usage distribution: invalid group identity")
			}
			identity = strconv.FormatUint(uint64(row.GroupID), 10)
		case requestlog.UsageDistributionDimensionModel:
			if row.GroupID != 0 || row.AccessKeyID != 0 || !validUsageDistributionModel(row.Model) {
				return usageDistributionResponse{}, fmt.Errorf("map usage distribution: invalid model identity")
			}
		case requestlog.UsageDistributionDimensionAccessKey:
			if row.GroupID != 0 || row.AccessKeyID == 0 || row.Model != "" {
				return usageDistributionResponse{}, fmt.Errorf("map usage distribution: invalid access key identity")
			}
			identity = strconv.FormatUint(uint64(row.AccessKeyID), 10)
		default:
			return usageDistributionResponse{}, fmt.Errorf("map usage distribution: invalid dimension")
		}
		if _, exists := seenDistributionIdentities[identity]; exists {
			return usageDistributionResponse{}, fmt.Errorf("map usage distribution: duplicate identity")
		}
		seenDistributionIdentities[identity] = struct{}{}
		aggregate, err := mapUsageDistributionAggregate(row.UsageDistributionAggregate)
		if err != nil {
			return usageDistributionResponse{}, err
		}
		item := usageDistributionItemResponse{
			RequestCount:         aggregate.RequestCount,
			TotalTokens:          aggregate.TotalTokens,
			EstimatedCostNanoUSD: aggregate.EstimatedCostNanoUSD,
		}
		switch distribution.Dimension {
		case requestlog.UsageDistributionDimensionGroup:
			groupID := row.GroupID
			item.GroupID = &groupID
		case requestlog.UsageDistributionDimensionModel:
			model := row.Model
			item.Model = &model
		case requestlog.UsageDistributionDimensionAccessKey:
			accessKeyID := row.AccessKeyID
			item.AccessKeyID = &accessKeyID
		}
		result.Items = append(result.Items, item)
	}
	if distribution.Other != nil {
		other, err := mapUsageDistributionAggregate(*distribution.Other)
		if err != nil {
			return usageDistributionResponse{}, err
		}
		result.Other = &other
	}
	return result, nil
}

func validateMappedUsageDistribution(
	summary usageAggregateResponse,
	distribution usageDistributionResponse,
) error {
	requestCount := int64(0)
	totalTokens := int64(0)
	cost := int64(0)
	items := make([]usageDistributionAggregateResponse, 0, len(distribution.Items)+1)
	for _, item := range distribution.Items {
		items = append(items, usageDistributionAggregateResponse{
			RequestCount: item.RequestCount, TotalTokens: item.TotalTokens,
			EstimatedCostNanoUSD: item.EstimatedCostNanoUSD,
		})
	}
	if distribution.Other != nil {
		items = append(items, *distribution.Other)
	}
	for _, item := range items {
		var ok bool
		requestCount, ok = usage.CheckedAdd(requestCount, item.RequestCount)
		if !ok {
			return fmt.Errorf("map usage distribution: request count overflow")
		}
		totalTokens, ok = usage.CheckedAdd(totalTokens, item.TotalTokens)
		if !ok {
			return fmt.Errorf("map usage distribution: total tokens overflow")
		}
		itemCost, err := strconv.ParseInt(item.EstimatedCostNanoUSD, 10, 64)
		if err != nil {
			return fmt.Errorf("map usage distribution: invalid cost")
		}
		costValue, ok := pricing.CheckedAddNanoUSD(pricing.NanoUSD(cost), pricing.NanoUSD(itemCost))
		if !ok {
			return fmt.Errorf("map usage distribution: cost overflow")
		}
		cost = int64(costValue)
	}
	if requestCount != summary.RequestCount || totalTokens != summary.TotalTokens ||
		strconv.FormatInt(cost, 10) != summary.EstimatedCostNanoUSD {
		return fmt.Errorf("map usage distribution: total mismatch")
	}
	return nil
}

func mapUsageDistributionAggregate(
	source requestlog.UsageDistributionAggregate,
) (usageDistributionAggregateResponse, error) {
	if source.RequestCount < 0 || source.RequestCount > maxSafeInteger ||
		source.TotalTokens < 0 || source.TotalTokens > maxSafeInteger ||
		source.EstimatedCostNanoUSD < 0 {
		return usageDistributionAggregateResponse{}, fmt.Errorf("map usage distribution: unsafe aggregate")
	}
	return usageDistributionAggregateResponse{
		RequestCount:         source.RequestCount,
		TotalTokens:          source.TotalTokens,
		EstimatedCostNanoUSD: strconv.FormatInt(source.EstimatedCostNanoUSD, 10),
	}, nil
}

func validUsageDistributionModel(value string) bool {
	if !utf8.ValidString(value) || len(value) > 255 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func usageResponseBucketWidth(query requestlog.UsageQuery) (int64, error) {
	bucketWidthMS := query.BucketWidthMS
	if bucketWidthMS == 0 {
		switch query.Granularity {
		case requestlog.UsageGranularityHour:
			bucketWidthMS = epochms.MillisecondsPerHour
		case requestlog.UsageGranularityDay:
			bucketWidthMS = epochms.MillisecondsPerDay
		default:
			return 0, fmt.Errorf("map usage response: invalid granularity")
		}
	}
	if bucketWidthMS < epochms.MillisecondsPerHour ||
		bucketWidthMS > epochms.MillisecondsPerDay ||
		bucketWidthMS%epochms.MillisecondsPerHour != 0 {
		return 0, fmt.Errorf("map usage response: invalid bucket width")
	}
	if query.Granularity == requestlog.UsageGranularityHour &&
		bucketWidthMS >= epochms.MillisecondsPerDay {
		return 0, fmt.Errorf("map usage response: invalid hourly bucket width")
	}
	if query.Granularity == requestlog.UsageGranularityDay &&
		bucketWidthMS != epochms.MillisecondsPerDay {
		return 0, fmt.Errorf("map usage response: invalid daily bucket width")
	}
	return bucketWidthMS, nil
}

func usageResponseRange(query requestlog.UsageQuery, bucketWidthMS int64) (string, error) {
	duration := query.ToMS - query.FromMS
	if query.Granularity != requestlog.UsageGranularityHour &&
		query.Granularity != requestlog.UsageGranularityDay {
		return "", fmt.Errorf("map usage response: invalid granularity")
	}
	for _, rangeValue := range []string{
		usageRange1Hour,
		usageRange24Hours,
		usageRange3Days,
		usageRange7Days,
		usageRange15Days,
		usageRange30Days,
	} {
		preset, _ := usageRangePreset(rangeValue)
		if query.Granularity == preset.granularity &&
			bucketWidthMS == preset.bucketWidthMS &&
			duration == int64(preset.bucketCount)*preset.bucketWidthMS {
			return rangeValue, nil
		}
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
