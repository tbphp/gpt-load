package db

import "gorm.io/gorm"

// RequestLog 用于迁移的临时结构体
type RequestLog struct {
	Retries int `gorm:"column:retries"`
}

// V1_0_22_DropRetriesColumn 删除request_logs表的retries字段
func V1_0_22_DropRetriesColumn(db *gorm.DB) error {
	// 检查retries列是否存在
	if db.Migrator().HasColumn(&RequestLog{}, "retries") {
		// 删除retries列
		if err := db.Migrator().DropColumn(&RequestLog{}, "retries"); err != nil {
			return err
		}
	}
	return nil
}
