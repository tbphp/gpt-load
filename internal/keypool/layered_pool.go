package keypool

import (
	"gpt-load/internal/errors"
	"gpt-load/internal/models"
	"time"
)

// PoolType 定义密钥池类型
type PoolType string

const (
	PoolTypeValidation PoolType = "validation" // 验证池：新密钥等待验证
	PoolTypeReady      PoolType = "ready"      // 就绪池：已验证可用的密钥
	PoolTypeActive     PoolType = "active"     // 活跃池：正在轮询使用的密钥
	PoolTypeCooling    PoolType = "cooling"    // 冷却池：429错误的密钥
)

// KeyStatus 定义密钥在池中的状态
type KeyStatus string

const (
	KeyStatusPending    KeyStatus = "pending"     // 等待验证
	KeyStatusValidated  KeyStatus = "validated"   // 已验证可用
	KeyStatusInUse      KeyStatus = "in_use"      // 正在使用
	KeyStatusCooling    KeyStatus = "cooling"     // 冷却中
	KeyStatusFailed     KeyStatus = "failed"      // 验证失败
)

// LayeredKeyPool 定义分层密钥池的核心接口
type LayeredKeyPool interface {
	// 核心操作
	SelectKey(groupID uint) (*models.APIKey, error)
	ReturnKey(keyID uint, success bool) error
	HandleRateLimit(keyID uint, rateLimitErr *errors.RateLimitError) error

	// 池管理
	AddKeys(groupID uint, keyIDs []uint) error
	RemoveKeys(groupID uint, keyIDs []uint) error
	MoveKey(keyID uint, fromPool, toPool PoolType) error

	// 状态查询
	GetPoolStats(groupID uint) (*PoolStats, error)
	GetKeyStatus(keyID uint) (KeyStatus, error)
	ListKeys(groupID uint, poolType PoolType) ([]uint, error)

	// 维护操作
	RefillPools(groupID uint) error
	RecoverCooledKeys(groupID uint) (int, error)
	ValidateKeys(groupID uint, keyIDs []uint) error

	// 配置管理
	UpdateConfig(groupID uint, config *PoolConfig) error
	GetConfig(groupID uint) (*PoolConfig, error)

	// 生命周期
	Start() error
	Stop() error
	Health() error
}

// PoolStats 定义池统计信息
type PoolStats struct {
	GroupID     uint                    `json:"group_id"`
	TotalKeys   int                     `json:"total_keys"`
	PoolCounts  map[PoolType]int        `json:"pool_counts"`
	StatusCounts map[KeyStatus]int      `json:"status_counts"`
	Performance *PoolPerformanceStats   `json:"performance"`
	LastUpdated time.Time               `json:"last_updated"`
}

// PoolPerformanceStats 定义性能统计
type PoolPerformanceStats struct {
	SelectLatency    time.Duration `json:"select_latency_ms"`    // 选择密钥的平均延迟
	HitRate          float64       `json:"hit_rate"`             // 命中率（成功选择/总选择）
	RefillRate       float64       `json:"refill_rate"`          // 补充频率（次/小时）
	RecoveryRate     float64       `json:"recovery_rate"`        // 恢复频率（次/小时）
	RateLimitRate    float64       `json:"rate_limit_rate"`      // 429错误率
	AvgCoolingTime   time.Duration `json:"avg_cooling_time"`     // 平均冷却时间
}

// PoolConfig 定义池配置
type PoolConfig struct {
	GroupID uint `json:"group_id"`

	// 池大小配置
	MinActiveKeys    int `json:"min_active_keys"`    // 活跃池最小密钥数
	MaxActiveKeys    int `json:"max_active_keys"`    // 活跃池最大密钥数
	MinReadyKeys     int `json:"min_ready_keys"`     // 就绪池最小密钥数
	MaxReadyKeys     int `json:"max_ready_keys"`     // 就绪池最大密钥数

	// 补充策略配置
	RefillThreshold  float64 `json:"refill_threshold"`  // 补充阈值（百分比）
	RefillBatchSize  int     `json:"refill_batch_size"` // 批量补充大小

	// 验证配置
	ValidationTimeout    time.Duration `json:"validation_timeout"`     // 验证超时时间
	ValidationConcurrency int          `json:"validation_concurrency"` // 验证并发数

	// 恢复配置
	RecoveryInterval     time.Duration `json:"recovery_interval"`      // 恢复检查间隔
	DefaultCoolingTime   time.Duration `json:"default_cooling_time"`   // 默认冷却时间
	MaxCoolingTime       time.Duration `json:"max_cooling_time"`       // 最大冷却时间

	// 性能配置
	EnablePredictiveRefill bool          `json:"enable_predictive_refill"` // 启用预测性补充
	EnableLocalCache       bool          `json:"enable_local_cache"`        // 启用本地缓存
	LocalCacheSize         int           `json:"local_cache_size"`          // 本地缓存大小
	LocalCacheTTL          time.Duration `json:"local_cache_ttl"`           // 本地缓存TTL
}

// DefaultPoolConfig 返回默认的池配置
func DefaultPoolConfig(groupID uint) *PoolConfig {
	return &PoolConfig{
		GroupID:               groupID,
		MinActiveKeys:         5,
		MaxActiveKeys:         50,
		MinReadyKeys:          10,
		MaxReadyKeys:          100,
		RefillThreshold:       0.3, // 30%
		RefillBatchSize:       10,
		ValidationTimeout:     30 * time.Second,
		ValidationConcurrency: 5,
		RecoveryInterval:      5 * time.Minute,
		DefaultCoolingTime:    60 * time.Second,
		MaxCoolingTime:        24 * time.Hour,
		EnablePredictiveRefill: true,
		EnableLocalCache:      true,
		LocalCacheSize:        20,
		LocalCacheTTL:         5 * time.Minute,
	}
}

// KeyPoolEvent 定义密钥池事件
type KeyPoolEvent struct {
	Type      EventType   `json:"type"`
	GroupID   uint        `json:"group_id"`
	KeyID     uint        `json:"key_id,omitempty"`
	PoolType  PoolType    `json:"pool_type,omitempty"`
	Message   string      `json:"message"`
	Timestamp time.Time   `json:"timestamp"`
	Metadata  interface{} `json:"metadata,omitempty"`
}

// EventType 定义事件类型
type EventType string

const (
	EventKeySelected     EventType = "key_selected"
	EventKeyReturned     EventType = "key_returned"
	EventKeyMoved        EventType = "key_moved"
	EventKeyValidated    EventType = "key_validated"
	EventKeyRecovered    EventType = "key_recovered"
	EventPoolRefilled    EventType = "pool_refilled"
	EventRateLimitHit    EventType = "rate_limit_hit"
	EventValidationFailed EventType = "validation_failed"
)

// EventHandler 定义事件处理器接口
type EventHandler interface {
	HandleEvent(event *KeyPoolEvent) error
}

// KeyValidator 定义密钥验证器接口
type KeyValidator interface {
	ValidateKey(key *models.APIKey, group *models.Group) error
	ValidateBatch(keys []*models.APIKey, group *models.Group) []ValidationResult
}

// ValidationResult 定义验证结果
type ValidationResult struct {
	KeyID   uint   `json:"key_id"`
	Valid   bool   `json:"valid"`
	Error   string `json:"error,omitempty"`
	Latency time.Duration `json:"latency"`
}

// PoolMetrics 定义池指标接口
type PoolMetrics interface {
	RecordKeySelection(groupID uint, latency time.Duration, success bool)
	RecordPoolRefill(groupID uint, count int)
	RecordKeyRecovery(groupID uint, count int)
	RecordRateLimit(groupID uint, keyID uint)
	GetMetrics(groupID uint) (*PoolPerformanceStats, error)
}

// PoolFactory 定义密钥池工厂接口
type PoolFactory interface {
	CreatePool(poolType PoolImplementationType, config *FactoryConfig) (LayeredKeyPool, error)
	GetSupportedTypes() []PoolImplementationType
	ValidateConfig(poolType PoolImplementationType, config *FactoryConfig) error
}

// PoolImplementationType 定义池实现类型
type PoolImplementationType string

const (
	PoolImplRedis  PoolImplementationType = "redis"  // Redis实现
	PoolImplMemory PoolImplementationType = "memory" // 内存实现
	PoolImplHybrid PoolImplementationType = "hybrid" // 混合实现
)

// FactoryConfig 定义工厂配置
type FactoryConfig struct {
	// 存储配置
	RedisConfig  *RedisPoolConfig  `json:"redis_config,omitempty"`
	MemoryConfig *MemoryPoolConfig `json:"memory_config,omitempty"`

	// 通用配置
	DefaultPoolConfig *PoolConfig     `json:"default_pool_config"`
	EventHandler      EventHandler    `json:"-"`
	Metrics          PoolMetrics     `json:"-"`
	Validator        KeyValidator    `json:"-"`

	// 依赖注入
	Store            interface{}     `json:"-"` // store.Store
	DB               interface{}     `json:"-"` // *gorm.DB
	SettingsManager  interface{}     `json:"-"` // *config.SystemSettingsManager
}

// RedisPoolConfig Redis池特定配置
type RedisPoolConfig struct {
	KeyPrefix        string        `json:"key_prefix"`
	BatchSize        int           `json:"batch_size"`
	PipelineSize     int           `json:"pipeline_size"`
	ConnectionPool   int           `json:"connection_pool"`
	CommandTimeout   time.Duration `json:"command_timeout"`
	EnableClustering bool          `json:"enable_clustering"`
}

// MemoryPoolConfig 内存池特定配置
type MemoryPoolConfig struct {
	ShardCount       int           `json:"shard_count"`
	EnableSharding   bool          `json:"enable_sharding"`
	LockTimeout      time.Duration `json:"lock_timeout"`
	GCInterval       time.Duration `json:"gc_interval"`
	MaxMemoryUsage   int64         `json:"max_memory_usage"`
}

// DefaultRedisPoolConfig 返回默认Redis池配置
func DefaultRedisPoolConfig() *RedisPoolConfig {
	return &RedisPoolConfig{
		KeyPrefix:        "layered_pool:",
		BatchSize:        100,
		PipelineSize:     10,
		ConnectionPool:   10,
		CommandTimeout:   5 * time.Second,
		EnableClustering: false,
	}
}

// DefaultMemoryPoolConfig 返回默认内存池配置
func DefaultMemoryPoolConfig() *MemoryPoolConfig {
	return &MemoryPoolConfig{
		ShardCount:     16,
		EnableSharding: true,
		LockTimeout:    1 * time.Second,
		GCInterval:     10 * time.Minute,
		MaxMemoryUsage: 100 * 1024 * 1024, // 100MB
	}
}

// ErrorHandler 定义错误处理接口
type ErrorHandler interface {
	HandleError(err error, context *ErrorContext) error
	HandleRateLimit(rateLimitErr *errors.RateLimitError, context *ErrorContext) error
	ShouldRetry(err error, attempt int) bool
	GetRetryDelay(err error, attempt int) time.Duration
}

// ErrorContext 定义错误上下文
type ErrorContext struct {
	GroupID     uint        `json:"group_id"`
	KeyID       uint        `json:"key_id"`
	Operation   string      `json:"operation"`
	Attempt     int         `json:"attempt"`
	StartTime   time.Time   `json:"start_time"`
	Metadata    interface{} `json:"metadata,omitempty"`
}

// PoolError 定义池特定错误
type PoolError struct {
	Type    PoolErrorType `json:"type"`
	Code    string        `json:"code"`
	Message string        `json:"message"`
	GroupID uint          `json:"group_id,omitempty"`
	KeyID   uint          `json:"key_id,omitempty"`
	Cause   error         `json:"-"`
}

// Error 实现error接口
func (e *PoolError) Error() string {
	return e.Message
}

// Unwrap 支持错误链
func (e *PoolError) Unwrap() error {
	return e.Cause
}

// PoolErrorType 定义池错误类型
type PoolErrorType string

const (
	ErrorTypeValidation    PoolErrorType = "validation"     // 验证错误
	ErrorTypeConfiguration PoolErrorType = "configuration"  // 配置错误
	ErrorTypeStorage       PoolErrorType = "storage"        // 存储错误
	ErrorTypeTimeout       PoolErrorType = "timeout"        // 超时错误
	ErrorTypeRateLimit     PoolErrorType = "rate_limit"     // 限流错误
	ErrorTypeCapacity      PoolErrorType = "capacity"       // 容量错误
	ErrorTypeInternal      PoolErrorType = "internal"       // 内部错误
)

// 预定义错误
var (
	ErrPoolNotFound       = &PoolError{Type: ErrorTypeConfiguration, Code: "POOL_NOT_FOUND", Message: "Pool not found"}
	ErrInvalidConfig      = &PoolError{Type: ErrorTypeConfiguration, Code: "INVALID_CONFIG", Message: "Invalid pool configuration"}
	ErrKeyNotFound        = &PoolError{Type: ErrorTypeValidation, Code: "KEY_NOT_FOUND", Message: "Key not found in pool"}
	ErrPoolFull           = &PoolError{Type: ErrorTypeCapacity, Code: "POOL_FULL", Message: "Pool is at maximum capacity"}
	ErrPoolEmpty          = &PoolError{Type: ErrorTypeCapacity, Code: "POOL_EMPTY", Message: "Pool is empty"}
	ErrValidationTimeout  = &PoolError{Type: ErrorTypeTimeout, Code: "VALIDATION_TIMEOUT", Message: "Key validation timeout"}
	ErrStorageUnavailable = &PoolError{Type: ErrorTypeStorage, Code: "STORAGE_UNAVAILABLE", Message: "Storage backend unavailable"}
)

// NewPoolError 创建新的池错误
func NewPoolError(errorType PoolErrorType, code, message string) *PoolError {
	return &PoolError{
		Type:    errorType,
		Code:    code,
		Message: message,
	}
}

// NewPoolErrorWithCause 创建带原因的池错误
func NewPoolErrorWithCause(errorType PoolErrorType, code, message string, cause error) *PoolError {
	return &PoolError{
		Type:    errorType,
		Code:    code,
		Message: message,
		Cause:   cause,
	}
}
