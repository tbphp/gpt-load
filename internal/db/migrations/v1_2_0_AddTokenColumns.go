package db

import (
	"gpt-load/internal/models"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// V1_2_0_AddTokenColumns adds token usage columns to request_logs table
func V1_2_0_AddTokenColumns(db *gorm.DB) error {
	if !db.Migrator().HasColumn(&models.RequestLog{}, "prompt_tokens") {
		if err := db.Migrator().AddColumn(&models.RequestLog{}, "prompt_tokens"); err != nil {
			return err
		}
		logrus.Info("Added column prompt_tokens to request_logs")
	}
	if !db.Migrator().HasColumn(&models.RequestLog{}, "completion_tokens") {
		if err := db.Migrator().AddColumn(&models.RequestLog{}, "completion_tokens"); err != nil {
			return err
		}
		logrus.Info("Added column completion_tokens to request_logs")
	}
	if !db.Migrator().HasColumn(&models.RequestLog{}, "total_tokens") {
		if err := db.Migrator().AddColumn(&models.RequestLog{}, "total_tokens"); err != nil {
			return err
		}
		logrus.Info("Added column total_tokens to request_logs")
	}
	if !db.Migrator().HasColumn(&models.RequestLog{}, "token_cost_usd") {
		if err := db.Migrator().AddColumn(&models.RequestLog{}, "token_cost_usd"); err != nil {
			return err
		}
		logrus.Info("Added column token_cost_usd to request_logs")
	}
	return nil
}
