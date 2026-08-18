package migrations

import (
	"database/sql"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0009 = "0009_device_oauth"

const credentialStageAuthorizationConstraint0009 = "chk_credential_stage_authorization_method"

type credentialStageAuthorization0009 struct {
	AuthorizationMethod string `gorm:"column:authorization_method;check:chk_credential_stage_authorization_method,authorization_method IN ('browser_oauth','device_oauth','oauth_file')"`
}

func (credentialStageAuthorization0009) TableName() string { return "credential_stages" }

// Up0009 extends the durable authorization method contract for RFC 8628 device
// OAuth while retaining all existing credential stages and indexes.
func Up0009(db *gorm.DB) error {
	if err := validateCredentialStagesTable0009(db); err != nil {
		return err
	}
	if definition, err := credentialStageAuthorizationConstraintDefinition0009(db); err == nil &&
		strings.Contains(strings.ToLower(definition), "device_oauth") {
		return Validate0009(db)
	}
	if strings.EqualFold(db.Dialector.Name(), "sqlite") {
		if err := rebuildCredentialStagesSQLite0009(db); err != nil {
			return err
		}
		return Validate0009(db)
	}
	model := &credentialStageAuthorization0009{}
	if db.Migrator().HasConstraint(model, credentialStageAuthorizationConstraint0009) {
		if err := db.Migrator().DropConstraint(model, credentialStageAuthorizationConstraint0009); err != nil {
			return fmt.Errorf("drop credential stage authorization constraint: %w", err)
		}
	}
	if err := db.Migrator().CreateConstraint(model, credentialStageAuthorizationConstraint0009); err != nil {
		return fmt.Errorf("create credential stage authorization constraint: %w", err)
	}
	return Validate0009(db)
}

func rebuildCredentialStagesSQLite0009(db *gorm.DB) error {
	const temporaryTable = "credential_stages_0009"
	if err := db.Exec(`DROP TABLE IF EXISTS credential_stages_0009`).Error; err != nil {
		return fmt.Errorf("drop stale SQLite credential stages table: %w", err)
	}
	if err := db.Exec(`CREATE TABLE credential_stages_0009 (
		id varchar(36) NOT NULL,
		channel_id varchar(64) NOT NULL,
		connection_type varchar(32) NOT NULL,
		authorization_method varchar(32) NOT NULL,
		status varchar(32) NOT NULL,
		encrypted_payload text NOT NULL,
		payload_schema_version integer NOT NULL DEFAULT 1,
		safe_summary_json json NOT NULL,
		identity_fingerprint varchar(128) NOT NULL DEFAULT '',
		oauth_state_hash varchar(128),
		expires_at_ms integer NOT NULL,
		consumed_at_ms integer,
		consumed_group_id integer,
		error_code varchar(64) NOT NULL DEFAULT '',
		created_at_ms integer NOT NULL,
		updated_at_ms integer NOT NULL,
		PRIMARY KEY (id),
		CONSTRAINT chk_credential_stage_updated_at CHECK (updated_at_ms >= 0),
		CONSTRAINT chk_credential_stage_payload_schema CHECK (payload_schema_version > 0),
		CONSTRAINT chk_credential_stage_consumed_at CHECK (consumed_at_ms IS NULL OR consumed_at_ms >= 0),
		CONSTRAINT chk_credential_stage_expires_at CHECK (expires_at_ms >= 0),
		CONSTRAINT chk_credential_stage_created_at CHECK (created_at_ms >= 0),
		CONSTRAINT chk_credential_stage_connection_type CHECK (connection_type = 'subscription'),
		CONSTRAINT chk_credential_stage_authorization_method CHECK (authorization_method IN ('browser_oauth','device_oauth','oauth_file')),
		CONSTRAINT chk_credential_stage_status CHECK (status IN ('pending_authorization','exchanging','ready','consumed','failed','cancelled','expired','outcome_unknown'))
	)`).Error; err != nil {
		return fmt.Errorf("create SQLite credential stages replacement: %w", err)
	}
	columns := `id, channel_id, connection_type, authorization_method, status,
		encrypted_payload, payload_schema_version, safe_summary_json, identity_fingerprint,
		oauth_state_hash, expires_at_ms, consumed_at_ms, consumed_group_id, error_code,
		created_at_ms, updated_at_ms`
	if err := db.Exec(fmt.Sprintf(
		"INSERT INTO %s (%s) SELECT %s FROM credential_stages",
		temporaryTable, columns, columns,
	)).Error; err != nil {
		return fmt.Errorf("copy SQLite credential stages: %w", err)
	}
	if err := db.Exec(`DROP TABLE credential_stages`).Error; err != nil {
		return fmt.Errorf("drop old SQLite credential stages: %w", err)
	}
	if err := db.Exec(`ALTER TABLE credential_stages_0009 RENAME TO credential_stages`).Error; err != nil {
		return fmt.Errorf("rename SQLite credential stages replacement: %w", err)
	}
	for _, statement := range []string{
		`CREATE UNIQUE INDEX idx_credential_stages_oauth_state ON credential_stages (oauth_state_hash)`,
		`CREATE INDEX idx_credential_stages_status_expires ON credential_stages (status, expires_at_ms, updated_at_ms)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("restore SQLite credential stage index: %w", err)
		}
	}
	return nil
}

func ValidateRecoverable0009(db *gorm.DB) error {
	if err := validateCredentialStagesTable0009(db); err != nil {
		return err
	}
	if !db.Migrator().HasConstraint(&credentialStageAuthorization0009{}, credentialStageAuthorizationConstraint0009) {
		return nil
	}
	definition, err := credentialStageAuthorizationConstraintDefinition0009(db)
	if err != nil {
		return err
	}
	if strings.Contains(strings.ToLower(definition), "device_oauth") {
		return nil
	}
	// The frozen 0002 constraint is a safe, known predecessor which Up0009 can replace.
	lower := strings.ToLower(definition)
	if strings.Contains(lower, "browser_oauth") && strings.Contains(lower, "oauth_file") {
		return nil
	}
	return fmt.Errorf("credential stage authorization constraint is not recoverable")
}

func Validate0009(db *gorm.DB) error {
	if err := validateCredentialStagesTable0009(db); err != nil {
		return err
	}
	if !db.Migrator().HasConstraint(&credentialStageAuthorization0009{}, credentialStageAuthorizationConstraint0009) {
		return fmt.Errorf("credential stage authorization constraint is missing")
	}
	definition, err := credentialStageAuthorizationConstraintDefinition0009(db)
	if err != nil {
		return err
	}
	lower := strings.ToLower(definition)
	for _, method := range []string{"browser_oauth", "device_oauth", "oauth_file"} {
		if !strings.Contains(lower, method) {
			return fmt.Errorf("credential stage authorization constraint does not allow %q", method)
		}
	}
	return nil
}

func validateCredentialStagesTable0009(db *gorm.DB) error {
	if db == nil || db.Dialector == nil || !db.Migrator().HasTable("credential_stages") {
		return fmt.Errorf("credential stages table is missing")
	}
	return nil
}

func credentialStageAuthorizationConstraintDefinition0009(db *gorm.DB) (string, error) {
	var definition sql.NullString
	switch strings.ToLower(db.Dialector.Name()) {
	case "sqlite":
		if err := db.Raw("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?", "credential_stages").Scan(&definition).Error; err != nil {
			return "", fmt.Errorf("inspect SQLite credential stage constraint: %w", err)
		}
	case "mysql":
		if err := db.Raw(
			"SELECT CHECK_CLAUSE FROM information_schema.CHECK_CONSTRAINTS WHERE CONSTRAINT_SCHEMA = DATABASE() AND CONSTRAINT_NAME = ? LIMIT 1",
			credentialStageAuthorizationConstraint0009,
		).Scan(&definition).Error; err != nil {
			return "", fmt.Errorf("inspect MySQL credential stage constraint: %w", err)
		}
	case "postgres", "postgresql":
		if err := db.Raw(`
SELECT pg_get_constraintdef(c.oid)
FROM pg_constraint c
JOIN pg_class t ON t.oid = c.conrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE c.conname = ? AND t.relname = ? AND n.nspname = current_schema()
LIMIT 1`, credentialStageAuthorizationConstraint0009, "credential_stages").Scan(&definition).Error; err != nil {
			return "", fmt.Errorf("inspect PostgreSQL credential stage constraint: %w", err)
		}
	default:
		return "", fmt.Errorf("unsupported database driver %q", db.Dialector.Name())
	}
	if !definition.Valid || strings.TrimSpace(definition.String) == "" {
		return "", fmt.Errorf("credential stage authorization constraint definition is missing")
	}
	return definition.String, nil
}
