package subscription

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription/providers/codex"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestCredentialRefreshPreservesStoredIdentityWithOptionalUserClaims(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, oldUser, newAccount, newUser string
		allowed                            bool
	}{
		{"same identity", "user-one", "account-one", "user-one", true},
		{"legacy metadata still absent", "", "account-one", "", true},
		{"enriched metadata", "", "account-one", "user-one", true},
		{"missing metadata", "user-one", "account-one", "", false},
		{"different user", "user-one", "account-one", "user-two", false},
		{"different account", "user-one", "account-two", "user-one", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			makeCredential := func(account, user, token string, expires time.Time) []byte {
				value := codex.Credential{Type: "codex", AccountID: account, AccessToken: "access-" + token, RefreshToken: "refresh-" + token, Expire: expires.UTC().Format(time.RFC3339)}
				if user != "" {
					claims, err := json.Marshal(map[string]any{"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": account, "chatgpt_user_id": user}})
					if err != nil {
						t.Fatal(err)
					}
					value.IDToken = "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
				}
				raw, err := codex.MarshalCredential(value)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			manager, db, registry, crypto, row := newCredentialManagerFixture(t, makeCredential("account-one", test.oldUser, "current", time.Now().Add(time.Minute)))
			before, ok := registry.CredentialRef(row.ID)
			if !ok {
				t.Fatal("missing credential")
			}
			manager.refresh = func(_ context.Context, driver subscriptionruntime.Driver, _ subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
				return driver.Parse(makeCredential(test.newAccount, test.newUser, "next", time.Now().Add(time.Hour)))
			}
			_, evidence := manager.Prepare(t.Context(), channel.Codex, credentialSnapshot(t, row, crypto), false)
			if test.allowed && evidence != nil {
				t.Fatalf("refresh rejected optional identity metadata: %s", evidence.Code)
			}
			if !test.allowed && (evidence == nil || evidence.Code != "refresh_identity_changed") {
				t.Fatalf("changed identity evidence = %#v", evidence)
			}
			var stored models.Credential
			if err := db.First(&stored, row.ID).Error; err != nil {
				t.Fatal(err)
			}
			after, ok := registry.CredentialRef(row.ID)
			if !ok {
				t.Fatal("missing refreshed credential")
			}
			if stored.IdentityFingerprint != row.IdentityFingerprint || after.IdentityGeneration != before.IdentityGeneration {
				t.Fatal("refresh rewrote durable identity or runtime generation")
			}
			if test.allowed && (stored.SecretVersion != 2 || stored.AuthState != models.CredentialAuthStateReady) {
				t.Fatal("refreshed credential was not persisted")
			}
			if !test.allowed && (stored.Data != row.Data || stored.SecretVersion != 1 || stored.AuthState != models.CredentialAuthStateReauthorizationRequired) {
				t.Fatal("changed identity overwrote the credential")
			}
		})
	}
}
