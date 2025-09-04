package db

import (
	"gorm.io/gorm"
)

func MigrateDatabase(db *gorm.DB, encryptionKey string) error {
	// Run v1.0.22 migration
	if err := V1_0_22_DropRetriesColumn(db); err != nil {
		return err
	}

	// Run v1.1.0 migration
	return V1_1_0_AddKeyHashColumn(db, encryptionKey)
}
