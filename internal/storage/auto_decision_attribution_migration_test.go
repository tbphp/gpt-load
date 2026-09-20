package storage

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
)

func TestAutoDecisionAttributionMigrationContract(t *testing.T) {
	testAutoDecisionAttributionMigration(t, openInternalMigrationTestDatabase)
}

func TestExternalAutoDecisionAttributionMigrationContract(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testAutoDecisionAttributionMigration(t, func(t *testing.T) *gorm.DB {
		return openExternalIncrementalMigrationDatabase(t, dsn)
	})
}

func TestAutoDecisionAttributionMigrationCleansInterruptedSwapBackup(t *testing.T) {
	db := openInternalMigrationTestDatabase(t)
	if err := applyMigrationRegistry(db, migrations[:18]); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&schemaMigration{ID: migrationResumeMarker(migrations[18].ID)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrations[18].Up(db); err != nil {
		t.Fatal(err)
	}
	const backupTable = "auto_decision_usage_stats_0019_old"
	if err := db.Exec(`CREATE TABLE auto_decision_usage_stats_0019_old AS
		SELECT * FROM auto_decision_usage_stats WHERE 1 = 0`).Error; err != nil {
		t.Fatal(err)
	}

	if err := AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasTable(backupTable) {
		t.Fatal("interrupted automatic decision attribution swap backup was not removed")
	}
}

func testAutoDecisionAttributionMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	t.Helper()
	for _, scenario := range []string{"fresh", "upgrade", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			db := open(t)
			if db.Dialector.Name() == "sqlite" {
				t.Parallel()
			}
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:18]); err != nil {
					t.Fatal(err)
				}
				if err := db.Exec(`INSERT INTO auto_decision_usage_stats
					(bucket_start_ms, access_key_id, model, estimated_cost_nano_usd,
					 unpriced_request_count, pricing_partial_count)
					VALUES (?, ?, ?, ?, ?, ?)`, int64(3_600_000), uint(7), "legacy-model", int64(84_000), int64(1), int64(0)).Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "interrupted" {
				registry := append([]migration(nil), migrations[:19]...)
				up := registry[18].Up
				registry[18].Up = func(tx *gorm.DB) error {
					if err := up(tx); err != nil {
						return err
					}
					return fmt.Errorf("interrupt after automatic decision attribution DDL")
				}
				if err := applyMigrationRegistry(db, registry); err == nil {
					t.Fatal("expected migration interruption")
				}
			}
			for range 2 {
				if err := AutoMigrate(db); err != nil {
					t.Fatal(err)
				}
			}
			for _, column := range []string{"decision_group_id", "decision_channel_id", "decision_credential_id"} {
				if !db.Migrator().HasColumn("request_logs", column) {
					t.Fatalf("request_logs.%s is missing", column)
				}
			}
			if scenario != "fresh" {
				var historical models.AutoDecisionUsageStat
				if err := db.Where("bucket_start_ms = ? AND access_key_id = ? AND model = ?", 3_600_000, 7, "legacy-model").Take(&historical).Error; err != nil {
					t.Fatal(err)
				}
				if historical.GroupID != 0 || historical.ChannelID != "" || historical.CredentialID != 0 ||
					historical.EstimatedCostNanoUSD != 84_000 || historical.UnpricedRequestCount != 1 {
					t.Fatalf("historical decision usage = %#v", historical)
				}
			}
			rows := []models.AutoDecisionUsageStat{
				{BucketStartMS: 7_200_000, AccessKeyID: 7, GroupID: 1, ChannelID: "jev", CredentialID: 11, Model: "jev-latest", EstimatedCostNanoUSD: 1},
				{BucketStartMS: 7_200_000, AccessKeyID: 7, GroupID: 2, ChannelID: "openrouter", CredentialID: 12, Model: "jev-latest", EstimatedCostNanoUSD: 2},
			}
			if err := db.Create(&rows).Error; err != nil {
				t.Fatalf("route dimensions are not part of the usage identity: %v", err)
			}
		})
	}
}
