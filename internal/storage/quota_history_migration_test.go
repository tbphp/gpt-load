package storage

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
)

func TestQuotaHistoryMigrationCreatesQueryableHistory(t *testing.T) {
	db := openInternalMigrationTestDatabase(t)
	if err := applyMigrations(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable("credential_quota_histories") {
		t.Fatal("credential quota history table is missing")
	}
	for _, index := range []string{"idx_quota_history_identity", "idx_quota_history_retention"} {
		if !db.Migrator().HasIndex("credential_quota_histories", index) {
			t.Fatalf("history index %s is missing", index)
		}
	}
	if err := applyMigrations(db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}

func TestQuotaHistoryMigrationContract(t *testing.T) {
	testQuotaHistoryMigration(t, openInternalMigrationTestDatabase)
}

func TestExternalQuotaHistoryMigrationContract(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testQuotaHistoryMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}

func testQuotaHistoryMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	for _, scenario := range []string{"fresh", "upgrade", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			db := open(t)
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:15]); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "interrupted" {
				entry := migrations[15]
				up := entry.Up
				entry.Up = func(tx *gorm.DB) error {
					if err := up(tx); err != nil {
						return err
					}
					return fmt.Errorf("interrupted quota history DDL")
				}
				registry := append(append([]migration(nil), migrations[:15]...), entry)
				if err := applyMigrationRegistry(db, registry); err == nil {
					t.Fatal("expected interruption")
				}
			}
			if err := applyMigrations(db); err != nil {
				t.Fatal(err)
			}
			row := models.CredentialQuotaHistory{GroupID: 1, CredentialID: 1, TargetIdentity: "identity", WindowKey: "session", WindowID: "primary", Label: "Session", Scope: "account", ObservedAtMS: 60_000, UsedBasisPoints: 100}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			duplicate := row
			duplicate.ID = 0
			if err := db.Create(&duplicate).Error; err == nil {
				t.Fatal("duplicate observation accepted")
			}
			invalid := row
			invalid.ID = 0
			invalid.ObservedAtMS = 120_000
			invalid.UsedBasisPoints = 10001
			if err := db.Create(&invalid).Error; err == nil {
				t.Fatal("invalid percentage accepted")
			}
			if err := applyMigrations(db); err != nil {
				t.Fatalf("repeat with existing history: %v", err)
			}
			var points []models.CredentialQuotaHistory
			if err := db.Raw("SELECT * FROM (SELECT credential_quota_histories.*, ROW_NUMBER() OVER (PARTITION BY window_key, reset_at_ms, observed_at_ms - observed_at_ms % ? ORDER BY observed_at_ms DESC) AS sample_rank FROM credential_quota_histories WHERE credential_id = ? AND target_identity = ?) AS samples WHERE sample_rank = 1", 60_000, 1, "identity").Scan(&points).Error; err != nil {
				t.Fatal(err)
			}
			if len(points) != 1 || points[0].UsedBasisPoints != 100 {
				t.Fatalf("history query: %+v", points)
			}
		})
	}
}
