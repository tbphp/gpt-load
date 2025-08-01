package handler

import (
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"
	"gpt-load/internal/response"
	"time"

	"github.com/gin-gonic/gin"
)

// getTimeRange 根据时间模式计算时间范围
func (s *Server) getTimeRange(timeMode string, now time.Time) (time.Time, time.Time) {
	switch timeMode {
	case "daily":
		// 自然日：从当天0点到现在
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return startOfDay, now
	default: // "rolling"
		// 滚动24小时：从24小时前到现在
		return now.Add(-24 * time.Hour), now
	}
}

// Stats Get dashboard statistics
func (s *Server) Stats(c *gin.Context) {
	var activeKeys, invalidKeys int64
	s.DB.Model(&models.APIKey{}).Where("status = ?", models.KeyStatusActive).Count(&activeKeys)
	s.DB.Model(&models.APIKey{}).Where("status = ?", models.KeyStatusInvalid).Count(&invalidKeys)

	now := time.Now()
	rpmStats, err := s.getRPMStats(now)
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrDatabase, "failed to get rpm stats"))
		return
	}

	// 获取系统设置中的时间模式
	settings := s.SettingsManager.GetSettings()
	timeMode := settings.DashboardTimeMode

	// 根据时间模式计算当前周期和上一周期的时间范围
	var currentStart, currentEnd, previousStart, previousEnd time.Time

	if timeMode == "daily" {
		// 自然日模式
		currentStart, currentEnd = s.getTimeRange("daily", now)
		// 前一天的同一时间段（昨天0点到昨天的当前时间）
		yesterday := now.AddDate(0, 0, -1)
		previousStart = time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, yesterday.Location())
		previousEnd = time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), now.Hour(), now.Minute(), now.Second(), 0, yesterday.Location())
	} else {
		// 滚动24小时模式（默认）
		currentStart, currentEnd = s.getTimeRange("rolling", now)
		// 前一个24小时周期
		previousStart = currentStart.Add(-24 * time.Hour)
		previousEnd = currentEnd.Add(-24 * time.Hour)
	}

	currentPeriod, err := s.getHourlyStats(currentStart, currentEnd)
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrDatabase, "failed to get current period stats"))
		return
	}
	previousPeriod, err := s.getHourlyStats(previousStart, previousEnd)
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrDatabase, "failed to get previous period stats"))
		return
	}

	// 计算请求量趋势
	reqTrend := 0.0
	reqTrendIsGrowth := true
	if previousPeriod.TotalRequests > 0 {
		// 有前期数据，计算百分比变化
		reqTrend = (float64(currentPeriod.TotalRequests-previousPeriod.TotalRequests) / float64(previousPeriod.TotalRequests)) * 100
		reqTrendIsGrowth = reqTrend >= 0
	} else if currentPeriod.TotalRequests > 0 {
		// 前期无数据，当前有数据，视为100%增长
		reqTrend = 100.0
		reqTrendIsGrowth = true
	} else {
		// 前期和当前都无数据
		reqTrend = 0.0
		reqTrendIsGrowth = true
	}

	// 计算当前和前期错误率
	currentErrorRate := 0.0
	if currentPeriod.TotalRequests > 0 {
		currentErrorRate = (float64(currentPeriod.TotalFailures) / float64(currentPeriod.TotalRequests)) * 100
	}

	previousErrorRate := 0.0
	if previousPeriod.TotalRequests > 0 {
		previousErrorRate = (float64(previousPeriod.TotalFailures) / float64(previousPeriod.TotalRequests)) * 100
	}

	// 计算错误率趋势
	errorRateTrend := 0.0
	errorRateTrendIsGrowth := false
	if previousPeriod.TotalRequests > 0 {
		// 有前期数据，计算百分点差异
		errorRateTrend = currentErrorRate - previousErrorRate
		errorRateTrendIsGrowth = errorRateTrend < 0 // 错误率下降是好事
	} else if currentPeriod.TotalRequests > 0 {
		// 前期无数据，当前有数据
		errorRateTrend = currentErrorRate // 显示当前错误率
		errorRateTrendIsGrowth = false    // 有错误是坏事（如果错误率>0）
		if currentErrorRate == 0 {
			errorRateTrendIsGrowth = true // 如果当前无错误，标记为正面
		}
	} else {
		// 都无数据
		errorRateTrend = 0.0
		errorRateTrendIsGrowth = true
	}

	stats := models.DashboardStatsResponse{
		KeyCount: models.StatCard{
			Value:       float64(activeKeys),
			SubValue:    invalidKeys,
			SubValueTip: "无效密钥数量",
		},
		RPM: rpmStats,
		RequestCount: models.StatCard{
			Value:         float64(currentPeriod.TotalRequests),
			Trend:         reqTrend,
			TrendIsGrowth: reqTrendIsGrowth,
		},
		ErrorRate: models.StatCard{
			Value:         currentErrorRate,
			Trend:         errorRateTrend,
			TrendIsGrowth: errorRateTrendIsGrowth,
		},
		TimeMode: timeMode,
	}

	response.Success(c, stats)
}

// Chart Get dashboard chart data
func (s *Server) Chart(c *gin.Context) {
	groupID := c.Query("groupId")

	now := time.Now()

	// 获取系统设置中的时间模式
	settings := s.SettingsManager.GetSettings()
	timeMode := settings.DashboardTimeMode

	var startHour, endHour time.Time
	var hourCount int

	if timeMode == "daily" {
		// 自然日模式：从当天0点开始
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		startHour = startOfDay.Truncate(time.Hour)
		endHour = now.Truncate(time.Hour)
		// 计算当天已过的小时数（+1是为了包含当前小时）
		hourCount = int(endHour.Sub(startHour).Hours()) + 1
	} else {
		// 滚动24小时模式（默认）
		endHour = now.Truncate(time.Hour)
		startHour = endHour.Add(-23 * time.Hour)
		hourCount = 24
	}

	var hourlyStats []models.GroupHourlyStat
	query := s.DB.Where("time >= ? AND time < ?", startHour, endHour.Add(time.Hour))
	if groupID != "" {
		query = query.Where("group_id = ?", groupID)
	}
	if err := query.Order("time asc").Find(&hourlyStats).Error; err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrDatabase, "failed to get chart data"))
		return
	}

	statsByHour := make(map[time.Time]map[string]int64)
	for _, stat := range hourlyStats {
		hour := stat.Time.Local().Truncate(time.Hour)
		if _, ok := statsByHour[hour]; !ok {
			statsByHour[hour] = make(map[string]int64)
		}
		statsByHour[hour]["success"] += stat.SuccessCount
		statsByHour[hour]["failure"] += stat.FailureCount
	}

	var labels []string
	var successData, failureData []int64

	for i := 0; i < hourCount; i++ {
		hour := startHour.Add(time.Duration(i) * time.Hour)
		labels = append(labels, hour.Format(time.RFC3339))

		if data, ok := statsByHour[hour]; ok {
			successData = append(successData, data["success"])
			failureData = append(failureData, data["failure"])
		} else {
			successData = append(successData, 0)
			failureData = append(failureData, 0)
		}
	}

	chartData := models.ChartData{
		Labels: labels,
		Datasets: []models.ChartDataset{
			{
				Label: "成功请求",
				Data:  successData,
				Color: "rgba(10, 200, 110, 1)",
			},
			{
				Label: "失败请求",
				Data:  failureData,
				Color: "rgba(255, 70, 70, 1)",
			},
		},
		TimeMode: timeMode,
	}

	response.Success(c, chartData)
}

type hourlyStatResult struct {
	TotalRequests int64
	TotalFailures int64
}

func (s *Server) getHourlyStats(startTime, endTime time.Time) (hourlyStatResult, error) {
	var result hourlyStatResult
	err := s.DB.Model(&models.GroupHourlyStat{}).
		Select("sum(success_count) + sum(failure_count) as total_requests, sum(failure_count) as total_failures").
		Where("time >= ? AND time < ?", startTime, endTime).
		Scan(&result).Error
	return result, err
}

type rpmStatResult struct {
	CurrentRequests  int64
	PreviousRequests int64
}

func (s *Server) getRPMStats(now time.Time) (models.StatCard, error) {
	tenMinutesAgo := now.Add(-10 * time.Minute)
	twentyMinutesAgo := now.Add(-20 * time.Minute)

	var result rpmStatResult
	err := s.DB.Model(&models.RequestLog{}).
		Select("count(case when timestamp >= ? then 1 end) as current_requests, count(case when timestamp >= ? and timestamp < ? then 1 end) as previous_requests", tenMinutesAgo, twentyMinutesAgo, tenMinutesAgo).
		Where("timestamp >= ?", twentyMinutesAgo).
		Scan(&result).Error

	if err != nil {
		return models.StatCard{}, err
	}

	currentRPM := float64(result.CurrentRequests) / 10.0
	previousRPM := float64(result.PreviousRequests) / 10.0

	rpmTrend := 0.0
	rpmTrendIsGrowth := true
	if previousRPM > 0 {
		rpmTrend = (currentRPM - previousRPM) / previousRPM * 100
		rpmTrendIsGrowth = rpmTrend >= 0
	} else if currentRPM > 0 {
		rpmTrend = 100.0
		rpmTrendIsGrowth = true
	}

	return models.StatCard{
		Value:         currentRPM,
		Trend:         rpmTrend,
		TrendIsGrowth: rpmTrendIsGrowth,
	}, nil
}
