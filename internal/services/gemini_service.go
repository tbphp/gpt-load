package services

import (
	"fmt"
	"gpt-load/internal/models"
	"gpt-load/internal/types"
	"gpt-load/internal/channel/gemini"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// GeminiService provides business logic for Gemini-specific operations
type GeminiService struct {
	DB              *gorm.DB
	SettingsManager SystemSettingsManager
	logger          *logrus.Logger
}

// SystemSettingsManager interface for settings management
type SystemSettingsManager interface {
	GetSettings() (*types.SystemSettings, error)
	UpdateSettings(*types.SystemSettings) error
}

// NewGeminiService creates a new Gemini service
func NewGeminiService(db *gorm.DB, settingsManager SystemSettingsManager, logger *logrus.Logger) *GeminiService {
	return &GeminiService{
		DB:              db,
		SettingsManager: settingsManager,
		logger:          logger.WithField("service", "gemini"),
	}
}

// GetGeminiSettings retrieves current Gemini settings
func (gs *GeminiService) GetGeminiSettings() (*gemini.GeminiConfig, error) {
	settings, err := gs.SettingsManager.GetSettings()
	if err != nil {
		return nil, fmt.Errorf("failed to get system settings: %w", err)
	}

	// 转换系统设置为 Gemini 配置
	config := &gemini.GeminiConfig{
		MaxConsecutiveRetries:      settings.GeminiMaxRetries,
		RetryDelayMs:              time.Duration(settings.GeminiRetryDelayMs) * time.Millisecond,
		SwallowThoughtsAfterRetry:  settings.GeminiSwallowThoughtsAfterRetry,
		EnablePunctuationHeuristic: settings.GeminiEnablePunctuationHeuristic,
		EnableDetailedLogging:      settings.GeminiEnableDetailedLogging,
		SaveRetryRequests:         settings.GeminiSaveRetryRequests,
		MaxOutputChars:            settings.GeminiMaxOutputChars,
		StreamTimeout:             time.Duration(settings.GeminiStreamTimeout) * time.Second,
	}

	return config, nil
}

// UpdateGeminiSettings updates Gemini settings
func (gs *GeminiService) UpdateGeminiSettings(update *gemini.ConfigUpdate) error {
	settings, err := gs.SettingsManager.GetSettings()
	if err != nil {
		return fmt.Errorf("failed to get current settings: %w", err)
	}

	// 更新 Gemini 相关设置
	if update.MaxConsecutiveRetries != nil {
		settings.GeminiMaxRetries = *update.MaxConsecutiveRetries
	}
	if update.RetryDelayMs != nil {
		settings.GeminiRetryDelayMs = *update.RetryDelayMs
	}
	if update.SwallowThoughtsAfterRetry != nil {
		settings.GeminiSwallowThoughtsAfterRetry = *update.SwallowThoughtsAfterRetry
	}
	if update.EnablePunctuationHeuristic != nil {
		settings.GeminiEnablePunctuationHeuristic = *update.EnablePunctuationHeuristic
	}
	if update.EnableDetailedLogging != nil {
		settings.GeminiEnableDetailedLogging = *update.EnableDetailedLogging
	}
	if update.SaveRetryRequests != nil {
		settings.GeminiSaveRetryRequests = *update.SaveRetryRequests
	}
	if update.MaxOutputChars != nil {
		settings.GeminiMaxOutputChars = *update.MaxOutputChars
	}
	if update.StreamTimeout != nil {
		settings.GeminiStreamTimeout = *update.StreamTimeout
	}

	// 保存更新的设置
	if err := gs.SettingsManager.UpdateSettings(settings); err != nil {
		return fmt.Errorf("failed to update settings: %w", err)
	}

	gs.logger.Info("Gemini settings updated successfully")
	return nil
}

// GetGeminiStats retrieves Gemini processing statistics
func (gs *GeminiService) GetGeminiStats(days int) (*models.GeminiLogStats, error) {
	if days <= 0 {
		days = 7 // 默认7天
	}

	startTime := time.Now().AddDate(0, 0, -days)
	endTime := time.Now()

	var stats models.GeminiLogStats
	stats.StartTime = startTime
	stats.EndTime = endTime

	// 基础统计查询
	var totalLogs, successfulLogs, thoughtFiltered int64
	var totalRetries int64
	var avgDuration, maxDuration, minDuration float64

	// 总日志数
	if err := gs.DB.Model(&models.GeminiLog{}).
		Where("created_at BETWEEN ? AND ?", startTime, endTime).
		Count(&totalLogs).Error; err != nil {
		return nil, fmt.Errorf("failed to count total logs: %w", err)
	}

	// 成功日志数
	if err := gs.DB.Model(&models.GeminiLog{}).
		Where("created_at BETWEEN ? AND ? AND final_success = ?", startTime, endTime, true).
		Count(&successfulLogs).Error; err != nil {
		return nil, fmt.Errorf("failed to count successful logs: %w", err)
	}

	// 思考过滤统计
	if err := gs.DB.Model(&models.GeminiLog{}).
		Where("created_at BETWEEN ? AND ? AND thought_filtered = ?", startTime, endTime, true).
		Count(&thoughtFiltered).Error; err != nil {
		return nil, fmt.Errorf("failed to count thought filtered logs: %w", err)
	}

	// 重试统计
	if err := gs.DB.Model(&models.GeminiLog{}).
		Where("created_at BETWEEN ?", startTime, endTime).
		Select("SUM(retry_count)").
		Scan(&totalRetries).Error; err != nil {
		return nil, fmt.Errorf("failed to sum retries: %w", err)
	}

	// 性能统计
	if err := gs.DB.Model(&models.GeminiLog{}).
		Where("created_at BETWEEN ? AND ?", startTime, endTime).
		Select("AVG(total_duration), MAX(total_duration), MIN(total_duration)").
		Row().Scan(&avgDuration, &maxDuration, &minDuration); err != nil {
		return nil, fmt.Errorf("failed to get duration stats: %w", err)
	}

	// 中断原因统计
	interruptionStats := make(map[string]int64)
	rows, err := gs.DB.Model(&models.GeminiLog{}).
		Where("created_at BETWEEN ? AND ? AND interrupt_reason != ''", startTime, endTime).
		Select("interrupt_reason, COUNT(*) as count").
		Group("interrupt_reason").
		Rows()
	if err != nil {
		return nil, fmt.Errorf("failed to get interruption stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var reason string
		var count int64
		if err := rows.Scan(&reason, &count); err != nil {
			continue
		}
		interruptionStats[reason] = count
	}

	// 计算统计数据
	stats.TotalLogs = totalLogs
	stats.SuccessfulLogs = successfulLogs
	stats.FailedLogs = totalLogs - successfulLogs
	stats.ThoughtFiltered = thoughtFiltered
	stats.TotalRetries = totalRetries
	stats.InterruptionStats = interruptionStats

	if totalLogs > 0 {
		stats.SuccessRate = float64(successfulLogs) / float64(totalLogs)
		stats.AverageRetries = float64(totalRetries) / float64(totalLogs)
		stats.FilterRate = float64(thoughtFiltered) / float64(totalLogs)
	}

	stats.AverageDuration = int64(avgDuration)
	stats.MaxDuration = int64(maxDuration)
	stats.MinDuration = int64(minDuration)

	// 获取最大重试次数
	var maxRetries int
	if err := gs.DB.Model(&models.GeminiLog{}).
		Where("created_at BETWEEN ? AND ?", startTime, endTime).
		Select("MAX(retry_count)").
		Scan(&maxRetries).Error; err != nil {
		gs.logger.WithError(err).Warn("Failed to get max retries")
	}
	stats.MaxRetries = maxRetries

	return &stats, nil
}

// GetGeminiLogs retrieves Gemini logs with pagination and filtering
func (gs *GeminiService) GetGeminiLogs(params *models.GeminiLogQueryParams) (*models.GeminiLogResponse, error) {
	// 设置默认值
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 20
	}
	if params.PageSize > 100 {
		params.PageSize = 100
	}

	// 构建查询
	query := gs.DB.Model(&models.GeminiLog{})

	// 应用过滤条件
	if params.GroupID != nil {
		query = query.Where("group_id = ?", *params.GroupID)
	}
	if params.GroupName != "" {
		query = query.Where("group_name LIKE ?", "%"+params.GroupName+"%")
	}
	if params.KeyValue != "" {
		query = query.Where("key_value LIKE ?", "%"+params.KeyValue+"%")
	}
	if params.InterruptReason != "" {
		query = query.Where("interrupt_reason = ?", params.InterruptReason)
	}
	if params.FinalSuccess != nil {
		query = query.Where("final_success = ?", *params.FinalSuccess)
	}
	if params.ThoughtFiltered != nil {
		query = query.Where("thought_filtered = ?", *params.ThoughtFiltered)
	}
	if params.MinRetryCount != nil {
		query = query.Where("retry_count >= ?", *params.MinRetryCount)
	}
	if params.MaxRetryCount != nil {
		query = query.Where("retry_count <= ?", *params.MaxRetryCount)
	}
	if params.StartTime != nil {
		query = query.Where("created_at >= ?", *params.StartTime)
	}
	if params.EndTime != nil {
		query = query.Where("created_at <= ?", *params.EndTime)
	}

	// 获取总数
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("failed to count logs: %w", err)
	}

	// 应用排序
	orderBy := "created_at"
	if params.OrderBy != "" {
		switch params.OrderBy {
		case "created_at", "retry_count", "total_duration", "output_chars":
			orderBy = params.OrderBy
		}
	}
	
	if params.OrderDesc {
		orderBy += " DESC"
	} else {
		orderBy += " ASC"
	}
	query = query.Order(orderBy)

	// 应用分页
	offset := (params.Page - 1) * params.PageSize
	query = query.Offset(offset).Limit(params.PageSize)

	// 执行查询
	var logs []models.GeminiLog
	if err := query.Find(&logs).Error; err != nil {
		return nil, fmt.Errorf("failed to query logs: %w", err)
	}

	// 计算总页数
	totalPages := int(total) / params.PageSize
	if int(total)%params.PageSize > 0 {
		totalPages++
	}

	return &models.GeminiLogResponse{
		Logs:       logs,
		Total:      total,
		Page:       params.Page,
		PageSize:   params.PageSize,
		TotalPages: totalPages,
	}, nil
}

// LogRetryAttempt logs a retry attempt
func (gs *GeminiService) LogRetryAttempt(entry *models.GeminiLog) error {
	if err := entry.Validate(); err != nil {
		return fmt.Errorf("invalid log entry: %w", err)
	}

	entry.SetDefaults()

	if err := gs.DB.Create(entry).Error; err != nil {
		return fmt.Errorf("failed to create log entry: %w", err)
	}

	return nil
}

// ResetGeminiStats resets Gemini statistics (deletes old logs)
func (gs *GeminiService) ResetGeminiStats(olderThanDays int) error {
	if olderThanDays <= 0 {
		olderThanDays = 30 // 默认删除30天前的数据
	}

	cutoffTime := time.Now().AddDate(0, 0, -olderThanDays)

	result := gs.DB.Where("created_at < ?", cutoffTime).Delete(&models.GeminiLog{})
	if result.Error != nil {
		return fmt.Errorf("failed to reset stats: %w", result.Error)
	}

	gs.logger.WithField("deleted_count", result.RowsAffected).
		WithField("cutoff_time", cutoffTime).
		Info("Gemini statistics reset completed")

	return nil
}

// GetRecentLogs gets recent Gemini logs for monitoring
func (gs *GeminiService) GetRecentLogs(limit int) ([]models.GeminiLogSummary, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	var logs []models.GeminiLog
	if err := gs.DB.Order("created_at DESC").
		Limit(limit).
		Find(&logs).Error; err != nil {
		return nil, fmt.Errorf("failed to get recent logs: %w", err)
	}

	summaries := make([]models.GeminiLogSummary, len(logs))
	for i, log := range logs {
		summaries[i] = log.ToSummary()
	}

	return summaries, nil
}
