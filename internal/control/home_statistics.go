package control

import (
	"context"
	"fmt"
	"net/url"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/epochms"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/requestlog"
)

const homeStatisticsRankingLimit = 5

type HomeStatisticsReader interface {
	QueryHomeStatistics(
		context.Context,
		requestlog.HomeStatisticsQuery,
	) (requestlog.HomeStatisticsReport, error)
}

type homeStatisticsAggregateResponse struct {
	RequestCount         int64  `json:"request_count"`
	SuccessCount         int64  `json:"success_count"`
	FailureCount         int64  `json:"failure_count"`
	TotalTokens          int64  `json:"total_tokens"`
	EstimatedCostNanoUSD string `json:"estimated_cost_nano_usd"`
	UsageMissingCount    int64  `json:"usage_missing_count"`
	PartialCount         int64  `json:"partial_count"`
	UnpricedRequestCount int64  `json:"unpriced_request_count"`
}

type homeStatisticsSeriesResponse struct {
	BucketStartMS int64 `json:"bucket_start_ms"`
	BucketEndMS   int64 `json:"bucket_end_ms"`
	RequestCount  int64 `json:"request_count"`
	FailureCount  int64 `json:"failure_count"`
}

type homeStatisticsRefResponse struct {
	ID      uint    `json:"id"`
	Name    *string `json:"name"`
	Deleted bool    `json:"deleted"`
}

type homeModelRankingResponse struct {
	Model                string `json:"model"`
	RequestCount         int64  `json:"request_count"`
	TotalTokens          int64  `json:"total_tokens"`
	EstimatedCostNanoUSD string `json:"estimated_cost_nano_usd"`
}

type homeGroupRankingResponse struct {
	Group                homeStatisticsRefResponse `json:"group"`
	RequestCount         int64                     `json:"request_count"`
	TotalTokens          int64                     `json:"total_tokens"`
	EstimatedCostNanoUSD string                    `json:"estimated_cost_nano_usd"`
}

type homeAccessKeyRankingResponse struct {
	AccessKey            homeStatisticsRefResponse `json:"access_key"`
	RequestCount         int64                     `json:"request_count"`
	TotalTokens          int64                     `json:"total_tokens"`
	EstimatedCostNanoUSD string                    `json:"estimated_cost_nano_usd"`
}

type homeStatisticsRankingsResponse struct {
	Models     []homeModelRankingResponse     `json:"models"`
	Groups     []homeGroupRankingResponse     `json:"groups"`
	AccessKeys []homeAccessKeyRankingResponse `json:"access_keys"`
}

type homeStatisticsResponse struct {
	Range        requestlog.HomeStatisticsRange  `json:"range"`
	Granularity  requestlog.UsageGranularity     `json:"granularity"`
	FromMS       int64                           `json:"from_ms"`
	ToMS         int64                           `json:"to_ms"`
	ObservedAtMS int64                           `json:"observed_at_ms"`
	Summary      homeStatisticsAggregateResponse `json:"summary"`
	Series       []homeStatisticsSeriesResponse  `json:"series"`
	Rankings     homeStatisticsRankingsResponse  `json:"rankings"`
}

func (s *Service) QueryHomeStatistics(
	ctx context.Context,
	query requestlog.HomeStatisticsQuery,
) (requestlog.HomeStatisticsReport, error) {
	if s == nil || s.homeStatistics == nil {
		return requestlog.HomeStatisticsReport{}, app_errors.ErrInternalServer
	}
	report, err := s.homeStatistics.QueryHomeStatistics(ctx, query)
	if err != nil {
		return requestlog.HomeStatisticsReport{}, app_errors.ParseDBError(err)
	}
	return report, nil
}

func (s *Server) handleHomeStatistics(c *gin.Context) {
	observedAtMS, err := safeEpochMilliseconds(s.now())
	if err != nil {
		writeServiceError(c, "home_statistics", err)
		return
	}
	query, apiErr := parseHomeStatisticsQuery(
		c.Request.URL.RawQuery,
		observedAtMS,
	)
	if apiErr != nil {
		writeServiceError(c, "home_statistics", apiErr)
		return
	}
	report, err := s.service.QueryHomeStatistics(
		c.Request.Context(),
		query,
	)
	if err != nil {
		writeServiceError(c, "home_statistics", err)
		return
	}
	result, err := mapHomeStatisticsResponse(query, report)
	if err != nil {
		writeServiceError(c, "home_statistics", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func parseHomeStatisticsQuery(
	rawQuery string,
	observedAtMS int64,
) (requestlog.HomeStatisticsQuery, *app_errors.APIError) {
	if err := validateSafeMilliseconds(observedAtMS); err != nil {
		return requestlog.HomeStatisticsQuery{}, app_errors.ErrInternalServer
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return requestlog.HomeStatisticsQuery{}, app_errors.ErrBadRequest
	}
	for key, entries := range values {
		if key != "range" || len(entries) != 1 {
			return requestlog.HomeStatisticsQuery{}, app_errors.ErrBadRequest
		}
	}
	rangeValue := requestlog.HomeStatistics24H
	if entries, exists := values["range"]; exists {
		rangeValue = requestlog.HomeStatisticsRange(entries[0])
	}
	width, count, _, ok := homeStatisticsRangeContract(rangeValue)
	if !ok {
		return requestlog.HomeStatisticsQuery{}, app_errors.ErrValidation
	}
	fromMS, toMS, err := epochms.WindowEndingAt(
		observedAtMS,
		width,
		count,
	)
	if err != nil ||
		validateSafeMilliseconds(fromMS) != nil ||
		validateSafeMilliseconds(toMS) != nil {
		return requestlog.HomeStatisticsQuery{}, app_errors.ErrInternalServer
	}
	return requestlog.HomeStatisticsQuery{
		Range:        rangeValue,
		ObservedAtMS: observedAtMS,
	}, nil
}

func mapHomeStatisticsResponse(
	query requestlog.HomeStatisticsQuery,
	report requestlog.HomeStatisticsReport,
) (homeStatisticsResponse, error) {
	width, count, granularity, ok := homeStatisticsRangeContract(query.Range)
	if !ok {
		return homeStatisticsResponse{}, fmt.Errorf(
			"map home statistics: invalid query range",
		)
	}
	fromMS, toMS, err := epochms.WindowEndingAt(
		query.ObservedAtMS,
		width,
		count,
	)
	if err != nil {
		return homeStatisticsResponse{}, fmt.Errorf(
			"map home statistics window: %w",
			err,
		)
	}
	if report.Range != query.Range ||
		report.ObservedAtMS != query.ObservedAtMS ||
		report.FromMS != fromMS ||
		report.ToMS != toMS ||
		report.Granularity != granularity {
		return homeStatisticsResponse{}, fmt.Errorf(
			"map home statistics: report metadata mismatch",
		)
	}
	for _, value := range []int64{
		report.FromMS,
		report.ToMS,
		report.ObservedAtMS,
	} {
		if err := validateSafeMilliseconds(value); err != nil {
			return homeStatisticsResponse{}, fmt.Errorf(
				"map home statistics time: %w",
				err,
			)
		}
	}
	summary, err := mapHomeStatisticsAggregate(report.Summary)
	if err != nil {
		return homeStatisticsResponse{}, err
	}
	if len(report.Series) != count {
		return homeStatisticsResponse{}, fmt.Errorf(
			"map home statistics: series length mismatch",
		)
	}
	if len(report.TopModels) > homeStatisticsRankingLimit ||
		len(report.TopGroups) > homeStatisticsRankingLimit ||
		len(report.TopAccessKeys) > homeStatisticsRankingLimit {
		return homeStatisticsResponse{}, fmt.Errorf(
			"map home statistics: ranking limit exceeded",
		)
	}

	result := homeStatisticsResponse{
		Range:        report.Range,
		Granularity:  report.Granularity,
		FromMS:       report.FromMS,
		ToMS:         report.ToMS,
		ObservedAtMS: report.ObservedAtMS,
		Summary:      summary,
		Series:       make([]homeStatisticsSeriesResponse, 0, count),
		Rankings: homeStatisticsRankingsResponse{
			Models: make([]homeModelRankingResponse, 0, len(report.TopModels)),
			Groups: make([]homeGroupRankingResponse, 0, len(report.TopGroups)),
			AccessKeys: make(
				[]homeAccessKeyRankingResponse,
				0,
				len(report.TopAccessKeys),
			),
		},
	}
	for index, point := range report.Series {
		wantStartMS := fromMS + int64(index)*width
		if point.BucketStartMS != wantStartMS ||
			point.BucketEndMS != wantStartMS+width {
			return homeStatisticsResponse{}, fmt.Errorf(
				"map home statistics: non-dense series",
			)
		}
		if _, err := mapHomeStatisticsAggregate(point.UsageAggregate); err != nil {
			return homeStatisticsResponse{}, err
		}
		result.Series = append(result.Series, homeStatisticsSeriesResponse{
			BucketStartMS: point.BucketStartMS,
			BucketEndMS:   point.BucketEndMS,
			RequestCount:  point.RequestCount,
			FailureCount:  point.FailureCount,
		})
	}
	for _, row := range report.TopModels {
		aggregate, err := mapHomeStatisticsAggregate(row.UsageAggregate)
		if err != nil {
			return homeStatisticsResponse{}, err
		}
		result.Rankings.Models = append(
			result.Rankings.Models,
			homeModelRankingResponse{
				Model:                row.Model,
				RequestCount:         aggregate.RequestCount,
				TotalTokens:          aggregate.TotalTokens,
				EstimatedCostNanoUSD: aggregate.EstimatedCostNanoUSD,
			},
		)
	}
	for _, row := range report.TopGroups {
		aggregate, err := mapHomeStatisticsAggregate(row.UsageAggregate)
		if err != nil {
			return homeStatisticsResponse{}, err
		}
		group, err := mapHomeStatisticsRef(row.Group)
		if err != nil {
			return homeStatisticsResponse{}, err
		}
		result.Rankings.Groups = append(
			result.Rankings.Groups,
			homeGroupRankingResponse{
				Group: group, RequestCount: aggregate.RequestCount,
				TotalTokens:          aggregate.TotalTokens,
				EstimatedCostNanoUSD: aggregate.EstimatedCostNanoUSD,
			},
		)
	}
	for _, row := range report.TopAccessKeys {
		aggregate, err := mapHomeStatisticsAggregate(row.UsageAggregate)
		if err != nil {
			return homeStatisticsResponse{}, err
		}
		accessKey, err := mapHomeStatisticsRef(row.AccessKey)
		if err != nil {
			return homeStatisticsResponse{}, err
		}
		result.Rankings.AccessKeys = append(
			result.Rankings.AccessKeys,
			homeAccessKeyRankingResponse{
				AccessKey:            accessKey,
				RequestCount:         aggregate.RequestCount,
				TotalTokens:          aggregate.TotalTokens,
				EstimatedCostNanoUSD: aggregate.EstimatedCostNanoUSD,
			},
		)
	}
	return result, nil
}

func homeStatisticsRangeContract(
	value requestlog.HomeStatisticsRange,
) (int64, int, requestlog.UsageGranularity, bool) {
	switch value {
	case requestlog.HomeStatistics24H:
		return epochms.MillisecondsPerHour, 24, requestlog.UsageGranularityHour, true
	case requestlog.HomeStatistics30D:
		return epochms.MillisecondsPerDay, 30, requestlog.UsageGranularityDay, true
	default:
		return 0, 0, "", false
	}
}

func mapHomeStatisticsAggregate(
	source requestlog.UsageAggregate,
) (homeStatisticsAggregateResponse, error) {
	mapped, err := mapUsageAggregate(source)
	if err != nil {
		return homeStatisticsAggregateResponse{}, fmt.Errorf(
			"map home statistics aggregate: %w",
			err,
		)
	}
	if source.SuccessCount > maxSafeInteger-source.FailureCount ||
		source.SuccessCount+source.FailureCount != source.RequestCount {
		return homeStatisticsAggregateResponse{}, fmt.Errorf(
			"map home statistics aggregate: invalid request count",
		)
	}
	return homeStatisticsAggregateResponse{
		RequestCount:         mapped.RequestCount,
		SuccessCount:         mapped.SuccessCount,
		FailureCount:         mapped.FailureCount,
		TotalTokens:          mapped.TotalTokens,
		EstimatedCostNanoUSD: mapped.EstimatedCostNanoUSD,
		UsageMissingCount:    mapped.UsageMissingCount,
		PartialCount:         mapped.PartialCount,
		UnpricedRequestCount: mapped.UnpricedRequestCount,
	}, nil
}

func mapHomeStatisticsRef(
	source requestlog.HomeStatisticsRef,
) (homeStatisticsRefResponse, error) {
	if uint64(source.ID) > uint64(maxSafeInteger) ||
		source.Deleted && source.Name != nil ||
		!source.Deleted && source.Name == nil {
		return homeStatisticsRefResponse{}, fmt.Errorf(
			"map home statistics reference: invalid reference",
		)
	}
	var name *string
	if source.Name != nil {
		cloned := *source.Name
		name = &cloned
	}
	return homeStatisticsRefResponse{
		ID: source.ID, Name: name, Deleted: source.Deleted,
	}, nil
}
