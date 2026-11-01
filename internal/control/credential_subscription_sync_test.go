package control

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription"
	providerobservation "gpt-load/internal/subscription/providers/observation"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestSubscriptionSyncPreservesOldValuesOnlyOnFailure(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	for _, test := range []struct {
		name, input, want string
	}{
		{"confirmed", `{"state":"confirmed","period_end_ms":1800000000000,"will_renew":false}`, `{"state":"confirmed","period_end_ms":1800000000000,"will_renew":false}`},
		{"failed", `{"state":"error"}`, `{"state":"error","period_end_ms":1800000000000,"will_renew":false}`},
		{"renewed", `{"state":"confirmed","period_end_ms":1802500000000,"will_renew":true}`, `{"state":"confirmed","period_end_ms":1802500000000,"will_renew":true}`},
		{"not provided", `{"state":"unavailable"}`, `{"state":"unavailable"}`},
		{"failed without history", `{"state":"error"}`, `{"state":"error"}`},
		{"unknown renewal", `{"state":"confirmed","period_end_ms":1802500000000}`, `{"state":"confirmed","period_end_ms":1802500000000}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture.service.observeSubscriptionAccount = func(context.Context, channel.ID, subscriptionruntime.Credential, subscriptionruntime.Target) (subscriptionruntime.Observation, error) {
				return subscriptionruntime.Observation{QuotaObserved: true, Payload: []byte(`{"plan_summary":{"name":"Plus"},"quota_windows":[],"subscription":` + test.input + "}")}, nil
			}
			if _, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID); err != nil {
				t.Fatal(err)
			}
			result, err := fixture.service.GetCredentialObservation(t.Context(), groupID, credentialID)
			if err != nil || result.State != "fresh" {
				t.Fatalf("quota state changed: %v", err)
			}
			encoded, err := json.Marshal(result.Snapshot)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if json.Unmarshal(encoded, &fields) != nil || string(fields["subscription"]) != test.want {
				t.Fatalf("subscription = %s, want %s", fields["subscription"], test.want)
			}
			if strings.Contains(string(fields["subscription"]), "observed_at") {
				t.Fatal("subscription has its own persisted refresh time")
			}
		})
	}
}

func TestAccountSyncPreservesNewerPassiveQuotaAndCredits(t *testing.T) {
	for _, test := range []struct {
		name                   string
		creditOnly, afterReset bool
		passiveUsed, wantUsed  float64
	}{
		{name: "quota and credits", passiveUsed: 95, wantUsed: 95},
		{name: "unchanged quota and credits", passiveUsed: 100, wantUsed: 100},
		{name: "credits only", creditOnly: true, wantUsed: 25},
		{name: "credits after reset", creditOnly: true, afterReset: true, wantUsed: 25},
	} {
		for _, flushBeforeSave := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/passive_flushed_before_save=%t", test.name, flushBeforeSave), func(t *testing.T) {
				fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
				base := time.Now().UnixMilli()
				var clock atomic.Int64
				clock.Store(base)
				fixture.service.now = func() time.Time { return time.UnixMilli(clock.Load()) }
				var credential models.Credential
				if err := fixture.db.First(&credential, credentialID).Error; err != nil {
					t.Fatal(err)
				}
				storedAt := base - 1000
				if err := fixture.db.Create(&models.CredentialObservation{
					CredentialID: credentialID, IdentityFingerprint: credential.IdentityFingerprint,
					SchemaVersion: 1, ObservationVersion: 1, State: models.CredentialObservationFresh,
					ObservedAtMS: &storedAt, UpdatedAtMS: storedAt,
					SnapshotJSON: models.JSON(`{"plan_summary":{"name":"Plus"},"quota_windows":[{"id":"primary","source_id":"codex","label":"5h","scope":"account","unit":"percent","used":100,"utilization":1,"state":"exhausted","window_seconds":18000}]}`),
				}).Error; err != nil {
					t.Fatal(err)
				}
				ref, ok := fixture.registry.CredentialRef(credentialID)
				if !ok {
					t.Fatal("missing runtime credential")
				}
				if test.creditOnly && !test.afterReset {
					fixture.registry.SetQuotaCooldown(ref, "", time.UnixMilli(base).Add(time.Hour), time.UnixMilli(base-500))
				}
				started, release := make(chan struct{}), make(chan struct{})
				released := false
				defer func() {
					if !released {
						close(release)
					}
				}()
				fixture.service.observeSubscriptionAccount = func(ctx context.Context, _ channel.ID, _ subscriptionruntime.Credential, _ subscriptionruntime.Target) (subscriptionruntime.Observation, error) {
					close(started)
					select {
					case <-release:
					case <-ctx.Done():
						return subscriptionruntime.Observation{}, ctx.Err()
					}
					return subscriptionruntime.Observation{
						QuotaObserved: true, QuotaObservedAt: time.UnixMilli(base),
						Payload: []byte(`{"plan_summary":{"name":"Plus"},"quota_windows":[{"id":"primary","source_id":"codex","label":"5h","scope":"account","unit":"percent","used":25,"utilization":0.25,"state":"available","window_seconds":18000}],"credits":{"balance":"100"},"reset_credits_available":2,"subscription":{"state":"confirmed","period_end_ms":1800000000000,"will_renew":true}}`),
					}, nil
				}
				done := make(chan error, 1)
				go func() {
					mode := observationRefreshManual
					if test.afterReset {
						mode = observationRefreshAfterReset
					}
					_, err := fixture.service.refreshCredentialObservation(t.Context(), groupID, credentialID, mode)
					done <- err
				}()
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("active refresh did not start")
				}
				manager := subscription.NewCredentialManager(fixture.db, fixture.encryption, fixture.registry, fixture.mutations, fixture.service.subscriptions)
				used, ratio, period, passiveAt := test.passiveUsed, test.passiveUsed/100, int64(18000), base+1000
				windows := []providerobservation.QuotaWindow{
					{ID: "primary", SourceID: "codex", Used: &used, Utilization: &ratio, WindowSeconds: &period, State: "available"},
				}
				if test.creditOnly {
					windows = nil
				} else if used == 100 {
					windows[0].State = "exhausted"
				}
				manager.RecordPassiveQuotaObservation(credentialID, ref.IdentityGeneration, passiveAt, windows,
					&providerobservation.CreditSummary{Balance: "5", ObservedAtMS: &passiveAt})
				flush := func() {
					if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
						t.Fatal(err)
					}
				}
				if flushBeforeSave {
					flush()
				}
				clock.Store(base + 2000)
				close(release)
				released = true
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				if !flushBeforeSave {
					flush()
				}
				result, err := fixture.service.GetCredentialObservation(t.Context(), groupID, credentialID)
				if err != nil || result.Snapshot == nil {
					t.Fatalf("readback failed: %v", err)
				}
				snapshot := result.Snapshot
				if len(snapshot.QuotaWindows) != 1 || snapshot.QuotaWindows[0].Used == nil || *snapshot.QuotaWindows[0].Used != test.wantUsed ||
					snapshot.Credits == nil || snapshot.Credits.Balance != "5" || snapshot.Credits.ObservedAtMS == nil || *snapshot.Credits.ObservedAtMS != passiveAt ||
					result.ObservedAtMS == nil || *result.ObservedAtMS != passiveAt {
					t.Fatalf("newer passive values were lost: %s", observationJSON(t, result))
				}
				if test.creditOnly && !test.afterReset && !fixture.registry.Snapshot()[0].CooldownUntil.IsZero() {
					t.Fatal("credit-only update prevented active quota recovery")
				}
				encoded := observationJSON(t, snapshot)
				if !strings.Contains(encoded, `"subscription":{"state":"confirmed"`) || snapshot.ResetCreditsAvailable == nil || *snapshot.ResetCreditsAvailable != 2 {
					t.Fatalf("fresh subscription or reset credits were lost: %s", encoded)
				}
			})
		}
	}
}

func TestAccountSyncKeepsIndependentQuotaWindows(t *testing.T) {
	for _, test := range []struct {
		name, sourceID string
		period         int64
		passiveUsed    float64
	}{
		{name: "feature", sourceID: "codex_bengalfox", period: 18000, passiveUsed: 90},
		{name: "unchanged feature", sourceID: "codex_bengalfox", period: 18000, passiveUsed: 80},
		{name: "weekly account", sourceID: "codex", period: 604800, passiveUsed: 90},
	} {
		for _, flushBeforeSave := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/passive_flushed_before_save=%t", test.name, flushBeforeSave), func(t *testing.T) {
				fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
				base := time.Now().UnixMilli()
				var clock atomic.Int64
				clock.Store(base)
				fixture.service.now = func() time.Time { return time.UnixMilli(clock.Load()) }
				var credential models.Credential
				if err := fixture.db.First(&credential, credentialID).Error; err != nil {
					t.Fatal(err)
				}
				storedAt := base - 1000
				if err := fixture.db.Create(&models.CredentialObservation{
					CredentialID: credentialID, IdentityFingerprint: credential.IdentityFingerprint,
					SchemaVersion: 1, ObservationVersion: 1, State: models.CredentialObservationFresh,
					ObservedAtMS: &storedAt, UpdatedAtMS: storedAt,
					SnapshotJSON: models.JSON(fmt.Sprintf(`{"plan_summary":{"name":"Plus"},"quota_windows":[{"id":"primary","source_id":"codex","label":"5h","scope":"account","unit":"percent","used":100,"utilization":1,"state":"exhausted","window_seconds":18000},{"id":"secondary","source_id":"%s","label":"5h","scope":"Spark","unit":"percent","used":80,"utilization":0.8,"state":"available","window_seconds":%d}]}`, test.sourceID, test.period)),
				}).Error; err != nil {
					t.Fatal(err)
				}
				ref, ok := fixture.registry.CredentialRef(credentialID)
				if !ok {
					t.Fatal("missing runtime credential")
				}
				fixture.registry.SetQuotaCooldown(ref, "", time.UnixMilli(base).Add(time.Hour), time.UnixMilli(base-500))
				started, release := make(chan struct{}), make(chan struct{})
				released := false
				defer func() {
					if !released {
						close(release)
					}
				}()
				fixture.service.observeSubscriptionAccount = func(ctx context.Context, _ channel.ID, _ subscriptionruntime.Credential, _ subscriptionruntime.Target) (subscriptionruntime.Observation, error) {
					close(started)
					select {
					case <-release:
					case <-ctx.Done():
						return subscriptionruntime.Observation{}, ctx.Err()
					}
					return subscriptionruntime.Observation{
						QuotaObserved: true, QuotaObservedAt: time.UnixMilli(base),
						Payload: []byte(fmt.Sprintf(`{"plan_summary":{"name":"Plus"},"quota_windows":[{"id":"primary","source_id":"codex","label":"5h","scope":"account","unit":"percent","used":25,"utilization":0.25,"state":"available","window_seconds":18000},{"id":"secondary","source_id":"%s","label":"5h","scope":"Spark","unit":"percent","used":85,"utilization":0.85,"state":"available","window_seconds":%d}],"credits":{"balance":"100"},"reset_credits_available":2,"subscription":{"state":"confirmed","period_end_ms":1800000000000,"will_renew":true}}`, test.sourceID, test.period)),
					}, nil
				}
				done := make(chan error, 1)
				go func() {
					_, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
					done <- err
				}()
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("active refresh did not start")
				}
				manager := subscription.NewCredentialManager(fixture.db, fixture.encryption, fixture.registry, fixture.mutations, fixture.service.subscriptions)
				used, ratio, period, passiveAt := test.passiveUsed, test.passiveUsed/100, test.period, base+1000
				windows := []providerobservation.QuotaWindow{
					{ID: "primary", SourceID: test.sourceID, Used: &used, Utilization: &ratio, WindowSeconds: &period, State: "available"},
				}
				manager.RecordPassiveQuotaObservation(credentialID, ref.IdentityGeneration, passiveAt, windows,
					&providerobservation.CreditSummary{Balance: "5", ObservedAtMS: &passiveAt})
				flush := func() {
					if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
						t.Fatal(err)
					}
				}
				if flushBeforeSave {
					flush()
				}
				clock.Store(base + 2000)
				close(release)
				released = true
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				if !flushBeforeSave {
					flush()
				}
				result, err := fixture.service.GetCredentialObservation(t.Context(), groupID, credentialID)
				if err != nil || result.Snapshot == nil {
					t.Fatalf("readback failed: %v", err)
				}
				snapshot := result.Snapshot
				if len(snapshot.QuotaWindows) != 2 {
					t.Fatalf("expected account and feature windows: %s", observationJSON(t, result))
				}
				account, feature := snapshot.QuotaWindows[0], snapshot.QuotaWindows[1]
				t.Logf("account used=%.0f, feature used=%.0f, cooldown_active=%t", *account.Used, *feature.Used, !fixture.registry.Snapshot()[0].CooldownUntil.IsZero())
				if *account.Used != 25 {
					t.Errorf("unrefreshed account window displaced active result: used=%.0f, want 25", *account.Used)
				}
				if *feature.Used != test.passiveUsed {
					t.Errorf("newer second window lost: used=%.0f, want %.0f", *feature.Used, test.passiveUsed)
				}
				if !fixture.registry.Snapshot()[0].CooldownUntil.IsZero() {
					t.Error("account quota recovery did not clear the earlier cooldown")
				}
				encoded := observationJSON(t, snapshot)
				if strings.Contains(encoded, "quota_window_observed_at_ms") {
					t.Fatal("internal window timestamps leaked into the management API")
				}
				if !strings.Contains(encoded, `"subscription":{"state":"confirmed"`) || snapshot.ResetCreditsAvailable == nil || *snapshot.ResetCreditsAvailable != 2 {
					t.Fatalf("fresh subscription or reset credits were lost: %s", encoded)
				}
			})
		}
	}
}
