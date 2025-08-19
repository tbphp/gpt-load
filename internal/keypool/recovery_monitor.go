package keypool

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// RecoveryMonitor 恢复监控器
type RecoveryMonitor struct {
	// 配置
	config          *MonitorConfig

	// 运行时状态
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	running         bool
	mu              sync.RWMutex

	// 指标数据
	metrics         *RecoveryMonitorMetrics

	// 事件处理
	eventHandlers   []RecoveryEventHandler

	// 告警
	alertManager    *RecoveryAlertManager
}

// MonitorConfig 监控配置
type MonitorConfig struct {
	// 基础配置
	MetricsInterval     time.Duration `json:"metrics_interval"`      // 指标收集间隔
	RetentionPeriod     time.Duration `json:"retention_period"`      // 数据保留期

	// 告警配置
	EnableAlerts        bool          `json:"enable_alerts"`         // 启用告警
	SuccessRateThreshold float64      `json:"success_rate_threshold"` // 成功率告警阈值
	LatencyThreshold    time.Duration `json:"latency_threshold"`     // 延迟告警阈值

	// 性能配置
	MaxMetricsHistory   int           `json:"max_metrics_history"`   // 最大指标历史记录数
	EnableDetailedLogs  bool          `json:"enable_detailed_logs"`  // 启用详细日志
}

// RecoveryMonitorMetrics 恢复监控指标
type RecoveryMonitorMetrics struct {
	// 基础统计
	TotalRecoveryAttempts   int64         `json:"total_recovery_attempts"`
	SuccessfulRecoveries    int64         `json:"successful_recoveries"`
	FailedRecoveries        int64         `json:"failed_recoveries"`

	// 成功率统计
	OverallSuccessRate      float64       `json:"overall_success_rate"`
	RecentSuccessRate       float64       `json:"recent_success_rate"`    // 最近1小时

	// 延迟统计
	AvgRecoveryLatency      time.Duration `json:"avg_recovery_latency"`
	P50RecoveryLatency      time.Duration `json:"p50_recovery_latency"`
	P95RecoveryLatency      time.Duration `json:"p95_recovery_latency"`
	P99RecoveryLatency      time.Duration `json:"p99_recovery_latency"`

	// 频率统计
	RecoveriesPerHour       float64       `json:"recoveries_per_hour"`
	RecoveriesPerDay        float64       `json:"recoveries_per_day"`

	// 错误统计
	ErrorsByType            map[string]int64 `json:"errors_by_type"`
	TopErrors               []*ErrorStat     `json:"top_errors"`

	// 时间序列数据
	HourlyStats             []*HourlyRecoveryStats `json:"hourly_stats"`
	DailyStats              []*DailyRecoveryStats  `json:"daily_stats"`

	// 最后更新时间
	LastUpdated             time.Time     `json:"last_updated"`
}

// ErrorStat 错误统计
type ErrorStat struct {
	ErrorType   string    `json:"error_type"`
	Count       int64     `json:"count"`
	LastOccurred time.Time `json:"last_occurred"`
	Percentage  float64   `json:"percentage"`
}

// HourlyRecoveryStats 小时恢复统计
type HourlyRecoveryStats struct {
	Hour            time.Time     `json:"hour"`
	Attempts        int64         `json:"attempts"`
	Successes       int64         `json:"successes"`
	Failures        int64         `json:"failures"`
	SuccessRate     float64       `json:"success_rate"`
	AvgLatency      time.Duration `json:"avg_latency"`
}

// DailyRecoveryStats 日恢复统计
type DailyRecoveryStats struct {
	Date            time.Time     `json:"date"`
	Attempts        int64         `json:"attempts"`
	Successes       int64         `json:"successes"`
	Failures        int64         `json:"failures"`
	SuccessRate     float64       `json:"success_rate"`
	AvgLatency      time.Duration `json:"avg_latency"`
	UniqueKeys      int64         `json:"unique_keys"`
}

// RecoveryEventHandler 恢复事件处理器接口
type RecoveryEventHandler interface {
	HandleRecoveryEvent(event *RecoveryEvent) error
}

// RecoveryEvent 恢复事件
type RecoveryEvent struct {
	Type        RecoveryEventType `json:"type"`
	KeyID       uint              `json:"key_id"`
	GroupID     uint              `json:"group_id"`
	Success     bool              `json:"success"`
	Error       string            `json:"error,omitempty"`
	Duration    time.Duration     `json:"duration"`
	Timestamp   time.Time         `json:"timestamp"`
	Metadata    map[string]interface{} `json:"metadata"`
}

// RecoveryEventType 恢复事件类型
type RecoveryEventType string

const (
	EventTypeRecoveryStarted   RecoveryEventType = "recovery_started"
	EventTypeRecoveryCompleted RecoveryEventType = "recovery_completed"
	EventTypeRecoveryFailed    RecoveryEventType = "recovery_failed"
	EventTypeBatchStarted      RecoveryEventType = "batch_started"
	EventTypeBatchCompleted    RecoveryEventType = "batch_completed"
)

// DefaultMonitorConfig 返回默认监控配置
func DefaultMonitorConfig() *MonitorConfig {
	return &MonitorConfig{
		MetricsInterval:         1 * time.Minute,
		RetentionPeriod:         7 * 24 * time.Hour, // 7天
		EnableAlerts:            true,
		SuccessRateThreshold:    0.8, // 80%
		LatencyThreshold:        30 * time.Second,
		MaxMetricsHistory:       1000,
		EnableDetailedLogs:      false,
	}
}

// NewRecoveryMonitor 创建恢复监控器
func NewRecoveryMonitor(config *MonitorConfig) *RecoveryMonitor {
	if config == nil {
		config = DefaultMonitorConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	monitor := &RecoveryMonitor{
		config:        config,
		ctx:           ctx,
		cancel:        cancel,
		metrics:       &RecoveryMonitorMetrics{
			ErrorsByType: make(map[string]int64),
			TopErrors:    make([]*ErrorStat, 0),
			HourlyStats:  make([]*HourlyRecoveryStats, 0),
			DailyStats:   make([]*DailyRecoveryStats, 0),
		},
		eventHandlers: make([]RecoveryEventHandler, 0),
	}

	// 创建告警管理器
	if config.EnableAlerts {
		monitor.alertManager = NewRecoveryAlertManager(config)
	}

	return monitor
}

// Start 启动监控器
func (m *RecoveryMonitor) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("recovery monitor is already running")
	}

	// 启动指标收集循环
	m.wg.Add(1)
	go m.metricsCollectionLoop()

	// 启动数据清理循环
	m.wg.Add(1)
	go m.dataCleanupLoop()

	m.running = true
	logrus.Info("Recovery monitor started")

	return nil
}

// Stop 停止监控器
func (m *RecoveryMonitor) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return nil
	}

	m.cancel()
	m.wg.Wait()

	m.running = false
	logrus.Info("Recovery monitor stopped")

	return nil
}

// RecordRecoveryAttempt 记录恢复尝试
func (m *RecoveryMonitor) RecordRecoveryAttempt(keyID, groupID uint, success bool, duration time.Duration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 更新基础统计
	m.metrics.TotalRecoveryAttempts++
	if success {
		m.metrics.SuccessfulRecoveries++
	} else {
		m.metrics.FailedRecoveries++
	}

	// 更新成功率
	if m.metrics.TotalRecoveryAttempts > 0 {
		m.metrics.OverallSuccessRate = float64(m.metrics.SuccessfulRecoveries) / float64(m.metrics.TotalRecoveryAttempts)
	}

	// 更新延迟统计
	m.updateLatencyStats(duration)

	// 记录错误
	if err != nil {
		errorType := fmt.Sprintf("%T", err)
		m.metrics.ErrorsByType[errorType]++
	}

	// 更新时间序列数据
	m.updateTimeSeriesStats(success, duration)

	m.metrics.LastUpdated = time.Now()

	// 创建事件
	event := &RecoveryEvent{
		Type:      EventTypeRecoveryCompleted,
		KeyID:     keyID,
		GroupID:   groupID,
		Success:   success,
		Duration:  duration,
		Timestamp: time.Now(),
		Metadata:  make(map[string]interface{}),
	}

	if err != nil {
		event.Type = EventTypeRecoveryFailed
		event.Error = err.Error()
	}

	// 处理事件
	m.handleEvent(event)

	// 检查告警
	if m.alertManager != nil {
		m.alertManager.CheckAlerts(m.metrics)
	}
}

// updateLatencyStats 更新延迟统计
func (m *RecoveryMonitor) updateLatencyStats(duration time.Duration) {
	// 简化实现：使用移动平均
	if m.metrics.AvgRecoveryLatency == 0 {
		m.metrics.AvgRecoveryLatency = duration
	} else {
		// 指数移动平均
		alpha := 0.1
		m.metrics.AvgRecoveryLatency = time.Duration(
			float64(m.metrics.AvgRecoveryLatency)*(1-alpha) + float64(duration)*alpha,
		)
	}

	// 这里应该维护一个延迟历史来计算百分位数
	// 简化实现，使用近似值
	m.metrics.P50RecoveryLatency = m.metrics.AvgRecoveryLatency
	m.metrics.P95RecoveryLatency = time.Duration(float64(m.metrics.AvgRecoveryLatency) * 1.5)
	m.metrics.P99RecoveryLatency = time.Duration(float64(m.metrics.AvgRecoveryLatency) * 2.0)
}

// updateTimeSeriesStats 更新时间序列统计
func (m *RecoveryMonitor) updateTimeSeriesStats(success bool, duration time.Duration) {
	now := time.Now()

	// 更新小时统计
	m.updateHourlyStats(now, success, duration)

	// 更新日统计
	m.updateDailyStats(now, success, duration)
}

// updateHourlyStats 更新小时统计
func (m *RecoveryMonitor) updateHourlyStats(now time.Time, success bool, duration time.Duration) {
	hour := now.Truncate(time.Hour)

	// 查找或创建当前小时的统计
	var hourlyStats *HourlyRecoveryStats
	for _, stats := range m.metrics.HourlyStats {
		if stats.Hour.Equal(hour) {
			hourlyStats = stats
			break
		}
	}

	if hourlyStats == nil {
		hourlyStats = &HourlyRecoveryStats{
			Hour: hour,
		}
		m.metrics.HourlyStats = append(m.metrics.HourlyStats, hourlyStats)

		// 限制历史记录数量
		if len(m.metrics.HourlyStats) > 24 { // 保留24小时
			m.metrics.HourlyStats = m.metrics.HourlyStats[1:]
		}
	}

	// 更新统计
	hourlyStats.Attempts++
	if success {
		hourlyStats.Successes++
	} else {
		hourlyStats.Failures++
	}

	// 更新成功率
	if hourlyStats.Attempts > 0 {
		hourlyStats.SuccessRate = float64(hourlyStats.Successes) / float64(hourlyStats.Attempts)
	}

	// 更新平均延迟
	if hourlyStats.AvgLatency == 0 {
		hourlyStats.AvgLatency = duration
	} else {
		totalTime := hourlyStats.AvgLatency * time.Duration(hourlyStats.Attempts-1)
		hourlyStats.AvgLatency = (totalTime + duration) / time.Duration(hourlyStats.Attempts)
	}
}

// updateDailyStats 更新日统计
func (m *RecoveryMonitor) updateDailyStats(now time.Time, success bool, duration time.Duration) {
	date := now.Truncate(24 * time.Hour)

	// 查找或创建当前日期的统计
	var dailyStats *DailyRecoveryStats
	for _, stats := range m.metrics.DailyStats {
		if stats.Date.Equal(date) {
			dailyStats = stats
			break
		}
	}

	if dailyStats == nil {
		dailyStats = &DailyRecoveryStats{
			Date: date,
		}
		m.metrics.DailyStats = append(m.metrics.DailyStats, dailyStats)

		// 限制历史记录数量
		if len(m.metrics.DailyStats) > 30 { // 保留30天
			m.metrics.DailyStats = m.metrics.DailyStats[1:]
		}
	}

	// 更新统计
	dailyStats.Attempts++
	if success {
		dailyStats.Successes++
	} else {
		dailyStats.Failures++
	}

	// 更新成功率
	if dailyStats.Attempts > 0 {
		dailyStats.SuccessRate = float64(dailyStats.Successes) / float64(dailyStats.Attempts)
	}

	// 更新平均延迟
	if dailyStats.AvgLatency == 0 {
		dailyStats.AvgLatency = duration
	} else {
		totalTime := dailyStats.AvgLatency * time.Duration(dailyStats.Attempts-1)
		dailyStats.AvgLatency = (totalTime + duration) / time.Duration(dailyStats.Attempts)
	}
}

// handleEvent 处理事件
func (m *RecoveryMonitor) handleEvent(event *RecoveryEvent) {
	// 记录详细日志
	if m.config.EnableDetailedLogs {
		logrus.WithFields(logrus.Fields{
			"type":     event.Type,
			"keyID":    event.KeyID,
			"groupID":  event.GroupID,
			"success":  event.Success,
			"duration": event.Duration,
			"error":    event.Error,
		}).Debug("Recovery event")
	}

	// 调用事件处理器
	for _, handler := range m.eventHandlers {
		if err := handler.HandleRecoveryEvent(event); err != nil {
			logrus.WithError(err).Warn("Recovery event handler failed")
		}
	}
}

// metricsCollectionLoop 指标收集循环
func (m *RecoveryMonitor) metricsCollectionLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.config.MetricsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.collectMetrics()
		}
	}
}

// collectMetrics 收集指标
func (m *RecoveryMonitor) collectMetrics() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 计算频率统计
	now := time.Now()

	// 计算最近1小时的成功率
	m.calculateRecentSuccessRate(now)

	// 计算每小时/每天恢复次数
	m.calculateRecoveryRates(now)

	// 更新错误统计
	m.updateErrorStats()
}

// calculateRecentSuccessRate 计算最近成功率
func (m *RecoveryMonitor) calculateRecentSuccessRate(now time.Time) {
	oneHourAgo := now.Add(-time.Hour)

	var recentAttempts, recentSuccesses int64

	for _, hourlyStats := range m.metrics.HourlyStats {
		if hourlyStats.Hour.After(oneHourAgo) {
			recentAttempts += hourlyStats.Attempts
			recentSuccesses += hourlyStats.Successes
		}
	}

	if recentAttempts > 0 {
		m.metrics.RecentSuccessRate = float64(recentSuccesses) / float64(recentAttempts)
	}
}

// calculateRecoveryRates 计算恢复频率
func (m *RecoveryMonitor) calculateRecoveryRates(now time.Time) {
	// 计算每小时恢复次数（基于最近24小时）
	if len(m.metrics.HourlyStats) > 0 {
		var totalAttempts int64
		for _, stats := range m.metrics.HourlyStats {
			totalAttempts += stats.Attempts
		}
		m.metrics.RecoveriesPerHour = float64(totalAttempts) / float64(len(m.metrics.HourlyStats))
	}

	// 计算每天恢复次数（基于最近30天）
	if len(m.metrics.DailyStats) > 0 {
		var totalAttempts int64
		for _, stats := range m.metrics.DailyStats {
			totalAttempts += stats.Attempts
		}
		m.metrics.RecoveriesPerDay = float64(totalAttempts) / float64(len(m.metrics.DailyStats))
	}
}

// updateErrorStats 更新错误统计
func (m *RecoveryMonitor) updateErrorStats() {
	// 计算错误百分比并排序
	var totalErrors int64
	for _, count := range m.metrics.ErrorsByType {
		totalErrors += count
	}

	m.metrics.TopErrors = m.metrics.TopErrors[:0] // 清空但保留容量

	for errorType, count := range m.metrics.ErrorsByType {
		percentage := float64(count) / float64(totalErrors) * 100
		m.metrics.TopErrors = append(m.metrics.TopErrors, &ErrorStat{
			ErrorType:  errorType,
			Count:      count,
			Percentage: percentage,
		})
	}

	// 按计数排序
	for i := 0; i < len(m.metrics.TopErrors)-1; i++ {
		for j := i + 1; j < len(m.metrics.TopErrors); j++ {
			if m.metrics.TopErrors[i].Count < m.metrics.TopErrors[j].Count {
				m.metrics.TopErrors[i], m.metrics.TopErrors[j] = m.metrics.TopErrors[j], m.metrics.TopErrors[i]
			}
		}
	}

	// 只保留前10个
	if len(m.metrics.TopErrors) > 10 {
		m.metrics.TopErrors = m.metrics.TopErrors[:10]
	}
}

// dataCleanupLoop 数据清理循环
func (m *RecoveryMonitor) dataCleanupLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(1 * time.Hour) // 每小时清理一次
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.cleanupOldData()
		}
	}
}

// cleanupOldData 清理过期数据
func (m *RecoveryMonitor) cleanupOldData() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-m.config.RetentionPeriod)

	// 清理小时统计
	var validHourlyStats []*HourlyRecoveryStats
	for _, stats := range m.metrics.HourlyStats {
		if stats.Hour.After(cutoff) {
			validHourlyStats = append(validHourlyStats, stats)
		}
	}
	m.metrics.HourlyStats = validHourlyStats

	// 清理日统计
	var validDailyStats []*DailyRecoveryStats
	for _, stats := range m.metrics.DailyStats {
		if stats.Date.After(cutoff) {
			validDailyStats = append(validDailyStats, stats)
		}
	}
	m.metrics.DailyStats = validDailyStats
}

// AddEventHandler 添加事件处理器
func (m *RecoveryMonitor) AddEventHandler(handler RecoveryEventHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.eventHandlers = append(m.eventHandlers, handler)
}

// GetMetrics 获取监控指标
func (m *RecoveryMonitor) GetMetrics() *RecoveryMonitorMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 返回指标的深拷贝
	metrics := &RecoveryMonitorMetrics{
		TotalRecoveryAttempts: m.metrics.TotalRecoveryAttempts,
		SuccessfulRecoveries:  m.metrics.SuccessfulRecoveries,
		FailedRecoveries:      m.metrics.FailedRecoveries,
		OverallSuccessRate:    m.metrics.OverallSuccessRate,
		RecentSuccessRate:     m.metrics.RecentSuccessRate,
		AvgRecoveryLatency:    m.metrics.AvgRecoveryLatency,
		P50RecoveryLatency:    m.metrics.P50RecoveryLatency,
		P95RecoveryLatency:    m.metrics.P95RecoveryLatency,
		P99RecoveryLatency:    m.metrics.P99RecoveryLatency,
		RecoveriesPerHour:     m.metrics.RecoveriesPerHour,
		RecoveriesPerDay:      m.metrics.RecoveriesPerDay,
		ErrorsByType:          make(map[string]int64),
		TopErrors:             make([]*ErrorStat, len(m.metrics.TopErrors)),
		HourlyStats:           make([]*HourlyRecoveryStats, len(m.metrics.HourlyStats)),
		DailyStats:            make([]*DailyRecoveryStats, len(m.metrics.DailyStats)),
		LastUpdated:           m.metrics.LastUpdated,
	}

	// 复制映射和切片
	for k, v := range m.metrics.ErrorsByType {
		metrics.ErrorsByType[k] = v
	}

	copy(metrics.TopErrors, m.metrics.TopErrors)
	copy(metrics.HourlyStats, m.metrics.HourlyStats)
	copy(metrics.DailyStats, m.metrics.DailyStats)

	return metrics
}

// RecoveryAlertManager 恢复告警管理器
type RecoveryAlertManager struct {
	config      *MonitorConfig
	lastAlerts  map[string]time.Time
	mu          sync.RWMutex
}

// NewRecoveryAlertManager 创建告警管理器
func NewRecoveryAlertManager(config *MonitorConfig) *RecoveryAlertManager {
	return &RecoveryAlertManager{
		config:     config,
		lastAlerts: make(map[string]time.Time),
	}
}

// CheckAlerts 检查告警条件
func (a *RecoveryAlertManager) CheckAlerts(metrics *RecoveryMonitorMetrics) {
	now := time.Now()

	// 检查成功率告警
	if metrics.RecentSuccessRate < a.config.SuccessRateThreshold {
		alertKey := "low_success_rate"
		if a.shouldSendAlert(alertKey, now) {
			a.sendAlert(&RecoveryAlert{
				Type:        AlertTypeLowSuccessRate,
				Severity:    AlertSeverityWarning,
				Message:     fmt.Sprintf("Recovery success rate (%.2f%%) is below threshold (%.2f%%)", metrics.RecentSuccessRate*100, a.config.SuccessRateThreshold*100),
				Timestamp:   now,
				Metrics:     metrics,
			})
			a.lastAlerts[alertKey] = now
		}
	}

	// 检查延迟告警
	if metrics.AvgRecoveryLatency > a.config.LatencyThreshold {
		alertKey := "high_latency"
		if a.shouldSendAlert(alertKey, now) {
			a.sendAlert(&RecoveryAlert{
				Type:        AlertTypeHighLatency,
				Severity:    AlertSeverityWarning,
				Message:     fmt.Sprintf("Average recovery latency (%v) exceeds threshold (%v)", metrics.AvgRecoveryLatency, a.config.LatencyThreshold),
				Timestamp:   now,
				Metrics:     metrics,
			})
			a.lastAlerts[alertKey] = now
		}
	}
}

// shouldSendAlert 判断是否应该发送告警
func (a *RecoveryAlertManager) shouldSendAlert(alertKey string, now time.Time) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	lastAlert, exists := a.lastAlerts[alertKey]
	if !exists {
		return true
	}

	// 防止告警风暴，至少间隔5分钟
	return now.Sub(lastAlert) > 5*time.Minute
}

// sendAlert 发送告警
func (a *RecoveryAlertManager) sendAlert(alert *RecoveryAlert) {
	logrus.WithFields(logrus.Fields{
		"type":     alert.Type,
		"severity": alert.Severity,
		"message":  alert.Message,
	}).Warn("Recovery alert triggered")

	// 这里可以集成到告警系统，如发送邮件、Slack通知等
}

// RecoveryAlert 恢复告警
type RecoveryAlert struct {
	Type        AlertType                 `json:"type"`
	Severity    AlertSeverity             `json:"severity"`
	Message     string                    `json:"message"`
	Timestamp   time.Time                 `json:"timestamp"`
	Metrics     *RecoveryMonitorMetrics   `json:"metrics"`
}

// AlertType 告警类型
type AlertType string

const (
	AlertTypeLowSuccessRate AlertType = "low_success_rate"
	AlertTypeHighLatency    AlertType = "high_latency"
	AlertTypeHighErrorRate  AlertType = "high_error_rate"
)

// AlertSeverity 告警严重程度
type AlertSeverity string

const (
	AlertSeverityInfo     AlertSeverity = "info"
	AlertSeverityWarning  AlertSeverity = "warning"
	AlertSeverityError    AlertSeverity = "error"
	AlertSeverityCritical AlertSeverity = "critical"
)

// DefaultRecoveryEventHandler 默认恢复事件处理器
type DefaultRecoveryEventHandler struct{}

// HandleRecoveryEvent 处理恢复事件
func (h *DefaultRecoveryEventHandler) HandleRecoveryEvent(event *RecoveryEvent) error {
	// 记录事件到日志
	logLevel := logrus.InfoLevel
	if !event.Success {
		logLevel = logrus.WarnLevel
	}

	logrus.WithFields(logrus.Fields{
		"type":     event.Type,
		"keyID":    event.KeyID,
		"groupID":  event.GroupID,
		"success":  event.Success,
		"duration": event.Duration,
		"error":    event.Error,
	}).Log(logLevel, "Recovery event processed")

	return nil
}
