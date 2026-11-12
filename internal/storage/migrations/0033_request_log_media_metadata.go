package migrations

import (
	"fmt"

	"gorm.io/gorm"
)

const ID0033 = "0033_request_log_media_metadata"

type requestLogMedia0033 struct {
	ID            string `gorm:"type:varchar(36);primaryKey;not null"`
	MediaMetadata string `gorm:"column:media_metadata;type:json"`
}

func (requestLogMedia0033) TableName() string { return "request_logs" }

func Up0033(db *gorm.DB) error {
	if err := ValidateRecoverable0033(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn(&requestLogMedia0033{}, "media_metadata") {
		if err := db.Migrator().AddColumn(&requestLogMedia0033{}, "MediaMetadata"); err != nil {
			return fmt.Errorf("add request log media metadata: %w", err)
		}
	}
	return Validate0033(db)
}

func ValidateRecoverable0033(db *gorm.DB) error {
	if !db.Migrator().HasTable(&requestLogMedia0033{}) {
		return fmt.Errorf("request log media metadata: request_logs table is missing")
	}
	return nil
}

func Validate0033(db *gorm.DB) error {
	model := &requestLogMedia0033{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("request_logs table is missing")
	}
	if !db.Migrator().HasColumn(model, "media_metadata") {
		return fmt.Errorf("request log media metadata column is missing")
	}
	return nil
}
