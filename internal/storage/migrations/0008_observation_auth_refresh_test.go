package migrations_test

import (
	"testing"

	"gorm.io/gorm"

	"gpt-load/internal/storage/migrations"
)

func TestObservationAuthRefreshMigrationAddsNullableSecretVersion(t *testing.T) {
	db := openInitialTestDatabase(t)
	for _, up := range []func(*gorm.DB) error{
		migrations.Up0001,
		migrations.Up0002,
	} {
		if err := up(db); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrations.Up0008(db); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Validate0008(db); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up0008(db); err != nil {
		t.Fatal(err)
	}
}
