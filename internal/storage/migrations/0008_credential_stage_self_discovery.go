package migrations

import (
	"fmt"
	"strings"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

const ID0008 = "0008_credential_stage_self_discovery"

const (
	credentialStageTable0008            = "credential_stages"
	authMethodConstraint0008            = "chk_credential_stage_authorization_method"
	authMethodExpression0008            = "authorization_method IN ('browser_oauth','device_oauth','oauth_file','self_discovery')"
	credentialStageStatusExpression0008 = "status IN ('pending_authorization','exchanging','ready','consumed','failed','cancelled','expired','outcome_unknown')"
	credentialStageStateExpression0008  = "connection_type = 'subscription'"
)

// Up0008 broadens the credential stage authorization_method CHECK constraint
// to include self_discovery for databases that applied the 0001 baseline before
// the Kiro self-discovery feature landed. Fresh installs already receive the
// widened constraint from 0001; this migration only repairs pre-existing tables.
func Up0008(db *gorm.DB) error {
	if !db.Migrator().HasTable(credentialStageTable0008) {
		return fmt.Errorf("add credential stage self discovery: table %q is missing", credentialStageTable0008)
	}
	if Validate0008(db) == nil {
		return nil
	}
	if strings.EqualFold(db.Dialector.Name(), "sqlite") {
		return rebuildSQLiteCredentialStage0008(db)
	}
	if db.Migrator().HasConstraint(credentialStageTable0008, authMethodConstraint0008) {
		if err := dropCredentialStageAuthMethodCheck0008(db); err != nil {
			return fmt.Errorf("replace credential stage authorization method constraint: %w", err)
		}
	}
	if err := createCredentialStageAuthMethodCheck0008(db); err != nil {
		return err
	}
	return nil
}

func createCredentialStageAuthMethodCheck0008(db *gorm.DB) error {
	table := quoteMigrationIdentifier0006(db, credentialStageTable0008)
	name := quoteMigrationIdentifier0006(db, authMethodConstraint0008)
	if err := db.Exec(fmt.Sprintf(
		"ALTER TABLE %s ADD CONSTRAINT %s CHECK (%s)", table, name, authMethodExpression0008,
	)).Error; err != nil {
		return fmt.Errorf("create credential stage authorization method constraint: %w", err)
	}
	return nil
}

func dropCredentialStageAuthMethodCheck0008(db *gorm.DB) error {
	if strings.EqualFold(db.Dialector.Name(), "mysql") {
		if dialector, ok := db.Dialector.(*gormmysql.Dialector); ok && dialector.Config != nil &&
			mysqlRequiresCheckDropSyntax0003(dialector.ServerVersion) {
			return db.Exec(fmt.Sprintf(
				"ALTER TABLE `%s` DROP CHECK `%s`", credentialStageTable0008, authMethodConstraint0008,
			)).Error
		}
	}
	return db.Migrator().DropConstraint(credentialStageTable0008, authMethodConstraint0008)
}

func rebuildSQLiteCredentialStage0008(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE credential_stages__0008 (
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
			CONSTRAINT chk_credential_stage_connection_type CHECK (connection_type = 'subscription'),
			CONSTRAINT chk_credential_stage_authorization_method CHECK (authorization_method IN ('browser_oauth','device_oauth','oauth_file','self_discovery')),
			CONSTRAINT chk_credential_stage_status CHECK (status IN ('pending_authorization','exchanging','ready','consumed','failed','cancelled','expired','outcome_unknown')),
			CONSTRAINT chk_credential_stage_payload_schema CHECK (payload_schema_version > 0),
			CONSTRAINT chk_credential_stage_expires_at CHECK (expires_at_ms >= 0),
			CONSTRAINT chk_credential_stage_consumed_at CHECK (consumed_at_ms IS NULL OR consumed_at_ms >= 0),
			CONSTRAINT chk_credential_stage_created_at CHECK (created_at_ms >= 0),
			CONSTRAINT chk_credential_stage_updated_at CHECK (updated_at_ms >= 0)
		)`,
		`INSERT INTO credential_stages__0008 (
			id, channel_id, connection_type, authorization_method, status,
			encrypted_payload, payload_schema_version, safe_summary_json,
			identity_fingerprint, oauth_state_hash, expires_at_ms, consumed_at_ms,
			consumed_group_id, error_code, created_at_ms, updated_at_ms
		) SELECT
			id, channel_id, connection_type, authorization_method, status,
			encrypted_payload, payload_schema_version, safe_summary_json,
			identity_fingerprint, oauth_state_hash, expires_at_ms, consumed_at_ms,
			consumed_group_id, error_code, created_at_ms, updated_at_ms
		FROM credential_stages`,
		`DROP TABLE credential_stages`,
		`ALTER TABLE credential_stages__0008 RENAME TO credential_stages`,
		`CREATE UNIQUE INDEX idx_credential_stages_oauth_state ON credential_stages(oauth_state_hash)`,
		`CREATE INDEX idx_credential_stages_status_expires ON credential_stages(status, expires_at_ms DESC, updated_at_ms DESC)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("rebuild SQLite credential stages: %w", err)
		}
	}
	return nil
}

// ValidateRecoverable0008 accepts the pre- and post-migration table shapes so a
// partially applied rebuild can be resumed safely.
func ValidateRecoverable0008(db *gorm.DB) error {
	if !db.Migrator().HasTable(credentialStageTable0008) {
		return fmt.Errorf("validate recoverable credential stage self discovery: table %q is missing", credentialStageTable0008)
	}
	definition, err := credentialStageAuthMethodDefinition0008(db)
	if err != nil {
		return nil
	}
	if strings.Contains(strings.ToLower(definition), "self_discovery") {
		return nil
	}
	return fmt.Errorf("credential stage authorization method constraint does not include self_discovery")
}

// Validate0008 verifies the widened authorization_method constraint is present.
func Validate0008(db *gorm.DB) error {
	if !db.Migrator().HasTable(credentialStageTable0008) {
		return fmt.Errorf("validate credential stage self discovery: table %q is missing", credentialStageTable0008)
	}
	if !db.Migrator().HasConstraint(credentialStageTable0008, authMethodConstraint0008) {
		return fmt.Errorf("validate credential stage self discovery: constraint %q is missing", authMethodConstraint0008)
	}
	if _, err := credentialStageAuthMethodDefinition0008(db); err != nil {
		return err
	}
	return nil
}

func credentialStageAuthMethodDefinition0008(db *gorm.DB) (string, error) {
	var definition string
	switch strings.ToLower(db.Dialector.Name()) {
	case "sqlite":
		if err := db.Raw(
			"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?",
			credentialStageTable0008,
		).Scan(&definition).Error; err != nil {
			return "", fmt.Errorf("inspect SQLite credential stage constraint: %w", err)
		}
	case "mysql":
		if err := db.Raw(
			"SELECT CHECK_CLAUSE FROM information_schema.check_constraints WHERE constraint_schema = DATABASE() AND constraint_name = ?",
			authMethodConstraint0008,
		).Scan(&definition).Error; err != nil {
			return "", fmt.Errorf("inspect MySQL credential stage constraint: %w", err)
		}
	case "postgres", "postgresql":
		if err := db.Raw(
			"SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = ? AND conrelid = 'credential_stages'::regclass",
			authMethodConstraint0008,
		).Scan(&definition).Error; err != nil {
			return "", fmt.Errorf("inspect PostgreSQL credential stage constraint: %w", err)
		}
	default:
		return "", fmt.Errorf("inspect credential stage constraint: unsupported driver %q", db.Dialector.Name())
	}
	if strings.TrimSpace(definition) == "" {
		return "", fmt.Errorf("credential stage authorization method constraint definition is missing")
	}
	return definition, nil
}

var (
	_ = credentialStageStatusExpression0008
	_ = credentialStageStateExpression0008
)
