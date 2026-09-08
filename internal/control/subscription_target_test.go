package control

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/storage/models"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func setSubscriptionTestTarget(t *testing.T, fixture serviceFixture, groupID uint, root string) {
	t.Helper()
	params, err := json.Marshal(map[string]string{"base_url": root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		Params: optionalField[json.RawMessage]{Set: true, Value: params},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionTargetChangeInvalidatesQuotaWithoutChangingCredential(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	var before models.Credential
	if err := fixture.db.First(&before, credentialID).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&before).Update("auth_state", models.CredentialAuthStateReauthorizationRequired).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&models.CredentialObservation{
		CredentialID: credentialID, IdentityFingerprint: before.IdentityFingerprint,
		SchemaVersion: 1, ObservationVersion: 7, State: models.CredentialObservationFresh,
		SnapshotJSON: models.JSON(`{"plan_summary":{"name":"Old target"},"quota_windows":[]}`),
	}).Error; err != nil {
		t.Fatal(err)
	}
	setSubscriptionTestTarget(t, fixture, groupID, "https://relay.example/team-a")
	observation, err := fixture.service.GetCredentialObservation(t.Context(), groupID, credentialID)
	if err != nil || observation.State != string(models.CredentialObservationUnavailable) || observation.Snapshot != nil || observation.ObservationVersion <= 7 {
		t.Fatalf("observation after target change = %#v, %v", observation, err)
	}
	var after models.Credential
	if err := fixture.db.First(&after, credentialID).Error; err != nil {
		t.Fatal(err)
	}
	if after.AuthState != models.CredentialAuthStateReauthorizationRequired || after.Data != before.Data ||
		after.IdentityFingerprint != before.IdentityFingerprint || after.SecretVersion != before.SecretVersion {
		t.Fatal("changing target changed durable credential identity, secret or authorization state")
	}
}

func TestSubscriptionTargetChangeDoesNotJoinOrPersistOldObservation(t *testing.T) {
	for _, oldFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "old success", true: "old failure"}[oldFails], func(t *testing.T) {
			fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
			// 放开全局观测并发上限，只验证相同凭据在不同目标下的合并边界。
			fixture.service.observationSemaphore = make(chan struct{}, 2)
			started, release := make(chan struct{}), make(chan struct{})
			released := false
			defer func() {
				if !released {
					close(release)
				}
			}()
			fixture.service.observeSubscriptionAccount = func(ctx context.Context, _ channel.ID, _ subscriptionruntime.Credential, target subscriptionruntime.Target) (subscriptionruntime.Observation, error) {
				root, err := target.BaseURL()
				if err != nil {
					return subscriptionruntime.Observation{}, err
				}
				name := "New target"
				if root == "" {
					close(started)
					select {
					case <-release:
					case <-ctx.Done():
						return subscriptionruntime.Observation{}, ctx.Err()
					}
					if oldFails {
						return subscriptionruntime.Observation{}, errors.New("old target failed")
					}
					name = "Old target"
				}
				return subscriptionruntime.Observation{QuotaObserved: true, Payload: []byte(`{"plan_summary":{"name":"` + name + `"},"quota_windows":[]}`)}, nil
			}
			oldDone := make(chan error, 1)
			go func() {
				_, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
				oldDone <- err
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("old observation did not start")
			}
			setSubscriptionTestTarget(t, fixture, groupID, "https://relay.example/team-a")
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			fresh, err := fixture.service.RefreshCredentialObservation(ctx, groupID, credentialID)
			if err != nil || fresh.Snapshot == nil || fresh.Snapshot.Plan.Name != "New target" {
				t.Fatalf("new target observation joined old request: %#v, %v", fresh, err)
			}
			close(release)
			released = true
			select {
			case <-oldDone:
			case <-time.After(3 * time.Second):
				t.Fatal("old observation did not complete")
			}
			current, err := fixture.service.GetCredentialObservation(t.Context(), groupID, credentialID)
			if err != nil || current.Snapshot == nil || current.Snapshot.Plan.Name != "New target" || current.LastErrorCode != "" {
				t.Fatalf("old target overwrote current observation: %#v, %v", current, err)
			}
		})
	}
}

func TestResetCreditForOldTargetDoesNotRestoreNewTargetRuntime(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	fixture.service.consumeSubscriptionResetCredit = func(context.Context, channel.ID, subscriptionruntime.Credential, subscriptionruntime.Target, string) (subscriptionruntime.ResetCreditResult, error) {
		setSubscriptionTestTarget(t, fixture, groupID, "https://relay.example/new")
		if !fixture.registry.SetBlacklisted(credentialID) {
			t.Fatal("new target credential is missing")
		}
		return subscriptionruntime.ResetCreditResult{Status: "succeeded", WindowsReset: 1}, nil
	}
	observed := false
	fixture.service.observeSubscriptionAccount = func(context.Context, channel.ID, subscriptionruntime.Credential, subscriptionruntime.Target) (subscriptionruntime.Observation, error) {
		observed = true
		return subscriptionruntime.Observation{Payload: []byte(`{"plan_summary":{},"quota_windows":[]}`)}, nil
	}
	result, err := fixture.service.ConsumeCredentialResetCredit(t.Context(), groupID, credentialID, resetCreditTestKey)
	if err != nil || result.Status != "succeeded" || !result.ObservationPending || observed {
		t.Fatalf("old reset result = %#v, %v; observed=%t", result, err, observed)
	}
	current, ok := findRuntimeCredential(fixture.registry.Snapshot(), credentialID)
	if !ok || !current.Blacklisted {
		t.Fatal("old reset restored new target health")
	}
}
