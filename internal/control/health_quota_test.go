package control

import (
	"encoding/json"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

// 额度观测只服务于管理面展示，不影响调度可用性；首页仍需单独暴露额度快用完的凭据。
func TestRuntimeHealthReportsLowQuotaCredentials(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	now := healthNow()
	fixture.service.now = func() time.Time { return now }

	if _, err := fixture.manager.Publish(state.CompileInput{
		ChannelRegistry: fixture.channelRegistry,
		Groups: []state.GroupConfig{
			{
				ConnectionType: "api_key", ID: 1, Name: "codex", ChannelID: channel.OpenAI,
				Params: json.RawMessage(`{}`),
				Models: []state.ModelConfig{{ID: "model"}}, Enabled: true,
			},
		},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{
		{ID: 11, GroupID: 1, Version: 1, IdentityGeneration: 11, Fingerprint: "test-11", Status: state.CredentialStatusActive, EncryptedValue: "low"},
		{ID: 12, GroupID: 1, Version: 1, IdentityGeneration: 12, Fingerprint: "test-12", Status: state.CredentialStatusActive, EncryptedValue: "healthy"},
		{ID: 13, GroupID: 1, Version: 1, IdentityGeneration: 13, Fingerprint: "test-13", Status: state.CredentialStatusActive, EncryptedValue: "stale"},
	}); err != nil {
		t.Fatalf("ReplaceCredentials() error = %v", err)
	}

	resetAt := now.Add(3 * time.Hour)
	low := 0.12
	healthy := 0.85
	recordedRemaining := 0.05

	if !fixture.registry.SetCredentialQuotaObservation(11, &low, resetAt) {
		t.Fatal("SetCredentialQuotaObservation(11) = false")
	}
	if !fixture.registry.SetCredentialQuotaObservation(12, &healthy, resetAt) {
		t.Fatal("SetCredentialQuotaObservation(12) = false")
	}
	if !fixture.registry.SetCredentialQuotaObservation(13, &recordedRemaining, resetAt) {
		t.Fatal("SetCredentialQuotaObservation(13) = false")
	}

	result, err := fixture.service.RuntimeHealth()
	if err != nil {
		t.Fatalf("RuntimeHealth() error = %v", err)
	}

	if len(result.LowQuotaCredentials) != 2 {
		t.Fatalf(
			"LowQuotaCredentials = %#v, want every recorded credential below the threshold",
			result.LowQuotaCredentials,
		)
	}
	if result.LowQuotaCredentials[0].CredentialID != 11 || result.LowQuotaCredentials[1].CredentialID != 13 {
		t.Fatalf("credential IDs = %d/%d, want 11/13", result.LowQuotaCredentials[0].CredentialID, result.LowQuotaCredentials[1].CredentialID)
	}
	got := result.LowQuotaCredentials[0]
	if got.GroupID != 1 || got.GroupName != "codex" {
		t.Fatalf("group = %d/%q, want 1/\"codex\"", got.GroupID, got.GroupName)
	}
	if got.Remaining != low {
		t.Fatalf("Remaining = %v, want %v", got.Remaining, low)
	}
	if got.ResetAtMS != resetAt.UnixMilli() {
		t.Fatalf("ResetAtMS = %d, want %d", got.ResetAtMS, resetAt.UnixMilli())
	}
}

func TestRuntimeHealthReportsResetCreditsExpiringWithinFortyEightHours(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	now := healthNow()
	fixture.service.now = func() time.Time { return now }
	groupID := createGroupWithCredentials(t, fixture, `{"api_key":"sk-expiring-reset-credit"}`)

	var credential models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).First(&credential).Error; err != nil {
		t.Fatal(err)
	}
	near := now.Add(23 * time.Hour).UnixMilli()
	later := now.Add(47 * time.Hour).UnixMilli()
	outside := now.Add(72 * time.Hour).UnixMilli()
	snapshot, err := json.Marshal(CredentialObservationSnapshot{
		QuotaWindows: []ObservationQuotaWindow{},
		ResetCredits: []ObservationResetCredit{
			{ExpiresAtMS: &near},
			{ExpiresAtMS: &later},
			{ExpiresAtMS: &outside},
			{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	observedAtMS := now.UnixMilli()
	if err := fixture.db.Create(&models.CredentialObservation{
		CredentialID:        credential.ID,
		IdentityFingerprint: credential.IdentityFingerprint,
		SchemaVersion:       1,
		ObservationVersion:  1,
		SnapshotJSON:        models.JSON(snapshot),
		State:               models.CredentialObservationFresh,
		ObservedAtMS:        &observedAtMS,
		UpdatedAtMS:         observedAtMS,
	}).Error; err != nil {
		t.Fatal(err)
	}

	result, err := fixture.service.RuntimeHealth()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ExpiringResetCredits) != 1 {
		t.Fatalf("ExpiringResetCredits = %#v", result.ExpiringResetCredits)
	}
	got := result.ExpiringResetCredits[0]
	if got.CredentialID != credential.ID || got.GroupID != groupID ||
		got.Count != 2 || got.NearestExpiresAtMS != near {
		t.Fatalf("expiring reset credit = %#v", got)
	}
}

func TestRuntimeHealthHonorsRecordedResetCreditAvailability(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		available int64
		wantCount int
	}{
		{name: "zero available", available: 0, wantCount: 0},
		{name: "caps detailed credits", available: 1, wantCount: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			now := healthNow()
			fixture.service.now = func() time.Time { return now }
			groupID := createGroupWithCredentials(t, fixture, `{"api_key":"sk-reset-credit-availability"}`)

			var credential models.Credential
			if err := fixture.db.Where("group_id = ?", groupID).First(&credential).Error; err != nil {
				t.Fatal(err)
			}
			near := now.Add(23 * time.Hour).UnixMilli()
			later := now.Add(47 * time.Hour).UnixMilli()
			snapshot, err := json.Marshal(CredentialObservationSnapshot{
				QuotaWindows:          []ObservationQuotaWindow{},
				ResetCreditsAvailable: &test.available,
				ResetCredits: []ObservationResetCredit{
					{ExpiresAtMS: &near},
					{ExpiresAtMS: &later},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			observedAtMS := now.UnixMilli()
			if err := fixture.db.Create(&models.CredentialObservation{
				CredentialID:        credential.ID,
				IdentityFingerprint: credential.IdentityFingerprint,
				SchemaVersion:       1,
				ObservationVersion:  1,
				SnapshotJSON:        models.JSON(snapshot),
				State:               models.CredentialObservationFresh,
				ObservedAtMS:        &observedAtMS,
				UpdatedAtMS:         observedAtMS,
			}).Error; err != nil {
				t.Fatal(err)
			}

			result, err := fixture.service.RuntimeHealth()
			if err != nil {
				t.Fatal(err)
			}
			if len(result.ExpiringResetCredits) != test.wantCount {
				t.Fatalf("ExpiringResetCredits = %#v, want count %d", result.ExpiringResetCredits, test.wantCount)
			}
			if test.wantCount == 0 {
				return
			}
			got := result.ExpiringResetCredits[0]
			if got.Count != test.wantCount || got.NearestExpiresAtMS != near {
				t.Fatalf("expiring reset credit = %#v", got)
			}
		})
	}
}
