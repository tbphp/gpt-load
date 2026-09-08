package subscription

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestFlushPassiveQuotaObservationsDoesNotPublishOldTargetAfterURLChange(t *testing.T) {
	manager, db, registry, _, credential := newCredentialManagerFixture(t,
		credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	initial := newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh,
		`{"quota_windows":[{"id":"primary","scope":"account","unit":"percent","state":"available","utilization":0.1,"reset_at_ms":1800000000000}]}`)
	entries, err := registry.SnapshotGroupCredentialEntriesExact(credential.GroupID, []uint{credential.ID})
	if err != nil || len(entries) != 1 {
		t.Fatalf("load credential entries: %v", err)
	}
	previousIdentity := entries[0].IdentityGeneration
	manager.RecordPassiveQuotaObservation(credential.ID, previousIdentity, 2000,
		[]providerobservation.QuotaWindow{{ID: "primary", State: "exhausted"}})

	// 数据库已落盘但额度尚未投影时暂停，复现旧目标结果迟到的窗口。
	persisted := make(chan struct{})
	resume := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(resume) }) }
	t.Cleanup(release)
	if err := db.Callback().Update().After("gorm:commit_or_rollback_transaction").
		Register("test:pause_passive_quota_projection", func(tx *gorm.DB) {
			if tx.Statement == nil || tx.Statement.Table != "credential_observations" {
				return
			}
			updates, ok := tx.Statement.Dest.(map[string]any)
			if !ok || updates["observed_at_ms"] != int64(2000) {
				return
			}
			close(persisted)
			<-resume
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Update().Remove("test:pause_passive_quota_projection")
	})
	flushed := make(chan error, 1)
	go func() {
		remaining, flushErr := manager.FlushPassiveQuotaObservations(t.Context())
		if flushErr == nil && remaining {
			flushErr = fmt.Errorf("old target sample remained pending after flush")
		}
		flushed <- flushErr
	}()
	select {
	case <-persisted:
	case <-time.After(time.Second):
		t.Fatal("passive quota persistence did not reach the projection barrier")
	}

	// 对齐分组 URL 更新的原子边界：清空观测、递增 CAS 版本，再发布新目标身份。
	params := models.JSON(`{"base_url":"https://relay.example/team-a"}`)
	entries[0].IdentityGeneration = stateloader.CredentialIdentityGeneration(
		credential.IdentityFingerprint, "codex", string(models.ConnectionTypeSubscription), json.RawMessage(params))
	emptySnapshot := models.JSON(`{"quota_windows":[]}`)
	changed := make(chan error, 1)
	go func() {
		var changeErr error
		manager.mutations.Do(credential.ID, func() {
			changeErr = db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Model(&models.Group{}).Where("id = ?", credential.GroupID).
					Update("params", params).Error; err != nil {
					return err
				}
				return tx.Model(&models.CredentialObservation{}).
					Where("credential_id = ?", credential.ID).
					Updates(map[string]any{
						"snapshot_json": emptySnapshot, "state": models.CredentialObservationUnavailable,
						"observed_at_ms": nil, "updated_at_ms": int64(3000),
						"observation_version": initial.ObservationVersion + 1,
					}).Error
			})
			if changeErr == nil {
				_, changeErr = registry.ReconcileGroup(credential.GroupID, entries)
			}
		})
		changed <- changeErr
	}()
	select {
	case changeErr := <-changed:
		if changeErr != nil {
			t.Fatal(changeErr)
		}
	case <-time.After(100 * time.Millisecond):
		// 正确实现会把目标切换排在本次落盘及额度投影之后。
		release()
		select {
		case changeErr := <-changed:
			if changeErr != nil {
				t.Fatal(changeErr)
			}
		case <-time.After(time.Second):
			t.Fatal("URL change did not finish after releasing passive quota projection")
		}
	}
	release()
	select {
	case flushErr := <-flushed:
		if flushErr != nil {
			t.Fatal(flushErr)
		}
	case <-time.After(time.Second):
		t.Fatal("passive quota flush did not finish")
	}

	var stored models.CredentialObservation
	if err := db.Take(&stored, "credential_id = ?", credential.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored.SnapshotJSON, emptySnapshot) || stored.ObservedAtMS != nil ||
		stored.ObservationVersion != initial.ObservationVersion+1 {
		t.Fatalf("old target changed the new observation: %#v", stored)
	}
	ref, ok := registry.CredentialRef(credential.ID)
	if !ok || ref.IdentityGeneration != entries[0].IdentityGeneration || ref.IdentityGeneration == previousIdentity {
		t.Fatal("URL change did not publish the new target identity")
	}
	views := registry.Snapshot()
	if len(views) != 1 || views[0].ObservedQuotaRemaining() != nil {
		t.Fatalf("old target quota was projected onto the new target: %#v", views)
	}
}
