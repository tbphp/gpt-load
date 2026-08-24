package control

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestBatchEnableDoesNotExposeCredentialsBeforeDatabaseCommit(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "first-secret\nsecond-secret")

	var rows []models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	ids := []uint{rows[0].ID, rows[1].ID}
	if _, err := fixture.service.BatchGroupCredentials(t.Context(), groupID, CredentialBatchRequest{
		Action: CredentialBatchDisable, CredentialIDs: ids,
	}); err != nil {
		t.Fatalf("disable credentials: %v", err)
	}

	forced := errors.New("forced credential update failure")
	observedActive := false
	const callbackName = "test:observe_batch_enable_before_commit"
	if err := fixture.db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table != "credentials" {
			return
		}
		for _, view := range fixture.registry.Snapshot() {
			if (view.ID == ids[0] || view.ID == ids[1]) && view.Status == state.CredentialStatusActive {
				observedActive = true
			}
		}
		tx.AddError(forced)
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fixture.db.Callback().Update().Remove(callbackName) })

	_, err := fixture.service.BatchGroupCredentials(t.Context(), groupID, CredentialBatchRequest{
		Action: CredentialBatchEnable, CredentialIDs: ids,
	})
	if err == nil {
		t.Fatal("BatchGroupCredentials() error = nil, want database failure")
	}
	if observedActive {
		t.Fatal("credentials became routable before their database transaction committed")
	}
	for _, view := range fixture.registry.Snapshot() {
		if (view.ID == ids[0] || view.ID == ids[1]) && view.Status != state.CredentialStatusDisabled {
			t.Fatalf("credential %d runtime status = %q, want disabled", view.ID, view.Status)
		}
	}
}

func TestBatchEnableRuntimeFailureReloadsCommittedDatabaseTruth(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "runtime-recovery-secret")
	var row models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.BatchGroupCredentials(t.Context(), groupID, CredentialBatchRequest{
		Action: CredentialBatchDisable, CredentialIDs: []uint{row.ID},
	}); err != nil {
		t.Fatalf("disable credential: %v", err)
	}
	fixture.service.applyBatchRegistryMutation = func(uint, []uint, CredentialBatchAction) error {
		return errors.New("forced runtime activation failure")
	}
	fixture.service.restoreBatchRegistryEntries = func(uint, []state.CredentialEntry) error {
		return errors.New("forced exact restore failure")
	}

	_, err := fixture.service.BatchGroupCredentials(t.Context(), groupID, CredentialBatchRequest{
		Action: CredentialBatchEnable, CredentialIDs: []uint{row.ID},
	})
	if err == nil {
		t.Fatal("BatchGroupCredentials() error = nil, want runtime publication failure")
	}
	var stored models.Credential
	if err := fixture.db.Take(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.CredentialStatusActive {
		t.Fatalf("stored credential status = %q, want active", stored.Status)
	}
	view, exists := findRuntimeCredential(fixture.registry.Snapshot(), row.ID)
	if !exists || view.Status != state.CredentialStatusActive {
		t.Fatalf("runtime credential = %#v, exists=%t; want committed active state", view, exists)
	}
}

func TestBatchDeleteRetiresCommittedCredentialRuntimes(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	runtime := &recordingCredentialRuntimeExecutor{}
	fixture.service.executor = runtime
	groupID := createGroupWithCredentials(t, fixture, "first-secret\nsecond-secret")

	var rows []models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	ids := []uint{rows[0].ID, rows[1].ID}
	if _, err := fixture.service.BatchGroupCredentials(t.Context(), groupID, CredentialBatchRequest{
		Action: CredentialBatchDelete, CredentialIDs: ids,
	}); err != nil {
		t.Fatalf("BatchGroupCredentials() error = %v", err)
	}
	if got := runtime.retiredCredentialIDs(); !reflect.DeepEqual(got, ids) {
		t.Fatalf("retired credential runtimes = %#v, want %#v", got, ids)
	}
}

func TestBatchAllCredentialsAffectsOnlyCurrentGroup(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "first-secret\nsecond-secret")
	otherGroupName := "other-credential-group"
	otherGroup, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name:              &otherGroupName,
		ChannelID:         channel.OpenAI,
		Params:            json.RawMessage(`{}`),
		Models:            optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-4o"}}},
		Credentials:       "other-secret",
		ConnectionType:    models.ConnectionTypeAPIKey,
		ConfirmSameTarget: true,
	})
	if err != nil {
		t.Fatalf("CreateGroup(%q) error = %v", otherGroupName, err)
	}
	otherGroupID := otherGroup.GroupID

	result, err := fixture.service.BatchGroupCredentials(t.Context(), groupID, CredentialBatchRequest{
		Action: CredentialBatchDisable,
		Scope:  CredentialBatchScopeAll,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.AffectedCredentialIDs) != 2 || result.Summary.Disabled != 2 {
		t.Fatalf("batch-all response = %#v", result)
	}
	for _, view := range fixture.registry.Snapshot() {
		switch view.GroupID {
		case groupID:
			if view.Status != state.CredentialStatusDisabled {
				t.Fatalf("credential %d status = %q, want disabled", view.ID, view.Status)
			}
		case otherGroupID:
			if view.Status != state.CredentialStatusActive {
				t.Fatalf("other-group credential %d status = %q, want active", view.ID, view.Status)
			}
		}
	}
}

func TestBatchAllCredentialsRejectsDeleteAndExplicitIDs(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "first-secret")

	for _, request := range []CredentialBatchRequest{
		{Action: CredentialBatchDelete, Scope: CredentialBatchScopeAll},
		{Action: CredentialBatchDisable, Scope: CredentialBatchScopeAll, CredentialIDs: []uint{1}},
	} {
		if _, err := fixture.service.BatchGroupCredentials(t.Context(), groupID, request); !errors.Is(err, app_errors.ErrValidation) {
			t.Fatalf("BatchGroupCredentials(%#v) error = %v, want validation", request, err)
		}
	}
}
