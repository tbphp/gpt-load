package migrations_test

import (
	"testing"

	"gpt-load/internal/storage"
	"gpt-load/internal/storage/migrations"
)

func TestCodexChannelMigrationMovesSubscriptionTargets(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.Up0001(db); err != nil {
		t.Fatalf("Up0001() error = %v", err)
	}
	if err := migrations.Up0002(db); err != nil {
		t.Fatalf("Up0002() error = %v", err)
	}
	if err := db.Exec(`CREATE TABLE schema_migrations (
		id varchar(255) PRIMARY KEY NOT NULL
	)`).Error; err != nil {
		t.Fatalf("create migration ledger: %v", err)
	}
	for _, id := range []string{migrations.ID0001, migrations.ID0002} {
		if err := db.Exec("INSERT INTO schema_migrations(id) VALUES (?)", id).Error; err != nil {
			t.Fatalf("record %s: %v", id, err)
		}
	}
	if err := db.Exec(`INSERT INTO groups (
		id, name, channel_id, connection_type, params, models, overrides, enabled,
		created_at_ms, updated_at_ms
	) VALUES
		(1, 'subscription', 'openai', 'subscription', '{}', '[]', '{}', true, 1, 1),
		(2, 'api-key', 'openai', 'api_key', '{}', '[]', '{}', true, 1, 1)`).Error; err != nil {
		t.Fatalf("insert groups: %v", err)
	}
	if err := db.Exec(`INSERT INTO credential_stages (
		id, channel_id, connection_type, authorization_method, status,
		encrypted_payload, payload_schema_version, safe_summary_json,
		identity_fingerprint, expires_at_ms, error_code, created_at_ms, updated_at_ms
	) VALUES ('stage', 'openai', 'subscription', 'oauth_file', 'ready', '', 1, '{}', 'identity', 2, '', 1, 1)`).Error; err != nil {
		t.Fatalf("insert credential stage: %v", err)
	}

	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	if err := migrations.Up0003(db); err != nil {
		t.Fatalf("repeat Up0003() error = %v", err)
	}
	if err := migrations.Validate0003(db); err != nil {
		t.Fatalf("Validate0003() error = %v", err)
	}

	var subscriptionChannel, apiKeyChannel, stageChannel string
	if err := db.Table("groups").Where("id = 1").Pluck("channel_id", &subscriptionChannel).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("groups").Where("id = 2").Pluck("channel_id", &apiKeyChannel).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("credential_stages").Where("id = ?", "stage").Pluck("channel_id", &stageChannel).Error; err != nil {
		t.Fatal(err)
	}
	if subscriptionChannel != "codex" || stageChannel != "codex" || apiKeyChannel != "openai" {
		t.Fatalf("channels = subscription %q, stage %q, api key %q", subscriptionChannel, stageChannel, apiKeyChannel)
	}
}
