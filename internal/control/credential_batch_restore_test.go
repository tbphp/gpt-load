package control

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/health"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestBatchRestoreAllCredentialsPreservesConfigurationAndHistory(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "cooldown-secret\nblacklisted-secret\ndisabled-secret\navailable-secret")
	other, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("other restore group"), ChannelID: channel.OpenAI,
		Params: json.RawMessage(`{}`), Models: optionalGroupModels{Set: true},
		Credentials: "other-secret", ConnectionType: models.ConnectionTypeAPIKey, ConfirmSameTarget: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := batchRestoreRows(t, fixture, groupID)
	otherID := batchRestoreRows(t, fixture, other.GroupID)[0].ID
	now := time.Now().UTC()
	fixture.service.now = func() time.Time { return now }
	weight := 17
	if _, err := fixture.service.UpdateGroupCredential(t.Context(), groupID, rows[0].ID, CredentialUpdateRequest{
		WeightManual: optionalField[int]{Set: true, Value: weight},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.BatchGroupCredentials(t.Context(), groupID, CredentialBatchRequest{
		Action: CredentialBatchDisable, CredentialIDs: []uint{rows[2].ID},
	}); err != nil {
		t.Fatal(err)
	}
	fixture.registry.SetCooldown(rows[0].ID, now.Add(time.Hour))
	for _, id := range []uint{rows[1].ID, rows[2].ID, otherID} {
		fixture.registry.SetBlacklisted(id)
	}
	for _, row := range rows {
		fixture.registry.IncrFailure(row.ID)
		fixture.stats.RecordSuccess(row.ID, now.Add(-time.Second))
		fixture.stats.RecordFailure(row.ID, health.FailureCategoryInvalidKey, 401, now)
	}
	beforeRows := batchRestoreRows(t, fixture, groupID)
	beforeViews := fixture.registry.Snapshot()
	executor := &credentialProbeTestExecutor{}
	fixture.service.executor = executor
	result, err := fixture.service.BatchGroupCredentials(t.Context(), groupID, CredentialBatchRequest{
		Action: CredentialBatchAction("restore"), Scope: CredentialBatchScopeAll,
	})
	if err != nil {
		t.Fatalf("restore all: %v", err)
	}
	if !reflect.DeepEqual(result.AffectedCredentialIDs, []uint{rows[0].ID, rows[1].ID}) ||
		result.Summary != (CredentialSummaryResponse{Total: 4, Available: 3, Disabled: 1}) {
		t.Fatalf("restore response = %#v", result)
	}
	if !reflect.DeepEqual(beforeRows, batchRestoreRows(t, fixture, groupID)) {
		t.Fatal("restore changed persisted credentials")
	}
	for _, before := range beforeViews {
		after, exists := findRuntimeCredential(fixture.registry.Snapshot(), before.ID)
		if !exists {
			t.Fatalf("credential %d missing after restore", before.ID)
		}
		if before.ID != rows[0].ID && before.ID != rows[1].ID {
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("non-target credential %d changed", before.ID)
			}
			continue
		}
		stats := fixture.stats.Snapshot(before.ID, now)
		if stats.Success != 1 || stats.Failure != 1 || stats.ConsecutiveFailure != 0 ||
			stats.ConsecutiveProblem != 0 || stats.LastStatusCode != 0 || stats.LastFailureCategory != health.FailureCategoryAmbiguous {
			t.Fatalf("credential %d history = %#v", before.ID, stats)
		}
		want := before
		want.CooldownUntil = time.Time{}
		want.Blacklisted = false
		want.FailureCount = 0
		if !reflect.DeepEqual(after, want) {
			t.Fatalf("credential %d runtime = %#v, want %#v", before.ID, after, want)
		}
	}
	if calls := executor.recordedCalls(); len(calls) != 0 {
		t.Fatalf("restore sent %d upstream requests", len(calls))
	}
}

func TestBatchRestoreAllSubscriptionCredentialsPreservesQuotaAndAuth(t *testing.T) {
	t.Parallel()
	for _, authState := range []models.CredentialAuthState{
		models.CredentialAuthStateReady, models.CredentialAuthStateRefreshing,
		models.CredentialAuthStateReauthorizationRequired, models.CredentialAuthStateOutcomeUnknown,
	} {
		t.Run(string(authState), func(t *testing.T) {
			t.Parallel()
			fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
			markStoredCredentialAuthState(t, fixture, credentialID, authState, "test_auth_state")
			remaining, utilization := 30.0, 0.7
			resetAt := time.Now().Add(time.Hour).UnixMilli()
			if !fixture.registry.ApplyQuotaWindows(credentialID, []providerobservation.QuotaWindow{{
				ID: "primary", Scope: "account", State: "available", Remaining: &remaining,
				Utilization: &utilization, ResetAtMS: &resetAt,
			}}) {
				t.Fatal("publish quota failed")
			}
			fixture.registry.SetBlacklisted(credentialID)
			before, _ := findRuntimeCredential(fixture.registry.Snapshot(), credentialID)
			beforeRows := batchRestoreRows(t, fixture, groupID)
			fixture.service.prepareSubscriptionCredential = nil
			fixture.service.recoverSubscriptionCredential = nil
			executor := &credentialProbeTestExecutor{}
			fixture.service.executor = executor
			result, err := fixture.service.BatchGroupCredentials(t.Context(), groupID, CredentialBatchRequest{
				Action: CredentialBatchAction("restore"), Scope: CredentialBatchScopeAll,
			})
			if err != nil {
				t.Fatalf("restore subscription group: %v", err)
			}
			after, _ := findRuntimeCredential(fixture.registry.Snapshot(), credentialID)
			if authState == models.CredentialAuthStateReady {
				if !reflect.DeepEqual(result.AffectedCredentialIDs, []uint{credentialID}) || after.Blacklisted {
					t.Fatalf("ready credential was not restored: %#v", result)
				}
			} else if len(result.AffectedCredentialIDs) != 0 || !reflect.DeepEqual(before, after) {
				t.Fatalf("restore changed unavailable auth state %q", authState)
			}
			if !reflect.DeepEqual(before.QuotaRemaining, after.QuotaRemaining) || !before.QuotaResetAt.Equal(after.QuotaResetAt) ||
				before.AuthState != after.AuthState || !reflect.DeepEqual(beforeRows, batchRestoreRows(t, fixture, groupID)) {
				t.Fatal("restore changed quota, auth, or persisted credentials")
			}
			if calls := executor.recordedCalls(); len(calls) != 0 {
				t.Fatalf("restore sent %d upstream requests", len(calls))
			}
		})
	}
}

func TestBatchRestoreAllCredentialsWithoutEligibleTargets(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"available", "empty", "disabled group", "zero weight group"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			fixture := newServiceFixture(t)
			groupID := createGroupWithCredentials(t, fixture, "no-target-secret")
			credentialID := batchRestoreRows(t, fixture, groupID)[0].ID
			switch scenario {
			case "empty":
				if err := fixture.service.DeleteGroupCredential(t.Context(), groupID, credentialID); err != nil {
					t.Fatal(err)
				}
			case "disabled group", "zero weight group":
				fixture.registry.SetBlacklisted(credentialID)
				field, value := "enabled", any(false)
				if scenario == "zero weight group" {
					field, value = "weight_manual", 0
				}
				if err := fixture.db.Model(&models.Group{}).Where("id = ?", groupID).Update(field, value).Error; err != nil {
					t.Fatal(err)
				}
			}
			before := fixture.registry.Snapshot()
			result, err := fixture.service.BatchGroupCredentials(t.Context(), groupID, CredentialBatchRequest{
				Action: CredentialBatchAction("restore"), Scope: CredentialBatchScopeAll,
			})
			if err != nil || result.AffectedCredentialIDs == nil || len(result.AffectedCredentialIDs) != 0 {
				t.Fatalf("restore response = %#v, error = %v", result, err)
			}
			if !reflect.DeepEqual(before, fixture.registry.Snapshot()) {
				t.Fatal("restore changed a group without eligible targets")
			}
		})
	}
}

func TestBatchRestoreRequiresAllScope(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "scope-secret")
	credentialID := batchRestoreRows(t, fixture, groupID)[0].ID
	for _, request := range []CredentialBatchRequest{
		{Action: CredentialBatchAction("restore"), CredentialIDs: []uint{credentialID}},
		{Action: CredentialBatchAction("restore")},
		{Action: CredentialBatchAction("restore"), Scope: CredentialBatchScopeAll, CredentialIDs: []uint{credentialID}},
	} {
		if _, err := fixture.service.BatchGroupCredentials(t.Context(), groupID, request); !errors.Is(err, app_errors.ErrValidation) {
			t.Fatalf("restore scope error = %v, want validation", err)
		}
	}
}

func TestBatchRestoreUsesStateAtMutationBoundary(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "recovered-secret\nnew-problem-secret")
	rows := batchRestoreRows(t, fixture, groupID)
	fixture.registry.SetBlacklisted(rows[0].ID)
	fixture.service.mutations = &batchRestoreBoundaryCoordinator{
		MutationCoordinator: health.NewMutationCoordinator(),
		before: func() {
			fixture.registry.RestoreRuntimeState(rows[0].ID)
			fixture.registry.SetBlacklisted(rows[1].ID)
		},
	}
	result, err := fixture.service.BatchGroupCredentials(t.Context(), groupID, CredentialBatchRequest{
		Action: CredentialBatchAction("restore"), Scope: CredentialBatchScopeAll,
	})
	if err != nil || !reflect.DeepEqual(result.AffectedCredentialIDs, []uint{rows[1].ID}) {
		t.Fatalf("restore response = %#v, error = %v", result, err)
	}
}

func TestBatchRestoreWaitsForOperationRecovery(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "recovery-barrier-secret")
	credentialID := batchRestoreRows(t, fixture, groupID)[0].ID
	fixture.registry.SetBlacklisted(credentialID)
	fixture.service.reconcileRegistryGroup = func(uint, []state.CredentialEntry) (bool, error) {
		return false, errors.New("registry remains unavailable")
	}
	mutations := 0
	operation := newDurableGroupOperationInput(t, fixture, "048f47a2-9c35-4d6e-8b1a-1234567890ab", &mutations)
	_, err := fixture.service.executeIdempotentOperation(t.Context(), operation)
	assertAPIErrorCode(t, err, app_errors.ErrControlOperationIncomplete.Code)
	before := fixture.registry.Snapshot()
	_, err = fixture.service.BatchGroupCredentials(t.Context(), groupID, CredentialBatchRequest{
		Action: CredentialBatchAction("restore"), Scope: CredentialBatchScopeAll,
	})
	assertAPIErrorCode(t, err, app_errors.ErrControlRecoveryPending.Code)
	if !reflect.DeepEqual(before, fixture.registry.Snapshot()) {
		t.Fatal("restore changed runtime state before pending recovery completed")
	}
}

type batchRestoreBoundaryCoordinator struct {
	*health.MutationCoordinator
	before func()
}

func (coordinator *batchRestoreBoundaryCoordinator) DoMany(ids []uint, fn func()) {
	coordinator.MutationCoordinator.DoMany(ids, func() {
		coordinator.before()
		fn()
	})
}

func batchRestoreRows(t *testing.T, fixture serviceFixture, groupID uint) []models.Credential {
	t.Helper()
	var rows []models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}
