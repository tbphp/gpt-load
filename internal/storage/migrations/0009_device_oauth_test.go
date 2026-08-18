package migrations_test

import (
	"testing"

	"gorm.io/gorm"

	"gpt-load/internal/storage/migrations"
)

func TestDeviceOAuthMigrationPreservesStagesAndExtendsConstraint(t *testing.T) {
	db := openInitialTestDatabase(t)
	for _, up := range []func(*gorm.DB) error{migrations.Up0001, migrations.Up0002} {
		if err := up(db); err != nil {
			t.Fatal(err)
		}
	}
	insertCredentialStage0009(t, db, "existing", "browser_oauth")
	if err := migrations.Up0009(db); err != nil {
		t.Fatal(err)
	}
	insertCredentialStage0009(t, db, "device", "device_oauth")
	if err := db.Exec(`UPDATE credential_stages SET authorization_method = 'unsupported' WHERE id = 'device'`).Error; err == nil {
		t.Fatal("unsupported authorization method was accepted")
	}
	var count int64
	if err := db.Table("credential_stages").Where("id IN ?", []string{"existing", "device"}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("preserved stages = %d, %v", count, err)
	}
	if err := migrations.Up0009(db); err != nil {
		t.Fatalf("idempotent Up0009() error = %v", err)
	}
}

func insertCredentialStage0009(t *testing.T, db *gorm.DB, id, method string) {
	t.Helper()
	if err := db.Exec(`INSERT INTO credential_stages (
		id, channel_id, connection_type, authorization_method, status,
		encrypted_payload, payload_schema_version, safe_summary_json,
		identity_fingerprint, expires_at_ms, error_code, created_at_ms, updated_at_ms
	) VALUES (?, 'grok', 'subscription', ?, 'pending_authorization', 'encrypted', 2, '{}', '', 100, '', 1, 1)`, id, method).Error; err != nil {
		t.Fatalf("insert stage %q/%q: %v", id, method, err)
	}
}
