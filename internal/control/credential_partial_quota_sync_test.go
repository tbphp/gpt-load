package control

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription"
	"gpt-load/internal/subscription/providers/codex"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestAccountSyncPreservesOnlyObservedPassiveFields(t *testing.T) {
	for _, test := range []struct {
		name, used, wantState string
		reset, denied         bool
		wantUsed              float64
	}{
		{name: "reset only", reset: true, wantUsed: 25, wantState: "available"},
		{name: "usage only", used: "98", wantUsed: 98, wantState: "available"},
		{name: "state only", denied: true, wantUsed: 25, wantState: "exhausted"},
		{name: "usage and reset", used: "98", reset: true, wantUsed: 98, wantState: "available"},
	} {
		for _, flushBeforeSave := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/passive_flushed_before_save=%t", test.name, flushBeforeSave), func(t *testing.T) {
				fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
				base := time.Now().Truncate(time.Second).UnixMilli()
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
					SnapshotJSON: models.JSON(fmt.Sprintf(`{"plan_summary":{"name":"Plus"},"quota_windows":[{"id":"primary","source_id":"codex","scope":"account","unit":"percent","used":100,"utilization":1,"state":"exhausted","window_seconds":18000,"reset_at_ms":%d}]}`, base+900000)),
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
				activeResetAt := base + 1800000
				fixture.service.observeSubscriptionAccount = func(ctx context.Context, _ channel.ID, _ subscriptionruntime.Credential, _ subscriptionruntime.Target) (subscriptionruntime.Observation, error) {
					close(started)
					select {
					case <-release:
					case <-ctx.Done():
						return subscriptionruntime.Observation{}, ctx.Err()
					}
					return subscriptionruntime.Observation{
						QuotaObserved: true, QuotaObservedAt: time.UnixMilli(base),
						Payload: []byte(fmt.Sprintf(`{"plan_summary":{"name":"Plus"},"quota_windows":[{"id":"primary","source_id":"codex","scope":"account","unit":"percent","used":25,"utilization":0.25,"state":"available","window_seconds":18000,"reset_at_ms":%d}]}`, activeResetAt)),
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
				passiveAt, passiveResetAt := base+1000, base+3600000
				signals := map[string]string{"X-Codex-Primary-Window-Minutes": "300"}
				if test.reset {
					signals["X-Codex-Primary-Reset-At"] = strconv.FormatInt(passiveResetAt/1000, 10)
				}
				if test.used != "" {
					signals["X-Codex-Primary-Used-Percent"] = test.used
				}
				if test.denied {
					signals["X-Codex-Allowed"] = "false"
				}
				windows := codex.NormalizePassiveQuotaWindows(signals, time.UnixMilli(passiveAt))
				if len(windows) != 1 {
					t.Fatalf("partial headers produced %d windows", len(windows))
				}
				manager.RecordPassiveQuotaObservation(credentialID, ref.IdentityGeneration, passiveAt, windows)
				flush := func() {
					t.Helper()
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
				if err != nil || result.Snapshot == nil || len(result.Snapshot.QuotaWindows) != 1 {
					t.Fatalf("readback failed: %v", err)
				}
				window := result.Snapshot.QuotaWindows[0]
				wantResetAt := activeResetAt
				if test.reset {
					wantResetAt = passiveResetAt
				}
				if window.Used == nil || *window.Used != test.wantUsed || window.State != test.wantState ||
					window.ResetAtMS == nil || *window.ResetAtMS != wantResetAt {
					t.Errorf("partial sample overwrote unobserved fields: %s", observationJSON(t, window))
				}
				wantCooldown := flushBeforeSave && (test.used != "" || test.denied)
				if active := !fixture.registry.Snapshot()[0].CooldownUntil.IsZero(); active != wantCooldown {
					t.Errorf("cooldown active=%t, want %t", active, wantCooldown)
				}
			})
		}
	}
}
