package subscriptionruntime

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"gpt-load/internal/antigravity"
	"gpt-load/internal/channel/modules"
)

func TestAntigravityDriverProducesProviderNeutralCredential(t *testing.T) {
	driver := newAntigravityDriver()
	credential, err := driver.Parse([]byte(`{
		"type":"antigravity",
		"access_token":"access-secret",
		"refresh_token":"refresh-secret",
		"account_id":"google-account-one",
		"email":"owner@example.com",
		"project_id":"project-one",
		"expired":"2030-01-01T00:00:00Z"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if credential.Identity() != "google-account-one" || credential.Account().Email != "owner@example.com" ||
		!credential.Account().ExpiresAtKnown {
		t.Fatalf("credential metadata = %q %#v", credential.Identity(), credential.Account())
	}
	if got := credential.SecretValues(); !reflect.DeepEqual(got, []string{
		"access-secret", "refresh-secret", "google-account-one", "owner@example.com", "project-one",
	}) {
		t.Fatalf("SecretValues() = %q", got)
	}
}

func TestAntigravityDriverDeclaresCallbackAndImporter(t *testing.T) {
	driver := newAntigravityDriver()
	callback, local := driver.LocalCallback()
	if !local || callback.RedirectURI != "http://localhost:51121/oauth-callback" {
		t.Fatalf("callback = %#v/%t", callback, local)
	}
	if _, ok := any(driver).(CredentialFileImporter); !ok {
		t.Fatal("Antigravity driver does not implement CredentialFileImporter")
	}
}

func TestAntigravityDriverClassifiesRefreshFailures(t *testing.T) {
	driver := newAntigravityDriver()
	if got := driver.ClassifyRefreshFailure(antigravity.ErrCredentialIdentityChanged); got != RefreshFailureIdentityChanged {
		t.Fatalf("identity changed classification = %v", got)
	}
	if got := driver.ClassifyRefreshFailure(&antigravity.TokenEndpointError{StatusCode: 400, Code: "invalid_grant"}); got != RefreshFailureReauthorizationRequired {
		t.Fatalf("invalid_grant classification = %v", got)
	}
	if got := driver.ClassifyRefreshFailure(errors.New("network unavailable")); got != RefreshFailureOutcomeUnknown {
		t.Fatalf("network classification = %v", got)
	}
}

func TestAntigravityImplementationsExposeCompleteReadOnlyCapabilities(t *testing.T) {
	implementations := AntigravityImplementations()
	if len(implementations.Drivers) != 1 || implementations.Drivers[0].ID() != modules.AntigravitySubscriptionDriver ||
		len(implementations.ModelDiscoveries) != 1 ||
		implementations.ModelDiscoveries[0].ID() != modules.AntigravityModelDiscovery ||
		len(implementations.QuotaObservations) != 1 ||
		implementations.QuotaObservations[0].ID() != modules.AntigravityQuotaObservation ||
		len(implementations.ResetCreditActions) != 0 {
		t.Fatalf("implementations = %#v", implementations)
	}
}

func TestNormalizeAntigravityObservationShowsCreditsWithoutInventingQuota(t *testing.T) {
	raw, err := NormalizeAntigravityObservation("owner@example.com", antigravity.AccountObservation{
		PlanID: "google-one-ai",
		GoogleOneAICredits: &antigravity.GoogleOneAICredit{
			Amount: 25000, MinimumAmount: 50,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot quotaSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Plan.Name != "google-one-ai" || snapshot.Account == nil || snapshot.Account.Email != "owner@example.com" ||
		len(snapshot.QuotaWindows) != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	window := snapshot.QuotaWindows[0]
	if window.ID != "google_one_ai" || window.Scope != "credits" || window.Unit != "credits" ||
		window.Remaining == nil || *window.Remaining != 25000 || window.State != "available" ||
		window.Limit != nil || window.Used != nil || window.Utilization != nil || window.ResetAtMS != nil {
		t.Fatalf("credit window = %#v", window)
	}

	empty, err := NormalizeAntigravityObservation("owner@example.com", antigravity.AccountObservation{PlanID: "free-tier"})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(empty, &snapshot); err != nil || len(snapshot.QuotaWindows) != 0 {
		t.Fatalf("missing credit snapshot = %#v, %v", snapshot, err)
	}
}

func TestAntigravityObservationCompletenessProtectsLastKnownGoodSections(t *testing.T) {
	credit := &antigravity.GoogleOneAICredit{Amount: 100, MinimumAmount: 10}
	tests := []struct {
		name        string
		observation antigravity.AccountObservation
		wantAccount bool
		wantQuota   bool
		wantPartial bool
		wantError   bool
	}{
		{
			name: "complete", observation: antigravity.AccountObservation{
				PlanID: "google-one-ai", GoogleOneAICredits: credit,
			}, wantAccount: true, wantQuota: true,
		},
		{
			name: "plan only is partial", observation: antigravity.AccountObservation{PlanID: "free-tier"},
			wantAccount: true, wantPartial: true,
		},
		{
			name: "credits only is partial", observation: antigravity.AccountObservation{GoogleOneAICredits: credit},
			wantQuota: true, wantPartial: true,
		},
		{name: "empty is payload failure", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			account, quota, partial, err := antigravityObservationCompleteness(test.observation)
			if (err != nil) != test.wantError || account != test.wantAccount || quota != test.wantQuota || partial != test.wantPartial {
				t.Fatalf("completeness = %t/%t/%t, %v", account, quota, partial, err)
			}
			if test.wantError && !errors.Is(err, ErrObservationPayloadInvalid) {
				t.Fatalf("error = %v, want ErrObservationPayloadInvalid", err)
			}
		})
	}
}
