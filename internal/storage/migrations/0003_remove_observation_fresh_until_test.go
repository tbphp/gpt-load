package migrations_test

import (
	"testing"

	"gpt-load/internal/storage/migrations"
	"gpt-load/internal/storage/models"
)

func TestRemoveObservationFreshUntilMigrationDropsColumnAndPreservesRows(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.Up0001(db); err != nil {
		t.Fatalf("Up0001() error = %v", err)
	}
	group := models.Group{
		Name: "observation migration", ChannelID: "codex",
		ConnectionType: models.ConnectionTypeSubscription,
		Params:         models.JSON(`{}`), Models: models.JSON(`[]`), Enabled: true,
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	credential := models.Credential{
		GroupID: group.ID, Data: "cipher", Fingerprint: "fingerprint",
		IdentityFingerprint: "identity", Status: models.CredentialStatusActive,
	}
	if err := db.Create(&credential).Error; err != nil {
		t.Fatalf("create credential: %v", err)
	}
	if err := db.Exec(`INSERT INTO credential_observations (
		credential_id, identity_fingerprint, schema_version, observation_version,
		snapshot_json, state, observed_at_ms, fresh_until_ms, last_attempt_at_ms,
		last_error_code, updated_at_ms
	) VALUES (?, ?, 1, 1, ?, 'fresh', 1000, 2000, 1000, '', 1000)`,
		credential.ID, credential.IdentityFingerprint, `{"quota_windows":[]}`,
	).Error; err != nil {
		t.Fatalf("create credential observation: %v", err)
	}

	if err := migrations.Up0003(db); err != nil {
		t.Fatalf("Up0003() error = %v", err)
	}
	if err := migrations.Validate0003(db); err != nil {
		t.Fatalf("Validate0003() error = %v", err)
	}
	if db.Migrator().HasColumn("credential_observations", "fresh_until_ms") {
		t.Fatal("fresh_until_ms still exists")
	}
	var observation models.CredentialObservation
	if err := db.Take(&observation, "credential_id = ?", credential.ID).Error; err != nil {
		t.Fatalf("read preserved credential observation: %v", err)
	}
	if observation.IdentityFingerprint != credential.IdentityFingerprint ||
		observation.State != models.CredentialObservationFresh ||
		string(observation.SnapshotJSON) != `{"quota_windows":[]}` {
		t.Fatalf("preserved observation = %#v", observation)
	}
	if err := migrations.Up0003(db); err != nil {
		t.Fatalf("repeated Up0003() error = %v", err)
	}
}
