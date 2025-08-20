package gemini

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// StatsCollector collects and manages Gemini processing statistics
type StatsCollector struct {
	mutex  sync.RWMutex
	logger *logrus.Logger
	
	// 基础统计
	totalStreams       int64
	successfulStreams  int64
	interruptedStreams int64
	totalRetries       int64
	thoughtsFiltered   int64
	
	// 性能统计
	totalDuration      time.Duration
	totalRetryDuration time.Duration
	
	// 中断原因统计
	interruptionReasons map[InterruptionReason]int64
	
	// 启动时间
	startTime time.Time
}

// NewStatsCollector creates a new statistics collector
func NewStatsCollector(logger *logrus.Logger) *StatsCollector {
	return &StatsCollector{
		logger:              logger,
		interruptionReasons: make(map[InterruptionReason]int64),
		startTime:          time.Now(),
	}
}

// RecordStreamStart records the start of a new stream processing
func (sc *StatsCollector) RecordStreamStart() {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()
	
	sc.totalStreams++
}

// RecordStreamSuccess records a successful stream completion
func (sc *StatsCollector) RecordStreamSuccess(duration time.Duration, retryCount int) {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()
	
	sc.successfulStreams++
	sc.totalDuration += duration
	sc.totalRetries += int64(retryCount)
}

// RecordStreamInterruption records a stream interruption
func (sc *StatsCollector) RecordStreamInterruption(reason InterruptionReason, duration time.Duration, retryCount int) {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()
	
	sc.interruptedStreams++
	sc.totalDuration += duration
	sc.totalRetries += int64(retryCount)
	sc.interruptionReasons[reason]++
}

// RecordRetryAttempt records a single retry attempt
func (sc *StatsCollector) RecordRetryAttempt(duration time.Duration) {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()
	
	sc.totalRetryDuration += duration
}

// RecordThoughtFiltered records that thought content was filtered
func (sc *StatsCollector) RecordThoughtFiltered() {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()
	
	sc.thoughtsFiltered++
}

// GetStats returns current statistics
func (sc *StatsCollector) GetStats() *StreamStats {
	sc.mutex.RLock()
	defer sc.mutex.RUnlock()
	
	var averageRetries float64
	if sc.totalStreams > 0 {
		averageRetries = float64(sc.totalRetries) / float64(sc.totalStreams)
	}
	
	var averageLatency time.Duration
	if sc.totalStreams > 0 {
		averageLatency = sc.totalDuration / time.Duration(sc.totalStreams)
	}
	
	return &StreamStats{
		TotalStreams:       sc.totalStreams,
		SuccessfulStreams:  sc.successfulStreams,
		InterruptedStreams: sc.interruptedStreams,
		TotalRetries:       sc.totalRetries,
		AverageRetries:     averageRetries,
		AverageLatency:     averageLatency,
		ThoughtsFiltered:   sc.thoughtsFiltered,
	}
}

// GetDetailedStats returns detailed statistics including interruption reasons
func (sc *StatsCollector) GetDetailedStats() *DetailedStats {
	sc.mutex.RLock()
	defer sc.mutex.RUnlock()
	
	stats := sc.GetStats()
	
	// 计算成功率
	var successRate float64
	if sc.totalStreams > 0 {
		successRate = float64(sc.successfulStreams) / float64(sc.totalStreams)
	}
	
	// 复制中断原因统计
	interruptionReasons := make(map[InterruptionReason]int64)
	for reason, count := range sc.interruptionReasons {
		interruptionReasons[reason] = count
	}
	
	return &DetailedStats{
		StreamStats:         *stats,
		SuccessRate:         successRate,
		InterruptionReasons: interruptionReasons,
		TotalRetryDuration:  sc.totalRetryDuration,
		UptimeDuration:      time.Since(sc.startTime),
		StartTime:           sc.startTime,
	}
}

// Reset resets all statistics
func (sc *StatsCollector) Reset() {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()
	
	sc.totalStreams = 0
	sc.successfulStreams = 0
	sc.interruptedStreams = 0
	sc.totalRetries = 0
	sc.thoughtsFiltered = 0
	sc.totalDuration = 0
	sc.totalRetryDuration = 0
	sc.interruptionReasons = make(map[InterruptionReason]int64)
	sc.startTime = time.Now()
	
	sc.logger.Info("Gemini statistics reset")
}

// GetHealthStatus returns the current health status
func (sc *StatsCollector) GetHealthStatus() *HealthStatus {
	sc.mutex.RLock()
	defer sc.mutex.RUnlock()
	
	var successRate float64
	if sc.totalStreams > 0 {
		successRate = float64(sc.successfulStreams) / float64(sc.totalStreams)
	}
	
	var averageRetries float64
	if sc.totalStreams > 0 {
		averageRetries = float64(sc.totalRetries) / float64(sc.totalStreams)
	}
	
	// 确定健康状态
	status := "healthy"
	if successRate < 0.8 {
		status = "degraded"
	}
	if successRate < 0.5 {
		status = "unhealthy"
	}
	
	var lastProcessed time.Time
	if sc.totalStreams > 0 {
		// 估算最后处理时间（实际实现中应该记录真实的最后处理时间）
		lastProcessed = time.Now()
	}
	
	return &HealthStatus{
		Status:         status,
		ActiveStreams:  0, // 需要在实际使用中维护活跃流计数
		TotalProcessed: sc.totalStreams,
		SuccessRate:    successRate,
		AverageRetries: averageRetries,
		LastProcessed:  lastProcessed,
		UptimeDuration: time.Since(sc.startTime),
	}
}

// DetailedStats extends StreamStats with additional detailed information
type DetailedStats struct {
	StreamStats
	SuccessRate         float64                            `json:"success_rate"`
	InterruptionReasons map[InterruptionReason]int64       `json:"interruption_reasons"`
	TotalRetryDuration  time.Duration                      `json:"total_retry_duration"`
	UptimeDuration      time.Duration                      `json:"uptime_duration"`
	StartTime           time.Time                          `json:"start_time"`
}

// LogStats logs current statistics at INFO level
func (sc *StatsCollector) LogStats() {
	stats := sc.GetDetailedStats()
	
	sc.logger.WithFields(logrus.Fields{
		"total_streams":       stats.TotalStreams,
		"successful_streams":  stats.SuccessfulStreams,
		"interrupted_streams": stats.InterruptedStreams,
		"success_rate":        stats.SuccessRate,
		"average_retries":     stats.AverageRetries,
		"thoughts_filtered":   stats.ThoughtsFiltered,
		"uptime":             stats.UptimeDuration,
	}).Info("Gemini processing statistics")
}

// LogStatsIfSignificant logs statistics only if there's significant activity
func (sc *StatsCollector) LogStatsIfSignificant() {
	sc.mutex.RLock()
	totalStreams := sc.totalStreams
	sc.mutex.RUnlock()
	
	// 只有在处理了至少10个流时才记录统计
	if totalStreams >= 10 {
		sc.LogStats()
	}
}
