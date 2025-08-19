package db

import (
	"fmt"
	"gorm.io/gorm"
)

func MigrateDatabase(db *gorm.DB) error {
	// return V1_0_13_FixRequestLogs(db)

	// 执行429相关字段的迁移
	if err := V1_1_0_Add429Fields(db); err != nil {
		return err
	}

	return nil
}

// V1_1_0_Add429Fields 添加429相关字段到api_keys表
func V1_1_0_Add429Fields(db *gorm.DB) error {
	// 检查是否已经执行过此迁移
	migrationKey := "v1.1.0_add_429_fields"
	var count int64
	if err := db.Table("system_settings").Where("setting_key = ?", migrationKey).Count(&count).Error; err != nil {
		return fmt.Errorf("failed to check migration status: %w", err)
	}

	if count > 0 {
		// 迁移已执行，跳过
		return nil
	}

	// 开始事务
	return db.Transaction(func(tx *gorm.DB) error {
		// 尝试添加新字段（分别执行，兼容不同数据库）
		// 添加 rate_limit_count 字段
		if err := tx.Exec(`ALTER TABLE api_keys ADD COLUMN rate_limit_count BIGINT DEFAULT 0`).Error; err != nil {
			// 字段可能已存在，忽略错误
		}

		// 添加 last_429_at 字段
		if err := tx.Exec(`ALTER TABLE api_keys ADD COLUMN last_429_at TIMESTAMP`).Error; err != nil {
			// 字段可能已存在，忽略错误
		}

		// 添加 rate_limit_reset_at 字段
		if err := tx.Exec(`ALTER TABLE api_keys ADD COLUMN rate_limit_reset_at TIMESTAMP`).Error; err != nil {
			// 字段可能已存在，忽略错误
		}

		// 记录迁移已完成
		if err := tx.Exec(`
			INSERT INTO system_settings (setting_key, setting_value, description, created_at, updated_at)
			VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, migrationKey, "true", "429字段迁移标记").Error; err != nil {
			return fmt.Errorf("failed to record migration completion: %w", err)
		}

		return nil
	})
}
