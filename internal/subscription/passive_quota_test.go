package subscription

import (
	"testing"

	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func testCredentialManagerForPassiveQuota() *CredentialManager {
	return NewCredentialManager(nil, nil, nil, nil, nil)
}

func floatPointer(value float64) *float64 { return &value }

func TestRecordPassiveQuotaObservationIsVisibleAsDirty(t *testing.T) {
	manager := testCredentialManagerForPassiveQuota()
	windows := []providerobservation.QuotaWindow{
		{ID: "primary", Scope: "account", Unit: "percent", State: "available", Used: floatPointer(10)},
	}
	manager.RecordPassiveQuotaObservation(7, 100, 1000, windows)

	dirty := manager.DirtyPassiveQuotaObservations(10)
	if len(dirty) != 1 || dirty[0].CredentialID != 7 || dirty[0].IdentityGeneration != 100 ||
		dirty[0].ObservedAtMS != 1000 || len(dirty[0].Windows) != 1 || dirty[0].Windows[0].ID != "primary" {
		t.Fatalf("dirty observations = %#v", dirty)
	}
}

func TestRecordPassiveQuotaObservationMergesByWindowID(t *testing.T) {
	manager := testCredentialManagerForPassiveQuota()
	manager.RecordPassiveQuotaObservation(7, 100, 1000, []providerobservation.QuotaWindow{
		{ID: "primary", Used: floatPointer(10)},
	})
	manager.RecordPassiveQuotaObservation(7, 100, 2000, []providerobservation.QuotaWindow{
		{ID: "secondary", Used: floatPointer(20)},
	})

	dirty := manager.DirtyPassiveQuotaObservations(10)
	if len(dirty) != 1 || len(dirty[0].Windows) != 2 || dirty[0].ObservedAtMS != 2000 {
		t.Fatalf("dirty observations = %#v, want both windows merged", dirty)
	}
}

func TestRecordPassiveQuotaObservationDropsOlderObservedAt(t *testing.T) {
	manager := testCredentialManagerForPassiveQuota()
	manager.RecordPassiveQuotaObservation(7, 100, 2000, []providerobservation.QuotaWindow{
		{ID: "primary", Used: floatPointer(90)},
	})
	manager.RecordPassiveQuotaObservation(7, 100, 1000, []providerobservation.QuotaWindow{
		{ID: "primary", Used: floatPointer(10)},
	})

	dirty := manager.DirtyPassiveQuotaObservations(10)
	if len(dirty) != 1 || dirty[0].ObservedAtMS != 2000 || *dirty[0].Windows[0].Used != 90 {
		t.Fatalf("dirty observations = %#v, want the newer 2000ms observation preserved", dirty)
	}
}

func TestRecordPassiveQuotaObservationIgnoresEmptyWindows(t *testing.T) {
	manager := testCredentialManagerForPassiveQuota()
	manager.RecordPassiveQuotaObservation(7, 100, 1000, nil)

	if dirty := manager.DirtyPassiveQuotaObservations(10); len(dirty) != 0 {
		t.Fatalf("dirty observations = %#v, want none for an empty-window record", dirty)
	}
}

func TestRecordPassiveQuotaObservationReplacesOnIdentityGenerationChange(t *testing.T) {
	manager := testCredentialManagerForPassiveQuota()
	manager.RecordPassiveQuotaObservation(7, 100, 1000, []providerobservation.QuotaWindow{
		{ID: "primary", Used: floatPointer(10)},
	})
	manager.RecordPassiveQuotaObservation(7, 200, 2000, []providerobservation.QuotaWindow{
		{ID: "secondary", Used: floatPointer(20)},
	})

	dirty := manager.DirtyPassiveQuotaObservations(10)
	if len(dirty) != 1 || dirty[0].IdentityGeneration != 200 || len(dirty[0].Windows) != 1 ||
		dirty[0].Windows[0].ID != "secondary" {
		t.Fatalf("dirty observations = %#v, want replaced (not merged) after identity change", dirty)
	}
}

func TestAckPassiveQuotaObservationClearsOnlyMatchingVersion(t *testing.T) {
	manager := testCredentialManagerForPassiveQuota()
	manager.RecordPassiveQuotaObservation(7, 100, 1000, []providerobservation.QuotaWindow{
		{ID: "primary", Used: floatPointer(10)},
	})
	dirty := manager.DirtyPassiveQuotaObservations(10)
	if len(dirty) != 1 {
		t.Fatalf("dirty observations = %#v", dirty)
	}
	staleVersion := dirty[0].Version

	manager.RecordPassiveQuotaObservation(7, 100, 2000, []providerobservation.QuotaWindow{
		{ID: "primary", Used: floatPointer(20)},
	})
	manager.AckPassiveQuotaObservation(7, staleVersion)
	if stillDirty := manager.DirtyPassiveQuotaObservations(10); len(stillDirty) != 1 {
		t.Fatalf("a stale ACK cleared a newer pending version: %#v", stillDirty)
	}

	current := manager.DirtyPassiveQuotaObservations(10)[0].Version
	manager.AckPassiveQuotaObservation(7, current)
	if stillDirty := manager.DirtyPassiveQuotaObservations(10); len(stillDirty) != 0 {
		t.Fatalf("matching ACK did not clear the pending entry: %#v", stillDirty)
	}
}

func TestSetPassiveQuotaDirtyNotifierFiresOnRecord(t *testing.T) {
	manager := testCredentialManagerForPassiveQuota()
	fired := make(chan struct{}, 1)
	manager.SetPassiveQuotaDirtyNotifier(func() {
		select {
		case fired <- struct{}{}:
		default:
		}
	})
	manager.RecordPassiveQuotaObservation(7, 100, 1000, []providerobservation.QuotaWindow{
		{ID: "primary", Used: floatPointer(10)},
	})
	select {
	case <-fired:
	default:
		t.Fatal("dirty notifier was not called after a record")
	}
}
