package migrations

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	ID0005                         = "0005_credential_attempt_stats"
	credentialAttemptBackfillHours = 25
)

type credentialAttemptStat struct {
	ID            uint  `gorm:"primaryKey;autoIncrement;index:idx_credential_attempt_stats_bucket_id,priority:2"`
	CredentialID  uint  `gorm:"not null;check:chk_credential_attempt_stat_credential,credential_id > 0;uniqueIndex:idx_credential_attempt_stats_identity,priority:1"`
	BucketStartMS int64 `gorm:"column:bucket_start_ms;not null;check:chk_credential_attempt_stat_bucket,bucket_start_ms >= 0;uniqueIndex:idx_credential_attempt_stats_identity,priority:2;index:idx_credential_attempt_stats_bucket_id,priority:1"`
	SuccessCount  int64 `gorm:"not null;default:0;check:chk_credential_attempt_stat_success_count,success_count >= 0"`
	FailureCount  int64 `gorm:"not null;default:0;check:chk_credential_attempt_stat_failure_count,failure_count >= 0"`
}

func (credentialAttemptStat) TableName() string { return "credential_attempt_stats" }

type credentialUsageStatIndex struct {
	CredentialID  uint  `gorm:"column:credential_id;index:idx_usage_stats_credential_bucket,priority:1"`
	BucketStartMS int64 `gorm:"column:bucket_start_ms;index:idx_usage_stats_credential_bucket,priority:2"`
}

func (credentialUsageStatIndex) TableName() string { return "usage_stats" }

// Up0005 creates the credential/hour attempt aggregate and seeds the window
// shown by subscription account cards from retained attempt records.
func Up0005(db *gorm.DB) error {
	if err := ValidateRecoverable0005(db); err != nil {
		return err
	}
	if err := db.AutoMigrate(&credentialAttemptStat{}); err != nil {
		return fmt.Errorf("create credential attempt stats: %w", err)
	}
	if !db.Migrator().HasIndex("usage_stats", "idx_usage_stats_credential_bucket") {
		if err := db.Migrator().CreateIndex(
			&credentialUsageStatIndex{},
			"idx_usage_stats_credential_bucket",
		); err != nil {
			return fmt.Errorf("create usage stat credential/time index: %w", err)
		}
	}
	cutoffMS := time.Now().UTC().Add(-credentialAttemptBackfillHours * time.Hour).UnixMilli()
	if err := backfillCredentialAttemptStats(db, cutoffMS); err != nil {
		return fmt.Errorf("backfill credential attempt stats: %w", err)
	}
	return nil
}

func backfillCredentialAttemptStats(db *gorm.DB, cutoffMS int64) error {
	const aggregate = `
		SELECT
			attempt.credential_id,
			attempt.completed_at_ms - (attempt.completed_at_ms % 3600000) AS bucket_start_ms,
			SUM(CASE WHEN attempt.failure_category = 'ok' THEN 1 ELSE 0 END) AS success_count,
			SUM(CASE WHEN attempt.failure_category <> 'ok' THEN 1 ELSE 0 END) AS failure_count
		FROM request_log_attempts AS attempt
		WHERE attempt.request_id IN (
			SELECT request_log.id
			FROM request_logs AS request_log
			WHERE request_log.completed_at_ms >= ?
		)
			AND attempt.completed_at_ms >= ?
			AND attempt.failure_category <> 'downstream_cancel'
		GROUP BY
			attempt.credential_id,
			attempt.completed_at_ms - (attempt.completed_at_ms % 3600000)`

	var statement string
	switch strings.ToLower(db.Dialector.Name()) {
	case "sqlite", "postgres", "postgresql":
		statement = `
			INSERT INTO credential_attempt_stats (
				credential_id, bucket_start_ms, success_count, failure_count
			)` + aggregate + `
			ON CONFLICT (credential_id, bucket_start_ms) DO NOTHING`
	case "mysql":
		statement = `
			INSERT INTO credential_attempt_stats (
				credential_id, bucket_start_ms, success_count, failure_count
			)` + aggregate + `
			ON DUPLICATE KEY UPDATE
				credential_id = credential_attempt_stats.credential_id`
	default:
		return fmt.Errorf("unsupported database driver %q", db.Dialector.Name())
	}
	return db.Exec(statement, cutoffMS, cutoffMS).Error
}

func ValidateRecoverable0005(db *gorm.DB) error {
	for _, table := range []string{"request_logs", "request_log_attempts", "usage_stats"} {
		if !db.Migrator().HasTable(table) {
			return fmt.Errorf("required credential activity source table %q is missing", table)
		}
	}
	return nil
}

func Validate0005(db *gorm.DB) error {
	if err := ValidateRecoverable0005(db); err != nil {
		return err
	}
	if !db.Migrator().HasTable(&credentialAttemptStat{}) {
		return fmt.Errorf("validate credential attempt stats schema: table is missing")
	}
	for _, column := range []string{
		"id", "credential_id", "bucket_start_ms", "success_count", "failure_count",
	} {
		if !db.Migrator().HasColumn(&credentialAttemptStat{}, column) {
			return fmt.Errorf("validate credential attempt stats schema: column %q is missing", column)
		}
	}
	for _, index := range []string{
		"idx_credential_attempt_stats_identity",
		"idx_credential_attempt_stats_bucket_id",
	} {
		if !db.Migrator().HasIndex(&credentialAttemptStat{}, index) {
			return fmt.Errorf("validate credential attempt stats schema: index %q is missing", index)
		}
	}
	if !db.Migrator().HasIndex("usage_stats", "idx_usage_stats_credential_bucket") {
		return fmt.Errorf("validate credential attempt stats schema: usage stat credential/time index is missing")
	}
	return nil
}
