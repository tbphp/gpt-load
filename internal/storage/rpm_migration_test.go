package storage

import "testing"

func TestRPMMigrationCreatesHistoryAndKeepsExistingSchema(t *testing.T) {
	t.Parallel()
	db := openInternalMigrationTestDatabase(t)
	if err := applyMigrationRegistry(db, migrations[:21]); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable("rpm_stats") {
		t.Fatal("RPM minute statistics table is missing")
	}
	for _, index := range []string{"idx_rpm_stats_identity", "idx_rpm_stats_retention"} {
		if !db.Migrator().HasIndex("rpm_stats", index) {
			t.Fatalf("missing index %s", index)
		}
	}
	if err := applyMigrations(db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}
