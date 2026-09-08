package storage

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
)

func TestModelCooldownMigrationContract(t *testing.T) {
	testModelCooldownMigration(t, openInternalMigrationTestDatabase)
}

func TestExternalModelCooldownMigrationContract(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testModelCooldownMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}

func TestExternalModelCooldownMigrationRecoversConstraintReplacement(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	db := openExternalIncrementalMigrationDatabase(t, dsn)
	if db.Dialector.Name() != "mysql" {
		t.Skip("DDL auto-commit recovery is specific to MySQL")
	}
	if err := applyMigrationRegistry(db, migrations[:9]); err != nil {
		t.Fatal(err)
	}
	interrupted := false
	const callback = "test:interrupt_model_cooldown_check"
	if err := db.Callback().Raw().After("gorm:raw").Register(callback, func(tx *gorm.DB) {
		sql := strings.ToLower(tx.Statement.SQL.String())
		if tx.Error == nil && strings.Contains(sql, "drop") && strings.Contains(sql, "chk_request_log_attempt_effect") {
			interrupted = true
			tx.AddError(fmt.Errorf("interrupted after replacing attempt effect constraint"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	err := AutoMigrate(db)
	if removeErr := db.Callback().Raw().Remove(callback); removeErr != nil {
		t.Fatal(removeErr)
	}
	if err == nil || !interrupted {
		t.Fatalf("DDL interruption was not exercised: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("cannot resume after constraint replacement: %v", err)
	}
}

func testModelCooldownMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	for _, scenario := range []string{"fresh", "upgrade", "interrupted", "column_added"} {
		t.Run(scenario, func(t *testing.T) {
			db := open(t)
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:9]); err != nil {
					t.Fatal(err)
				}
			}
			legacyRequest := models.RequestLog{ID: "00000000-0000-4000-8000-000000000009", CompletedAtMS: 1000, AccessKeyID: 1,
				Protocol: "openai-completions", ClientModel: "model", UpstreamModel: "model", ModelConsistency: "not_applicable",
				Status: "error", StatusCode: 429, DurationMs: 1, UsageState: "not_applicable", CostState: "not_applicable", PricingCompleteness: "not_applicable"}
			if scenario != "fresh" {
				if err := db.Create(&legacyRequest).Error; err != nil {
					t.Fatal(err)
				}
				legacy := models.RequestLogAttempt{RequestID: legacyRequest.ID, Sequence: 1, GroupID: 1, CredentialID: 1,
					FailureCategory: "rate_limited", Action: "cooldown_credential", Effect: "cooldown_credential", ErrorSummary: "legacy limited"}
				if err := db.Omit("CooldownUntilMS").Create(&legacy).Error; err != nil {
					t.Fatal(err)
				}
			}
			if (scenario == "interrupted" || scenario == "column_added") && len(migrations) > 9 {
				entry := migrations[9]
				up := entry.Up
				entry.Up = func(tx *gorm.DB) error {
					if scenario == "column_added" {
						if err := tx.Exec("ALTER TABLE request_log_attempts ADD COLUMN cooldown_until_ms BIGINT NULL CONSTRAINT chk_request_log_attempt_cooldown CHECK (cooldown_until_ms IS NULL OR cooldown_until_ms >= 0)").Error; err != nil {
							return err
						}
						return fmt.Errorf("interrupt after model cooldown column")
					}
					if err := up(tx); err != nil {
						return err
					}
					return fmt.Errorf("interrupt after model cooldown DDL")
				}
				registry := append(append([]migration(nil), migrations[:9]...), entry)
				if err := applyMigrationRegistry(db, registry); err == nil {
					t.Fatal("interruption succeeded")
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			if !db.Migrator().HasColumn("request_log_attempts", "cooldown_until_ms") {
				t.Fatal("model cooldown log deadline missing")
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			if scenario != "fresh" {
				var legacy models.RequestLogAttempt
				if err := db.Take(&legacy, "request_id = ?", legacyRequest.ID).Error; err != nil {
					t.Fatal(err)
				}
				if legacy.Effect != "cooldown_credential" || legacy.ErrorSummary != "legacy limited" || legacy.CooldownUntilMS != nil {
					t.Fatalf("legacy attempt changed: %#v", legacy)
				}
			}
			request := models.RequestLog{ID: "00000000-0000-4000-8000-000000000010", CompletedAtMS: 1000, AccessKeyID: 1,
				Protocol: "openai-completions", ClientModel: "model", UpstreamModel: "model", ModelConsistency: "not_applicable",
				Status: "error", StatusCode: 429, DurationMs: 1, UsageState: "not_applicable", CostState: "not_applicable", PricingCompleteness: "not_applicable"}
			if err := db.Create(&request).Error; err != nil {
				t.Fatal(err)
			}
			attempt := map[string]any{"request_id": request.ID, "sequence": 1, "completed_at_ms": 1000, "group_id": 1,
				"group_name": "group", "credential_id": 1, "status_code": 429, "duration_ms": 1, "failure_category": "rate_limited",
				"failure_scope": "model", "effect": "cooldown_model", "action": "retry", "error_summary": "limited", "cooldown_until_ms": int64(60000)}
			if err := db.Table("request_log_attempts").Create(attempt).Error; err != nil {
				t.Fatal(err)
			}
			for _, invalid := range []any{nil, int64(-1)} {
				if err := db.Table("request_log_attempts").Where("request_id = ?", request.ID).Update("cooldown_until_ms", invalid).Error; err == nil {
					t.Fatalf("accepted invalid deadline %v", invalid)
				}
			}
			if err := db.Table("request_log_attempts").Where("request_id = ?", request.ID).Update("effect", "invalid").Error; err == nil {
				t.Fatal("accepted invalid effect")
			}
		})
	}
}
