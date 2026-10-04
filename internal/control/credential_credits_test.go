package control

import (
	"context"
	"testing"
	"time"

	"gpt-load/internal/subscription/providers/codex"
)

func TestRefreshCredentialCreditsStoresTimeZeroAndMissing(t *testing.T) {
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	now := time.UnixMilli(1_800_000_000_000)
	fixture.service.now = func() time.Time { return now }
	for _, test := range []struct {
		payload string
		balance string
	}{
		{`{"credits":{"balance":"62500","has_credits":true,"unlimited":false}}`, "62500"},
		{`{"credits":{"balance":"0","has_credits":false,"unlimited":false}}`, "0"},
		{`{"plan_type":"pro"}`, ""},
	} {
		setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
			return codex.AccountObservation{Payload: []byte(test.payload)}, nil
		})
		result, err := fixture.service.RefreshCredentialObservation(t.Context(), groupID, credentialID)
		if err != nil || result.Snapshot == nil {
			t.Fatalf("refresh failed: %#v, %v", result, err)
		}
		if test.balance == "" {
			if result.Snapshot.Credits != nil {
				t.Fatal("successful refresh without credit data retained the old balance")
			}
		} else if credits := result.Snapshot.Credits; credits == nil || credits.Balance != test.balance || credits.ObservedAtMS == nil || *credits.ObservedAtMS != now.UnixMilli() {
			t.Fatalf("credit observation time was not persisted: %#v", result.Snapshot.Credits)
		}
		now = now.Add(time.Second)
	}
}
