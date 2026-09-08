package subscription

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestRecordPassiveQuotaObservationPreservesNewTargetWhenOldResponseArrives(t *testing.T) {
	manager, db, registry, _, row := newCredentialManagerFixture(t,
		credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	entries, err := registry.SnapshotGroupCredentialEntriesExact(row.GroupID, []uint{row.ID})
	if err != nil || len(entries) != 1 {
		t.Fatalf("read credential entries: %v", err)
	}
	oldIdentity := entries[0].IdentityGeneration
	params := models.JSON(`{"base_url":"https://relay.example/current"}`)
	manager.mutations.Do(row.ID, func() {
		if err := db.Model(&models.Group{}).Where("id = ?", row.GroupID).Update("params", params).Error; err != nil {
			t.Fatal(err)
		}
		entries[0].IdentityGeneration = stateloader.CredentialIdentityGeneration(
			row.IdentityFingerprint, "codex", "subscription", json.RawMessage(params))
		if _, err := registry.ReconcileGroup(row.GroupID, entries); err != nil {
			t.Fatal(err)
		}
	})
	newFlushableCredentialObservation(t, manager, row.ID, models.CredentialObservationFresh,
		`{"quota_windows":[{"id":"primary","scope":"account","unit":"percent","state":"available","utilization":0.1,"reset_at_ms":1800000000000}]}`)
	newUtilization := 0.25
	manager.RecordPassiveQuotaObservation(row.ID, entries[0].IdentityGeneration, 2000,
		[]providerobservation.QuotaWindow{{ID: "primary", Utilization: &newUtilization}})
	current := manager.DirtyPassiveQuotaObservations(1)
	if len(current) != 1 {
		t.Fatal("new target observation was not queued")
	}

	// 旧请求可以晚于新请求返回，时间戳更新也不能替换当前目标已排队的样本。
	manager.RecordPassiveQuotaObservation(row.ID, oldIdentity, 3000,
		[]providerobservation.QuotaWindow{{ID: "primary", State: "exhausted"}})
	if pending := manager.DirtyPassiveQuotaObservations(1); !reflect.DeepEqual(pending, current) {
		t.Fatalf("old target response replaced the new pending observation: got=%#v want=%#v", pending, current)
	}
	if pending, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || pending {
		t.Fatalf("flush current target observation: pending=%t error=%v", pending, err)
	}
	var stored models.CredentialObservation
	if err := db.Take(&stored, "credential_id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot providerobservation.Snapshot
	if err := json.Unmarshal(stored.SnapshotJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	if stored.ObservedAtMS == nil || *stored.ObservedAtMS != 2000 || len(snapshot.QuotaWindows) != 1 ||
		snapshot.QuotaWindows[0].Utilization == nil || *snapshot.QuotaWindows[0].Utilization != newUtilization {
		t.Fatalf("new target observation was lost before persistence: %s", stored.SnapshotJSON)
	}
}
