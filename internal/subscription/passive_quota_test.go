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

func TestRecordPassiveQuotaObservationReplacesRatherThanCombiningResponses(t *testing.T) {
	manager := testCredentialManagerForPassiveQuota()
	manager.RecordPassiveQuotaObservation(7, 100, 1000, []providerobservation.QuotaWindow{
		{ID: "primary", Used: floatPointer(10)},
	})
	manager.RecordPassiveQuotaObservation(7, 100, 2000, []providerobservation.QuotaWindow{
		{ID: "secondary", Used: floatPointer(20)},
	})

	// A pending entry carries exactly one response, so its windows and its
	// observation time always describe the same instant. Combining responses
	// would let a field observed at 1000 be persisted stamped as 2000, which
	// can then outrank -- and overwrite -- an active refresh committed in
	// between. The window dropped here is not lost data: the stored snapshot
	// keeps its current value and the next full response refreshes it.
	dirty := manager.DirtyPassiveQuotaObservations(10)
	if len(dirty) != 1 || len(dirty[0].Windows) != 1 ||
		dirty[0].Windows[0].ID != "secondary" || dirty[0].ObservedAtMS != 2000 {
		t.Fatalf("dirty observations = %#v, want only the newest response's window", dirty)
	}
}

func TestRecordPassiveQuotaObservationCopiesCallerWindows(t *testing.T) {
	manager := testCredentialManagerForPassiveQuota()
	resetAt := int64(4242)
	windows := []providerobservation.QuotaWindow{
		{ID: "primary", Used: floatPointer(10), Utilization: floatPointer(0.1), ResetAtMS: &resetAt},
	}
	manager.RecordPassiveQuotaObservation(7, 100, 1000, windows)

	// Both the struct fields and the values behind its pointers must be
	// detached: sharing them would let a caller change what is about to be
	// persisted, under an observation time captured before the change.
	windows[0].ID = "mutated"
	*windows[0].Used = 999
	*windows[0].Utilization = 9.99
	*windows[0].ResetAtMS = 999999

	dirty := manager.DirtyPassiveQuotaObservations(10)
	if len(dirty) != 1 {
		t.Fatalf("dirty observations = %#v", dirty)
	}
	stored := dirty[0].Windows[0]
	if stored.ID != "primary" || stored.Used == nil || *stored.Used != 10 ||
		stored.Utilization == nil || *stored.Utilization != 0.1 ||
		stored.ResetAtMS == nil || *stored.ResetAtMS != 4242 {
		t.Fatalf("pending window = %#v, want it unaffected by caller mutation", stored)
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
