package loader_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/state"
	"gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

func TestLoadPreservesLegacySubscriptionDataAndIdentity(t *testing.T) {
	t.Parallel()
	token := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"account","chatgpt_user_id":"scope"}}`)) + ".signature"
	for _, test := range []struct {
		channel   channel.ID
		canonical string
	}{
		{channel.Codex, fmt.Sprintf(`{"type":"codex","id_token":%q,"access_token":"access","refresh_token":"refresh","account_id":"account"}`, token)},
		{channel.Claude, fmt.Sprintf(`{"type":"claude","access_token":"access","refresh_token":"refresh","account_uuid":"account","organization_uuid":"scope","claude_device_ids":[%q],"expired":"2099-01-01T00:00:00Z"}`, strings.Repeat("a", 64))},
	} {
		t.Run(string(test.channel), func(t *testing.T) {
			db := openMigratedDatabase(t)
			group := models.Group{Name: "legacy identity", ChannelID: string(test.channel), ConnectionType: models.ConnectionTypeSubscription, Params: models.JSON(`{}`), Models: models.JSON(`[]`), Overrides: models.JSON(`{}`), Enabled: true}
			mustCreate(t, db, &group)
			crypto, err := encryption.NewService("legacy-identity-test-key-material")
			if err != nil {
				t.Fatal(err)
			}
			ciphertext, err := crypto.Encrypt(test.canonical)
			if err != nil {
				t.Fatal(err)
			}
			legacyIdentity := crypto.Hash("credential-identity/v1|" + string(test.channel) + "|" + string(test.channel) + "|account")
			row := models.Credential{GroupID: group.ID, Data: ciphertext, Fingerprint: crypto.Hash(test.canonical), IdentityFingerprint: legacyIdentity, SecretVersion: 1, AuthState: models.CredentialAuthStateReady, Status: models.CredentialStatusActive}
			mustCreate(t, db, &row)
			observation := models.CredentialObservation{CredentialID: row.ID, IdentityFingerprint: legacyIdentity, SchemaVersion: 1, ObservationVersion: 1, SnapshotJSON: models.JSON(`{}`), State: models.CredentialObservationFresh}
			mustCreate(t, db, &observation)
			channels, subscriptions := testSubscriptionRuntime(t)
			driver, ok := subscriptions.Driver(test.channel)
			if !ok {
				t.Fatal("missing driver")
			}
			parsed, err := driver.Parse([]byte(test.canonical))
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Identity() != "account/scope" {
				t.Fatalf("derived identity = %q", parsed.Identity())
			}
			registry := state.NewCredentialRegistry()
			if err := loader.NewWithCredentialValidation(db, state.NewManager(), registry, channels, subscriptions, crypto).Load(t.Context()); err != nil {
				t.Fatal(err)
			}
			var after models.Credential
			if err := db.First(&after, row.ID).Error; err != nil {
				t.Fatal(err)
			}
			if after.Data != row.Data || after.Fingerprint != row.Fingerprint || after.IdentityFingerprint != legacyIdentity || after.SecretVersion != 1 {
				t.Fatal("loading rewrote existing subscription data")
			}
			ref, ok := registry.CredentialRef(row.ID)
			expectedGeneration := loader.CredentialIdentityGeneration(legacyIdentity, string(test.channel), "subscription", json.RawMessage(`{}`))
			if !ok || ref.IdentityGeneration != expectedGeneration {
				t.Fatal("loading changed the stored identity generation")
			}
			var savedObservation models.CredentialObservation
			if err := db.First(&savedObservation, row.ID).Error; err != nil {
				t.Fatal(err)
			}
			if savedObservation.IdentityFingerprint != after.IdentityFingerprint || savedObservation.State != models.CredentialObservationFresh {
				t.Fatal("loading invalidated the existing observation")
			}
		})
	}
}
