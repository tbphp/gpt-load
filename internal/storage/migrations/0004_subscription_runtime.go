package migrations

import (
	"fmt"

	"gorm.io/gorm"
)

const ID0004 = "0004_subscription_runtime"

type subscriptionResetOperation struct {
	IdempotencyKey  string      `gorm:"column:idempotency_key;type:char(36);primaryKey;not null"`
	RequestDigest   []byte      `gorm:"column:request_digest;not null"`
	GroupID         uint        `gorm:"column:group_id;not null;index:idx_credential_reset_operations_credential,priority:1"`
	CredentialID    uint        `gorm:"column:credential_id;not null;index:idx_credential_reset_operations_credential,priority:2"`
	RedeemRequestID string      `gorm:"column:redeem_request_id;type:char(36);not null;uniqueIndex"`
	State           string      `gorm:"type:varchar(32);not null;check:chk_credential_reset_operation_state,state IN ('prepared','succeeded','rejected','outcome_unknown')"`
	ResultJSON      initialJSON `gorm:"column:result_json;type:json"`
	ErrorCode       string      `gorm:"column:error_code;type:varchar(64);not null;default:''"`
	CreatedAtMS     int64       `gorm:"column:created_at_ms;not null;check:chk_credential_reset_operation_created_at,created_at_ms >= 0"`
	UpdatedAtMS     int64       `gorm:"column:updated_at_ms;not null;check:chk_credential_reset_operation_updated_at,updated_at_ms >= 0"`
	CompletedAtMS   *int64      `gorm:"column:completed_at_ms;check:chk_credential_reset_operation_completed_at,completed_at_ms IS NULL OR completed_at_ms >= 0"`
}

func (subscriptionResetOperation) TableName() string { return "credential_reset_operations" }

type subscriptionRequestLogIndex struct {
	ID            string `gorm:"column:id;index:idx_request_logs_credential_completed_id,priority:3,sort:desc"`
	CompletedAtMS int64  `gorm:"column:completed_at_ms;index:idx_request_logs_credential_completed_id,priority:2,sort:desc"`
	CredentialID  uint   `gorm:"column:credential_id;index:idx_request_logs_credential_completed_id,priority:1"`
}

func (subscriptionRequestLogIndex) TableName() string { return "request_logs" }

// Up0004 adds the durable external reset operation and the credential/time
// lookup needed by subscription window usage. Each step is safely repeatable
// after MySQL implicit DDL commits.
func Up0004(db *gorm.DB) error {
	if err := db.AutoMigrate(&subscriptionResetOperation{}); err != nil {
		return fmt.Errorf("create credential reset operations: %w", err)
	}
	if !db.Migrator().HasIndex("request_logs", "idx_request_logs_credential_completed_id") {
		if err := db.Migrator().CreateIndex(&subscriptionRequestLogIndex{}, "idx_request_logs_credential_completed_id"); err != nil {
			return fmt.Errorf("create request log credential/time index: %w", err)
		}
	}
	return nil
}

func ValidateRecoverable0004(db *gorm.DB) error {
	for _, table := range []string{"credentials", "request_logs"} {
		if !db.Migrator().HasTable(table) {
			return fmt.Errorf("required subscription runtime table %q is missing", table)
		}
	}
	return nil
}

func Validate0004(db *gorm.DB) error {
	if err := ValidateRecoverable0004(db); err != nil {
		return err
	}
	if !db.Migrator().HasTable(&subscriptionResetOperation{}) {
		return fmt.Errorf("validate subscription runtime schema: credential reset operations table is missing")
	}
	for _, column := range []string{
		"idempotency_key", "request_digest", "group_id", "credential_id", "redeem_request_id",
		"state", "result_json", "error_code", "created_at_ms", "updated_at_ms", "completed_at_ms",
	} {
		if !db.Migrator().HasColumn(&subscriptionResetOperation{}, column) {
			return fmt.Errorf("validate subscription runtime schema: column %q is missing", column)
		}
	}
	if !db.Migrator().HasIndex("request_logs", "idx_request_logs_credential_completed_id") {
		return fmt.Errorf("validate subscription runtime schema: request log credential/time index is missing")
	}
	return nil
}
