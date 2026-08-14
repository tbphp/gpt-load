package migrations_test

import (
	"testing"

	"gpt-load/internal/storage"
	"gpt-load/internal/storage/migrations"
)

func TestSubscriptionConnectionsMigrationCreatesSchema(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}

	assertColumns(t, db, "groups", []string{"connection_type"})
	assertColumns(t, db, "credentials", []string{
		"identity_fingerprint", "secret_version", "auth_state", "auth_error_code",
	})
	assertUniqueIndex(t, db, "credentials", "idx_credentials_group_identity", []string{
		"group_id", "identity_fingerprint",
	})
	assertColumns(t, db, "credential_stages", []string{
		"id", "channel_id", "connection_type", "authorization_method", "status",
		"encrypted_payload", "payload_schema_version", "safe_summary_json",
		"identity_fingerprint", "oauth_state_hash", "expires_at_ms", "consumed_at_ms",
		"consumed_group_id", "error_code", "created_at_ms",
		"updated_at_ms",
	})
	assertColumns(t, db, "credential_observations", []string{
		"credential_id", "identity_fingerprint", "schema_version", "observation_version",
		"snapshot_json", "state", "observed_at_ms", "fresh_until_ms",
		"last_attempt_at_ms", "next_allowed_at_ms", "last_error_code", "updated_at_ms",
	})
}

func TestSubscriptionConnectionsMigrationBackfillsLegacyIdentity(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.Up0001(db); err != nil {
		t.Fatalf("Up0001() error = %v", err)
	}
	if err := db.Exec(`CREATE TABLE schema_migrations (
		id varchar(255) PRIMARY KEY NOT NULL
	)`).Error; err != nil {
		t.Fatalf("create migration ledger: %v", err)
	}
	if err := db.Exec("INSERT INTO schema_migrations(id) VALUES (?)", migrations.ID0001).Error; err != nil {
		t.Fatalf("record 0001: %v", err)
	}
	if err := db.Exec(`INSERT INTO groups (
		id, name, channel_id, params, models, overrides, enabled, created_at_ms, updated_at_ms
	) VALUES (1, 'legacy', 'openai', '{}', '[]', '{}', true, 1, 1)`).Error; err != nil {
		t.Fatalf("insert legacy group: %v", err)
	}
	if err := db.Exec(`INSERT INTO credentials (
		id, group_id, data, fingerprint, status, created_at_ms, updated_at_ms
	) VALUES (1, 1, 'cipher', 'legacy-fingerprint', 'active', 1, 1)`).Error; err != nil {
		t.Fatalf("insert legacy credential: %v", err)
	}

	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	var row struct {
		ConnectionType      string
		IdentityFingerprint string
		SecretVersion       uint64
		AuthState           string
	}
	if err := db.Table("groups").
		Select("groups.connection_type, credentials.identity_fingerprint, credentials.secret_version, credentials.auth_state").
		Joins("JOIN credentials ON credentials.group_id = groups.id").
		Where("groups.id = 1").Scan(&row).Error; err != nil {
		t.Fatalf("read backfill: %v", err)
	}
	if row.ConnectionType != "api_key" || row.IdentityFingerprint != "legacy-fingerprint" ||
		row.SecretVersion != 1 || row.AuthState != "ready" {
		t.Fatalf("legacy backfill = %#v", row)
	}
}

func TestSubscriptionConnectionsConstraintsRejectInvalidStates(t *testing.T) {
	db := openMigratedInitialTestDatabase(t)

	if err := db.Exec(`INSERT INTO groups (
		name, channel_id, connection_type, params, models, overrides, enabled,
		created_at_ms, updated_at_ms
	) VALUES ('invalid', 'openai', 'other', '{}', '[]', '{}', true, 1, 1)`).Error; err == nil {
		t.Fatal("invalid connection_type was accepted")
	}
	if err := db.Exec(`INSERT INTO credential_stages (
		id, channel_id, connection_type, authorization_method, status,
		encrypted_payload, payload_schema_version, safe_summary_json,
		identity_fingerprint, expires_at_ms, error_code, created_at_ms, updated_at_ms
	) VALUES ('stage', 'openai', 'subscription', 'oauth_file', 'other', '', 1, '{}', '', 1, '', 1, 1)`).Error; err == nil {
		t.Fatal("invalid credential stage status was accepted")
	}
}

func TestSubscriptionObservationIsDeletedWithCredential(t *testing.T) {
	db := openMigratedInitialTestDatabase(t)
	if err := db.Exec(`INSERT INTO groups (
		id, name, channel_id, connection_type, params, models, overrides, enabled,
		created_at_ms, updated_at_ms
	) VALUES (1, 'subscription', 'openai', 'subscription', '{}', '[]', '{}', true, 1, 1)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO credentials (
		id, group_id, data, fingerprint, identity_fingerprint, secret_version,
		auth_state, auth_error_code, status, created_at_ms, updated_at_ms
	) VALUES (1, 1, 'cipher', 'secret', 'account', 1, 'ready', '', 'active', 1, 1)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO credential_observations (
		credential_id, identity_fingerprint, schema_version, observation_version,
		snapshot_json, state, last_error_code, updated_at_ms
	) VALUES (1, 'account', 1, 1, '{}', 'fresh', '', 1)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`DELETE FROM credentials WHERE id = 1`).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Table("credential_observations").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("credential observation count = %d, want 0", count)
	}
}
