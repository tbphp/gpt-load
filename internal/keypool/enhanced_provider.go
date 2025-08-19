package keypool

import (
	"fmt"
	"gpt-load/internal/config"
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"
	"gpt-load/internal/store"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// EnhancedKeyProvider 增强版密钥提供者，集成分层密钥池
type EnhancedKeyProvider struct {
	// 原有组件
	db              *gorm.DB
	store           store.Store
	settingsManager *config.SystemSettingsManager

	// 新增组件
	layeredPool     LayeredKeyPool
	factory         PoolFactory

	// 配置
	useLayeredPool  bool
	legacyProvider  *KeyProvider

	// 性能监控
	metrics         PoolMetrics
	validator       KeyValidator
	eventHandler    EventHandler
}

// EnhancedProviderConfig 增强提供者配置
type EnhancedProviderConfig struct {
	UseLayeredPool      bool                           `json:"use_layered_pool"`
	PoolImplementation  PoolImplementationType         `json:"pool_implementation"`
	PoolConfig          *PoolConfig                    `json:"pool_config"`
	RedisConfig         *RedisPoolConfig               `json:"redis_config"`
	MemoryConfig        *MemoryPoolConfig              `json:"memory_config"`
	EnableMetrics       bool                           `json:"enable_metrics"`
	EnableValidation    bool                           `json:"enable_validation"`
	EnableEvents        bool                           `json:"enable_events"`
}

// NewEnhancedKeyProvider 创建增强版密钥提供者
func NewEnhancedKeyProvider(
	db *gorm.DB,
	store store.Store,
	settingsManager *config.SystemSettingsManager,
	config *EnhancedProviderConfig,
) (*EnhancedKeyProvider, error) {

	provider := &EnhancedKeyProvider{
		db:              db,
		store:           store,
		settingsManager: settingsManager,
		useLayeredPool:  config.UseLayeredPool,
	}

	// 创建传统提供者作为回退
	provider.legacyProvider = NewProvider(db, store, settingsManager)

	// 如果启用分层池，初始化相关组件
	if config.UseLayeredPool {
		if err := provider.initializeLayeredPool(config); err != nil {
			return nil, fmt.Errorf("failed to initialize layered pool: %w", err)
		}
	}

	return provider, nil
}

// initializeLayeredPool 初始化分层密钥池
func (p *EnhancedKeyProvider) initializeLayeredPool(config *EnhancedProviderConfig) error {
	// 创建工厂
	p.factory = &DefaultPoolFactory{}

	// 创建组件
	if config.EnableMetrics {
		p.metrics = NewDefaultPoolMetrics()
	}

	if config.EnableValidation {
		p.validator = &DefaultKeyValidator{}
	}

	if config.EnableEvents {
		p.eventHandler = &DefaultEventHandler{}
	}

	// 创建工厂配置
	factoryConfig := &FactoryConfig{
		Store:             p.store,
		DB:                p.db,
		SettingsManager:   p.settingsManager,
		DefaultPoolConfig: config.PoolConfig,
		RedisConfig:       config.RedisConfig,
		MemoryConfig:      config.MemoryConfig,
		Metrics:           p.metrics,
		Validator:         p.validator,
		EventHandler:      p.eventHandler,
	}

	// 验证配置
	if err := p.factory.ValidateConfig(config.PoolImplementation, factoryConfig); err != nil {
		return fmt.Errorf("invalid factory config: %w", err)
	}

	// 创建分层池
	layeredPool, err := p.factory.CreatePool(config.PoolImplementation, factoryConfig)
	if err != nil {
		return fmt.Errorf("failed to create layered pool: %w", err)
	}

	p.layeredPool = layeredPool

	// 启动分层池
	if err := p.layeredPool.Start(); err != nil {
		return fmt.Errorf("failed to start layered pool: %w", err)
	}

	logrus.Info("Enhanced key provider initialized with layered pool")
	return nil
}

// SelectKey 选择一个可用的密钥
func (p *EnhancedKeyProvider) SelectKey(groupID uint) (*models.APIKey, error) {
	if p.useLayeredPool && p.layeredPool != nil {
		return p.layeredPool.SelectKey(groupID)
	}

	// 回退到传统实现
	return p.legacyProvider.SelectKey(groupID)
}

// UpdateStatus 更新密钥状态
func (p *EnhancedKeyProvider) UpdateStatus(apiKey *models.APIKey, group *models.Group, isSuccess bool) {
	if p.useLayeredPool && p.layeredPool != nil {
		// 使用分层池的异步更新
		go func() {
			if err := p.layeredPool.ReturnKey(apiKey.ID, isSuccess); err != nil {
				logrus.WithFields(logrus.Fields{
					"keyID":     apiKey.ID,
					"isSuccess": isSuccess,
					"error":     err,
				}).Error("Failed to return key to layered pool")
			}
		}()
		return
	}

	// 回退到传统实现
	p.legacyProvider.UpdateStatus(apiKey, group, isSuccess)
}

// UpdateStatusWithRateLimit 处理429错误
func (p *EnhancedKeyProvider) UpdateStatusWithRateLimit(apiKey *models.APIKey, group *models.Group, rateLimitErr *app_errors.RateLimitError) {
	if p.useLayeredPool && p.layeredPool != nil {
		// 使用分层池的429处理
		go func() {
			if err := p.layeredPool.HandleRateLimit(apiKey.ID, rateLimitErr); err != nil {
				logrus.WithFields(logrus.Fields{
					"keyID": apiKey.ID,
					"error": err,
				}).Error("Failed to handle rate limit in layered pool")
			}
		}()
		return
	}

	// 回退到传统实现
	p.legacyProvider.UpdateStatusWithRateLimit(apiKey, group, rateLimitErr)
}

// AddKeys 批量添加密钥
func (p *EnhancedKeyProvider) AddKeys(groupID uint, keys []models.APIKey) error {
	if p.useLayeredPool && p.layeredPool != nil {
		// 首先添加到数据库
		if err := p.db.Create(&keys).Error; err != nil {
			return fmt.Errorf("failed to create keys in database: %w", err)
		}

		// 提取密钥ID
		keyIDs := make([]uint, len(keys))
		for i, key := range keys {
			keyIDs[i] = key.ID
		}

		// 添加到分层池
		return p.layeredPool.AddKeys(groupID, keyIDs)
	}

	// 回退到传统实现
	return p.legacyProvider.AddKeys(groupID, keys)
}

// RemoveKeys 批量移除密钥
func (p *EnhancedKeyProvider) RemoveKeys(groupID uint, keyValues []string) (int64, error) {
	if p.useLayeredPool && p.layeredPool != nil {
		// 查找要删除的密钥
		var keysToRemove []models.APIKey
		if err := p.db.Where("group_id = ? AND key_value IN ?", groupID, keyValues).Find(&keysToRemove).Error; err != nil {
			return 0, fmt.Errorf("failed to find keys to remove: %w", err)
		}

		if len(keysToRemove) == 0 {
			return 0, nil
		}

		// 从数据库删除
		result := p.db.Where("id IN ?", pluckIDs(keysToRemove)).Delete(&models.APIKey{})
		if result.Error != nil {
			return 0, fmt.Errorf("failed to delete keys from database: %w", result.Error)
		}

		// 从分层池移除
		keyIDs := make([]uint, len(keysToRemove))
		for i, key := range keysToRemove {
			keyIDs[i] = key.ID
		}

		if err := p.layeredPool.RemoveKeys(groupID, keyIDs); err != nil {
			logrus.WithFields(logrus.Fields{
				"groupID": groupID,
				"keyIDs":  keyIDs,
				"error":   err,
			}).Warn("Failed to remove keys from layered pool")
		}

		return result.RowsAffected, nil
	}

	// 回退到传统实现
	return p.legacyProvider.RemoveKeys(groupID, keyValues)
}

// LoadKeysFromDB 从数据库加载密钥
func (p *EnhancedKeyProvider) LoadKeysFromDB() error {
	if p.useLayeredPool && p.layeredPool != nil {
		// 使用分层池的加载逻辑
		return p.loadKeysToLayeredPool()
	}

	// 回退到传统实现
	return p.legacyProvider.LoadKeysFromDB()
}

// loadKeysToLayeredPool 将密钥加载到分层池
func (p *EnhancedKeyProvider) loadKeysToLayeredPool() error {
	// 获取所有分组
	var groups []models.Group
	if err := p.db.Find(&groups).Error; err != nil {
		return fmt.Errorf("failed to load groups: %w", err)
	}

	for _, group := range groups {
		// 获取分组的活跃密钥
		var keys []models.APIKey
		if err := p.db.Where("group_id = ? AND status = ?", group.ID, models.KeyStatusActive).Find(&keys).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"groupID": group.ID,
				"error":   err,
			}).Error("Failed to load keys for group")
			continue
		}

		if len(keys) == 0 {
			continue
		}

		// 提取密钥ID
		keyIDs := make([]uint, len(keys))
		for i, key := range keys {
			keyIDs[i] = key.ID
		}

		// 添加到分层池
		if err := p.layeredPool.AddKeys(group.ID, keyIDs); err != nil {
			logrus.WithFields(logrus.Fields{
				"groupID": group.ID,
				"keyCount": len(keyIDs),
				"error":   err,
			}).Error("Failed to add keys to layered pool")
			continue
		}

		logrus.WithFields(logrus.Fields{
			"groupID":  group.ID,
			"keyCount": len(keyIDs),
		}).Info("Loaded keys to layered pool")
	}

	return nil
}

// GetPoolStats 获取池统计信息
func (p *EnhancedKeyProvider) GetPoolStats(groupID uint) (*PoolStats, error) {
	if p.useLayeredPool && p.layeredPool != nil {
		return p.layeredPool.GetPoolStats(groupID)
	}

	return nil, fmt.Errorf("layered pool not enabled")
}

// SwitchToLayeredPool 切换到分层池模式
func (p *EnhancedKeyProvider) SwitchToLayeredPool(config *EnhancedProviderConfig) error {
	if p.useLayeredPool {
		return fmt.Errorf("already using layered pool")
	}

	// 初始化分层池
	if err := p.initializeLayeredPool(config); err != nil {
		return err
	}

	// 迁移现有数据
	if err := p.loadKeysToLayeredPool(); err != nil {
		return fmt.Errorf("failed to migrate keys to layered pool: %w", err)
	}

	p.useLayeredPool = true
	logrus.Info("Switched to layered pool mode")
	return nil
}

// SwitchToLegacyMode 切换到传统模式
func (p *EnhancedKeyProvider) SwitchToLegacyMode() error {
	if !p.useLayeredPool {
		return fmt.Errorf("already using legacy mode")
	}

	// 停止分层池
	if p.layeredPool != nil {
		if err := p.layeredPool.Stop(); err != nil {
			logrus.WithError(err).Warn("Failed to stop layered pool gracefully")
		}
		p.layeredPool = nil
	}

	p.useLayeredPool = false
	logrus.Info("Switched to legacy mode")
	return nil
}

// Close 关闭提供者
func (p *EnhancedKeyProvider) Close() error {
	if p.layeredPool != nil {
		return p.layeredPool.Stop()
	}
	return nil
}

// IsUsingLayeredPool 检查是否使用分层池
func (p *EnhancedKeyProvider) IsUsingLayeredPool() bool {
	return p.useLayeredPool && p.layeredPool != nil
}

// GetLegacyProvider 获取内部的传统提供者
func (p *EnhancedKeyProvider) GetLegacyProvider() *KeyProvider {
	return p.legacyProvider
}

// DefaultEventHandler 默认事件处理器
type DefaultEventHandler struct{}

func (h *DefaultEventHandler) HandleEvent(event *KeyPoolEvent) error {
	logrus.WithFields(logrus.Fields{
		"type":      event.Type,
		"groupID":   event.GroupID,
		"keyID":     event.KeyID,
		"poolType":  event.PoolType,
		"message":   event.Message,
		"timestamp": event.Timestamp,
	}).Debug("Pool event")
	return nil
}

// pluckIDs 提取密钥ID
func pluckIDs(keys []models.APIKey) []uint {
	ids := make([]uint, len(keys))
	for i, key := range keys {
		ids[i] = key.ID
	}
	return ids
}
