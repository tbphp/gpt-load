package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0002 = "0002_subscription_connections"

type subscriptionCredentialStage struct {
	ID                   string      `gorm:"type:varchar(36);primaryKey;not null"`
	ChannelID            string      `gorm:"type:varchar(64);not null"`
	ConnectionType       string      `gorm:"type:varchar(32);not null;check:chk_credential_stage_connection_type,connection_type = 'subscription'"`
	AuthorizationMethod  string      `gorm:"type:varchar(32);not null;check:chk_credential_stage_authorization_method,authorization_method IN ('browser_oauth','oauth_file')"`
	Status               string      `gorm:"type:varchar(32);not null;check:chk_credential_stage_status,status IN ('pending_authorization','exchanging','ready','consumed','failed','cancelled','expired','outcome_unknown');index:idx_credential_stages_status_expires,priority:1"`
	EncryptedPayload     string      `gorm:"type:text;not null"`
	PayloadSchemaVersion uint        `gorm:"not null;default:1;check:chk_credential_stage_payload_schema,payload_schema_version > 0"`
	SafeSummaryJSON      initialJSON `gorm:"column:safe_summary_json;type:json;not null"`
	IdentityFingerprint  string      `gorm:"type:varchar(128);not null;default:''"`
	OAuthStateHash       *string     `gorm:"column:oauth_state_hash;type:varchar(128);uniqueIndex:idx_credential_stages_oauth_state"`
	ExpiresAtMS          int64       `gorm:"column:expires_at_ms;not null;check:chk_credential_stage_expires_at,expires_at_ms >= 0;index:idx_credential_stages_status_expires,priority:2"`
	ConsumedAtMS         *int64      `gorm:"column:consumed_at_ms;check:chk_credential_stage_consumed_at,consumed_at_ms IS NULL OR consumed_at_ms >= 0"`
	ConsumedGroupID      *uint
	ErrorCode            string `gorm:"type:varchar(64);not null;default:''"`
	CreatedAtMS          int64  `gorm:"column:created_at_ms;not null;check:chk_credential_stage_created_at,created_at_ms >= 0"`
	UpdatedAtMS          int64  `gorm:"column:updated_at_ms;not null;check:chk_credential_stage_updated_at,updated_at_ms >= 0;index:idx_credential_stages_status_expires,priority:3"`
}

func (subscriptionCredentialStage) TableName() string { return "credential_stages" }

type subscriptionCredentialObservation struct {
	CredentialID        uint               `gorm:"primaryKey;not null"`
	Credential          *initialCredential `gorm:"foreignKey:CredentialID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	IdentityFingerprint string             `gorm:"type:varchar(128);not null"`
	SchemaVersion       uint               `gorm:"not null;default:1;check:chk_credential_observation_schema,schema_version > 0"`
	ObservationVersion  uint64             `gorm:"not null;default:1;check:chk_credential_observation_version,observation_version > 0"`
	SnapshotJSON        initialJSON        `gorm:"column:snapshot_json;type:json;not null"`
	State               string             `gorm:"type:varchar(32);not null;check:chk_credential_observation_state,state IN ('fresh','stale','refreshing','error','unavailable')"`
	ObservedAtMS        *int64             `gorm:"column:observed_at_ms;check:chk_credential_observation_observed_at,observed_at_ms IS NULL OR observed_at_ms >= 0"`
	FreshUntilMS        *int64             `gorm:"column:fresh_until_ms;check:chk_credential_observation_fresh_until,fresh_until_ms IS NULL OR fresh_until_ms >= 0"`
	LastAttemptAtMS     *int64             `gorm:"column:last_attempt_at_ms;check:chk_credential_observation_last_attempt,last_attempt_at_ms IS NULL OR last_attempt_at_ms >= 0"`
	NextAllowedAtMS     *int64             `gorm:"column:next_allowed_at_ms;check:chk_credential_observation_next_allowed,next_allowed_at_ms IS NULL OR next_allowed_at_ms >= 0"`
	LastErrorCode       string             `gorm:"type:varchar(64);not null;default:''"`
	UpdatedAtMS         int64              `gorm:"column:updated_at_ms;not null;check:chk_credential_observation_updated_at,updated_at_ms >= 0"`
}

func (subscriptionCredentialObservation) TableName() string { return "credential_observations" }

// Up0002 extends the frozen v2 schema without changing any 0001 definition.
// Every operation is idempotent so MySQL can safely resume after implicit DDL commits.
func Up0002(db *gorm.DB) error {
	for _, column := range subscriptionColumns0002() {
		if db.Migrator().HasColumn(column.table, column.name) {
			continue
		}
		if err := db.Exec(column.sql(db.Dialector.Name())).Error; err != nil {
			return fmt.Errorf("add %s.%s: %w", column.table, column.name, err)
		}
	}
	if err := db.Exec(`UPDATE credentials
		SET identity_fingerprint = fingerprint
		WHERE identity_fingerprint = '' OR identity_fingerprint IS NULL`).Error; err != nil {
		return fmt.Errorf("backfill credential identity: %w", err)
	}
	if !db.Migrator().HasIndex("credentials", "idx_credentials_group_identity") {
		if err := db.Exec(`CREATE UNIQUE INDEX idx_credentials_group_identity
			ON credentials (group_id, identity_fingerprint)`).Error; err != nil {
			return fmt.Errorf("create credential identity index: %w", err)
		}
	}
	if err := db.AutoMigrate(&subscriptionCredentialStage{}, &subscriptionCredentialObservation{}); err != nil {
		return fmt.Errorf("create subscription tables: %w", err)
	}
	return nil
}

type subscriptionColumn0002 struct {
	table string
	name  string
	base  string
}

func (column subscriptionColumn0002) sql(driver string) string {
	definition := column.base
	if strings.EqualFold(driver, "postgres") || strings.EqualFold(driver, "postgresql") {
		definition = strings.ReplaceAll(definition, "varchar", "varchar")
	}
	return fmt.Sprintf(
		"ALTER TABLE %s ADD COLUMN %s %s",
		quoteSubscriptionIdentifier(driver, column.table),
		quoteSubscriptionIdentifier(driver, column.name),
		definition,
	)
}

func quoteSubscriptionIdentifier(driver string, value string) string {
	if strings.EqualFold(driver, "mysql") {
		return "`" + value + "`"
	}
	return `"` + value + `"`
}

func subscriptionColumns0002() []subscriptionColumn0002 {
	return []subscriptionColumn0002{
		{table: "groups", name: "connection_type", base: "varchar(32) NOT NULL DEFAULT 'api_key' CHECK (connection_type IN ('api_key','subscription'))"},
		{table: "credentials", name: "identity_fingerprint", base: "varchar(128) NOT NULL DEFAULT ''"},
		{table: "credentials", name: "secret_version", base: "bigint NOT NULL DEFAULT 1 CHECK (secret_version > 0)"},
		{table: "credentials", name: "auth_state", base: "varchar(32) NOT NULL DEFAULT 'ready' CHECK (auth_state IN ('ready','refreshing','reauthorization_required','outcome_unknown'))"},
		{table: "credentials", name: "auth_error_code", base: "varchar(64) NOT NULL DEFAULT ''"},
	}
}

func ValidateRecoverable0002(db *gorm.DB) error {
	for _, table := range []string{"groups", "credentials"} {
		if !db.Migrator().HasTable(table) {
			return fmt.Errorf("required baseline table %q is missing", table)
		}
	}
	return nil
}

func Validate0002(db *gorm.DB) error {
	for _, column := range subscriptionColumns0002() {
		if !db.Migrator().HasColumn(column.table, column.name) {
			return fmt.Errorf("validate subscription schema: column %q.%q is missing", column.table, column.name)
		}
	}
	if !db.Migrator().HasIndex("credentials", "idx_credentials_group_identity") {
		return fmt.Errorf("validate subscription schema: credential identity index is missing")
	}
	for _, table := range []any{&subscriptionCredentialStage{}, &subscriptionCredentialObservation{}} {
		if !db.Migrator().HasTable(table) {
			return fmt.Errorf("validate subscription schema: table is missing")
		}
	}
	return nil
}
