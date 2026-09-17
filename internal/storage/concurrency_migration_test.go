package storage

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"

	migrationfiles "gpt-load/internal/storage/migrations"
	"gpt-load/internal/storage/models"
)

func TestConcurrencyMigration(t *testing.T) {
	testConcurrencyMigration(t, openInternalMigrationTestDatabase)
	testLegacyConcurrencyMigration(t, openInternalMigrationTestDatabase)
}

func TestExternalConcurrencyMigration(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testConcurrencyMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
	testLegacyConcurrencyMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}

func testLegacyConcurrencyMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	for _, interrupted := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy_interrupted_%t", interrupted), func(t *testing.T) {
			db := open(t)
			legacy := migrations[15]
			legacy.ID = "0015_concurrency"
			entries := append(append([]migration{}, migrations[:14]...), legacy)
			if err := applyMigrationRegistry(db, entries); err != nil {
				t.Fatal(err)
			}
			policy := models.ConcurrencyPolicy{Subject: "global", MaxConcurrency: 12}
			if err := db.Create(&policy).Error; err != nil {
				t.Fatal(err)
			}
			if interrupted {
				if err := db.Model(&schemaMigration{}).Where("id = ?", legacy.ID).
					Update("id", migrationResumeMarker(legacy.ID)).Error; err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := AutoMigrate(db); err != nil {
					t.Fatal(err)
				}
			}
			var saved models.ConcurrencyPolicy
			if err := db.Where("subject = ?", policy.Subject).First(&saved).Error; err != nil || saved.MaxConcurrency != 12 {
				t.Fatalf("legacy policy was not preserved: %+v, %v", saved, err)
			}
			if err := migrationfiles.Validate0015(db); err != nil {
				t.Fatalf("upstream usage index: %v", err)
			}
			var ids []string
			if err := db.Model(&schemaMigration{}).Order("id ASC").Pluck("id", &ids).Error; err != nil {
				t.Fatal(err)
			}
			if len(ids) != len(migrations) || ids[14] != migrationfiles.ID0015 || ids[15] != migrationfiles.ID0016 {
				t.Fatalf("unexpected migration ledger: %v", ids)
			}
		})
	}
}

func testConcurrencyMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
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
					return fmt.Errorf("interrupted concurrency DDL")
				}
				if err := applyMigrationRegistry(db, append(append([]migration{}, migrations[:15]...), entry)); err == nil {
					t.Fatal("expected interruption")
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			var count int64
			if err := db.Model(&models.ConcurrencyPolicy{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("fresh policies=%d %v", count, err)
			}
			for _, maximum := range []int64{0, 1, 1_000_000} {
				if err := db.Create(&models.ConcurrencyPolicy{Subject: fmt.Sprintf("valid:%d", maximum), MaxConcurrency: maximum}).Error; err != nil {
					t.Fatal(err)
				}
			}
			for _, maximum := range []int64{-1, 1_000_001} {
				if err := db.Create(&models.ConcurrencyPolicy{Subject: fmt.Sprintf("invalid:%d", maximum), MaxConcurrency: maximum}).Error; err == nil {
					t.Fatalf("accepted invalid limit %d", maximum)
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&models.ConcurrencyPolicy{}).Count(&count).Error; err != nil || count != 3 {
				t.Fatalf("policies after re-open=%d %v", count, err)
			}
		})
	}
}
