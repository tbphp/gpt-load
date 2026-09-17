package migrations

import (
	"fmt"

	"gorm.io/gorm"
)

const ID0015 = "0015_group_usage_index"
const requestLogGroupIndex0015 = "idx_request_logs_group_completed"

type requestLog0015 struct {
	GroupID       uint  `gorm:"column:group_id;index:idx_request_logs_group_completed,priority:1"`
	CompletedAtMS int64 `gorm:"column:completed_at_ms;index:idx_request_logs_group_completed,priority:2,sort:desc"`
}

func (requestLog0015) TableName() string { return "request_logs" }

// Up0015 只新增精确窗口查询索引，不改日志、用量或转发语义。
func Up0015(db *gorm.DB) error {
	if err := ValidateRecoverable0015(db); err != nil {
		return err
	}
	if db.Migrator().HasIndex(&requestLog0015{}, requestLogGroupIndex0015) {
		return nil
	}
	return db.Migrator().CreateIndex(&requestLog0015{}, requestLogGroupIndex0015)
}

func ValidateRecoverable0015(db *gorm.DB) error {
	if !db.Migrator().HasTable(&requestLog0015{}) {
		return fmt.Errorf("group usage index: request_logs table is missing")
	}
	return nil
}

func Validate0015(db *gorm.DB) error {
	if err := ValidateRecoverable0015(db); err != nil {
		return err
	}
	if !db.Migrator().HasIndex(&requestLog0015{}, requestLogGroupIndex0015) {
		return fmt.Errorf("group usage index is missing")
	}
	return nil
}
