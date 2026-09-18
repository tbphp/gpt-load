package migrations_test

import (
	"testing"

	"gpt-load/internal/storage/migrations"
)

func TestRequestLogOperationIndexRejectsMissingTableAndIndex(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.ValidateRecoverable0017(db); err == nil {
		t.Fatal("missing request_logs table accepted")
	}
	if err := migrations.Up0001(db); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Validate0017(db); err == nil {
		t.Fatal("missing operation index accepted")
	}
	for range 2 {
		if err := migrations.Up0017(db); err != nil {
			t.Fatal(err)
		}
		if err := migrations.Validate0017(db); err != nil {
			t.Fatal(err)
		}
	}
}
