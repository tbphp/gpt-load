package migrations_test

import (
	"testing"

	"gpt-load/internal/storage/migrations"
	"gpt-load/internal/storage/models"
)

func TestAccessKeyLifecycleMigrationAddsNullableExpiryAndPreservesRows(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.Up0001(db); err != nil {
		t.Fatal(err)
	}
	accessKey := models.AccessKey{
		Name: "legacy", KeyValue: "ciphertext", KeyHash: "legacy-hash",
		KeySuffix: "cafe", Status: "active", Filters: models.JSON(`{}`),
	}
	if err := db.Omit("ExpiresAtMS").Create(&accessKey).Error; err != nil {
		t.Fatalf("create legacy access key: %v", err)
	}

	if err := migrations.Up0007(db); err != nil {
		t.Fatalf("Up0007() error = %v", err)
	}
	if err := migrations.Validate0007(db); err != nil {
		t.Fatalf("Validate0007() error = %v", err)
	}
	if !db.Migrator().HasColumn("access_keys", "expires_at_ms") {
		t.Fatal("access_keys.expires_at_ms is missing")
	}

	var expiresAtMS *int64
	if err := db.Table("access_keys").Select("expires_at_ms").Where("id = ?", accessKey.ID).
		Scan(&expiresAtMS).Error; err != nil {
		t.Fatalf("read preserved access key expiry: %v", err)
	}
	if expiresAtMS != nil {
		t.Fatalf("legacy expires_at_ms = %v, want nil", *expiresAtMS)
	}
	if err := migrations.Up0007(db); err != nil {
		t.Fatalf("repeated Up0007() error = %v", err)
	}
}

func TestAccessKeyLifecycleMigrationValidationRejectsMissingColumn(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.Up0001(db); err != nil {
		t.Fatal(err)
	}
	if err := migrations.ValidateRecoverable0007(db); err != nil {
		t.Fatalf("ValidateRecoverable0007() before column = %v", err)
	}
	if err := migrations.Validate0007(db); err == nil {
		t.Fatal("Validate0007() error = nil, want missing column error")
	}
}
