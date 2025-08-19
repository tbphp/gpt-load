package keypool

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

// PerformanceMonitor 性能监控器
type PerformanceMonitor struct {
	// 配置
	config *PerformanceConfig
	
	// 运行时状态
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	
	// 指标数据
	metrics *PerformanceMetrics
	mu      sync.RWMutex
	
	// 计数器
	counters *PerformanceCounters
	
	// 时间序列数据
	timeSeries *TimeSeriesData
	
	// 事件处理器
	eventHandlers []PerformanceEventHandler
}

// PerformanceConfig 性能监控配置
type PerformanceConfig struct {
	// 基础配置
	CollectionInterval time.Duration `json:"collection_interval"` // 数据收集间隔
	RetentionPeriod    time.Duration `json:"retention_period"`    // 数据保留期
	
	// 指标配置
	EnableDetailedMetrics bool `json:"enable_detailed_metrics"` // 启用详细指标
	EnableLatencyTracking bool `json:"enable_latency_tracking"` // 启用延迟跟踪
	
	// 告警配置
	EnableAlerts         bool    `json:"enable_alerts"`          // 启用告警
	ThroughputThreshold  float64 `json:"throughput_threshold"`   // 吞吐量告警阈值
	LatencyThreshold     int64   `json:"latency_threshold"`      // 延迟告警阈值(ms)
	ErrorRateThreshold   float64 `json:"error_rate_threshold"`   // 错误率告警阈值
	CacheHitRateThreshold float64 `json:"cache_hit_rate_threshold"` // 缓存命中率告警阈值
}

// PerformanceMetrics 性能指标
type PerformanceMetrics struct {
	// 基础指标
	TotalRequests    int64   `json:"total_requests"`
	SuccessfulReqs   int64   `json:"successful_requests"`
	FailedReqs       int64   `json:"failed_requests"`
	Throughput       float64 `json:"throughput"`        // req/s
	ErrorRate        float64 `json:"error_rate"`
	
	// 延迟指标
	AvgLatency       int64 `json:"avg_latency"`        // ms
	P50Latency       int64 `json:"p50_latency"`        // ms
	P95Latency       int64 `json:"p95_latency"`        // ms
	P99Latency       int64 `json:"p99_latency"`        // ms
	MaxLatency       int64 `json:"max_latency"`        // ms
	
	// 池指标
	PoolUtilization  float64 `json:"pool_utilization"`  // 池利用率
	CacheHitRate     float64 `json:"cache_hit_rate"`    // 缓存命中率
	CacheMissRate    float64 `json:"cache_miss_rate"`   // 缓存未命中率
	
	// 429相关指标
	RateLimitCount   int64   `json:"rate_limit_count"`  // 429错误次数
	RateLimitRate    float64 `json:"rate_limit_rate"`   // 429错误率
	RecoveryCount    int64   `json:"recovery_count"`    // 恢复次数
	RecoveryRate     float64 `json:"recovery_rate"`     // 恢复成功率
	
	// 时间戳
	LastUpdated      time.Time `json:"last_updated"`
}

// PerformanceCounters 性能计数器
type PerformanceCounters struct {
	// 请求计数
	TotalRequests    int64
	SuccessfulReqs   int64
	FailedReqs       int64
	
	// 缓存计数
	CacheHits        int64
	CacheMisses      int64
	
	// 429计数
	RateLimitErrors  int64
	RecoveryAttempts int64
	RecoverySuccess  int64
	
	// 延迟统计
	LatencySum       int64
	LatencyCount     int64
	MaxLatency       int64
}

// TimeSeriesData 时间序列数据
type TimeSeriesData struct {
	// 数据点
	ThroughputSeries []DataPoint `json:"throughput_series"`
	LatencySeries    []DataPoint `json:"latency_series"`
	ErrorRateSeries  []DataPoint `json:"error_rate_series"`
	CacheHitSeries   []DataPoint `json:"cache_hit_series"`
	
	// 数据保留
	MaxDataPoints    int           `json:"max_data_points"`
	DataInterval     time.Duration `json:"data_interval"`
}

// DataPoint 数据点
type DataPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// PerformanceEventHandler 性能事件处理器
type PerformanceEventHandler interface {
	HandlePerformanceEvent(event *PerformanceEvent) error
}

// PerformanceEvent 性能事件
type PerformanceEvent struct {
	Type      PerformanceEventType `json:"type"`
	Timestamp time.Time            `json:"timestamp"`
	Data      map[string]interface{} `json:"data"`
}

// PerformanceEventType 性能事件类型
type PerformanceEventType string

const (
	EventTypeHighLatency     PerformanceEventType = "high_latency"
	EventTypeLowThroughput   PerformanceEventType = "low_throughput"
	EventTypeHighErrorRate   PerformanceEventType = "high_error_rate"
	EventTypeLowCacheHitRate PerformanceEventType = "low_cache_hit_rate"
	EventTypePoolExhaustion  PerformanceEventType = "pool_exhaustion"
)

// DefaultPerformanceConfig 返回默认性能监控配置
func DefaultPerformanceConfig() *PerformanceConfig {
	return &PerformanceConfig{
		CollectionInterval:    10 * time.Second,
		RetentionPeriod:       24 * time.Hour,
		EnableDetailedMetrics: true,
		EnableLatencyTracking: true,
		EnableAlerts:          true,
		ThroughputThreshold:   100.0,  // 100 req/s
		LatencyThreshold:      1000,   // 1000ms
		ErrorRateThreshold:    0.05,   // 5%
		CacheHitRateThreshold: 0.8,    // 80%
	}
}

// NewPerformanceMonitor 创建性能监控器
func NewPerformanceMonitor(config *PerformanceConfig) *PerformanceMonitor {
	if config == nil {
		config = DefaultPerformanceConfig()
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	
	monitor := &PerformanceMonitor{
		config:    config,
		ctx:       ctx,
		cancel:    cancel,
		metrics:   &PerformanceMetrics{},
		counters:  &PerformanceCounters{},
		timeSeries: &TimeSeriesData{
			ThroughputSeries: make([]DataPoint, 0),
			LatencySeries:    make([]DataPoint, 0),
			ErrorRateSeries:  make([]DataPoint, 0),
			CacheHitSeries:   make([]DataPoint, 0),
			MaxDataPoints:    1000,
			DataInterval:     config.CollectionInterval,
		},
		eventHandlers: make([]PerformanceEventHandler, 0),
	}
	
	return monitor
}

// Start 启动性能监控
func (pm *PerformanceMonitor) Start() error {
	// 启动数据收集循环
	pm.wg.Add(1)
	go pm.collectionLoop()
	
	// 启动数据清理循环
	pm.wg.Add(1)
	go pm.cleanupLoop()
	
	logrus.Info("Performance monitor started")
	return nil
}

// Stop 停止性能监控
func (pm *PerformanceMonitor) Stop() error {
	pm.cancel()
	pm.wg.Wait()
	
	logrus.Info("Performance monitor stopped")
	return nil
}

// RecordRequest 记录请求
func (pm *PerformanceMonitor) RecordRequest(success bool, latency time.Duration) {
	atomic.AddInt64(&pm.counters.TotalRequests, 1)
	
	if success {
		atomic.AddInt64(&pm.counters.SuccessfulReqs, 1)
	} else {
		atomic.AddInt64(&pm.counters.FailedReqs, 1)
	}
	
	if pm.config.EnableLatencyTracking {
		latencyMs := latency.Milliseconds()
		atomic.AddInt64(&pm.counters.LatencySum, latencyMs)
		atomic.AddInt64(&pm.counters.LatencyCount, 1)
		
		// 更新最大延迟
		for {
			current := atomic.LoadInt64(&pm.counters.MaxLatency)
			if latencyMs <= current {
				break
			}
			if atomic.CompareAndSwapInt64(&pm.counters.MaxLatency, current, latencyMs) {
				break
			}
		}
	}
}

// RecordCacheHit 记录缓存命中
func (pm *PerformanceMonitor) RecordCacheHit() {
	atomic.AddInt64(&pm.counters.CacheHits, 1)
}

// RecordCacheMiss 记录缓存未命中
func (pm *PerformanceMonitor) RecordCacheMiss() {
	atomic.AddInt64(&pm.counters.CacheMisses, 1)
}

// RecordRateLimit 记录429错误
func (pm *PerformanceMonitor) RecordRateLimit() {
	atomic.AddInt64(&pm.counters.RateLimitErrors, 1)
}

// RecordRecovery 记录恢复操作
func (pm *PerformanceMonitor) RecordRecovery(success bool) {
	atomic.AddInt64(&pm.counters.RecoveryAttempts, 1)
	if success {
		atomic.AddInt64(&pm.counters.RecoverySuccess, 1)
	}
}

// collectionLoop 数据收集循环
func (pm *PerformanceMonitor) collectionLoop() {
	defer pm.wg.Done()
	
	ticker := time.NewTicker(pm.config.CollectionInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-pm.ctx.Done():
			return
		case <-ticker.C:
			pm.collectMetrics()
		}
	}
}

// collectMetrics 收集指标
func (pm *PerformanceMonitor) collectMetrics() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	now := time.Now()
	
	// 读取计数器
	totalReqs := atomic.LoadInt64(&pm.counters.TotalRequests)
	successReqs := atomic.LoadInt64(&pm.counters.SuccessfulReqs)
	failedReqs := atomic.LoadInt64(&pm.counters.FailedReqs)
	cacheHits := atomic.LoadInt64(&pm.counters.CacheHits)
	cacheMisses := atomic.LoadInt64(&pm.counters.CacheMisses)
	rateLimitErrors := atomic.LoadInt64(&pm.counters.RateLimitErrors)
	recoveryAttempts := atomic.LoadInt64(&pm.counters.RecoveryAttempts)
	recoverySuccess := atomic.LoadInt64(&pm.counters.RecoverySuccess)
	
	// 计算速率（基于上次收集的差值）
	timeDiff := now.Sub(pm.metrics.LastUpdated).Seconds()
	if timeDiff > 0 {
		reqDiff := totalReqs - pm.metrics.TotalRequests
		pm.metrics.Throughput = float64(reqDiff) / timeDiff
	}
	
	// 更新基础指标
	pm.metrics.TotalRequests = totalReqs
	pm.metrics.SuccessfulReqs = successReqs
	pm.metrics.FailedReqs = failedReqs
	
	// 计算错误率
	if totalReqs > 0 {
		pm.metrics.ErrorRate = float64(failedReqs) / float64(totalReqs)
		pm.metrics.RateLimitRate = float64(rateLimitErrors) / float64(totalReqs)
	}
	
	// 计算缓存命中率
	totalCacheOps := cacheHits + cacheMisses
	if totalCacheOps > 0 {
		pm.metrics.CacheHitRate = float64(cacheHits) / float64(totalCacheOps)
		pm.metrics.CacheMissRate = float64(cacheMisses) / float64(totalCacheOps)
	}
	
	// 计算延迟指标
	if pm.config.EnableLatencyTracking {
		latencyCount := atomic.LoadInt64(&pm.counters.LatencyCount)
		if latencyCount > 0 {
			latencySum := atomic.LoadInt64(&pm.counters.LatencySum)
			pm.metrics.AvgLatency = latencySum / latencyCount
			pm.metrics.MaxLatency = atomic.LoadInt64(&pm.counters.MaxLatency)
			
			// 简化的百分位计算（实际应用中应使用更精确的算法）
			pm.metrics.P50Latency = pm.metrics.AvgLatency
			pm.metrics.P95Latency = int64(float64(pm.metrics.AvgLatency) * 1.5)
			pm.metrics.P99Latency = int64(float64(pm.metrics.AvgLatency) * 2.0)
		}
	}
	
	// 计算恢复率
	if recoveryAttempts > 0 {
		pm.metrics.RecoveryRate = float64(recoverySuccess) / float64(recoveryAttempts)
	}
	
	pm.metrics.RateLimitCount = rateLimitErrors
	pm.metrics.RecoveryCount = recoverySuccess
	pm.metrics.LastUpdated = now
	
	// 添加到时间序列
	pm.addToTimeSeries(now)
	
	// 检查告警条件
	if pm.config.EnableAlerts {
		pm.checkAlerts()
	}
}

// addToTimeSeries 添加到时间序列
func (pm *PerformanceMonitor) addToTimeSeries(timestamp time.Time) {
	// 添加吞吐量数据点
	pm.timeSeries.ThroughputSeries = append(pm.timeSeries.ThroughputSeries, DataPoint{
		Timestamp: timestamp,
		Value:     pm.metrics.Throughput,
	})
	
	// 添加延迟数据点
	pm.timeSeries.LatencySeries = append(pm.timeSeries.LatencySeries, DataPoint{
		Timestamp: timestamp,
		Value:     float64(pm.metrics.AvgLatency),
	})
	
	// 添加错误率数据点
	pm.timeSeries.ErrorRateSeries = append(pm.timeSeries.ErrorRateSeries, DataPoint{
		Timestamp: timestamp,
		Value:     pm.metrics.ErrorRate,
	})
	
	// 添加缓存命中率数据点
	pm.timeSeries.CacheHitSeries = append(pm.timeSeries.CacheHitSeries, DataPoint{
		Timestamp: timestamp,
		Value:     pm.metrics.CacheHitRate,
	})
	
	// 限制数据点数量
	pm.limitTimeSeriesSize()
}

// limitTimeSeriesSize 限制时间序列大小
func (pm *PerformanceMonitor) limitTimeSeriesSize() {
	maxPoints := pm.timeSeries.MaxDataPoints
	
	if len(pm.timeSeries.ThroughputSeries) > maxPoints {
		pm.timeSeries.ThroughputSeries = pm.timeSeries.ThroughputSeries[len(pm.timeSeries.ThroughputSeries)-maxPoints:]
	}
	
	if len(pm.timeSeries.LatencySeries) > maxPoints {
		pm.timeSeries.LatencySeries = pm.timeSeries.LatencySeries[len(pm.timeSeries.LatencySeries)-maxPoints:]
	}
	
	if len(pm.timeSeries.ErrorRateSeries) > maxPoints {
		pm.timeSeries.ErrorRateSeries = pm.timeSeries.ErrorRateSeries[len(pm.timeSeries.ErrorRateSeries)-maxPoints:]
	}
	
	if len(pm.timeSeries.CacheHitSeries) > maxPoints {
		pm.timeSeries.CacheHitSeries = pm.timeSeries.CacheHitSeries[len(pm.timeSeries.CacheHitSeries)-maxPoints:]
	}
}

// checkAlerts 检查告警条件
func (pm *PerformanceMonitor) checkAlerts() {
	// 检查吞吐量告警
	if pm.metrics.Throughput < pm.config.ThroughputThreshold {
		pm.triggerEvent(EventTypeLowThroughput, map[string]interface{}{
			"current_throughput": pm.metrics.Throughput,
			"threshold":          pm.config.ThroughputThreshold,
		})
	}
	
	// 检查延迟告警
	if pm.metrics.AvgLatency > pm.config.LatencyThreshold {
		pm.triggerEvent(EventTypeHighLatency, map[string]interface{}{
			"current_latency": pm.metrics.AvgLatency,
			"threshold":       pm.config.LatencyThreshold,
		})
	}
	
	// 检查错误率告警
	if pm.metrics.ErrorRate > pm.config.ErrorRateThreshold {
		pm.triggerEvent(EventTypeHighErrorRate, map[string]interface{}{
			"current_error_rate": pm.metrics.ErrorRate,
			"threshold":          pm.config.ErrorRateThreshold,
		})
	}
	
	// 检查缓存命中率告警
	if pm.metrics.CacheHitRate < pm.config.CacheHitRateThreshold {
		pm.triggerEvent(EventTypeLowCacheHitRate, map[string]interface{}{
			"current_cache_hit_rate": pm.metrics.CacheHitRate,
			"threshold":              pm.config.CacheHitRateThreshold,
		})
	}
}

// triggerEvent 触发事件
func (pm *PerformanceMonitor) triggerEvent(eventType PerformanceEventType, data map[string]interface{}) {
	event := &PerformanceEvent{
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      data,
	}
	
	for _, handler := range pm.eventHandlers {
		if err := handler.HandlePerformanceEvent(event); err != nil {
			logrus.WithError(err).Warn("Performance event handler failed")
		}
	}
}

// cleanupLoop 数据清理循环
func (pm *PerformanceMonitor) cleanupLoop() {
	defer pm.wg.Done()
	
	ticker := time.NewTicker(1 * time.Hour) // 每小时清理一次
	defer ticker.Stop()
	
	for {
		select {
		case <-pm.ctx.Done():
			return
		case <-ticker.C:
			pm.cleanupOldData()
		}
	}
}

// cleanupOldData 清理过期数据
func (pm *PerformanceMonitor) cleanupOldData() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	cutoff := time.Now().Add(-pm.config.RetentionPeriod)
	
	// 清理时间序列数据
	pm.timeSeries.ThroughputSeries = pm.filterDataPoints(pm.timeSeries.ThroughputSeries, cutoff)
	pm.timeSeries.LatencySeries = pm.filterDataPoints(pm.timeSeries.LatencySeries, cutoff)
	pm.timeSeries.ErrorRateSeries = pm.filterDataPoints(pm.timeSeries.ErrorRateSeries, cutoff)
	pm.timeSeries.CacheHitSeries = pm.filterDataPoints(pm.timeSeries.CacheHitSeries, cutoff)
}

// filterDataPoints 过滤数据点
func (pm *PerformanceMonitor) filterDataPoints(dataPoints []DataPoint, cutoff time.Time) []DataPoint {
	var filtered []DataPoint
	for _, point := range dataPoints {
		if point.Timestamp.After(cutoff) {
			filtered = append(filtered, point)
		}
	}
	return filtered
}

// GetMetrics 获取性能指标
func (pm *PerformanceMonitor) GetMetrics() *PerformanceMetrics {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	// 返回指标的副本
	metrics := *pm.metrics
	return &metrics
}

// GetTimeSeries 获取时间序列数据
func (pm *PerformanceMonitor) GetTimeSeries() *TimeSeriesData {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	// 返回时间序列的副本
	timeSeries := &TimeSeriesData{
		ThroughputSeries: make([]DataPoint, len(pm.timeSeries.ThroughputSeries)),
		LatencySeries:    make([]DataPoint, len(pm.timeSeries.LatencySeries)),
		ErrorRateSeries:  make([]DataPoint, len(pm.timeSeries.ErrorRateSeries)),
		CacheHitSeries:   make([]DataPoint, len(pm.timeSeries.CacheHitSeries)),
		MaxDataPoints:    pm.timeSeries.MaxDataPoints,
		DataInterval:     pm.timeSeries.DataInterval,
	}
	
	copy(timeSeries.ThroughputSeries, pm.timeSeries.ThroughputSeries)
	copy(timeSeries.LatencySeries, pm.timeSeries.LatencySeries)
	copy(timeSeries.ErrorRateSeries, pm.timeSeries.ErrorRateSeries)
	copy(timeSeries.CacheHitSeries, pm.timeSeries.CacheHitSeries)
	
	return timeSeries
}

// AddEventHandler 添加事件处理器
func (pm *PerformanceMonitor) AddEventHandler(handler PerformanceEventHandler) {
	pm.eventHandlers = append(pm.eventHandlers, handler)
}

// ResetCounters 重置计数器
func (pm *PerformanceMonitor) ResetCounters() {
	atomic.StoreInt64(&pm.counters.TotalRequests, 0)
	atomic.StoreInt64(&pm.counters.SuccessfulReqs, 0)
	atomic.StoreInt64(&pm.counters.FailedReqs, 0)
	atomic.StoreInt64(&pm.counters.CacheHits, 0)
	atomic.StoreInt64(&pm.counters.CacheMisses, 0)
	atomic.StoreInt64(&pm.counters.RateLimitErrors, 0)
	atomic.StoreInt64(&pm.counters.RecoveryAttempts, 0)
	atomic.StoreInt64(&pm.counters.RecoverySuccess, 0)
	atomic.StoreInt64(&pm.counters.LatencySum, 0)
	atomic.StoreInt64(&pm.counters.LatencyCount, 0)
	atomic.StoreInt64(&pm.counters.MaxLatency, 0)
	
	logrus.Info("Performance counters reset")
}
