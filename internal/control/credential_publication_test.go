package control

import (
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/concurrency"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type credentialPublicationReconciler func(*state.ConfigSnapshot) error

func (reconcile credentialPublicationReconciler) ReconcileConfigSnapshot(snapshot *state.ConfigSnapshot) error {
	return reconcile(snapshot)
}

func TestCredentialPublicationReleasesMutationLockBeforeSnapshotLock(t *testing.T) {
	for _, scenario := range []string{"save", "publication_failure", "registry_failure"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newServiceFixture(t)
			groupID := createGroupWithCredentials(t, fixture, "sk-publication-lock-test")
			var row models.Credential
			if err := fixture.db.Where("group_id = ?", groupID).First(&row).Error; err != nil {
				t.Fatal(err)
			}
			before := fixture.manager.Current().Revision
			switch scenario {
			case "publication_failure":
				fixture.service.publishSnapshot = func(state.CompileInput) (*state.ConfigSnapshot, error) {
					return nil, errors.New("forced publication failure")
				}
			case "registry_failure":
				const callback = "test:remove_credential_before_registry_apply"
				if err := fixture.db.Callback().Update().After("gorm:update").Register(callback, func(*gorm.DB) {
					fixture.registry.RemoveCredential(row.ID)
				}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = fixture.db.Callback().Update().Remove(callback) })
			}

			var workers []<-chan struct{}
			t.Cleanup(func() {
				for _, done := range workers {
					select {
					case <-done:
					case <-time.After(2 * time.Second):
						t.Error("credential mutation worker did not finish")
					}
				}
			})
			publications := 0
			fixture.manager.SetSnapshotReconciler(credentialPublicationReconciler(func(snapshot *state.ConfigSnapshot) error {
				publications++
				if fixture.service.writeMu.TryLock() {
					fixture.service.writeMu.Unlock()
					t.Error("control writes were not serialized through publication")
				}
				// Publish already holds publishMu. Taking the credential stripe here
				// exercises validation recovery's lock order. Time out on regression
				// so publication can return and release the stripe for worker cleanup.
				done := make(chan struct{})
				workers = append(workers, done)
				go func() {
					fixture.mutations.Do(row.ID, func() {})
					close(done)
				}()
				select {
				case <-done:
					return fixture.accessQuota.Reconcile(snapshot.AccessQuotaDefinitions())
				case <-time.After(2 * time.Second):
					t.Error("publication blocked validation recovery on the credential stripe")
					return errors.New("credential publication lock inversion")
				}
			}))

			_, err := fixture.service.UpdateGroupCredential(t.Context(), groupID, row.ID, CredentialUpdateRequest{
				WeightManual:   optionalField[int]{Set: true, Value: 7},
				MaxConcurrency: optionalField[int64]{Set: true, Value: 2},
			})
			if scenario == "save" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				wantStage := stagePublishCommittedSnapshot
				if scenario == "registry_failure" {
					wantStage = stageApplyCommittedRegistryMutation
				}
				var operationErr *controlOperationError
				if !errors.As(err, &operationErr) || operationErr.stage != wantStage {
					t.Fatalf("save error = %v, want stage %s", err, wantStage)
				}
			}
			snapshot := fixture.manager.Current()
			if publications != 1 || snapshot.Revision != before+1 || snapshot.ConcurrencyPolicies[concurrency.Credential(row.ID)] != 2 {
				t.Fatalf("committed concurrency policy was not published once: publications=%d revision=%d", publications, snapshot.Revision)
			}
			view, exists := findRuntimeCredential(fixture.registry.Snapshot(), row.ID)
			if !exists || view.WeightManual == nil || *view.WeightManual != 7 {
				t.Fatalf("committed credential weight was not applied: %+v", view)
			}
		})
	}
}
