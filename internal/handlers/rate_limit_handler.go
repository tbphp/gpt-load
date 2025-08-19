package handlers

import (
	"net/http"
	"strconv"

	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/proxy"
	"gpt-load/internal/response"

	"github.com/gin-gonic/gin"
)

// RateLimitHandler 429错误处理器
type RateLimitHandler struct {
	monitor *app_errors.RateLimitMonitor
}

// NewRateLimitHandler 创建429错误处理器
func NewRateLimitHandler(proxyServer *proxy.ProxyServer) *RateLimitHandler {
	// 从ProxyServer获取RateLimitMonitor
	monitor := proxyServer.GetRateLimitMonitor()

	return &RateLimitHandler{
		monitor: monitor,
	}
}

// GetRateLimitStats 获取429错误统计
func (h *RateLimitHandler) GetRateLimitStats(c *gin.Context) {
	stats := map[string]interface{}{
		"total_errors": h.monitor.GetTotalRateLimitErrors(),
		"key_stats":    h.monitor.GetAllKeyStats(),
		"group_stats":  h.monitor.GetAllGroupStats(),
	}

	response.Success(c, stats)
}

// GetKeyRateLimitStats 获取指定密钥的429错误统计
func (h *RateLimitHandler) GetKeyRateLimitStats(c *gin.Context) {
	keyIDStr := c.Param("keyId")
	keyID, err := strconv.ParseUint(keyIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid key ID"})
		return
	}

	stats := h.monitor.GetKeyStats(uint(keyID))
	if stats == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Key stats not found"})
		return
	}

	response.Success(c, stats)
}

// GetGroupRateLimitStats 获取指定分组的429错误统计
func (h *RateLimitHandler) GetGroupRateLimitStats(c *gin.Context) {
	groupIDStr := c.Param("groupId")
	groupID, err := strconv.ParseUint(groupIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	stats := h.monitor.GetGroupStats(uint(groupID))
	if stats == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Group stats not found"})
		return
	}

	response.Success(c, stats)
}

// GetRecentRateLimitEvents 获取最近的429错误事件
func (h *RateLimitHandler) GetRecentRateLimitEvents(c *gin.Context) {
	limitStr := c.DefaultQuery("limit", "100")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 100
	}

	events := h.monitor.GetRecentEvents(limit)
	response.Success(c, events)
}

// GetRateLimitSummary 获取429错误摘要
func (h *RateLimitHandler) GetRateLimitSummary(c *gin.Context) {
	totalErrors := h.monitor.GetTotalRateLimitErrors()
	keyStats := h.monitor.GetAllKeyStats()
	groupStats := h.monitor.GetAllGroupStats()
	recentEvents := h.monitor.GetRecentEvents(10)

	// 计算摘要统计
	affectedKeys := len(keyStats)
	affectedGroups := len(groupStats)

	// 计算最近1小时的错误数
	recentErrorCount := int64(0)
	for _, stats := range keyStats {
		recentErrorCount += stats.RecentCount
	}

	// 分析错误模式分布
	patternDistribution := make(map[string]int)
	for _, event := range recentEvents {
		patternDistribution[event.Pattern]++
	}

	// 分析严重程度分布
	severityDistribution := make(map[string]int)
	for _, event := range recentEvents {
		severityDistribution[string(event.Severity)]++
	}

	summary := map[string]interface{}{
		"total_errors":           totalErrors,
		"affected_keys":          affectedKeys,
		"affected_groups":        affectedGroups,
		"recent_error_count":     recentErrorCount,
		"pattern_distribution":   patternDistribution,
		"severity_distribution":  severityDistribution,
		"recent_events":          recentEvents,
	}

	response.Success(c, summary)
}

// CleanupRateLimitData 清理过期的429错误数据
func (h *RateLimitHandler) CleanupRateLimitData(c *gin.Context) {
	h.monitor.CleanupOldEvents()

	response.Success(c, map[string]interface{}{
		"message": "Rate limit data cleanup completed",
	})
}

// ResetRateLimitMonitor 重置429错误监控器
func (h *RateLimitHandler) ResetRateLimitMonitor(c *gin.Context) {
	h.monitor.Reset()

	response.Success(c, map[string]interface{}{
		"message": "Rate limit monitor reset completed",
	})
}

// GetRateLimitTrends 获取429错误趋势分析
func (h *RateLimitHandler) GetRateLimitTrends(c *gin.Context) {
	recentEvents := h.monitor.GetRecentEvents(1000) // 获取更多事件用于趋势分析

	// 按小时分组统计
	hourlyStats := make(map[string]int)

	// 按天分组统计
	dailyStats := make(map[string]int)

	// 按错误模式分组统计
	patternStats := make(map[string]int)

	// 按严重程度分组统计
	severityStats := make(map[string]int)

	for _, event := range recentEvents {
		// 小时统计
		hourKey := event.Timestamp.Format("2006-01-02 15:00")
		hourlyStats[hourKey]++

		// 天统计
		dayKey := event.Timestamp.Format("2006-01-02")
		dailyStats[dayKey]++

		// 模式统计
		patternStats[event.Pattern]++

		// 严重程度统计
		severityStats[string(event.Severity)]++
	}

	trends := map[string]interface{}{
		"hourly_stats":   hourlyStats,
		"daily_stats":    dailyStats,
		"pattern_stats":  patternStats,
		"severity_stats": severityStats,
		"total_events":   len(recentEvents),
	}

	response.Success(c, trends)
}

// GetTopRateLimitedKeys 获取429错误最多的密钥
func (h *RateLimitHandler) GetTopRateLimitedKeys(c *gin.Context) {
	limitStr := c.DefaultQuery("limit", "10")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 10
	}

	keyStats := h.monitor.GetAllKeyStats()

	// 转换为切片并排序
	type KeyStatWithID struct {
		KeyID uint                              `json:"key_id"`
		Stats *app_errors.KeyRateLimitStats     `json:"stats"`
	}

	var keyStatsList []KeyStatWithID
	for keyID, stats := range keyStats {
		keyStatsList = append(keyStatsList, KeyStatWithID{
			KeyID: keyID,
			Stats: stats,
		})
	}

	// 简单的冒泡排序（按总错误数排序）
	for i := 0; i < len(keyStatsList)-1; i++ {
		for j := 0; j < len(keyStatsList)-i-1; j++ {
			if keyStatsList[j].Stats.TotalCount < keyStatsList[j+1].Stats.TotalCount {
				keyStatsList[j], keyStatsList[j+1] = keyStatsList[j+1], keyStatsList[j]
			}
		}
	}

	// 限制返回数量
	if limit > len(keyStatsList) {
		limit = len(keyStatsList)
	}

	topKeys := keyStatsList[:limit]

	response.Success(c, topKeys)
}

// GetTopRateLimitedGroups 获取429错误最多的分组
func (h *RateLimitHandler) GetTopRateLimitedGroups(c *gin.Context) {
	limitStr := c.DefaultQuery("limit", "10")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 10
	}

	groupStats := h.monitor.GetAllGroupStats()

	// 转换为切片并排序
	type GroupStatWithID struct {
		GroupID uint                                `json:"group_id"`
		Stats   *app_errors.GroupRateLimitStats     `json:"stats"`
	}

	var groupStatsList []GroupStatWithID
	for groupID, stats := range groupStats {
		groupStatsList = append(groupStatsList, GroupStatWithID{
			GroupID: groupID,
			Stats:   stats,
		})
	}

	// 简单的冒泡排序（按总错误数排序）
	for i := 0; i < len(groupStatsList)-1; i++ {
		for j := 0; j < len(groupStatsList)-i-1; j++ {
			if groupStatsList[j].Stats.TotalCount < groupStatsList[j+1].Stats.TotalCount {
				groupStatsList[j], groupStatsList[j+1] = groupStatsList[j+1], groupStatsList[j]
			}
		}
	}

	// 限制返回数量
	if limit > len(groupStatsList) {
		limit = len(groupStatsList)
	}

	topGroups := groupStatsList[:limit]

	response.Success(c, topGroups)
}
