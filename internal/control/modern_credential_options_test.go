package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestModernCredentialOptionsPreserveExactAccountMemberships(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	firstGroup, _ := createHomeSubscriptionCredential(t, fixture, "first", "shared", "same@example.com")
	secondGroup, _ := createHomeSubscriptionCredential(t, fixture, "second", "shared", "same@example.com")
	otherGroup, _ := createHomeSubscriptionCredential(t, fixture, "other", "different", "same@example.com")
	apiGroup := createGroupWithCredentials(t, fixture, "sk-credential-options-secret")
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	recorder := performGroupCollectionRequest(engine, "/api/modern/credentials/options", "Bearer "+authTestKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("options status = %d, want 200", recorder.Code)
	}
	var envelope struct {
		Data struct {
			Items []struct {
				Key      string `json:"key"`
				Label    string `json:"label"`
				GroupIDs []uint `json:"group_ids"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Items) != 3 {
		t.Fatalf("got %d options, want 3 distinct identities", len(envelope.Data.Items))
	}
	for _, option := range envelope.Data.Items {
		if len(option.Key) != 64 {
			t.Fatal("credential selector must use a stable identity key, not a record ID")
		}
		switch {
		case slices.Contains(option.GroupIDs, firstGroup):
			if option.Label != "same@example.com" || !slices.Equal(option.GroupIDs, []uint{firstGroup, secondGroup}) {
				t.Fatalf("shared account memberships = %#v", option)
			}
		case slices.Contains(option.GroupIDs, otherGroup):
			if len(option.GroupIDs) != 1 {
				t.Fatal("same email incorrectly merged different identities")
			}
		default:
			if !slices.Equal(option.GroupIDs, []uint{apiGroup}) ||
				!strings.Contains(option.Label, "*") {
				t.Fatalf("API key is not masked: %#v", option)
			}
		}
	}
	if strings.Contains(recorder.Body.String(), "sk-credential-options-secret") {
		t.Fatal("options exposed an unmasked credential")
	}
	if strings.Contains(recorder.Body.String(), `"credential_id"`) || strings.Contains(recorder.Body.String(), `"id"`) {
		t.Fatal("selector options exposed record IDs instead of identity keys")
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("credential options must not be cached by the browser")
	}
	readOnly, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "options readonly"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path   string
		auth   string
		status int
	}{
		{"/api/modern/credentials/options", "", http.StatusUnauthorized},
		{"/api/modern/credentials/options", "Bearer " + readOnly.Key, http.StatusForbidden},
		{"/api/modern/credentials/options?q=secret", "Bearer " + authTestKey, http.StatusBadRequest},
	} {
		response := performGroupCollectionRequest(engine, test.path, test.auth)
		if response.Code != test.status {
			t.Fatalf("%s status = %d, want %d", test.path, response.Code, test.status)
		}
	}
}

func TestCredentialFiltersAndHomeMergeCurrentIdentityDespiteStoredFingerprintDifferences(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.September, 17, 8, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	firstGroup, first := createHomeSubscriptionCredential(t, fixture, "legacy", "shared", "admin@example.com")
	secondGroup, second := createHomeSubscriptionCredential(t, fixture, "current", "shared", "admin@example.com")
	legacyFingerprint := fixture.encryption.Hash("old-workspace-only-identity")
	if err := fixture.db.Model(&first).Update("identity_fingerprint", legacyFingerprint).Error; err != nil {
		t.Fatal(err)
	}
	var group models.Group
	if err := fixture.db.First(&group, firstGroup).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.registry.ApplyCredentialImport(firstGroup, []state.CredentialEntry{{
		ID: first.ID, GroupID: firstGroup, Version: first.SecretVersion,
		Fingerprint: first.Fingerprint, EncryptedValue: first.Data,
		IdentityGeneration: groupCollectionCredentialIdentity(legacyFingerprint, group),
		Status:             state.CredentialStatusActive, AuthState: state.CredentialAuthStateReady,
	}}); err != nil {
		t.Fatal(err)
	}
	bucket := now.Add(-time.Hour).UnixMilli()
	stats := []models.CredentialAttemptStat{
		{CredentialID: first.ID, BucketStartMS: bucket, SuccessCount: 5},
		{CredentialID: second.ID, BucketStartMS: bucket, SuccessCount: 5},
	}
	for index := range 5 {
		_, credential := createHomeSubscriptionCredential(t, fixture,
			fmt.Sprintf("rank-%d", index), fmt.Sprintf("rank-%d", index), fmt.Sprintf("rank-%d@example.com", index))
		stats = append(stats, models.CredentialAttemptStat{
			CredentialID: credential.ID, BucketStartMS: bucket, SuccessCount: int64(9 - index),
		})
	}
	if err := fixture.db.Create(&stats).Error; err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	options := performGroupCollectionRequest(engine, "/api/modern/credentials/options", "Bearer "+authTestKey)
	var optionEnvelope struct {
		Data struct {
			Items []struct {
				Key      string `json:"key"`
				Label    string `json:"label"`
				GroupIDs []uint `json:"group_ids"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(options.Body.Bytes(), &optionEnvelope); err != nil {
		t.Fatal(err)
	}
	var key string
	matched := 0
	for _, option := range optionEnvelope.Data.Items {
		if option.Label != "admin@example.com" {
			continue
		}
		matched++
		key = option.Key
		if !slices.Equal(option.GroupIDs, []uint{firstGroup, secondGroup}) {
			t.Fatal("current identity did not discover both groups")
		}
	}
	if matched != 1 || len(key) != 64 {
		t.Fatal("same account with different stored fingerprints was not deduplicated")
	}
	for _, membership := range []struct{ groupID, credentialID uint }{{firstGroup, first.ID}, {secondGroup, second.ID}} {
		response := performGroupCollectionRequest(engine,
			fmt.Sprintf("/api/modern/groups/%d/credentials?credential_key=%s", membership.groupID, key), "Bearer "+authTestKey)
		var envelope struct {
			Data CredentialCollectionResponse `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || len(envelope.Data.Items) != 1 || envelope.Data.Items[0].CredentialID != membership.credentialID {
			t.Fatal("stable selector did not match the same account in both groups")
		}
	}
	home, err := fixture.service.ReadHomeSubscriptionAccounts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(home.Items) != 4 || home.Items[0].Credential.Account.Email != "admin@example.com" || home.Items[0].GroupCount != 2 || home.Items[0].GroupID != nil {
		t.Fatal("homepage must aggregate account activity before ranking and count both groups")
	}
	payload, err := json.Marshal(home.Items[0])
	if err != nil {
		t.Fatal(err)
	}
	var link struct {
		CredentialKey string `json:"credential_key"`
	}
	if err := json.Unmarshal(payload, &link); err != nil {
		t.Fatal(err)
	}
	if link.CredentialKey != key {
		t.Fatal("homepage and group selector use different identity keys")
	}
	var unchanged models.Credential
	if err := fixture.db.First(&unchanged, first.ID).Error; err != nil {
		t.Fatal(err)
	}
	if unchanged.IdentityFingerprint != legacyFingerprint || unchanged.Data != first.Data {
		t.Fatal("read-only identity grouping rewrote stored credentials")
	}
}
