package migrations_test

import (
	"testing"

	"gpt-load/internal/storage/migrations"
)

func TestRequestLogProcessingMigrationIsIdempotent(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.ValidateRecoverable0024(db); err == nil {
		t.Fatal("missing request_logs table accepted")
	}
	if err := migrations.Up0001(db); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Validate0024(db); err == nil {
		t.Fatal("missing processing schema accepted")
	}
	for range 2 {
		if err := migrations.Up0024(db); err != nil {
			t.Fatal(err)
		}
		if err := migrations.Validate0024(db); err != nil {
			t.Fatal(err)
		}
	}
}
