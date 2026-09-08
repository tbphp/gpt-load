package control

import (
	"testing"
	"time"

	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestManualRestoreClearsEveryModelWithoutEnablingCredential(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "disabled"}[disabled], func(t *testing.T) {
			fixture := newServiceFixture(t)
			groupID := createGroupWithCredentials(t, fixture, "model-limit-secret")
			id := batchRestoreRows(t, fixture, groupID)[0].ID
			if disabled {
				if _, err := fixture.service.BatchGroupCredentials(t.Context(), groupID, CredentialBatchRequest{Action: CredentialBatchDisable, CredentialIDs: []uint{id}}); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now()
			before, _ := fixture.registry.CredentialRef(id)
			for _, model := range []string{"a", "b"} {
				fixture.registry.SetModelCooldown(before, model, now.Add(time.Hour), now)
			}
			item, err := fixture.service.RestoreGroupCredential(t.Context(), groupID, id)
			if err != nil {
				t.Fatalf("restore model-only cooldown: %v", err)
			}
			if len(fixture.registry.ModelCooldowns(id, now)) != 0 {
				t.Fatal("model limits remain")
			}
			if disabled && item.ConfiguredStatus != string(state.CredentialStatusDisabled) {
				t.Fatal("restore enabled credential")
			}
			if accepted, _ := fixture.registry.SetModelCooldown(before, "a", now.Add(time.Hour), now); accepted {
				t.Fatal("pre-restore result restored cooldown")
			}
		})
	}
}

func TestRestoreAllAndQuotaResetClearModelCooldowns(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "limit-one\nlimit-two")
	rows := batchRestoreRows(t, fixture, groupID)
	now := time.Now()
	for _, row := range rows {
		ref, _ := fixture.registry.CredentialRef(row.ID)
		fixture.registry.SetModelCooldown(ref, "a", now.Add(time.Hour), now)
	}
	result, err := fixture.service.BatchGroupCredentials(t.Context(), groupID, CredentialBatchRequest{Action: CredentialBatchRestore, Scope: CredentialBatchScopeAll})
	if err != nil || len(result.AffectedCredentialIDs) != 2 {
		t.Fatalf("restore all = %#v, %v", result, err)
	}
	ref, _ := fixture.registry.CredentialRef(rows[0].ID)
	fixture.registry.SetModelCooldown(ref, "a", now.Add(time.Hour), now)
	if !fixture.service.restoreCredentialRuntimeAfterReset(rows[0].ID) || len(fixture.registry.ModelCooldowns(rows[0].ID, now)) != 0 {
		t.Fatal("quota reset retained model limits")
	}
}

func TestModelCooldownCollectionAndHealthKeepIndependentAccountStatus(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "limited-model-secret\nhealthy-model-secret")
	id := batchRestoreRows(t, fixture, groupID)[0].ID
	now := time.Now()
	fixture.service.now = func() time.Time { return now }
	ref, _ := fixture.registry.CredentialRef(id)
	for _, model := range []string{"b", "a"} {
		fixture.registry.SetModelCooldown(ref, model, now.Add(time.Hour), now)
	}
	query, parseErr := parseCredentialCollectionQuery("model_cooldown=true")
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	capture, err := fixture.service.captureCredentials(t.Context(), groupID)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := validateCredentialCapture(capture)
	if err != nil {
		t.Fatal(err)
	}
	collection, err := fixture.service.mapCredentialCollection(t.Context(), observation, query)
	if err != nil {
		t.Fatal(err)
	}
	if collection.Summary.ModelCooldown != 1 || collection.Summary.Available != 2 || collection.Pagination.TotalItems != 1 || len(collection.Items) != 1 {
		t.Fatalf("collection = %#v", collection)
	}
	item := collection.Items[0]
	if item.CooldownUntilMS != nil || item.Recovery.Mode != "none" || len(item.ModelCooldowns) != 2 || item.ModelCooldowns[0].Model != "a" {
		t.Fatalf("model projection = %#v", item)
	}
	health, err := fixture.service.RuntimeHealth()
	if err != nil {
		t.Fatal(err)
	}
	if health.Counts.ModelCooldown != 1 || len(health.ModelCooldownCredentials) != 1 || len(health.CooldownCredentials) != 0 {
		t.Fatalf("health = %#v", health)
	}
}

func TestRuntimeHealthKeepsModelCooldownIdentityWhenGroupIsDisabled(t *testing.T) {
	for _, connectionType := range []string{"api_key", "subscription"} {
		t.Run(connectionType, func(t *testing.T) {
			var fixture serviceFixture
			var groupID, credentialID uint
			if connectionType == "subscription" {
				fixture, groupID, credentialID = newSubscriptionCredentialFixture(t)
			} else {
				fixture = newServiceFixture(t)
				groupID = createGroupWithCredentials(t, fixture, "disabled-group-model-secret")
				credentialID = batchRestoreRows(t, fixture, groupID)[0].ID
			}
			now := time.Now()
			fixture.service.now = func() time.Time { return now }
			ref, _ := fixture.registry.CredentialRef(credentialID)
			if accepted, _ := fixture.registry.SetModelCooldown(ref, "gpt-test", now.Add(time.Hour), now); !accepted {
				t.Fatal("model cooldown was rejected")
			}
			before, err := fixture.service.RuntimeHealth()
			if err != nil || len(before.ModelCooldownCredentials) != 1 {
				t.Fatalf("initial health = %#v, %v", before, err)
			}
			for _, enabled := range []bool{false, true} {
				if _, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
					Enabled: optionalField[bool]{Set: true, Value: enabled},
				}); err != nil {
					t.Fatal(err)
				}
				got, err := fixture.service.RuntimeHealth()
				if err != nil {
					t.Fatalf("health with group enabled=%t: %v", enabled, err)
				}
				if got.Counts.ModelCooldown != 1 || len(got.ModelCooldownCredentials) != 1 {
					t.Fatalf("model cooldown disappeared after group enabled=%t: %#v", enabled, got)
				}
				detail := got.ModelCooldownCredentials[0]
				if detail.CredentialID != credentialID || detail.GroupID != groupID ||
					detail.Identity != before.ModelCooldownCredentials[0].Identity || len(detail.ModelCooldowns) != 1 {
					t.Fatalf("model cooldown identity changed: %#v", detail)
				}
				if len(got.Groups) != 1 || got.Groups[0].Enabled != enabled || (got.Counts.Credentials > 0) != enabled {
					t.Fatalf("group health changed: %#v", got)
				}
			}
		})
	}
}

func TestRestoreModelCooldownPreservesSubscriptionAuthorization(t *testing.T) {
	for _, authState := range []models.CredentialAuthState{models.CredentialAuthStateRefreshing, models.CredentialAuthStateReauthorizationRequired, models.CredentialAuthStateOutcomeUnknown} {
		t.Run(string(authState), func(t *testing.T) {
			fixture, groupID, id := newSubscriptionCredentialFixture(t)
			markStoredCredentialAuthState(t, fixture, id, authState, "test_auth_state")
			now := time.Now()
			for _, batch := range []bool{false, true} {
				ref, _ := fixture.registry.CredentialRef(id)
				fixture.registry.SetModelCooldown(ref, "gpt-test", now.Add(time.Hour), now)
				var err error
				if batch {
					_, err = fixture.service.BatchGroupCredentials(t.Context(), groupID, CredentialBatchRequest{Action: CredentialBatchRestore, Scope: CredentialBatchScopeAll})
				} else {
					_, err = fixture.service.RestoreGroupCredential(t.Context(), groupID, id)
				}
				if err != nil {
					t.Fatal(err)
				}
				view, _ := findRuntimeCredential(fixture.registry.Snapshot(), id)
				if view.AuthState != state.CredentialAuthState(authState) || view.Status != state.CredentialStatusActive || len(fixture.registry.ModelCooldowns(id, now)) != 0 {
					t.Fatalf("restored subscription = %#v", view)
				}
			}
		})
	}
}
