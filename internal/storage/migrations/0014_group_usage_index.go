package migrations

import (
	"fmt"

	"gorm.io/gorm"
)

const ID0014 = "0014_group_usage_index"
const requestLogGroupIndex0014 = "idx_request_logs_group_completed"

type requestLog0014 struct {
	GroupID       uint  `gorm:"column:group_id;index:idx_request_logs_group_completed,priority:1"`
	CompletedAtMS int64 `gorm:"column:completed_at_ms;index:idx_request_logs_group_completed,priority:2,sort:desc"`
}

func (requestLog0014) TableName() string { return "request_logs" }

// Up0014 只新增精确窗口查询索引，不改日志、用量或转发语义。
func Up0014(db *gorm.DB) error {
	if err := ValidateRecoverable0014(db); err != nil {
		return err
	}
	if db.Migrator().HasIndex(&requestLog0014{}, requestLogGroupIndex0014) {
		return nil
	}
	return db.Migrator().CreateIndex(&requestLog0014{}, requestLogGroupIndex0014)
}

func ValidateRecoverable0014(db *gorm.DB) error {
	if !db.Migrator().HasTable(&requestLog0014{}) {
		return fmt.Errorf("group usage index: request_logs table is missing")
	}
	return nil
}

func Validate0014(db *gorm.DB) error {
	if err := ValidateRecoverable0014(db); err != nil {
		return err
	}
	if !db.Migrator().HasIndex(&requestLog0014{}, requestLogGroupIndex0014) {
		return fmt.Errorf("group usage index is missing")
	}
	return nil
}
