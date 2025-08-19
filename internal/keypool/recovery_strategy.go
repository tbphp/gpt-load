package keypool

import (
	"fmt"
	"gpt-load/internal/models"
	"math"
	"time"

	"github.com/sirupsen/logrus"
)

// RecoveryStrategy 定义429恢复策略接口
type RecoveryStrategy interface {
	CalculateRecoveryTime(key *models.APIKey, rateLimitHistory []*RateLimitRecord) time.Time
	ShouldAttemptRecovery(key *models.APIKey, now time.Time) bool
	GetRecoveryPriority(key *models.APIKey) RecoveryPriority
	ValidateRecoveryConditions(key *models.APIKey, group *models.Group) error
}

// RateLimitRecord 429错误记录
type RateLimitRecord struct {
	KeyID       uint      `json:"key_id"`
	GroupID     uint      `json:"group_id"`
	OccurredAt  time.Time `json:"occurred_at"`
	ResetAt     time.Time `json:"reset_at"`
	RetryAfter  time.Duration `json:"retry_after"`
	RequestPath string    `json:"request_path"`
	ErrorCode   string    `json:"error_code"`
	Severity    RateLimitSeverity `json:"severity"`
}

// RateLimitSeverity 429错误严重程度
type RateLimitSeverity string

const (
	SeverityLow    RateLimitSeverity = "low"    // 轻微限流
	SeverityMedium RateLimitSeverity = "medium" // 中等限流
	SeverityHigh   RateLimitSeverity = "high"   // 严重限流
	SeverityCritical RateLimitSeverity = "critical" // 关键限流
)

// RecoveryPriority 恢复优先级
type RecoveryPriority int

const (
	PriorityLow RecoveryPriority = iota
	PriorityNormal
	PriorityHigh
	PriorityCritical
)

// SmartRecoveryStrategy 智能恢复策略实现
type SmartRecoveryStrategy struct {
	config *RecoveryConfig
}

// RecoveryConfig 恢复配置
type RecoveryConfig struct {
	// 基础配置
	MinRecoveryInterval    time.Duration `json:"min_recovery_interval"`    // 最小恢复间隔
	MaxRecoveryInterval    time.Duration `json:"max_recovery_interval"`    // 最大恢复间隔
	DefaultRecoveryDelay   time.Duration `json:"default_recovery_delay"`   // 默认恢复延迟
	
	// 动态调整参数
	BackoffMultiplier      float64       `json:"backoff_multiplier"`       // 退避乘数
	MaxBackoffAttempts     int           `json:"max_backoff_attempts"`     // 最大退避次数
	RecoverySuccessBonus   float64       `json:"recovery_success_bonus"`   // 成功恢复奖励
	
	// 频率控制
	MaxRecoveryAttempts    int           `json:"max_recovery_attempts"`    // 最大恢复尝试次数
	RecoveryWindowHours    int           `json:"recovery_window_hours"`    // 恢复窗口小时数
	
	// 优先级配置
	HighPriorityThreshold  int           `json:"high_priority_threshold"`  // 高优先级阈值
	CriticalPriorityThreshold int        `json:"critical_priority_threshold"` // 关键优先级阈值
	
	// 安全配置
	EnableSafeMode         bool          `json:"enable_safe_mode"`          // 启用安全模式
	SafeModeRecoveryDelay  time.Duration `json:"safe_mode_recovery_delay"`  // 安全模式恢复延迟
}

// DefaultRecoveryConfig 返回默认恢复配置
func DefaultRecoveryConfig() *RecoveryConfig {
	return &RecoveryConfig{
		MinRecoveryInterval:       5 * time.Minute,
		MaxRecoveryInterval:       24 * time.Hour,
		DefaultRecoveryDelay:      15 * time.Minute,
		BackoffMultiplier:         1.5,
		MaxBackoffAttempts:        5,
		RecoverySuccessBonus:      0.8,
		MaxRecoveryAttempts:       10,
		RecoveryWindowHours:       24,
		HighPriorityThreshold:     5,
		CriticalPriorityThreshold: 10,
		EnableSafeMode:            true,
		SafeModeRecoveryDelay:     30 * time.Minute,
	}
}

// NewSmartRecoveryStrategy 创建智能恢复策略
func NewSmartRecoveryStrategy(config *RecoveryConfig) *SmartRecoveryStrategy {
	if config == nil {
		config = DefaultRecoveryConfig()
	}
	
	return &SmartRecoveryStrategy{
		config: config,
	}
}

// CalculateRecoveryTime 计算恢复时间
func (s *SmartRecoveryStrategy) CalculateRecoveryTime(key *models.APIKey, rateLimitHistory []*RateLimitRecord) time.Time {
	now := time.Now()
	
	// 如果有明确的重置时间，使用它
	if key.RateLimitResetAt != nil && key.RateLimitResetAt.After(now) {
		baseRecoveryTime := *key.RateLimitResetAt
		
		// 根据历史记录调整恢复时间
		adjustedDelay := s.calculateDynamicDelay(key, rateLimitHistory)
		return baseRecoveryTime.Add(adjustedDelay)
	}
	
	// 否则使用默认策略
	baseDelay := s.config.DefaultRecoveryDelay
	
	// 根据429频率调整
	if len(rateLimitHistory) > 0 {
		frequencyMultiplier := s.calculateFrequencyMultiplier(rateLimitHistory)
		baseDelay = time.Duration(float64(baseDelay) * frequencyMultiplier)
	}
	
	// 应用退避策略
	backoffDelay := s.calculateBackoffDelay(key)
	totalDelay := baseDelay + backoffDelay
	
	// 限制在合理范围内
	if totalDelay < s.config.MinRecoveryInterval {
		totalDelay = s.config.MinRecoveryInterval
	}
	if totalDelay > s.config.MaxRecoveryInterval {
		totalDelay = s.config.MaxRecoveryInterval
	}
	
	return now.Add(totalDelay)
}

// calculateDynamicDelay 计算动态延迟
func (s *SmartRecoveryStrategy) calculateDynamicDelay(key *models.APIKey, history []*RateLimitRecord) time.Duration {
	if len(history) == 0 {
		return 0
	}
	
	// 分析最近的429模式
	recentHistory := s.getRecentHistory(history, 24*time.Hour)
	if len(recentHistory) == 0 {
		return 0
	}
	
	// 计算平均间隔
	avgInterval := s.calculateAverageInterval(recentHistory)
	
	// 计算严重程度
	severity := s.calculateSeverity(recentHistory)
	
	// 根据严重程度调整延迟
	var multiplier float64
	switch severity {
	case SeverityLow:
		multiplier = 0.5
	case SeverityMedium:
		multiplier = 1.0
	case SeverityHigh:
		multiplier = 2.0
	case SeverityCritical:
		multiplier = 4.0
	default:
		multiplier = 1.0
	}
	
	dynamicDelay := time.Duration(float64(avgInterval) * multiplier)
	
	// 限制在合理范围内
	if dynamicDelay > s.config.MaxRecoveryInterval {
		dynamicDelay = s.config.MaxRecoveryInterval
	}
	
	return dynamicDelay
}

// calculateFrequencyMultiplier 计算频率乘数
func (s *SmartRecoveryStrategy) calculateFrequencyMultiplier(history []*RateLimitRecord) float64 {
	if len(history) <= 1 {
		return 1.0
	}
	
	// 计算最近24小时的429频率
	recentHistory := s.getRecentHistory(history, 24*time.Hour)
	frequency := len(recentHistory)
	
	// 根据频率计算乘数
	if frequency <= 2 {
		return 1.0 // 低频率，正常恢复
	} else if frequency <= 5 {
		return 1.5 // 中等频率，稍微延长
	} else if frequency <= 10 {
		return 2.0 // 高频率，显著延长
	} else {
		return 3.0 // 极高频率，大幅延长
	}
}

// calculateBackoffDelay 计算退避延迟
func (s *SmartRecoveryStrategy) calculateBackoffDelay(key *models.APIKey) time.Duration {
	// 根据失败次数计算退避延迟
	failureCount := key.RateLimitCount
	if failureCount <= 1 {
		return 0
	}
	
	// 指数退避
	attempts := int(math.Min(float64(failureCount), float64(s.config.MaxBackoffAttempts)))
	backoffMultiplier := math.Pow(s.config.BackoffMultiplier, float64(attempts-1))
	
	backoffDelay := time.Duration(float64(s.config.DefaultRecoveryDelay) * backoffMultiplier)
	
	return backoffDelay
}

// ShouldAttemptRecovery 判断是否应该尝试恢复
func (s *SmartRecoveryStrategy) ShouldAttemptRecovery(key *models.APIKey, now time.Time) bool {
	// 检查密钥状态
	if key.Status != models.KeyStatusRateLimited {
		return false
	}
	
	// 检查是否到了恢复时间
	if key.RateLimitResetAt != nil && now.Before(*key.RateLimitResetAt) {
		return false
	}
	
	// 检查恢复尝试次数
	if key.RateLimitCount >= int64(s.config.MaxRecoveryAttempts) {
		logrus.WithFields(logrus.Fields{
			"keyID":        key.ID,
			"attemptCount": key.RateLimitCount,
			"maxAttempts":  s.config.MaxRecoveryAttempts,
		}).Warn("Key exceeded maximum recovery attempts")
		return false
	}
	
	// 安全模式检查
	if s.config.EnableSafeMode {
		if key.Last429At != nil {
			timeSinceLastError := now.Sub(*key.Last429At)
			if timeSinceLastError < s.config.SafeModeRecoveryDelay {
				return false
			}
		}
	}
	
	return true
}

// GetRecoveryPriority 获取恢复优先级
func (s *SmartRecoveryStrategy) GetRecoveryPriority(key *models.APIKey) RecoveryPriority {
	// 根据密钥的重要性和使用频率确定优先级
	
	// 基于请求计数的优先级
	if key.RequestCount >= int64(s.config.CriticalPriorityThreshold*1000) {
		return PriorityCritical
	} else if key.RequestCount >= int64(s.config.HighPriorityThreshold*1000) {
		return PriorityHigh
	}
	
	// 基于429频率的优先级（频率低的优先恢复）
	if key.RateLimitCount <= 2 {
		return PriorityHigh
	} else if key.RateLimitCount <= 5 {
		return PriorityNormal
	} else {
		return PriorityLow
	}
}

// ValidateRecoveryConditions 验证恢复条件
func (s *SmartRecoveryStrategy) ValidateRecoveryConditions(key *models.APIKey, group *models.Group) error {
	// 检查密钥基本状态
	if key.Status != models.KeyStatusRateLimited {
		return fmt.Errorf("key is not in rate limited status")
	}
	
	// 检查分组状态
	if group == nil {
		return fmt.Errorf("group not found")
	}
	
	// 检查分组配置
	if group.EffectiveConfig == nil {
		return fmt.Errorf("group effective config not found")
	}
	
	// 检查是否启用了自动恢复
	if !group.EffectiveConfig.EnableAutoRecovery {
		return fmt.Errorf("auto recovery is disabled for this group")
	}
	
	return nil
}

// 辅助方法

// getRecentHistory 获取最近的历史记录
func (s *SmartRecoveryStrategy) getRecentHistory(history []*RateLimitRecord, duration time.Duration) []*RateLimitRecord {
	cutoff := time.Now().Add(-duration)
	var recent []*RateLimitRecord
	
	for _, record := range history {
		if record.OccurredAt.After(cutoff) {
			recent = append(recent, record)
		}
	}
	
	return recent
}

// calculateAverageInterval 计算平均间隔
func (s *SmartRecoveryStrategy) calculateAverageInterval(history []*RateLimitRecord) time.Duration {
	if len(history) <= 1 {
		return s.config.DefaultRecoveryDelay
	}
	
	var totalInterval time.Duration
	for i := 1; i < len(history); i++ {
		interval := history[i].OccurredAt.Sub(history[i-1].OccurredAt)
		totalInterval += interval
	}
	
	return totalInterval / time.Duration(len(history)-1)
}

// calculateSeverity 计算严重程度
func (s *SmartRecoveryStrategy) calculateSeverity(history []*RateLimitRecord) RateLimitSeverity {
	if len(history) == 0 {
		return SeverityLow
	}
	
	// 根据频率和时间间隔计算严重程度
	frequency := len(history)
	
	if frequency >= 20 {
		return SeverityCritical
	} else if frequency >= 10 {
		return SeverityHigh
	} else if frequency >= 5 {
		return SeverityMedium
	} else {
		return SeverityLow
	}
}

// RecoveryPlan 恢复计划
type RecoveryPlan struct {
	KeyID           uint              `json:"key_id"`
	GroupID         uint              `json:"group_id"`
	Priority        RecoveryPriority  `json:"priority"`
	ScheduledAt     time.Time         `json:"scheduled_at"`
	EstimatedDelay  time.Duration     `json:"estimated_delay"`
	Strategy        string            `json:"strategy"`
	Conditions      []string          `json:"conditions"`
	Metadata        map[string]interface{} `json:"metadata"`
}

// CreateRecoveryPlan 创建恢复计划
func (s *SmartRecoveryStrategy) CreateRecoveryPlan(key *models.APIKey, group *models.Group, history []*RateLimitRecord) (*RecoveryPlan, error) {
	// 验证恢复条件
	if err := s.ValidateRecoveryConditions(key, group); err != nil {
		return nil, err
	}
	
	// 计算恢复时间
	recoveryTime := s.CalculateRecoveryTime(key, history)
	
	// 获取优先级
	priority := s.GetRecoveryPriority(key)
	
	// 创建恢复计划
	plan := &RecoveryPlan{
		KeyID:          key.ID,
		GroupID:        key.GroupID,
		Priority:       priority,
		ScheduledAt:    recoveryTime,
		EstimatedDelay: recoveryTime.Sub(time.Now()),
		Strategy:       "smart_recovery",
		Conditions:     []string{},
		Metadata:       make(map[string]interface{}),
	}
	
	// 添加条件和元数据
	plan.Conditions = append(plan.Conditions, "rate_limit_reset_time_passed")
	plan.Metadata["rate_limit_count"] = key.RateLimitCount
	plan.Metadata["last_429_at"] = key.Last429At
	plan.Metadata["history_count"] = len(history)
	
	if len(history) > 0 {
		severity := s.calculateSeverity(history)
		plan.Metadata["severity"] = severity
	}
	
	return plan, nil
}
