package migrations_test

import (
	"testing"

	"gpt-load/internal/storage/migrations"
)

func TestUsageStatsGroupActivityIndexMigrationCreatesAndValidatesIndex(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.Up0001(db); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up0002(db); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up0003(db); err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasIndex("usage_stats", "idx_usage_stats_group_bucket") {
		t.Fatal("usage_stats group activity index exists before migration")
	}
	if err := migrations.ValidateRecoverable0004(db); err != nil {
		t.Fatalf("ValidateRecoverable0004() before index = %v", err)
	}
	if err := migrations.Up0004(db); err != nil {
		t.Fatalf("Up0004() error = %v", err)
	}
	if !db.Migrator().HasIndex("usage_stats", "idx_usage_stats_group_bucket") {
		t.Fatal("usage_stats group activity index is missing")
	}
	if err := migrations.Validate0004(db); err != nil {
		t.Fatalf("Validate0004() error = %v", err)
	}
	if err := migrations.Up0004(db); err != nil {
		t.Fatalf("repeated Up0004() error = %v", err)
	}
}

func TestUsageStatsGroupActivityIndexMigrationRejectsMissingIndex(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.Up0001(db); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up0002(db); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up0003(db); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Validate0004(db); err == nil {
		t.Fatal("Validate0004() error = nil, want missing index error")
	}
}
