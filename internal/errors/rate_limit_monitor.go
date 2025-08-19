package errors

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

// RateLimitMonitor 429错误监控器
type RateLimitMonitor struct {
	// 统计数据
	totalRateLimitErrors int64
	rateLimitsByKey      map[uint]*KeyRateLimitStats
	rateLimitsByGroup    map[uint]*GroupRateLimitStats
	
	// 时间窗口统计
	recentErrors         []RateLimitEvent
	windowSize           time.Duration
	
	// 同步
	mu sync.RWMutex
	
	// 配置
	config *MonitorConfig
}

// KeyRateLimitStats 密钥429统计
type KeyRateLimitStats struct {
	KeyID            uint      `json:"key_id"`
	TotalCount       int64     `json:"total_count"`
	LastOccurrence   time.Time `json:"last_occurrence"`
	FirstOccurrence  time.Time `json:"first_occurrence"`
	AverageInterval  time.Duration `json:"average_interval"`
	RecentCount      int64     `json:"recent_count"`      // 最近1小时内的次数
	Pattern          string    `json:"pattern"`           // 错误模式
}

// GroupRateLimitStats 分组429统计
type GroupRateLimitStats struct {
	GroupID          uint      `json:"group_id"`
	TotalCount       int64     `json:"total_count"`
	AffectedKeys     int       `json:"affected_keys"`
	LastOccurrence   time.Time `json:"last_occurrence"`
	FirstOccurrence  time.Time `json:"first_occurrence"`
	AverageInterval  time.Duration `json:"average_interval"`
	RecentCount      int64     `json:"recent_count"`
}

// RateLimitEvent 429错误事件
type RateLimitEvent struct {
	Timestamp    time.Time         `json:"timestamp"`
	KeyID        uint              `json:"key_id"`
	GroupID      uint              `json:"group_id"`
	StatusCode   int               `json:"status_code"`
	ErrorMessage string            `json:"error_message"`
	RetryAfter   time.Duration     `json:"retry_after"`
	ResetAt      *time.Time        `json:"reset_at,omitempty"`
	Pattern      string            `json:"pattern"`
	Severity     RateLimitSeverity `json:"severity"`
}

// RateLimitSeverity 429错误严重程度
type RateLimitSeverity string

const (
	SeverityLow      RateLimitSeverity = "low"      // 短期限制（<5分钟）
	SeverityMedium   RateLimitSeverity = "medium"   // 中期限制（5分钟-1小时）
	SeverityHigh     RateLimitSeverity = "high"     // 长期限制（1小时-24小时）
	SeverityCritical RateLimitSeverity = "critical" // 严重限制（>24小时）
)

// MonitorConfig 监控配置
type MonitorConfig struct {
	WindowSize           time.Duration `json:"window_size"`            // 时间窗口大小
	MaxEvents            int           `json:"max_events"`             // 最大事件数量
	EnablePatternAnalysis bool         `json:"enable_pattern_analysis"` // 启用模式分析
	EnableAlerts         bool          `json:"enable_alerts"`          // 启用告警
	AlertThreshold       int64         `json:"alert_threshold"`        // 告警阈值
}

// DefaultMonitorConfig 返回默认监控配置
func DefaultMonitorConfig() *MonitorConfig {
	return &MonitorConfig{
		WindowSize:           24 * time.Hour,
		MaxEvents:            10000,
		EnablePatternAnalysis: true,
		EnableAlerts:         true,
		AlertThreshold:       10, // 10次429错误触发告警
	}
}

// NewRateLimitMonitor 创建429错误监控器
func NewRateLimitMonitor(config *MonitorConfig) *RateLimitMonitor {
	if config == nil {
		config = DefaultMonitorConfig()
	}
	
	return &RateLimitMonitor{
		rateLimitsByKey:   make(map[uint]*KeyRateLimitStats),
		rateLimitsByGroup: make(map[uint]*GroupRateLimitStats),
		recentErrors:      make([]RateLimitEvent, 0),
		windowSize:        config.WindowSize,
		config:            config,
	}
}

// RecordRateLimitError 记录429错误
func (m *RateLimitMonitor) RecordRateLimitError(keyID, groupID uint, rateLimitErr *RateLimitError) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	now := time.Now()
	
	// 增加总计数
	atomic.AddInt64(&m.totalRateLimitErrors, 1)
	
	// 创建事件
	event := RateLimitEvent{
		Timestamp:    now,
		KeyID:        keyID,
		GroupID:      groupID,
		StatusCode:   rateLimitErr.HTTPStatus,
		ErrorMessage: rateLimitErr.Message,
		RetryAfter:   rateLimitErr.RetryAfter,
		ResetAt:      rateLimitErr.ResetAt,
		Pattern:      m.analyzeErrorPattern(rateLimitErr.Message),
		Severity:     m.calculateSeverity(rateLimitErr.RetryAfter),
	}
	
	// 添加到最近事件列表
	m.recentErrors = append(m.recentErrors, event)
	
	// 限制事件数量
	if len(m.recentErrors) > m.config.MaxEvents {
		m.recentErrors = m.recentErrors[len(m.recentErrors)-m.config.MaxEvents:]
	}
	
	// 更新密钥统计
	m.updateKeyStats(keyID, event)
	
	// 更新分组统计
	m.updateGroupStats(groupID, event)
	
	// 检查告警条件
	if m.config.EnableAlerts {
		m.checkAlertConditions(keyID, groupID, event)
	}
	
	logrus.WithFields(logrus.Fields{
		"keyID":     keyID,
		"groupID":   groupID,
		"pattern":   event.Pattern,
		"severity":  event.Severity,
		"retryAfter": rateLimitErr.RetryAfter,
	}).Info("Rate limit error recorded")
}

// updateKeyStats 更新密钥统计
func (m *RateLimitMonitor) updateKeyStats(keyID uint, event RateLimitEvent) {
	stats, exists := m.rateLimitsByKey[keyID]
	if !exists {
		stats = &KeyRateLimitStats{
			KeyID:           keyID,
			FirstOccurrence: event.Timestamp,
			Pattern:         event.Pattern,
		}
		m.rateLimitsByKey[keyID] = stats
	}
	
	stats.TotalCount++
	stats.LastOccurrence = event.Timestamp
	
	// 计算平均间隔
	if stats.TotalCount > 1 {
		totalDuration := stats.LastOccurrence.Sub(stats.FirstOccurrence)
		stats.AverageInterval = totalDuration / time.Duration(stats.TotalCount-1)
	}
	
	// 计算最近1小时的次数
	oneHourAgo := event.Timestamp.Add(-1 * time.Hour)
	recentCount := int64(0)
	for i := len(m.recentErrors) - 1; i >= 0; i-- {
		if m.recentErrors[i].KeyID == keyID && m.recentErrors[i].Timestamp.After(oneHourAgo) {
			recentCount++
		} else if m.recentErrors[i].Timestamp.Before(oneHourAgo) {
			break
		}
	}
	stats.RecentCount = recentCount
}

// updateGroupStats 更新分组统计
func (m *RateLimitMonitor) updateGroupStats(groupID uint, event RateLimitEvent) {
	stats, exists := m.rateLimitsByGroup[groupID]
	if !exists {
		stats = &GroupRateLimitStats{
			GroupID:         groupID,
			FirstOccurrence: event.Timestamp,
		}
		m.rateLimitsByGroup[groupID] = stats
	}
	
	stats.TotalCount++
	stats.LastOccurrence = event.Timestamp
	
	// 计算受影响的密钥数量
	affectedKeys := make(map[uint]bool)
	for _, e := range m.recentErrors {
		if e.GroupID == groupID {
			affectedKeys[e.KeyID] = true
		}
	}
	stats.AffectedKeys = len(affectedKeys)
	
	// 计算平均间隔
	if stats.TotalCount > 1 {
		totalDuration := stats.LastOccurrence.Sub(stats.FirstOccurrence)
		stats.AverageInterval = totalDuration / time.Duration(stats.TotalCount-1)
	}
	
	// 计算最近1小时的次数
	oneHourAgo := event.Timestamp.Add(-1 * time.Hour)
	recentCount := int64(0)
	for i := len(m.recentErrors) - 1; i >= 0; i-- {
		if m.recentErrors[i].GroupID == groupID && m.recentErrors[i].Timestamp.After(oneHourAgo) {
			recentCount++
		} else if m.recentErrors[i].Timestamp.Before(oneHourAgo) {
			break
		}
	}
	stats.RecentCount = recentCount
}

// analyzeErrorPattern 分析错误模式
func (m *RateLimitMonitor) analyzeErrorPattern(errorMessage string) string {
	if !m.config.EnablePatternAnalysis {
		return "unknown"
	}
	
	errorLower := strings.ToLower(errorMessage)
	
	// 定义错误模式
	patterns := map[string][]string{
		"quota_exceeded": {"quota exceeded", "quota_exceeded", "daily limit", "monthly limit"},
		"rate_limit":     {"rate limit", "rate_limit", "too many requests", "throttled"},
		"rpm_exceeded":   {"rpm exceeded", "requests per minute", "per minute"},
		"rph_exceeded":   {"rph exceeded", "requests per hour", "per hour"},
		"rpd_exceeded":   {"rpd exceeded", "requests per day", "per day"},
		"openai_limit":   {"openai", "gpt", "chatgpt"},
		"anthropic_limit": {"anthropic", "claude"},
		"google_limit":   {"google", "gemini", "palm"},
		"azure_limit":    {"azure", "microsoft"},
		"aws_limit":      {"aws", "amazon", "bedrock"},
	}
	
	for pattern, keywords := range patterns {
		for _, keyword := range keywords {
			if strings.Contains(errorLower, keyword) {
				return pattern
			}
		}
	}
	
	return "generic"
}

// calculateSeverity 计算严重程度
func (m *RateLimitMonitor) calculateSeverity(retryAfter time.Duration) RateLimitSeverity {
	if retryAfter == 0 {
		return SeverityLow
	}
	
	if retryAfter < 5*time.Minute {
		return SeverityLow
	} else if retryAfter < 1*time.Hour {
		return SeverityMedium
	} else if retryAfter < 24*time.Hour {
		return SeverityHigh
	} else {
		return SeverityCritical
	}
}

// checkAlertConditions 检查告警条件
func (m *RateLimitMonitor) checkAlertConditions(keyID, groupID uint, event RateLimitEvent) {
	// 检查密钥级别告警
	if keyStats, exists := m.rateLimitsByKey[keyID]; exists {
		if keyStats.RecentCount >= m.config.AlertThreshold {
			m.triggerAlert("key_rate_limit_alert", map[string]interface{}{
				"keyID":       keyID,
				"recentCount": keyStats.RecentCount,
				"pattern":     keyStats.Pattern,
				"severity":    event.Severity,
			})
		}
	}
	
	// 检查分组级别告警
	if groupStats, exists := m.rateLimitsByGroup[groupID]; exists {
		if groupStats.RecentCount >= m.config.AlertThreshold*2 { // 分组阈值更高
			m.triggerAlert("group_rate_limit_alert", map[string]interface{}{
				"groupID":      groupID,
				"recentCount":  groupStats.RecentCount,
				"affectedKeys": groupStats.AffectedKeys,
				"severity":     event.Severity,
			})
		}
	}
}

// triggerAlert 触发告警
func (m *RateLimitMonitor) triggerAlert(alertType string, data map[string]interface{}) {
	logrus.WithFields(logrus.Fields{
		"alertType": alertType,
		"data":      data,
	}).Warn("Rate limit alert triggered")
	
	// 这里可以集成到告警系统，如发送邮件、Slack通知等
}

// GetTotalRateLimitErrors 获取总429错误数
func (m *RateLimitMonitor) GetTotalRateLimitErrors() int64 {
	return atomic.LoadInt64(&m.totalRateLimitErrors)
}

// GetKeyStats 获取密钥统计
func (m *RateLimitMonitor) GetKeyStats(keyID uint) *KeyRateLimitStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	if stats, exists := m.rateLimitsByKey[keyID]; exists {
		// 返回副本
		statsCopy := *stats
		return &statsCopy
	}
	
	return nil
}

// GetGroupStats 获取分组统计
func (m *RateLimitMonitor) GetGroupStats(groupID uint) *GroupRateLimitStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	if stats, exists := m.rateLimitsByGroup[groupID]; exists {
		// 返回副本
		statsCopy := *stats
		return &statsCopy
	}
	
	return nil
}

// GetRecentEvents 获取最近的429错误事件
func (m *RateLimitMonitor) GetRecentEvents(limit int) []RateLimitEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	if limit <= 0 || limit > len(m.recentErrors) {
		limit = len(m.recentErrors)
	}
	
	// 返回最近的事件
	start := len(m.recentErrors) - limit
	events := make([]RateLimitEvent, limit)
	copy(events, m.recentErrors[start:])
	
	return events
}

// GetAllKeyStats 获取所有密钥统计
func (m *RateLimitMonitor) GetAllKeyStats() map[uint]*KeyRateLimitStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	result := make(map[uint]*KeyRateLimitStats)
	for keyID, stats := range m.rateLimitsByKey {
		statsCopy := *stats
		result[keyID] = &statsCopy
	}
	
	return result
}

// GetAllGroupStats 获取所有分组统计
func (m *RateLimitMonitor) GetAllGroupStats() map[uint]*GroupRateLimitStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	result := make(map[uint]*GroupRateLimitStats)
	for groupID, stats := range m.rateLimitsByGroup {
		statsCopy := *stats
		result[groupID] = &statsCopy
	}
	
	return result
}

// CleanupOldEvents 清理过期事件
func (m *RateLimitMonitor) CleanupOldEvents() {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	cutoff := time.Now().Add(-m.windowSize)
	
	// 过滤掉过期事件
	var filteredEvents []RateLimitEvent
	for _, event := range m.recentErrors {
		if event.Timestamp.After(cutoff) {
			filteredEvents = append(filteredEvents, event)
		}
	}
	
	m.recentErrors = filteredEvents
	
	logrus.WithField("remainingEvents", len(m.recentErrors)).Debug("Cleaned up old rate limit events")
}

// Reset 重置监控器
func (m *RateLimitMonitor) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	atomic.StoreInt64(&m.totalRateLimitErrors, 0)
	m.rateLimitsByKey = make(map[uint]*KeyRateLimitStats)
	m.rateLimitsByGroup = make(map[uint]*GroupRateLimitStats)
	m.recentErrors = make([]RateLimitEvent, 0)
	
	logrus.Info("Rate limit monitor reset")
}
