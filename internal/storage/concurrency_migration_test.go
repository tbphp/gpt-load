package storage

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
)

func TestConcurrencyMigration(t *testing.T) {
	testConcurrencyMigration(t, openInternalMigrationTestDatabase)
}

func TestExternalConcurrencyMigration(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testConcurrencyMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}

func testConcurrencyMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	for _, scenario := range []string{"fresh", "upgrade", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			db := open(t)
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:14]); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "interrupted" {
				entry := migrations[14]
				up := entry.Up
				entry.Up = func(tx *gorm.DB) error {
					if err := up(tx); err != nil {
						return err
					}
					return fmt.Errorf("interrupted concurrency DDL")
				}
				if err := applyMigrationRegistry(db, append(append([]migration{}, migrations[:14]...), entry)); err == nil {
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
