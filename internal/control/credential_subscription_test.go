package control

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gpt-load/internal/channel"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestSubscriptionRefreshPreservesLastConfirmationWithoutBlockingQuota(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.Date(2026, time.October, 30, 0, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	for _, tc := range []struct {
		subscription, state string
		end                 int64
		hasEnd              bool
	}{
		{`{"state":"confirmed","period_end_ms":1800000000000,"will_renew":false,"observed_at_ms":1000}`, "confirmed", 1800000000000, true},
		{`{"state":"error"}`, "error", 1800000000000, true},
		{`{"state":"confirmed","period_end_ms":1802500000000,"will_renew":true,"observed_at_ms":2000}`, "confirmed", 1802500000000, true},
		{`{"state":"unavailable"}`, "unavailable", 0, false},
	} {
		t.Run(tc.state+fmt.Sprint(tc.end), func(t *testing.T) {
			now = now.Add(time.Minute)
			fixture.service.observeSubscriptionAccount = func(context.Context, channel.ID, subscriptionruntime.Credential, subscriptionruntime.Target) (subscriptionruntime.Observation, error) {
				return subscriptionruntime.Observation{QuotaObserved: true, Payload: []byte(`{"plan_summary":{"name":"Plus"},"quota_windows":[{"id":"primary","label":"5h","scope":"account","unit":"percent","used":25,"state":"available"}],"subscription":` + tc.subscription + `}`)}, nil
			}
			result, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
			if err != nil {
				t.Fatal(err)
			}
			// Read it back through the public query, not just the refresh return value.
			result, err = fixture.service.GetCredentialObservation(t.Context(), groupID, credentialID)
			if err != nil || result.State != "fresh" || result.Snapshot == nil {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			sub := result.Snapshot.Subscription
			if sub == nil || sub.State != tc.state || (sub.PeriodEndMS != nil) != tc.hasEnd {
				t.Fatalf("subscription=%#v", sub)
			}
			if tc.hasEnd && *sub.PeriodEndMS != tc.end {
				t.Fatalf("period=%d", *sub.PeriodEndMS)
			}
			if tc.state == "error" && (sub.ObservedAtMS == nil || *sub.ObservedAtMS != 1000 || sub.WillRenew == nil || *sub.WillRenew) {
				t.Fatalf("last confirmation was lost: %#v", sub)
			}
			if len(result.Snapshot.QuotaWindows) != 1 || *result.Snapshot.QuotaWindows[0].Used != 25 {
				t.Fatal("quota lost")
			}
		})
	}
}
