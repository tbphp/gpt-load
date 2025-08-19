package keypool

import (
	"fmt"
	"gpt-load/internal/models"
	"math"
	"sort"
	"time"

	"github.com/sirupsen/logrus"
)

// DynamicRecoveryCalculator 动态恢复时间计算器
type DynamicRecoveryCalculator struct {
	config *CalculatorConfig
}

// CalculatorConfig 计算器配置
type CalculatorConfig struct {
	// 基础参数
	BaseRecoveryTime     time.Duration `json:"base_recovery_time"`     // 基础恢复时间
	MinRecoveryTime      time.Duration `json:"min_recovery_time"`      // 最小恢复时间
	MaxRecoveryTime      time.Duration `json:"max_recovery_time"`      // 最大恢复时间
	
	// 历史分析参数
	HistoryWindowHours   int           `json:"history_window_hours"`   // 历史窗口小时数
	MinHistoryCount      int           `json:"min_history_count"`      // 最小历史记录数
	
	// 频率分析参数
	FrequencyWeight      float64       `json:"frequency_weight"`       // 频率权重
	PatternWeight        float64       `json:"pattern_weight"`         // 模式权重
	TrendWeight          float64       `json:"trend_weight"`           // 趋势权重
	
	// 时间段分析参数
	PeakHourMultiplier   float64       `json:"peak_hour_multiplier"`   // 高峰时段乘数
	OffPeakMultiplier    float64       `json:"off_peak_multiplier"`    // 非高峰时段乘数
	WeekendMultiplier    float64       `json:"weekend_multiplier"`     // 周末乘数
	
	// 自适应参数
	LearningRate         float64       `json:"learning_rate"`          // 学习率
	AdaptationThreshold  int           `json:"adaptation_threshold"`   // 自适应阈值
	
	// 安全参数
	SafetyMargin         float64       `json:"safety_margin"`          // 安全边际
	ConservativeMode     bool          `json:"conservative_mode"`      // 保守模式
}

// DefaultCalculatorConfig 返回默认计算器配置
func DefaultCalculatorConfig() *CalculatorConfig {
	return &CalculatorConfig{
		BaseRecoveryTime:     15 * time.Minute,
		MinRecoveryTime:      5 * time.Minute,
		MaxRecoveryTime:      4 * time.Hour,
		HistoryWindowHours:   72, // 3天
		MinHistoryCount:      3,
		FrequencyWeight:      0.4,
		PatternWeight:        0.3,
		TrendWeight:          0.3,
		PeakHourMultiplier:   1.5,
		OffPeakMultiplier:    0.8,
		WeekendMultiplier:    0.9,
		LearningRate:         0.1,
		AdaptationThreshold:  10,
		SafetyMargin:         1.2,
		ConservativeMode:     true,
	}
}

// NewDynamicRecoveryCalculator 创建动态恢复时间计算器
func NewDynamicRecoveryCalculator(config *CalculatorConfig) *DynamicRecoveryCalculator {
	if config == nil {
		config = DefaultCalculatorConfig()
	}
	
	return &DynamicRecoveryCalculator{
		config: config,
	}
}

// CalculateOptimalRecoveryTime 计算最优恢复时间
func (c *DynamicRecoveryCalculator) CalculateOptimalRecoveryTime(
	key *models.APIKey,
	history []*RateLimitRecord,
	currentTime time.Time,
) time.Duration {
	
	// 基础恢复时间
	baseTime := c.config.BaseRecoveryTime
	
	// 如果历史记录不足，使用基础时间
	if len(history) < c.config.MinHistoryCount {
		return c.applyTimeModifiers(baseTime, currentTime)
	}
	
	// 过滤相关历史记录
	relevantHistory := c.filterRelevantHistory(history, currentTime)
	if len(relevantHistory) < c.config.MinHistoryCount {
		return c.applyTimeModifiers(baseTime, currentTime)
	}
	
	// 计算各种因子
	frequencyFactor := c.calculateFrequencyFactor(relevantHistory, currentTime)
	patternFactor := c.calculatePatternFactor(relevantHistory)
	trendFactor := c.calculateTrendFactor(relevantHistory)
	
	// 加权计算
	adjustmentFactor := c.config.FrequencyWeight*frequencyFactor +
		c.config.PatternWeight*patternFactor +
		c.config.TrendWeight*trendFactor
	
	// 应用调整因子
	adjustedTime := time.Duration(float64(baseTime) * adjustmentFactor)
	
	// 应用安全边际
	if c.config.ConservativeMode {
		adjustedTime = time.Duration(float64(adjustedTime) * c.config.SafetyMargin)
	}
	
	// 应用时间修饰符
	finalTime := c.applyTimeModifiers(adjustedTime, currentTime)
	
	// 限制在合理范围内
	if finalTime < c.config.MinRecoveryTime {
		finalTime = c.config.MinRecoveryTime
	}
	if finalTime > c.config.MaxRecoveryTime {
		finalTime = c.config.MaxRecoveryTime
	}
	
	logrus.WithFields(logrus.Fields{
		"keyID":            key.ID,
		"baseTime":         baseTime,
		"frequencyFactor":  frequencyFactor,
		"patternFactor":    patternFactor,
		"trendFactor":      trendFactor,
		"adjustmentFactor": adjustmentFactor,
		"finalTime":        finalTime,
	}).Debug("Calculated optimal recovery time")
	
	return finalTime
}

// filterRelevantHistory 过滤相关历史记录
func (c *DynamicRecoveryCalculator) filterRelevantHistory(
	history []*RateLimitRecord,
	currentTime time.Time,
) []*RateLimitRecord {
	
	cutoff := currentTime.Add(-time.Duration(c.config.HistoryWindowHours) * time.Hour)
	var relevant []*RateLimitRecord
	
	for _, record := range history {
		if record.OccurredAt.After(cutoff) {
			relevant = append(relevant, record)
		}
	}
	
	// 按时间排序
	sort.Slice(relevant, func(i, j int) bool {
		return relevant[i].OccurredAt.Before(relevant[j].OccurredAt)
	})
	
	return relevant
}

// calculateFrequencyFactor 计算频率因子
func (c *DynamicRecoveryCalculator) calculateFrequencyFactor(
	history []*RateLimitRecord,
	currentTime time.Time,
) float64 {
	
	if len(history) == 0 {
		return 1.0
	}
	
	// 计算不同时间窗口的频率
	hour1 := c.countInWindow(history, currentTime, 1*time.Hour)
	hour6 := c.countInWindow(history, currentTime, 6*time.Hour)
	hour24 := c.countInWindow(history, currentTime, 24*time.Hour)
	
	// 根据频率计算因子
	var factor float64
	
	if hour1 >= 3 {
		factor = 3.0 // 1小时内3次以上，大幅延长
	} else if hour6 >= 5 {
		factor = 2.5 // 6小时内5次以上，显著延长
	} else if hour24 >= 10 {
		factor = 2.0 // 24小时内10次以上，延长
	} else if hour24 >= 5 {
		factor = 1.5 // 24小时内5次以上，稍微延长
	} else if hour24 <= 1 {
		factor = 0.8 // 24小时内1次以下，缩短
	} else {
		factor = 1.0 // 正常频率
	}
	
	return factor
}

// calculatePatternFactor 计算模式因子
func (c *DynamicRecoveryCalculator) calculatePatternFactor(history []*RateLimitRecord) float64 {
	if len(history) < 3 {
		return 1.0
	}
	
	// 分析时间间隔模式
	intervals := make([]time.Duration, len(history)-1)
	for i := 1; i < len(history); i++ {
		intervals[i-1] = history[i].OccurredAt.Sub(history[i-1].OccurredAt)
	}
	
	// 计算间隔的标准差
	avgInterval := c.calculateAverageInterval(intervals)
	variance := c.calculateVariance(intervals, avgInterval)
	stdDev := time.Duration(math.Sqrt(float64(variance)))
	
	// 如果间隔很规律（标准差小），说明可能是系统性问题，需要更长恢复时间
	if stdDev < avgInterval/4 {
		return 1.8 // 高度规律，延长恢复时间
	} else if stdDev < avgInterval/2 {
		return 1.4 // 中度规律，稍微延长
	} else {
		return 1.0 // 随机模式，正常恢复时间
	}
}

// calculateTrendFactor 计算趋势因子
func (c *DynamicRecoveryCalculator) calculateTrendFactor(history []*RateLimitRecord) float64 {
	if len(history) < 4 {
		return 1.0
	}
	
	// 分析最近的趋势
	recentCount := len(history)
	if recentCount > 10 {
		recentCount = 10 // 只看最近10次
	}
	
	recent := history[len(history)-recentCount:]
	
	// 计算时间间隔的趋势
	intervals := make([]float64, len(recent)-1)
	for i := 1; i < len(recent); i++ {
		interval := recent[i].OccurredAt.Sub(recent[i-1].OccurredAt)
		intervals[i-1] = float64(interval)
	}
	
	// 简单线性回归计算趋势
	trend := c.calculateLinearTrend(intervals)
	
	if trend < -0.5 {
		return 1.6 // 间隔在缩短，频率在增加，延长恢复时间
	} else if trend < -0.2 {
		return 1.3 // 轻微增加趋势
	} else if trend > 0.2 {
		return 0.8 // 间隔在增长，频率在减少，缩短恢复时间
	} else {
		return 1.0 // 趋势平稳
	}
}

// applyTimeModifiers 应用时间修饰符
func (c *DynamicRecoveryCalculator) applyTimeModifiers(
	baseTime time.Duration,
	currentTime time.Time,
) time.Duration {
	
	modifier := 1.0
	
	// 应用时段修饰符
	hour := currentTime.Hour()
	if c.isPeakHour(hour) {
		modifier *= c.config.PeakHourMultiplier
	} else {
		modifier *= c.config.OffPeakMultiplier
	}
	
	// 应用周末修饰符
	if c.isWeekend(currentTime) {
		modifier *= c.config.WeekendMultiplier
	}
	
	return time.Duration(float64(baseTime) * modifier)
}

// isPeakHour 判断是否为高峰时段
func (c *DynamicRecoveryCalculator) isPeakHour(hour int) bool {
	// 定义高峰时段：9-11点，14-16点，19-21点
	return (hour >= 9 && hour <= 11) ||
		(hour >= 14 && hour <= 16) ||
		(hour >= 19 && hour <= 21)
}

// isWeekend 判断是否为周末
func (c *DynamicRecoveryCalculator) isWeekend(t time.Time) bool {
	weekday := t.Weekday()
	return weekday == time.Saturday || weekday == time.Sunday
}

// countInWindow 计算时间窗口内的记录数
func (c *DynamicRecoveryCalculator) countInWindow(
	history []*RateLimitRecord,
	currentTime time.Time,
	window time.Duration,
) int {
	
	cutoff := currentTime.Add(-window)
	count := 0
	
	for _, record := range history {
		if record.OccurredAt.After(cutoff) {
			count++
		}
	}
	
	return count
}

// calculateAverageInterval 计算平均间隔
func (c *DynamicRecoveryCalculator) calculateAverageInterval(intervals []time.Duration) time.Duration {
	if len(intervals) == 0 {
		return 0
	}
	
	var total time.Duration
	for _, interval := range intervals {
		total += interval
	}
	
	return total / time.Duration(len(intervals))
}

// calculateVariance 计算方差
func (c *DynamicRecoveryCalculator) calculateVariance(
	intervals []time.Duration,
	avg time.Duration,
) int64 {
	if len(intervals) == 0 {
		return 0
	}
	
	var sum int64
	for _, interval := range intervals {
		diff := int64(interval - avg)
		sum += diff * diff
	}
	
	return sum / int64(len(intervals))
}

// calculateLinearTrend 计算线性趋势
func (c *DynamicRecoveryCalculator) calculateLinearTrend(values []float64) float64 {
	n := len(values)
	if n < 2 {
		return 0
	}
	
	// 简单线性回归
	var sumX, sumY, sumXY, sumX2 float64
	
	for i, y := range values {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}
	
	// 计算斜率
	denominator := float64(n)*sumX2 - sumX*sumX
	if denominator == 0 {
		return 0
	}
	
	slope := (float64(n)*sumXY - sumX*sumY) / denominator
	
	// 归一化斜率
	if sumY != 0 {
		return slope / (sumY / float64(n))
	}
	
	return 0
}

// PredictNextRateLimit 预测下次429发生时间
func (c *DynamicRecoveryCalculator) PredictNextRateLimit(
	history []*RateLimitRecord,
	currentTime time.Time,
) (time.Time, float64) {
	
	if len(history) < 3 {
		return time.Time{}, 0.0 // 无法预测
	}
	
	relevantHistory := c.filterRelevantHistory(history, currentTime)
	if len(relevantHistory) < 3 {
		return time.Time{}, 0.0
	}
	
	// 计算平均间隔
	intervals := make([]time.Duration, len(relevantHistory)-1)
	for i := 1; i < len(relevantHistory); i++ {
		intervals[i-1] = relevantHistory[i].OccurredAt.Sub(relevantHistory[i-1].OccurredAt)
	}
	
	avgInterval := c.calculateAverageInterval(intervals)
	
	// 计算预测的置信度
	variance := c.calculateVariance(intervals, avgInterval)
	stdDev := time.Duration(math.Sqrt(float64(variance)))
	
	// 置信度基于标准差
	confidence := 1.0 / (1.0 + float64(stdDev)/float64(avgInterval))
	
	// 预测下次发生时间
	lastTime := relevantHistory[len(relevantHistory)-1].OccurredAt
	predictedTime := lastTime.Add(avgInterval)
	
	return predictedTime, confidence
}

// GetRecoveryRecommendation 获取恢复建议
func (c *DynamicRecoveryCalculator) GetRecoveryRecommendation(
	key *models.APIKey,
	history []*RateLimitRecord,
	currentTime time.Time,
) *RecoveryRecommendation {
	
	optimalTime := c.CalculateOptimalRecoveryTime(key, history, currentTime)
	nextPrediction, confidence := c.PredictNextRateLimit(history, currentTime)
	
	recommendation := &RecoveryRecommendation{
		KeyID:                key.ID,
		OptimalRecoveryTime:  optimalTime,
		RecommendedAt:        currentTime.Add(optimalTime),
		Confidence:           confidence,
		RiskLevel:           c.assessRiskLevel(history, currentTime),
		Reasoning:           c.generateReasoning(key, history, optimalTime),
	}
	
	if !nextPrediction.IsZero() {
		recommendation.NextPredictedRateLimit = &nextPrediction
	}
	
	return recommendation
}

// RecoveryRecommendation 恢复建议
type RecoveryRecommendation struct {
	KeyID                   uint       `json:"key_id"`
	OptimalRecoveryTime     time.Duration `json:"optimal_recovery_time"`
	RecommendedAt           time.Time  `json:"recommended_at"`
	Confidence              float64    `json:"confidence"`
	RiskLevel              string     `json:"risk_level"`
	Reasoning              []string   `json:"reasoning"`
	NextPredictedRateLimit *time.Time `json:"next_predicted_rate_limit,omitempty"`
}

// assessRiskLevel 评估风险级别
func (c *DynamicRecoveryCalculator) assessRiskLevel(
	history []*RateLimitRecord,
	currentTime time.Time,
) string {
	
	hour1Count := c.countInWindow(history, currentTime, 1*time.Hour)
	hour24Count := c.countInWindow(history, currentTime, 24*time.Hour)
	
	if hour1Count >= 3 {
		return "HIGH"
	} else if hour24Count >= 10 {
		return "MEDIUM"
	} else if hour24Count >= 3 {
		return "LOW"
	} else {
		return "MINIMAL"
	}
}

// generateReasoning 生成推理说明
func (c *DynamicRecoveryCalculator) generateReasoning(
	key *models.APIKey,
	history []*RateLimitRecord,
	optimalTime time.Duration,
) []string {
	
	var reasoning []string
	
	if len(history) < c.config.MinHistoryCount {
		reasoning = append(reasoning, "使用基础恢复时间（历史记录不足）")
	} else {
		reasoning = append(reasoning, fmt.Sprintf("基于%d条历史记录分析", len(history)))
	}
	
	if optimalTime > c.config.BaseRecoveryTime {
		reasoning = append(reasoning, "由于高频率429错误，延长恢复时间")
	} else if optimalTime < c.config.BaseRecoveryTime {
		reasoning = append(reasoning, "由于低频率429错误，缩短恢复时间")
	}
	
	if c.config.ConservativeMode {
		reasoning = append(reasoning, "应用保守模式安全边际")
	}
	
	return reasoning
}
