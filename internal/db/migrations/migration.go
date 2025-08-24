package db

import (
	"gorm.io/gorm"
)

func MigrateDatabase(db *gorm.DB) error {
	return V1_0_22_DropRetriesColumn(db)
}
