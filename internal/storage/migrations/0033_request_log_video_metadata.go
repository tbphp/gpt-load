package migrations

import (
	"fmt"

	"gorm.io/gorm"
)

const ID0033 = "0033_request_log_video_metadata"

type requestLogVideo0033 struct {
	ID            string `gorm:"type:varchar(36);primaryKey;not null"`
	VideoMetadata string `gorm:"column:video_metadata;type:json"`
}

func (requestLogVideo0033) TableName() string { return "request_logs" }

func Up0033(db *gorm.DB) error {
	if err := ValidateRecoverable0033(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn(&requestLogVideo0033{}, "video_metadata") {
		if err := db.Migrator().AddColumn(&requestLogVideo0033{}, "VideoMetadata"); err != nil {
			return fmt.Errorf("add request log video metadata: %w", err)
		}
	}
	return Validate0033(db)
}

func ValidateRecoverable0033(db *gorm.DB) error {
	if !db.Migrator().HasTable(&requestLogVideo0033{}) {
		return fmt.Errorf("request log video metadata: request_logs table is missing")
	}
	return nil
}

func Validate0033(db *gorm.DB) error {
	model := &requestLogVideo0033{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("request_logs table is missing")
	}
	if !db.Migrator().HasColumn(model, "video_metadata") {
		return fmt.Errorf("request log video metadata column is missing")
	}
	return nil
}
