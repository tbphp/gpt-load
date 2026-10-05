package subscription

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"gpt-load/internal/storage/models"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestPassiveCreditsRecordWithoutWindows(t *testing.T) {
	manager := testCredentialManagerForPassiveQuota(t)
	at := int64(2000)
	credits := &providerobservation.CreditSummary{Balance: "62500", ObservedAtMS: &at}
	manager.RecordPassiveQuotaObservation(7, 100, at, nil, credits)
	credits.Balance = "0"
	*credits.ObservedAtMS = 9999
	dirty := manager.DirtyPassiveQuotaObservations(1)
	if len(dirty) != 1 || dirty[0].Credits == nil || dirty[0].Credits.Balance != "62500" || *dirty[0].Credits.ObservedAtMS != 2000 {
		t.Fatalf("credit-only observation was not retained and detached: %#v", dirty)
	}
}

func TestPassiveCreditsMergeZeroAndPreserveOnWindowOnlyUpdates(t *testing.T) {
	at := int64(2000)
	raw := []byte(`{"plan_summary":{"name":"Pro"},"quota_windows":[],"reset_credits_available":2,"credits":{"balance":"62500","has_credits":true,"unlimited":false,"observed_at_ms":1000}}`)
	merged, observedAt, err := mergePassiveQuotaSamples(raw, nil, PassiveQuotaObservation{
		ObservedAtMS: at, Credits: &providerobservation.CreditSummary{Balance: "0", ObservedAtMS: &at},
	})
	if err != nil || !merged.Matched || !merged.Changed || observedAt != at {
		t.Fatalf("zero balance was not applied: %#v, at=%d, error=%v", merged, observedAt, err)
	}
	var snapshot providerobservation.Snapshot
	if err := json.Unmarshal(merged.Encoded, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Credits == nil || snapshot.Credits.Balance != "0" || snapshot.Credits.Unlimited == nil || *snapshot.Credits.Unlimited || snapshot.QuotaWindows == nil || snapshot.Plan.Name != "Pro" || *snapshot.ResetCreditsAvailable != 2 {
		t.Fatalf("credit patch lost unrelated fields: %s", merged.Encoded)
	}
	unchanged, _, err := mergePassiveQuotaSamples(merged.Encoded, nil, PassiveQuotaObservation{ObservedAtMS: 3000})
	if err != nil || string(unchanged.Encoded) != string(merged.Encoded) {
		t.Fatalf("missing credit data cleared the balance: %s, %v", unchanged.Encoded, err)
	}
}

func TestPassiveCreditsFlushAddsToLegacySnapshotAndRejectsOldSamples(t *testing.T) {
	manager, db, registry, _, credential := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh,
		`{"plan_summary":{"name":"Pro"},"quota_windows":[],"reset_credits_available":2}`)
	ref, _ := registry.CredentialRef(credential.ID)
	at := int64(2000)
	manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, at, nil,
		&providerobservation.CreditSummary{Balance: "62500", ObservedAtMS: &at})
	if remaining, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || remaining {
		t.Fatalf("flush failed: pending=%v, error=%v", remaining, err)
	}
	var row models.CredentialObservation
	if err := db.Take(&row, "credential_id = ?", credential.ID).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot providerobservation.Snapshot
	if err := json.Unmarshal(row.SnapshotJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Credits == nil || snapshot.Credits.Balance != "62500" || *snapshot.Credits.ObservedAtMS != at || *row.ObservedAtMS != at {
		t.Fatalf("legacy snapshot did not acquire credits: %s", row.SnapshotJSON)
	}
	baseline := string(row.SnapshotJSON)
	older := int64(1500)
	manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, older, nil,
		&providerobservation.CreditSummary{Balance: "0", ObservedAtMS: &older})
	if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.Take(&row, "credential_id = ?", credential.ID).Error; err != nil || string(row.SnapshotJSON) != baseline {
		t.Fatalf("older credit response overwrote newer data: %s, %v", row.SnapshotJSON, err)
	}
}

func TestPendingCreditsKeepTheirTimeAcrossWindowResponses(t *testing.T) {
	manager := testCredentialManagerForPassiveQuota(t)
	at := int64(2000)
	manager.RecordPassiveQuotaObservation(7, 100, at, nil,
		&providerobservation.CreditSummary{Balance: "62500", ObservedAtMS: &at})
	manager.RecordPassiveQuotaObservation(7, 100, 3000,
		[]providerobservation.QuotaWindow{{ID: "primary", Scope: "account", Unit: "percent", Used: floatPointer(20), Utilization: floatPointer(.2)}})
	dirty := manager.DirtyPassiveQuotaObservations(1)
	if len(dirty) != 1 || dirty[0].Credits == nil || *dirty[0].Credits.ObservedAtMS != at {
		t.Fatalf("window response changed or discarded pending credit evidence: %#v", dirty)
	}
	raw := []byte(`{"plan_summary":{},"quota_windows":[{"id":"primary","unit":"percent","scope":"account","state":"available"}],"credits":{"balance":"0","observed_at_ms":2500}}`)
	storedAt := int64(2500)
	merged, _, err := mergePassiveQuotaSamples(raw, &storedAt, dirty[0])
	if err != nil {
		t.Fatal(err)
	}
	var snapshot providerobservation.Snapshot
	if err := json.Unmarshal(merged.Encoded, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Credits == nil || snapshot.Credits.Balance != "0" || *snapshot.Credits.ObservedAtMS != storedAt || len(snapshot.QuotaWindows) != 1 || snapshot.QuotaWindows[0].Used == nil || *snapshot.QuotaWindows[0].Used != 20 {
		t.Fatalf("old point data borrowed a newer window time: %s", merged.Encoded)
	}
}

func TestPassiveCreditsArrivingAfterNewerWindow(t *testing.T) {
	for _, initialCredits := range []string{
		`{"balance":"62500","observed_at_ms":1000}`,
		`{"observed_at_ms":1000}`,
	} {
		for _, flushWindowFirst := range []bool{false, true} {
			t.Run(initialCredits+"/flush="+strconv.FormatBool(flushWindowFirst), func(t *testing.T) {
				manager, db, registry, _, credential := newCredentialManagerFixture(t,
					credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
				newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh,
					`{"plan_summary":{},"quota_windows":[{"id":"primary","scope":"account","unit":"percent","state":"available"}],"credits":`+initialCredits+`}`)
				ref, _ := registry.CredentialRef(credential.ID)
				manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, 3000,
					[]providerobservation.QuotaWindow{{ID: "primary", Used: floatPointer(20), Utilization: floatPointer(.2)}})
				flush := func() {
					t.Helper()
					if remaining, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || remaining {
						t.Fatalf("flush: remaining=%t error=%v", remaining, err)
					}
				}
				if flushWindowFirst {
					flush()
				}
				// 较早的 HTTP 响应晚到：点数比已存值更新，窗口比已收到的响应更旧。
				creditAt := int64(2000)
				manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, creditAt,
					[]providerobservation.QuotaWindow{{ID: "primary", Used: floatPointer(10), Utilization: floatPointer(.1)}},
					&providerobservation.CreditSummary{Balance: "0", ObservedAtMS: &creditAt})
				pending := manager.DirtyPassiveQuotaObservations(1)
				if len(pending) != 1 || pending[0].Credits == nil || pending[0].Credits.Balance != "0" {
					t.Fatalf("late credit was dropped before flush: %#v", pending)
				}
				flush()
				var row models.CredentialObservation
				if err := db.Take(&row, "credential_id = ?", credential.ID).Error; err != nil {
					t.Fatal(err)
				}
				var snapshot providerobservation.Snapshot
				if err := json.Unmarshal(row.SnapshotJSON, &snapshot); err != nil {
					t.Fatal(err)
				}
				if snapshot.Credits == nil || snapshot.Credits.Balance != "0" ||
					snapshot.Credits.ObservedAtMS == nil || *snapshot.Credits.ObservedAtMS != creditAt ||
					row.ObservedAtMS == nil || *row.ObservedAtMS != 3000 ||
					len(snapshot.QuotaWindows) != 1 || snapshot.QuotaWindows[0].Used == nil || *snapshot.QuotaWindows[0].Used != 20 {
					t.Fatalf("late credit lost or newer window/time overwritten: %s, time=%v", row.SnapshotJSON, row.ObservedAtMS)
				}
			})
		}
	}
}

func TestPassiveCreditsRespectActiveRefreshAndLegacyTimes(t *testing.T) {
	for _, credits := range []string{
		``,
		`,"credits":{"balance":"62500"}`,
		`,"credits":{"observed_at_ms":3000}`,
		`,"credits":{"balance":"0","observed_at_ms":3000}`,
	} {
		for _, at := range []int64{2000, 3000} {
			t.Run(credits+"/at="+strconv.FormatInt(at, 10), func(t *testing.T) {
				raw := []byte(`{"plan_summary":{},"quota_windows":[]` + credits + `}`)
				storedAt := int64(3000)
				merged, observedAt, err := mergePassiveQuotaSamples(raw, &storedAt, PassiveQuotaObservation{
					ObservedAtMS: at, Credits: &providerobservation.CreditSummary{Balance: "100", ObservedAtMS: &at},
				})
				if err != nil || merged.Matched || merged.Changed || string(merged.Encoded) != string(raw) || observedAt != storedAt {
					t.Fatalf("older or tied credit overwrote active/legacy data: %s, at=%d, error=%v", merged.Encoded, observedAt, err)
				}
			})
		}
	}
}
