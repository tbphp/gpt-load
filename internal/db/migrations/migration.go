package db

import (
	"gorm.io/gorm"
)

func MigrateDatabase(db *gorm.DB) error {
	// v1.0.13 Fix request log data
	return V1_0_13_FixRequestLogs(db)
}
