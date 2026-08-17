package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0008 = "0008_observation_auth_refresh"

const (
	credentialObservationsTable            = "credential_observations"
	lastAuthRefreshSecretVersionColumn0008 = "last_auth_refresh_secret_version"
)

// Up0008 records the credential secret version already force-refreshed after
// an observation authorization failure, preventing repeated token rotation.
func Up0008(db *gorm.DB) error {
	if err := validateObservationTable0008(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn(credentialObservationsTable, lastAuthRefreshSecretVersionColumn0008) {
		if err := db.Exec(fmt.Sprintf(
			"ALTER TABLE %s ADD COLUMN %s BIGINT NULL",
			quoteIdentifier0008(db, credentialObservationsTable),
			quoteIdentifier0008(db, lastAuthRefreshSecretVersionColumn0008),
		)).Error; err != nil {
			return fmt.Errorf("add observation auth refresh version: %w", err)
		}
	}
	return Validate0008(db)
}

func ValidateRecoverable0008(db *gorm.DB) error {
	if err := validateObservationTable0008(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn(credentialObservationsTable, lastAuthRefreshSecretVersionColumn0008) {
		return nil
	}
	return validateAuthRefreshSecretVersionColumn0008(db)
}

func Validate0008(db *gorm.DB) error {
	if err := validateObservationTable0008(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn(credentialObservationsTable, lastAuthRefreshSecretVersionColumn0008) {
		return fmt.Errorf("validate observation auth refresh schema: column %q is missing", lastAuthRefreshSecretVersionColumn0008)
	}
	return validateAuthRefreshSecretVersionColumn0008(db)
}

func validateObservationTable0008(db *gorm.DB) error {
	if db == nil || db.Dialector == nil || !db.Migrator().HasTable(credentialObservationsTable) {
		return fmt.Errorf("validate observation auth refresh schema: table %q is missing", credentialObservationsTable)
	}
	return nil
}

func validateAuthRefreshSecretVersionColumn0008(db *gorm.DB) error {
	columns, err := db.Migrator().ColumnTypes(credentialObservationsTable)
	if err != nil {
		return fmt.Errorf("inspect observation auth refresh version column: %w", err)
	}
	for _, column := range columns {
		if !strings.EqualFold(column.Name(), lastAuthRefreshSecretVersionColumn0008) {
			continue
		}
		databaseType := strings.ToLower(strings.TrimSpace(column.DatabaseTypeName()))
		if !strings.Contains(databaseType, "int") {
			return fmt.Errorf("validate observation auth refresh version column: type %q is not integer", column.DatabaseTypeName())
		}
		nullable, known := column.Nullable()
		if !known || !nullable {
			return fmt.Errorf("validate observation auth refresh version column: nullable is %t (known=%t), want true", nullable, known)
		}
		return nil
	}
	return fmt.Errorf("validate observation auth refresh version column: column %q is missing", lastAuthRefreshSecretVersionColumn0008)
}

func quoteIdentifier0008(db *gorm.DB, value string) string {
	if db != nil && db.Dialector != nil && strings.EqualFold(db.Dialector.Name(), "mysql") {
		return "`" + value + "`"
	}
	return `"` + value + `"`
}
