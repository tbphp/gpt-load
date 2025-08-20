package handler

import (
	"net/http"
	"strconv"
	"time"

	"gpt-load/internal/models"
	"gpt-load/internal/services"
	"gpt-load/internal/channel/gemini"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// GeminiHandler handles Gemini-specific HTTP requests
type GeminiHandler struct {
	GeminiService *services.GeminiService
	logger        *logrus.Logger
}

// NewGeminiHandler creates a new Gemini handler
func NewGeminiHandler(geminiService *services.GeminiService, logger *logrus.Logger) *GeminiHandler {
	return &GeminiHandler{
		GeminiService: geminiService,
		logger:        logger.WithField("handler", "gemini"),
	}
}

// GetGeminiSettings retrieves current Gemini settings
// @Summary Get Gemini settings
// @Description Get current Gemini configuration settings
// @Tags Gemini
// @Accept json
// @Produce json
// @Success 200 {object} gemini.GeminiConfig
// @Failure 500 {object} map[string]interface{}
// @Router /api/gemini/settings [get]
func (h *GeminiHandler) GetGeminiSettings(c *gin.Context) {
	settings, err := h.GeminiService.GetGeminiSettings()
	if err != nil {
		h.logger.WithError(err).Error("Failed to get Gemini settings")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to get Gemini settings",
			"details": err.Error(),
		})
		return
	}

	// 转换为响应格式
	response := map[string]interface{}{
		"max_consecutive_retries":       settings.MaxConsecutiveRetries,
		"retry_delay_ms":               int(settings.RetryDelayMs / time.Millisecond),
		"swallow_thoughts_after_retry":  settings.SwallowThoughtsAfterRetry,
		"enable_punctuation_heuristic": settings.EnablePunctuationHeuristic,
		"enable_detailed_logging":      settings.EnableDetailedLogging,
		"save_retry_requests":          settings.SaveRetryRequests,
		"max_output_chars":             settings.MaxOutputChars,
		"stream_timeout":               int(settings.StreamTimeout / time.Second),
	}

	c.JSON(http.StatusOK, response)
}

// UpdateGeminiSettings updates Gemini settings
// @Summary Update Gemini settings
// @Description Update Gemini configuration settings
// @Tags Gemini
// @Accept json
// @Produce json
// @Param settings body gemini.ConfigUpdate true "Gemini settings update"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/gemini/settings [put]
func (h *GeminiHandler) UpdateGeminiSettings(c *gin.Context) {
	var update gemini.ConfigUpdate
	if err := c.ShouldBindJSON(&update); err != nil {
		h.logger.WithError(err).Error("Invalid request body for Gemini settings update")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid request body",
			"details": err.Error(),
		})
		return
	}

	if err := h.GeminiService.UpdateGeminiSettings(&update); err != nil {
		h.logger.WithError(err).Error("Failed to update Gemini settings")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to update Gemini settings",
			"details": err.Error(),
		})
		return
	}

	h.logger.Info("Gemini settings updated successfully")
	c.JSON(http.StatusOK, gin.H{
		"message": "Gemini settings updated successfully",
	})
}

// GetGeminiStats retrieves Gemini processing statistics
// @Summary Get Gemini statistics
// @Description Get Gemini processing statistics for a specified time period
// @Tags Gemini
// @Accept json
// @Produce json
// @Param days query int false "Number of days to include in statistics (default: 7)"
// @Success 200 {object} models.GeminiLogStats
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/gemini/stats [get]
func (h *GeminiHandler) GetGeminiStats(c *gin.Context) {
	// 解析天数参数
	days := 7 // 默认7天
	if daysStr := c.Query("days"); daysStr != "" {
		if parsedDays, err := strconv.Atoi(daysStr); err == nil && parsedDays > 0 {
			days = parsedDays
		}
	}

	stats, err := h.GeminiService.GetGeminiStats(days)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get Gemini statistics")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to get Gemini statistics",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// GetGeminiLogs retrieves Gemini logs with pagination and filtering
// @Summary Get Gemini logs
// @Description Get Gemini processing logs with pagination and filtering options
// @Tags Gemini
// @Accept json
// @Produce json
// @Param page query int false "Page number (default: 1)"
// @Param page_size query int false "Page size (default: 20, max: 100)"
// @Param group_id query int false "Filter by group ID"
// @Param group_name query string false "Filter by group name (partial match)"
// @Param key_value query string false "Filter by key value (partial match)"
// @Param interrupt_reason query string false "Filter by interruption reason"
// @Param final_success query bool false "Filter by final success status"
// @Param thought_filtered query bool false "Filter by thought filtered status"
// @Param min_retry_count query int false "Minimum retry count"
// @Param max_retry_count query int false "Maximum retry count"
// @Param start_time query string false "Start time (RFC3339 format)"
// @Param end_time query string false "End time (RFC3339 format)"
// @Param order_by query string false "Order by field (created_at, retry_count, total_duration)"
// @Param order_desc query bool false "Order descending (default: true)"
// @Success 200 {object} models.GeminiLogResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/gemini/logs [get]
func (h *GeminiHandler) GetGeminiLogs(c *gin.Context) {
	var params models.GeminiLogQueryParams

	// 绑定查询参数
	if err := c.ShouldBindQuery(&params); err != nil {
		h.logger.WithError(err).Error("Invalid query parameters for Gemini logs")
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid query parameters",
			"details": err.Error(),
		})
		return
	}

	// 解析时间参数
	if startTimeStr := c.Query("start_time"); startTimeStr != "" {
		if startTime, err := time.Parse(time.RFC3339, startTimeStr); err == nil {
			params.StartTime = &startTime
		}
	}
	if endTimeStr := c.Query("end_time"); endTimeStr != "" {
		if endTime, err := time.Parse(time.RFC3339, endTimeStr); err == nil {
			params.EndTime = &endTime
		}
	}

	// 设置默认排序
	if params.OrderBy == "" {
		params.OrderBy = "created_at"
		params.OrderDesc = true
	}

	response, err := h.GeminiService.GetGeminiLogs(&params)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get Gemini logs")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to get Gemini logs",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// ResetGeminiStats resets Gemini statistics
// @Summary Reset Gemini statistics
// @Description Reset Gemini statistics by deleting old log entries
// @Tags Gemini
// @Accept json
// @Produce json
// @Param days query int false "Delete logs older than this many days (default: 30)"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/gemini/reset-stats [post]
func (h *GeminiHandler) ResetGeminiStats(c *gin.Context) {
	// 解析天数参数
	days := 30 // 默认删除30天前的数据
	if daysStr := c.Query("days"); daysStr != "" {
		if parsedDays, err := strconv.Atoi(daysStr); err == nil && parsedDays > 0 {
			days = parsedDays
		}
	}

	if err := h.GeminiService.ResetGeminiStats(days); err != nil {
		h.logger.WithError(err).Error("Failed to reset Gemini statistics")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to reset Gemini statistics",
			"details": err.Error(),
		})
		return
	}

	h.logger.WithField("days", days).Info("Gemini statistics reset successfully")
	c.JSON(http.StatusOK, gin.H{
		"message": "Gemini statistics reset successfully",
		"days":    days,
	})
}

// GetRecentGeminiLogs retrieves recent Gemini logs for monitoring
// @Summary Get recent Gemini logs
// @Description Get recent Gemini logs for real-time monitoring
// @Tags Gemini
// @Accept json
// @Produce json
// @Param limit query int false "Number of recent logs to retrieve (default: 10, max: 100)"
// @Success 200 {object} []models.GeminiLogSummary
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/gemini/recent-logs [get]
func (h *GeminiHandler) GetRecentGeminiLogs(c *gin.Context) {
	// 解析限制参数
	limit := 10 // 默认10条
	if limitStr := c.Query("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 && parsedLimit <= 100 {
			limit = parsedLimit
		}
	}

	logs, err := h.GeminiService.GetRecentLogs(limit)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get recent Gemini logs")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to get recent Gemini logs",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, logs)
}

// GetGeminiHealth retrieves Gemini health status
// @Summary Get Gemini health status
// @Description Get current health status of Gemini processing
// @Tags Gemini
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/gemini/health [get]
func (h *GeminiHandler) GetGeminiHealth(c *gin.Context) {
	// 获取最近24小时的统计作为健康指标
	stats, err := h.GeminiService.GetGeminiStats(1)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get Gemini health status")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to get Gemini health status",
			"details": err.Error(),
		})
		return
	}

	// 计算健康状态
	status := "healthy"
	if stats.SuccessRate < 0.8 {
		status = "degraded"
	}
	if stats.SuccessRate < 0.5 {
		status = "unhealthy"
	}

	health := map[string]interface{}{
		"status":           status,
		"success_rate":     stats.SuccessRate,
		"total_logs":       stats.TotalLogs,
		"average_retries":  stats.AverageRetries,
		"thoughts_filtered": stats.ThoughtFiltered,
		"last_24h_stats":   stats,
		"timestamp":        time.Now(),
	}

	c.JSON(http.StatusOK, health)
}
