package migrations

import (
	"fmt"
	"strings"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

const (
	// ID0003 removes the retired time-based freshness marker from credential observations.
	ID0003 = "0003_remove_observation_fresh_until"
)

const (
	credentialObservationTable0003      = "credential_observations"
	credentialObservationFreshUntil0003 = "fresh_until_ms"
	credentialObservationFreshCheck0003 = "chk_credential_observation_fresh_until"
)

type credentialObservation0003 struct {
	CredentialID uint   `gorm:"column:credential_id;primaryKey"`
	FreshUntilMS *int64 `gorm:"column:fresh_until_ms;check:chk_credential_observation_fresh_until,fresh_until_ms IS NULL OR fresh_until_ms >= 0"`
}

func (credentialObservation0003) TableName() string {
	return credentialObservationTable0003
}

// Up0003 physically removes the retired observation freshness column.
func Up0003(db *gorm.DB) error {
	if !db.Migrator().HasTable(&credentialObservation0003{}) {
		return fmt.Errorf("remove observation freshness: table %q is missing", credentialObservationTable0003)
	}
	if !db.Migrator().HasColumn(&credentialObservation0003{}, credentialObservationFreshUntil0003) {
		return nil
	}
	if strings.EqualFold(db.Dialector.Name(), "sqlite") {
		return rebuildSQLiteCredentialObservations0003(db)
	}
	if strings.EqualFold(db.Dialector.Name(), "mysql") &&
		db.Migrator().HasConstraint(&credentialObservation0003{}, credentialObservationFreshCheck0003) {
		if err := dropObservationFreshnessCheck0003(db); err != nil {
			return fmt.Errorf("remove observation freshness check constraint: %w", err)
		}
	}
	if err := db.Migrator().DropColumn(&credentialObservation0003{}, credentialObservationFreshUntil0003); err != nil {
		return fmt.Errorf("remove observation freshness column: %w", err)
	}
	return nil
}

func dropObservationFreshnessCheck0003(db *gorm.DB) error {
	if dialector, ok := db.Dialector.(*gormmysql.Dialector); ok &&
		dialector.Config != nil &&
		mysqlRequiresCheckDropSyntax0003(dialector.ServerVersion) {
		return db.Exec(
			"ALTER TABLE `credential_observations` DROP CHECK `chk_credential_observation_fresh_until`",
		).Error
	}
	return db.Migrator().DropConstraint(&credentialObservation0003{}, credentialObservationFreshCheck0003)
}

func mysqlRequiresCheckDropSyntax0003(serverVersion string) bool {
	var major, minor, patch int
	if _, err := fmt.Sscanf(serverVersion, "%d.%d.%d", &major, &minor, &patch); err != nil {
		return false
	}
	return major == 8 && minor == 0 && patch >= 16 && patch < 19
}

func rebuildSQLiteCredentialObservations0003(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE credential_observations__0003 (
			credential_id integer PRIMARY KEY AUTOINCREMENT NOT NULL,
			identity_fingerprint varchar(128) NOT NULL,
			schema_version integer NOT NULL DEFAULT 1,
			observation_version integer NOT NULL DEFAULT 1,
			snapshot_json json NOT NULL,
			state varchar(32) NOT NULL,
			observed_at_ms integer,
			last_attempt_at_ms integer,
			next_allowed_at_ms integer,
			last_auth_refresh_secret_version integer,
			last_error_code varchar(64) NOT NULL DEFAULT '',
			updated_at_ms integer NOT NULL,
			CONSTRAINT fk_credential_observations_credential FOREIGN KEY (credential_id)
				REFERENCES credentials(id) ON DELETE CASCADE ON UPDATE CASCADE,
			CONSTRAINT chk_credential_observation_schema CHECK (schema_version > 0),
			CONSTRAINT chk_credential_observation_version CHECK (observation_version > 0),
			CONSTRAINT chk_credential_observation_state CHECK
				(state IN ('fresh','stale','refreshing','error','unavailable')),
			CONSTRAINT chk_credential_observation_observed_at CHECK
				(observed_at_ms IS NULL OR observed_at_ms >= 0),
			CONSTRAINT chk_credential_observation_last_attempt CHECK
				(last_attempt_at_ms IS NULL OR last_attempt_at_ms >= 0),
			CONSTRAINT chk_credential_observation_next_allowed CHECK
				(next_allowed_at_ms IS NULL OR next_allowed_at_ms >= 0),
			CONSTRAINT chk_credential_observation_updated_at CHECK (updated_at_ms >= 0)
		)`,
		`INSERT INTO credential_observations__0003 (
			credential_id, identity_fingerprint, schema_version, observation_version,
			snapshot_json, state, observed_at_ms, last_attempt_at_ms, next_allowed_at_ms,
			last_auth_refresh_secret_version, last_error_code, updated_at_ms
		) SELECT
			credential_id, identity_fingerprint, schema_version, observation_version,
			snapshot_json, state, observed_at_ms, last_attempt_at_ms, next_allowed_at_ms,
			last_auth_refresh_secret_version, last_error_code, updated_at_ms
		FROM credential_observations`,
		`DROP TABLE credential_observations`,
		`ALTER TABLE credential_observations__0003 RENAME TO credential_observations`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("rebuild SQLite credential observations: %w", err)
		}
	}
	return nil
}

// ValidateRecoverable0003 accepts the complete schemas before and after the
// migration, plus the MySQL state after its first non-transactional DDL.
func ValidateRecoverable0003(db *gorm.DB) error {
	if db.Migrator().HasColumn(&credentialObservation0003{}, credentialObservationFreshUntil0003) &&
		!db.Migrator().HasConstraint(&credentialObservation0003{}, credentialObservationFreshCheck0003) {
		return validateInitialSchemaAfter0003(db)
	}
	return ValidateCurrent0001(db)
}

// Validate0003 verifies that the retired column is absent and the remaining
// initial schema is intact.
func Validate0003(db *gorm.DB) error {
	if db.Migrator().HasColumn(&credentialObservation0003{}, credentialObservationFreshUntil0003) {
		return fmt.Errorf(
			"validate observation freshness removal: column %q.%q still exists",
			credentialObservationTable0003,
			credentialObservationFreshUntil0003,
		)
	}
	return validateInitialSchemaAfter0003(db)
}

// ValidateCurrent0001 preserves the frozen 0001 validator before 0003 removes
// the freshness check constraint, and validates the same schema without it
// once removed. MySQL drops the constraint and the column as two separate,
// non-transactional statements, so a crash can leave the column present with
// the constraint already gone; the constraint's absence, not the column's,
// is therefore the correct signal that 0003 has progressed past this point.
func ValidateCurrent0001(db *gorm.DB) error {
	if db.Migrator().HasConstraint(&credentialObservation0003{}, credentialObservationFreshCheck0003) {
		return Validate0001(db)
	}
	return validateInitialSchemaAfter0003(db)
}

func validateInitialSchemaAfter0003(db *gorm.DB) error {
	definitions, err := initialSchemaDefinitions(db)
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		if !db.Migrator().HasTable(definition.model) {
			return fmt.Errorf("validate current initial schema: table %q is missing", definition.table)
		}
		for column := range definition.columns {
			if definition.table == credentialObservationTable0003 &&
				strings.EqualFold(column, credentialObservationFreshUntil0003) {
				continue
			}
			if !db.Migrator().HasColumn(definition.model, column) {
				return fmt.Errorf(
					"validate current initial schema: column %q.%q is missing",
					definition.table,
					column,
				)
			}
		}
		for _, index := range definition.indexes {
			if !db.Migrator().HasIndex(definition.model, index) {
				return fmt.Errorf(
					"validate current initial schema: index %q on %q is missing",
					index,
					definition.table,
				)
			}
		}
		for _, constraint := range definition.constraints {
			if definition.table == credentialObservationTable0003 &&
				strings.EqualFold(constraint, credentialObservationFreshCheck0003) {
				continue
			}
			if !db.Migrator().HasConstraint(definition.model, constraint) {
				return fmt.Errorf(
					"validate current initial schema: constraint %q on %q is missing",
					constraint,
					definition.table,
				)
			}
		}
	}
	return nil
}
