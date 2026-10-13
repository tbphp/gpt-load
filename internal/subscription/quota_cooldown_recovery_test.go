package subscription

import (
	"testing"
	"time"

	"gpt-load/internal/storage/models"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestPassiveQuotaRecoveryClearsBothLevelsOnce(t *testing.T) {
	manager, _, registry, _, row := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	newFlushableCredentialObservation(t, manager, row.ID, models.CredentialObservationFresh,
		`{"quota_windows":[{"id":"primary","source_id":"codex","scope":"account","unit":"percent","state":"exhausted","used":100,"window_seconds":604800}]}`)
	now := time.UnixMilli(1500)
	manager.now = func() time.Time { return time.UnixMilli(4000) }
	ref, _ := registry.CredentialRef(row.ID)
	registry.SetQuotaCooldown(ref, "", now.Add(time.Hour), now)
	registry.SetQuotaCooldown(ref, "a", now.Add(time.Hour), now)
	period, used, utilization := int64(604800), 95.0, 0.95
	patches := []providerobservation.QuotaWindow{{ID: "secondary", SourceID: "codex", WindowSeconds: &period, State: "available", Used: &used, Utilization: &utilization}}
	manager.RecordPassiveQuotaObservation(row.ID, ref.IdentityGeneration, 2000, patches)
	if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
		t.Fatal(err)
	}
	view := registry.Snapshot()[0]
	if !view.CooldownUntil.IsZero() || len(view.ModelCooldowns) != 0 {
		t.Fatalf("cooldowns survived recovery: %#v", view)
	}
	after, _ := registry.CredentialRef(row.ID)
	registry.SetQuotaCooldown(after, "a", now.Add(time.Hour), now)
	manager.RecordPassiveQuotaObservation(row.ID, ref.IdentityGeneration, 3000, patches)
	if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
		t.Fatal(err)
	}
	latest, _ := registry.CredentialRef(row.ID)
	if latest.QuotaCooldownGeneration != after.QuotaCooldownGeneration || registry.Snapshot()[0].ModelCooldowns["a"].IsZero() {
		t.Fatal("same quota cleared cooldown again")
	}
	if accepted, _ := registry.SetQuotaCooldown(ref, "", now.Add(time.Hour), now); accepted {
		t.Fatal("old account rejection was accepted")
	}
}

func TestPassiveQuotaRecoveryPreservesNewerRejections(t *testing.T) {
	for _, test := range []struct {
		name          string
		rejectedModel string
		rejectedAt    int64
		sampleAt      int64
		latest        string
		wantRecovered bool
	}{
		{name: "later account rejection", rejectedAt: 2500, sampleAt: 2000},
		{name: "later model rejection", rejectedModel: "a", rejectedAt: 2500, sampleAt: 2000},
		{name: "same millisecond rejection", rejectedAt: 2000, sampleAt: 2000},
		{name: "newer recovery observation", rejectedAt: 2500, sampleAt: 3000, wantRecovered: true},
		{name: "later credits do not redate recovery", rejectedAt: 2500, sampleAt: 2000, latest: "credits"},
		{name: "other window does not redate recovery", rejectedAt: 2500, sampleAt: 2000, latest: "other window"},
		{name: "metadata does not redate recovery", rejectedAt: 2500, sampleAt: 2000, latest: "metadata"},
		{name: "same recovered value freshly observed", rejectedAt: 2500, sampleAt: 2000, latest: "same window", wantRecovered: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, _, registry, _, row := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			manager.now = func() time.Time { return time.UnixMilli(4000) }
			newFlushableCredentialObservation(t, manager, row.ID, models.CredentialObservationFresh,
				`{"quota_windows":[{"id":"primary","source_id":"codex","scope":"account","unit":"percent","state":"available","used":80,"window_seconds":18000},{"id":"secondary","source_id":"codex","scope":"account","unit":"percent","state":"available","used":80,"window_seconds":604800}]}`)
			ref, _ := registry.CredentialRef(row.ID)
			until := time.UnixMilli(60000)
			for _, model := range []string{"", "a"} {
				registry.SetQuotaCooldown(ref, model, until, time.UnixMilli(1500))
			}
			period := int64(18000)
			recovery := PassiveQuotaSample{ObservedAtMS: test.sampleAt, Windows: []providerobservation.QuotaWindow{{
				ID: "primary", SourceID: "codex", WindowSeconds: &period, Unit: "percent", State: "available",
				Used: floatPointer(20), Utilization: floatPointer(.2),
			}}}
			record := func() {
				if test.latest == "" {
					manager.RecordPassiveQuotaObservation(row.ID, ref.IdentityGeneration, recovery.ObservedAtMS, recovery.Windows)
					return
				}
				latest := PassiveQuotaSample{ObservedAtMS: 3000}
				switch test.latest {
				case "credits":
					latest.Credits = &providerobservation.CreditSummary{Balance: "100"}
				case "other window":
					otherPeriod := int64(604800)
					latest.Windows = []providerobservation.QuotaWindow{{ID: "secondary", SourceID: "codex", WindowSeconds: &otherPeriod, Unit: "percent", State: "available", Used: floatPointer(80), Utilization: floatPointer(.8)}}
				case "metadata":
					latest.Windows = []providerobservation.QuotaWindow{{ID: "primary", SourceID: "codex", WindowSeconds: &period, State: "available"}}
				case "same window":
					latest.Windows = recovery.Windows
				}
				manager.RecordPassiveQuotaPair(row.ID, ref.IdentityGeneration, recovery, latest)
			}
			reject := func() {
				// 相同截止时间的更新拒绝也必须阻止旧观测清理冷却。
				accepted, changed := registry.SetQuotaCooldown(ref, test.rejectedModel, until, time.UnixMilli(test.rejectedAt))
				if !accepted || changed {
					t.Fatalf("same-deadline rejection = (%t, %t)", accepted, changed)
				}
			}
			if test.sampleAt > test.rejectedAt {
				reject()
				record()
			} else {
				record()
				reject()
			}
			if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
				t.Fatal(err)
			}
			view := registry.Snapshot()[0]
			if view.CooldownUntil.IsZero() != test.wantRecovered || view.ModelCooldowns["a"].IsZero() != test.wantRecovered {
				t.Fatalf("account/model cleared = %t/%t, want %t", view.CooldownUntil.IsZero(), view.ModelCooldowns["a"].IsZero(), test.wantRecovered)
			}
		})
	}
}

func TestPassiveQuotaRecoveryComparesPersistedAndFinalSamples(t *testing.T) {
	for _, test := range []struct {
		name          string
		precedingUsed float64
		finalUsed     float64
		wantRecovered bool
	}{
		{name: "accumulated decrease", precedingUsed: 77, finalUsed: 74, wantRecovered: true},
		{name: "intermediate recovery consumed", precedingUsed: 20, finalUsed: 79, wantRecovered: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			storedAt, period := int64(1000), int64(18000)
			window := func(used float64) []providerobservation.QuotaWindow {
				utilization := used / 100
				return []providerobservation.QuotaWindow{{ID: "primary", SourceID: "codex", WindowSeconds: &period, Unit: "percent", State: "available", Used: &used, Utilization: &utilization}}
			}
			merged, observedAt, err := mergePassiveQuotaSamples(
				[]byte(`{"quota_windows":[{"id":"primary","source_id":"codex","scope":"account","unit":"percent","state":"available","used":80,"window_seconds":18000}]}`),
				&storedAt, PassiveQuotaObservation{
					Preceding:    &PassiveQuotaSample{ObservedAtMS: 2000, Windows: window(test.precedingUsed)},
					ObservedAtMS: 3000, Windows: window(test.finalUsed),
				})
			if err != nil {
				t.Fatal(err)
			}
			if !merged.Matched || !merged.Changed || observedAt != 3000 || len(merged.Windows) != 1 ||
				merged.Windows[0].Used == nil || *merged.Windows[0].Used != test.finalUsed {
				t.Fatalf("merged quota = %#v, observed at %d", merged, observedAt)
			}
			if merged.Recovered != test.wantRecovered {
				t.Fatalf("recovered = %t, want %t for 80 -> %g -> %g", merged.Recovered, test.wantRecovered, test.precedingUsed, test.finalUsed)
			}
		})
	}
}
