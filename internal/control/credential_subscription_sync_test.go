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
	for _, flushBeforeSave := range []bool{true, false} {
		t.Run(fmt.Sprintf("passive_flushed_before_save=%t", flushBeforeSave), func(t *testing.T) {
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
				SnapshotJSON: models.JSON(`{"plan_summary":{"name":"Plus"},"quota_windows":[{"id":"primary","source_id":"codex","label":"5h","scope":"account","unit":"percent","used":20,"utilization":0.2,"state":"available","window_seconds":18000}]}`),
			}).Error; err != nil {
				t.Fatal(err)
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
				_, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("active refresh did not start")
			}
			manager := subscription.NewCredentialManager(fixture.db, fixture.encryption, fixture.registry, fixture.mutations, fixture.service.subscriptions)
			ref, ok := fixture.registry.CredentialRef(credentialID)
			if !ok {
				t.Fatal("missing runtime credential")
			}
			used, ratio, period, passiveAt := 95.0, 0.95, int64(18000), base+1000
			manager.RecordPassiveQuotaObservation(credentialID, ref.IdentityGeneration, passiveAt, []providerobservation.QuotaWindow{
				{ID: "primary", SourceID: "codex", Used: &used, Utilization: &ratio, WindowSeconds: &period, State: "available"},
			}, &providerobservation.CreditSummary{Balance: "5", ObservedAtMS: &passiveAt})
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
			if len(snapshot.QuotaWindows) != 1 || snapshot.QuotaWindows[0].Used == nil || *snapshot.QuotaWindows[0].Used != 95 ||
				snapshot.Credits == nil || snapshot.Credits.Balance != "5" || snapshot.Credits.ObservedAtMS == nil || *snapshot.Credits.ObservedAtMS != passiveAt ||
				result.ObservedAtMS == nil || *result.ObservedAtMS != passiveAt {
				t.Fatalf("newer passive values were lost: %s", observationJSON(t, result))
			}
			encoded := observationJSON(t, snapshot)
			if !strings.Contains(encoded, `"subscription":{"state":"confirmed"`) || snapshot.ResetCreditsAvailable == nil || *snapshot.ResetCreditsAvailable != 2 {
				t.Fatalf("fresh subscription or reset credits were lost: %s", encoded)
			}
		})
	}
}
