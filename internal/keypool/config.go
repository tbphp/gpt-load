package keypool

import (
	"fmt"
	"gpt-load/internal/config"
	"gpt-load/internal/store"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// ProviderType 定义提供者类型
type ProviderType string

const (
	ProviderTypeLegacy   ProviderType = "legacy"   // 传统提供者
	ProviderTypeEnhanced ProviderType = "enhanced" // 增强提供者
)

// GlobalConfig 全局密钥池配置
type GlobalConfig struct {
	// 提供者配置
	ProviderType       ProviderType               `json:"provider_type" yaml:"provider_type"`
	UseLayeredPool     bool                       `json:"use_layered_pool" yaml:"use_layered_pool"`
	PoolImplementation PoolImplementationType     `json:"pool_implementation" yaml:"pool_implementation"`
	
	// 功能开关
	EnableMetrics      bool                       `json:"enable_metrics" yaml:"enable_metrics"`
	EnableValidation   bool                       `json:"enable_validation" yaml:"enable_validation"`
	EnableEvents       bool                       `json:"enable_events" yaml:"enable_events"`
	EnableAutoRecovery bool                       `json:"enable_auto_recovery" yaml:"enable_auto_recovery"`
	
	// 池配置
	DefaultPoolConfig  *PoolConfig                `json:"default_pool_config" yaml:"default_pool_config"`
	RedisConfig        *RedisPoolConfig           `json:"redis_config" yaml:"redis_config"`
	MemoryConfig       *MemoryPoolConfig          `json:"memory_config" yaml:"memory_config"`
	
	// 性能配置
	BatchSize          int                        `json:"batch_size" yaml:"batch_size"`
	MaxConcurrency     int                        `json:"max_concurrency" yaml:"max_concurrency"`
	HealthCheckInterval time.Duration             `json:"health_check_interval" yaml:"health_check_interval"`
}

// DefaultGlobalConfig 返回默认全局配置
func DefaultGlobalConfig() *GlobalConfig {
	return &GlobalConfig{
		ProviderType:        ProviderTypeLegacy,
		UseLayeredPool:      false,
		PoolImplementation:  PoolImplRedis,
		EnableMetrics:       true,
		EnableValidation:    true,
		EnableEvents:        true,
		EnableAutoRecovery:  true,
		DefaultPoolConfig:   DefaultPoolConfig(0), // 通用默认配置
		RedisConfig:         DefaultRedisPoolConfig(),
		MemoryConfig:        DefaultMemoryPoolConfig(),
		BatchSize:           100,
		MaxConcurrency:      10,
		HealthCheckInterval: 30 * time.Second,
	}
}

// ProviderManager 提供者管理器
type ProviderManager struct {
	config          *GlobalConfig
	db              *gorm.DB
	store           store.Store
	settingsManager *config.SystemSettingsManager
	
	// 提供者实例
	legacyProvider   *KeyProvider
	enhancedProvider *EnhancedKeyProvider
	currentProvider  interface{}
}

// NewProviderManager 创建提供者管理器
func NewProviderManager(
	db *gorm.DB,
	store store.Store,
	settingsManager *config.SystemSettingsManager,
	config *GlobalConfig,
) (*ProviderManager, error) {
	
	if config == nil {
		config = DefaultGlobalConfig()
	}
	
	manager := &ProviderManager{
		config:          config,
		db:              db,
		store:           store,
		settingsManager: settingsManager,
	}
	
	// 初始化提供者
	if err := manager.initializeProviders(); err != nil {
		return nil, fmt.Errorf("failed to initialize providers: %w", err)
	}
	
	return manager, nil
}

// initializeProviders 初始化提供者
func (m *ProviderManager) initializeProviders() error {
	// 始终创建传统提供者作为回退
	m.legacyProvider = NewProvider(m.db, m.store, m.settingsManager)
	
	// 根据配置决定是否创建增强提供者
	if m.config.ProviderType == ProviderTypeEnhanced || m.config.UseLayeredPool {
		enhancedConfig := &EnhancedProviderConfig{
			UseLayeredPool:      m.config.UseLayeredPool,
			PoolImplementation:  m.config.PoolImplementation,
			PoolConfig:          m.config.DefaultPoolConfig,
			RedisConfig:         m.config.RedisConfig,
			MemoryConfig:        m.config.MemoryConfig,
			EnableMetrics:       m.config.EnableMetrics,
			EnableValidation:    m.config.EnableValidation,
			EnableEvents:        m.config.EnableEvents,
		}
		
		enhancedProvider, err := NewEnhancedKeyProvider(
			m.db, m.store, m.settingsManager, enhancedConfig)
		if err != nil {
			logrus.WithError(err).Warn("Failed to create enhanced provider, falling back to legacy")
			m.currentProvider = m.legacyProvider
			return nil
		}
		
		m.enhancedProvider = enhancedProvider
		m.currentProvider = m.enhancedProvider
		
		logrus.Info("Initialized enhanced key provider")
	} else {
		m.currentProvider = m.legacyProvider
		logrus.Info("Initialized legacy key provider")
	}
	
	return nil
}

// GetProvider 获取当前提供者
func (m *ProviderManager) GetProvider() interface{} {
	return m.currentProvider
}

// GetLegacyProvider 获取传统提供者
func (m *ProviderManager) GetLegacyProvider() *KeyProvider {
	return m.legacyProvider
}

// GetEnhancedProvider 获取增强提供者
func (m *ProviderManager) GetEnhancedProvider() *EnhancedKeyProvider {
	return m.enhancedProvider
}

// SwitchToEnhanced 切换到增强提供者
func (m *ProviderManager) SwitchToEnhanced() error {
	if m.enhancedProvider == nil {
		// 创建增强提供者
		enhancedConfig := &EnhancedProviderConfig{
			UseLayeredPool:      true,
			PoolImplementation:  m.config.PoolImplementation,
			PoolConfig:          m.config.DefaultPoolConfig,
			RedisConfig:         m.config.RedisConfig,
			MemoryConfig:        m.config.MemoryConfig,
			EnableMetrics:       m.config.EnableMetrics,
			EnableValidation:    m.config.EnableValidation,
			EnableEvents:        m.config.EnableEvents,
		}
		
		enhancedProvider, err := NewEnhancedKeyProvider(
			m.db, m.store, m.settingsManager, enhancedConfig)
		if err != nil {
			return fmt.Errorf("failed to create enhanced provider: %w", err)
		}
		
		m.enhancedProvider = enhancedProvider
	} else if !m.enhancedProvider.IsUsingLayeredPool() {
		// 切换现有增强提供者到分层池模式
		enhancedConfig := &EnhancedProviderConfig{
			UseLayeredPool:      true,
			PoolImplementation:  m.config.PoolImplementation,
			PoolConfig:          m.config.DefaultPoolConfig,
			RedisConfig:         m.config.RedisConfig,
			MemoryConfig:        m.config.MemoryConfig,
			EnableMetrics:       m.config.EnableMetrics,
			EnableValidation:    m.config.EnableValidation,
			EnableEvents:        m.config.EnableEvents,
		}
		
		if err := m.enhancedProvider.SwitchToLayeredPool(enhancedConfig); err != nil {
			return fmt.Errorf("failed to switch to layered pool: %w", err)
		}
	}
	
	m.currentProvider = m.enhancedProvider
	m.config.ProviderType = ProviderTypeEnhanced
	m.config.UseLayeredPool = true
	
	logrus.Info("Switched to enhanced provider")
	return nil
}

// SwitchToLegacy 切换到传统提供者
func (m *ProviderManager) SwitchToLegacy() error {
	if m.enhancedProvider != nil && m.enhancedProvider.IsUsingLayeredPool() {
		if err := m.enhancedProvider.SwitchToLegacyMode(); err != nil {
			logrus.WithError(err).Warn("Failed to switch enhanced provider to legacy mode")
		}
	}
	
	m.currentProvider = m.legacyProvider
	m.config.ProviderType = ProviderTypeLegacy
	m.config.UseLayeredPool = false
	
	logrus.Info("Switched to legacy provider")
	return nil
}

// UpdateConfig 更新配置
func (m *ProviderManager) UpdateConfig(newConfig *GlobalConfig) error {
	if newConfig == nil {
		return fmt.Errorf("config cannot be nil")
	}
	
	oldConfig := m.config
	m.config = newConfig
	
	// 检查是否需要切换提供者
	if oldConfig.ProviderType != newConfig.ProviderType || 
	   oldConfig.UseLayeredPool != newConfig.UseLayeredPool {
		
		if newConfig.ProviderType == ProviderTypeEnhanced || newConfig.UseLayeredPool {
			if err := m.SwitchToEnhanced(); err != nil {
				m.config = oldConfig // 回滚配置
				return fmt.Errorf("failed to switch to enhanced provider: %w", err)
			}
		} else {
			if err := m.SwitchToLegacy(); err != nil {
				m.config = oldConfig // 回滚配置
				return fmt.Errorf("failed to switch to legacy provider: %w", err)
			}
		}
	}
	
	logrus.Info("Provider configuration updated")
	return nil
}

// GetConfig 获取当前配置
func (m *ProviderManager) GetConfig() *GlobalConfig {
	return m.config
}

// GetStatus 获取提供者状态
func (m *ProviderManager) GetStatus() map[string]interface{} {
	status := map[string]interface{}{
		"provider_type":     m.config.ProviderType,
		"use_layered_pool":  m.config.UseLayeredPool,
		"pool_implementation": m.config.PoolImplementation,
		"legacy_available":  m.legacyProvider != nil,
		"enhanced_available": m.enhancedProvider != nil,
	}
	
	if m.enhancedProvider != nil {
		status["enhanced_using_layered_pool"] = m.enhancedProvider.IsUsingLayeredPool()
	}
	
	return status
}

// HealthCheck 健康检查
func (m *ProviderManager) HealthCheck() error {
	if m.currentProvider == nil {
		return fmt.Errorf("no active provider")
	}
	
	// 检查增强提供者的健康状态
	if m.enhancedProvider != nil && m.currentProvider == m.enhancedProvider {
		if m.enhancedProvider.layeredPool != nil {
			if err := m.enhancedProvider.layeredPool.Health(); err != nil {
				return fmt.Errorf("layered pool health check failed: %w", err)
			}
		}
	}
	
	// 检查存储连接
	if err := m.store.Set("health_check", []byte("ok"), 10*time.Second); err != nil {
		return fmt.Errorf("store health check failed: %w", err)
	}
	
	// 检查数据库连接
	sqlDB, err := m.db.DB()
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	
	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}
	
	return nil
}

// Close 关闭管理器
func (m *ProviderManager) Close() error {
	if m.enhancedProvider != nil {
		if err := m.enhancedProvider.Close(); err != nil {
			logrus.WithError(err).Warn("Failed to close enhanced provider")
		}
	}
	
	return nil
}

// LoadConfigFromSettings 从系统设置加载配置
func LoadConfigFromSettings(settingsManager *config.SystemSettingsManager) (*GlobalConfig, error) {
	config := DefaultGlobalConfig()
	
	// 从系统设置中读取配置
	// 这里可以根据实际的设置管理器实现来读取配置
	// 例如：
	// if value := settingsManager.Get("keypool.provider_type"); value != "" {
	//     config.ProviderType = ProviderType(value)
	// }
	
	return config, nil
}
