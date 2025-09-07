package handler

import (
	"fmt"
	"strings"
	"gpt-load/internal/encryption"
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"
	"gpt-load/internal/response"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

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
	twentyFourHoursAgo := now.Add(-24 * time.Hour)
	fortyEightHoursAgo := now.Add(-48 * time.Hour)

	currentPeriod, err := s.getHourlyStats(twentyFourHoursAgo, now)
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrDatabase, "failed to get current period stats"))
		return
	}
	previousPeriod, err := s.getHourlyStats(fortyEightHoursAgo, twentyFourHoursAgo)
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

	// 获取安全警告信息
	securityWarnings := s.getSecurityWarnings()

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
		SecurityWarnings: securityWarnings,
	}

	response.Success(c, stats)
}

// Chart Get dashboard chart data
func (s *Server) Chart(c *gin.Context) {
	groupID := c.Query("groupId")

	now := time.Now()
	endHour := now.Truncate(time.Hour)
	startHour := endHour.Add(-23 * time.Hour)

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

	for i := range 24 {
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
		Where("timestamp >= ? AND request_type = ?", twentyMinutesAgo, models.RequestTypeFinal).
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

// getSecurityWarnings 检查安全配置并返回警告信息
func (s *Server) getSecurityWarnings() []models.SecurityWarning {
	var warnings []models.SecurityWarning
	
	// 获取AUTH_KEY和ENCRYPTION_KEY
	authConfig := s.config.GetAuthConfig()
	encryptionKey := s.config.GetEncryptionKey()
	
	// 检查AUTH_KEY
	if authConfig.Key == "" {
		warnings = append(warnings, models.SecurityWarning{
			Type:     "AUTH_KEY",
			Message:  "AUTH_KEY未设置，系统无法正常工作",
			Severity: "high",
			Suggestion: "必须设置AUTH_KEY以保护管理界面",
		})
	} else {
		authWarnings := checkPasswordSecurity(authConfig.Key, "AUTH_KEY")
		warnings = append(warnings, authWarnings...)
	}
	
	// 检查ENCRYPTION_KEY
	if encryptionKey == "" {
		warnings = append(warnings, models.SecurityWarning{
			Type:     "ENCRYPTION_KEY",
			Message:  "未设置ENCRYPTION_KEY，敏感数据将明文存储",
			Severity: "high",
			Suggestion: "强烈建议设置ENCRYPTION_KEY以加密保护API密钥等敏感数据",
		})
	} else {
		encryptionWarnings := checkPasswordSecurity(encryptionKey, "ENCRYPTION_KEY")
		warnings = append(warnings, encryptionWarnings...)
	}
	
	return warnings
}

// checkPasswordSecurity 综合检查密码安全性
func checkPasswordSecurity(password, keyType string) []models.SecurityWarning {
	var warnings []models.SecurityWarning
	
	// 1. 长度检查
	if len(password) < 16 {
		warnings = append(warnings, models.SecurityWarning{
			Type:     keyType,
			Message:  fmt.Sprintf("%s长度不足（%d字符），建议至少16字符", keyType, len(password)),
			Severity: "high", // 长度不足是高风险
			Suggestion: "使用至少16个字符的强密码，推荐32字符以上",
		})
	} else if len(password) < 32 {
		warnings = append(warnings, models.SecurityWarning{
			Type:     keyType,
			Message:  fmt.Sprintf("%s长度偏短（%d字符），建议32字符以上", keyType, len(password)),
			Severity: "medium",
			Suggestion: "推荐使用32个字符以上的密码以提高安全性",
		})
	}
	
	// 2. 常见弱密码检查
	lower := strings.ToLower(password)
	weakPatterns := []string{
		"password", "123456", "admin", "secret", "test", "demo",
		"sk-123456", "key", "token", "pass", "pwd", "qwerty",
		"abc", "default", "user", "login", "auth", "temp",
	}
	
	for _, pattern := range weakPatterns {
		if strings.Contains(lower, pattern) {
			warnings = append(warnings, models.SecurityWarning{
				Type:     keyType,
				Message:  fmt.Sprintf("%s包含常见弱密码模式：%s", keyType, pattern),
				Severity: "high",
				Suggestion: "避免使用常见单词，建议使用随机生成的强密码",
			})
			break
		}
	}
	
	// 3. 复杂度检查（仅在长度足够时检查）
	if len(password) >= 16 && !hasGoodComplexity(password) {
		warnings = append(warnings, models.SecurityWarning{
			Type:     keyType,
			Message:  fmt.Sprintf("%s复杂度不足，缺少大小写字母、数字或特殊字符的组合", keyType),
			Severity: "medium",
			Suggestion: "建议包含大小写字母、数字和特殊字符以提高密码强度",
		})
	}
	
	return warnings
}

// hasGoodComplexity 检查密码复杂度
func hasGoodComplexity(password string) bool {
	var hasUpper, hasLower, hasDigit, hasSpecial bool
	
	for _, char := range password {
		switch {
		case char >= 'A' && char <= 'Z':
			hasUpper = true
		case char >= 'a' && char <= 'z':
			hasLower = true
		case char >= '0' && char <= '9':
			hasDigit = true
		case !((char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9')):
			hasSpecial = true
		}
	}
	
	// 至少包含3种类型的字符
	count := 0
	if hasUpper { count++ }
	if hasLower { count++ }
	if hasDigit { count++ }
	if hasSpecial { count++ }
	
	return count >= 3
}

// EncryptionStatus checks if ENCRYPTION_KEY is configured but keys are not encrypted
func (s *Server) EncryptionStatus(c *gin.Context) {
	hasMismatch, message, suggestion := s.checkEncryptionMismatch()
	
	response.Success(c, gin.H{
		"has_mismatch": hasMismatch,
		"message":      message,
		"suggestion":   suggestion,
	})
}

// checkEncryptionMismatch detects encryption configuration mismatches
func (s *Server) checkEncryptionMismatch() (bool, string, string) {
	encryptionKey := s.config.GetEncryptionKey()
	
	// Sample check API keys
	var sampleKeys []models.APIKey
	if err := s.DB.Limit(20).Where("key_hash IS NOT NULL AND key_hash != ''").Find(&sampleKeys).Error; err != nil {
		logrus.WithError(err).Error("Failed to fetch sample keys for encryption check")
		return false, "", ""
	}
	
	if len(sampleKeys) == 0 {
		// No keys in database, no mismatch
		return false, "", ""
	}
	
	// Check hash consistency with unencrypted data
	noopService, err := encryption.NewService("")
	if err != nil {
		logrus.WithError(err).Error("Failed to create noop encryption service")
		return false, "", ""
	}
	
	unencryptedHashMatchCount := 0
	for _, key := range sampleKeys {
		// For unencrypted data: key_hash should match SHA256(key_value)
		expectedHash := noopService.Hash(key.KeyValue)
		if expectedHash == key.KeyHash {
			unencryptedHashMatchCount++
		}
	}
	
	unencryptedConsistencyRate := float64(unencryptedHashMatchCount) / float64(len(sampleKeys))
	
	// If ENCRYPTION_KEY is configured, also check if current key can decrypt the data
	var currentKeyHashMatchCount int
	if encryptionKey != "" {
		currentService, err := encryption.NewService(encryptionKey)
		if err == nil {
			for _, key := range sampleKeys {
				// Try to decrypt and re-hash to check if current key matches
				decrypted, err := currentService.Decrypt(key.KeyValue)
				if err == nil {
					// Successfully decrypted, check if hash matches
					expectedHash := currentService.Hash(decrypted)
					if expectedHash == key.KeyHash {
						currentKeyHashMatchCount++
					}
				}
			}
		}
	}
	currentKeyConsistencyRate := float64(currentKeyHashMatchCount) / float64(len(sampleKeys))
	
	// Scenario A: ENCRYPTION_KEY configured but data not encrypted
	if encryptionKey != "" && unencryptedConsistencyRate > 0.8 {
		return true,
			"检测到您已配置 ENCRYPTION_KEY，但数据库中的密钥尚未加密。这会导致密钥无法正常读取（显示为 failed-to-decrypt）。",
			"请停止服务，执行密钥迁移命令后重启"
	}
	
	// Scenario B: ENCRYPTION_KEY not configured but data is encrypted
	if encryptionKey == "" && unencryptedConsistencyRate < 0.2 {
		return true,
			"检测到数据库中的密钥已加密，但未配置 ENCRYPTION_KEY。这会导致密钥无法正常读取。",
			"请配置与加密时相同的 ENCRYPTION_KEY，或执行解密迁移"
	}
	
	// Scenario C: ENCRYPTION_KEY configured but doesn't match encrypted data
	if encryptionKey != "" && unencryptedConsistencyRate < 0.2 && currentKeyConsistencyRate < 0.2 {
		return true,
			"检测到您配置的 ENCRYPTION_KEY 与数据加密时使用的密钥不匹配。这会导致密钥解密失败（显示为 failed-to-decrypt）。",
			"请使用正确的 ENCRYPTION_KEY，或执行密钥迁移"
	}
	
	return false, "", ""
}
