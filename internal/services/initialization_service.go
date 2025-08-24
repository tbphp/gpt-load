package services

import (
	"fmt"

	"gpt-load/internal/models"
	"gpt-load/internal/utils"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// InitializationService handles first-time setup
type InitializationService struct {
	db *gorm.DB
}

// NewInitializationService creates a new initialization service
func NewInitializationService(db *gorm.DB) *InitializationService {
	return &InitializationService{
		db: db,
	}
}

// CheckAndSetupAuth checks if auth key is set up, returns empty string if setup is needed
func (s *InitializationService) CheckAndSetupAuth() (string, error) {
	// Check if auth key already exists in database
	var setting models.SystemSetting
	err := s.db.Where("setting_key = ?", "auth_key").First(&setting).Error
	if err == nil {
		// Auth key exists, return it
		logrus.Info("管理员密码已配置")
		return setting.SettingValue, nil
	}

	if err != gorm.ErrRecordNotFound {
		return "", fmt.Errorf("failed to check auth key: %w", err)
	}

	// Auth key doesn't exist, need to set up via web interface
	logrus.Info("🔐 首次启动检测到，需要通过 Web 界面设置管理员密码")
	logrus.Info("请访问 Web 管理界面完成初始化设置")

	// Return empty string to indicate setup is needed
	return "", nil
}

// SetInitialPassword sets the initial admin password (called from web interface)
func (s *InitializationService) SetInitialPassword(password string) error {
	// Check if auth key already exists
	var count int64
	err := s.db.Model(&models.SystemSetting{}).Where("setting_key = ?", "auth_key").Count(&count).Error
	if err != nil {
		return fmt.Errorf("failed to check existing auth key: %w", err)
	}

	if count > 0 {
		return fmt.Errorf("管理员密码已经设置，无法重复设置")
	}

	// Validate password strength
	validation := utils.CheckPasswordStrength(password)
	if !validation.IsValid {
		return fmt.Errorf("密码强度不足: %s", validation.Message)
	}

	// Hash password
	hashedPassword, err := utils.HashPassword(password)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	// Save to database
	authSetting := models.SystemSetting{
		SettingKey:   "auth_key",
		SettingValue: hashedPassword,
		Description:  "管理员认证密钥哈希",
	}

	if err := s.db.Create(&authSetting).Error; err != nil {
		return fmt.Errorf("failed to save auth key: %w", err)
	}

	logrus.Info("管理员密码设置成功")
	return nil
}

// IsFirstTimeSetup checks if this is the first time setup
func (s *InitializationService) IsFirstTimeSetup() (bool, error) {
	var count int64
	err := s.db.Model(&models.SystemSetting{}).Where("setting_key = ?", "auth_key").Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("failed to check first time setup: %w", err)
	}
	return count == 0, nil
}

// GetAuthKey returns the stored auth key hash
func (s *InitializationService) GetAuthKey() (string, error) {
	var setting models.SystemSetting
	err := s.db.Where("setting_key = ?", "auth_key").First(&setting).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", nil
		}
		return "", fmt.Errorf("failed to get auth key: %w", err)
	}
	return setting.SettingValue, nil
}
