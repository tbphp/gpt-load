package db

import (
	"gorm.io/gorm"
)

func MigrateDatabase(db *gorm.DB) error {
	// Run v1.0.22 migration
	if err := V1_0_22_DropRetriesColumn(db); err != nil {
		return err
	}

	// Run v1.1.0 migration
	return V1_1_0_AddKeyHashColumn(db)
}

// HandleLegacyIndexes removes old indexes from previous versions to prevent migration errors
func HandleLegacyIndexes(db *gorm.DB) {
	if db.Dialector.Name() == "mysql" {
		db.Exec("ALTER TABLE api_keys DROP INDEX idx_group_key")
		db.Exec("ALTER TABLE api_keys DROP INDEX idx_api_keys_group_id_key_value")
	} else {

		db.Exec("DROP INDEX IF EXISTS idx_group_key")
		db.Exec("DROP INDEX IF EXISTS idx_api_keys_group_id_key_value")
	}
}
