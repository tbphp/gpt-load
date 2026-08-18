package antigravity

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"gpt-load/internal/channel/modules"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
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
	if _, ok := any(driver).(subscriptionruntime.CredentialFileImporter); !ok {
		t.Fatal("Antigravity driver does not implement CredentialFileImporter")
	}
}

func TestAntigravityDriverClassifiesRefreshFailures(t *testing.T) {
	driver := newAntigravityDriver()
	if got := driver.ClassifyRefreshFailure(ErrCredentialIdentityChanged); got != subscriptionruntime.RefreshFailureIdentityChanged {
		t.Fatalf("identity changed classification = %v", got)
	}
	if got := driver.ClassifyRefreshFailure(&TokenEndpointError{StatusCode: 400, Code: "invalid_grant"}); got != subscriptionruntime.RefreshFailureReauthorizationRequired {
		t.Fatalf("invalid_grant classification = %v", got)
	}
	if got := driver.ClassifyRefreshFailure(errors.New("network unavailable")); got != subscriptionruntime.RefreshFailureOutcomeUnknown {
		t.Fatalf("network classification = %v", got)
	}
}

func TestAntigravityImplementationsExposeCompleteReadOnlyCapabilities(t *testing.T) {
	implementations := Implementations()
	if len(implementations.Drivers) != 1 || implementations.Drivers[0].ID() != modules.AntigravitySubscriptionDriver ||
		len(implementations.ModelDiscoveries) != 1 ||
		implementations.ModelDiscoveries[0].ID() != modules.AntigravityModelDiscovery ||
		len(implementations.QuotaObservations) != 1 ||
		implementations.QuotaObservations[0].ID() != modules.AntigravityQuotaObservation ||
		len(implementations.ResetCreditActions) != 0 {
		t.Fatalf("implementations = %#v", implementations)
	}
}

func TestNormalizeObservationShowsCreditsWithoutInventingQuota(t *testing.T) {
	raw, err := NormalizeObservation("owner@example.com", AccountObservation{
		PlanID: "google-one-ai",
		GoogleOneAICredits: &GoogleOneAICredit{
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
	if snapshot.Plan.Name != "Google One AI" || snapshot.Account == nil || snapshot.Account.Email != "owner@example.com" ||
		len(snapshot.QuotaWindows) != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	window := snapshot.QuotaWindows[0]
	if window.ID != "google_one_ai" || window.Scope != "credits" || window.Unit != "credits" ||
		window.Remaining == nil || *window.Remaining != 25000 || window.State != "available" ||
		window.Limit != nil || window.Used != nil || window.Utilization != nil || window.ResetAtMS != nil {
		t.Fatalf("credit window = %#v", window)
	}

	empty, err := NormalizeObservation("owner@example.com", AccountObservation{PlanID: "free-tier"})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(empty, &snapshot); err != nil || snapshot.Plan.Name != "Free" || len(snapshot.QuotaWindows) != 0 {
		t.Fatalf("missing credit snapshot = %#v, %v", snapshot, err)
	}
}

func TestNormalizeObservationIncludesUpstreamQuotaBuckets(t *testing.T) {
	remaining := 0.75
	raw, err := NormalizeObservation("owner@example.com", AccountObservation{
		PlanID: "g1-pro-tier",
		QuotaGroups: []QuotaGroup{{
			DisplayName: "Gemini Models",
			Buckets: []QuotaBucket{{
				ID: "gemini-5h", DisplayName: "Five Hour Limit Remaining", Window: "5h",
				ResetTime: "2030-01-01T00:00:00Z", RemainingFraction: &remaining,
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot quotaSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.QuotaWindows) != 1 {
		t.Fatalf("quota windows = %#v", snapshot.QuotaWindows)
	}
	window := snapshot.QuotaWindows[0]
	if snapshot.Plan.Name != "Pro" || window.ID != "gemini-5h" || window.Label != "Gemini · 5h" ||
		window.Scope != "model" || window.Unit != "percent" || window.Remaining == nil || *window.Remaining != 75 ||
		window.Utilization == nil || *window.Utilization != 0.25 || window.WindowSeconds == nil || *window.WindowSeconds != 5*60*60 ||
		window.ResetAtMS == nil || *window.ResetAtMS != 1893456000000 {
		t.Fatalf("quota window = %#v", window)
	}
}

func TestNormalizeObservationNormalizesClaudeGPTWeeklyWindow(t *testing.T) {
	remaining := 1.0
	raw, err := NormalizeObservation("owner@example.com", AccountObservation{
		PlanID: "g1-pro-tier",
		QuotaGroups: []QuotaGroup{{
			DisplayName: "Claude and GPT Models",
			Buckets: []QuotaBucket{{
				ID: "claude-gpt-weekly", DisplayName: "Weekly Limit Remaining", Window: "weekly",
				RemainingFraction: &remaining,
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot quotaSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.QuotaWindows) != 1 || snapshot.QuotaWindows[0].Label != "Claude/GPT · 7d" {
		t.Fatalf("quota windows = %#v", snapshot.QuotaWindows)
	}
}

func TestNormalizeObservationKeepsUnknownWindowLabelsDistinct(t *testing.T) {
	remaining := 1.0
	raw, err := NormalizeObservation("owner@example.com", AccountObservation{
		PlanID: "free-tier",
		QuotaGroups: []QuotaGroup{{
			DisplayName: "Gemini Models",
			Buckets: []QuotaBucket{
				{ID: "weekly", DisplayName: "Weekly Limit Remaining", RemainingFraction: &remaining},
				{ID: "burst", DisplayName: "Experimental Burst", RemainingFraction: &remaining},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot quotaSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]string, len(snapshot.QuotaWindows))
	for _, window := range snapshot.QuotaWindows {
		byID[window.ID] = window.Label
	}
	if byID["weekly"] != "Gemini · Weekly" || byID["burst"] != "Gemini · Experimental Burst" {
		t.Fatalf("quota labels = %#v", byID)
	}
}

func TestAntigravityObservationCompletenessProtectsLastKnownGoodSections(t *testing.T) {
	credit := &GoogleOneAICredit{Amount: 100, MinimumAmount: 10}
	tests := []struct {
		name        string
		observation AccountObservation
		wantAccount bool
		wantQuota   bool
		wantPartial bool
		wantError   bool
	}{
		{
			name: "complete", observation: AccountObservation{
				PlanID: "google-one-ai", GoogleOneAICredits: credit,
				QuotaGroups: []QuotaGroup{{DisplayName: "Gemini Models", Buckets: []QuotaBucket{{ID: "weekly", RemainingFraction: float64Ptr(1)}}}},
			}, wantAccount: true, wantQuota: true,
		},
		{
			name: "plan without quota is partial", observation: AccountObservation{PlanID: "free-tier"},
			wantAccount: true, wantPartial: true,
		},
		{
			name: "plan with credit balance but no quota is partial", observation: AccountObservation{
				PlanID: "g1-pro-tier", GoogleOneAICredits: credit,
			},
			wantAccount: true,
			wantPartial: true,
		},
		{name: "empty is payload failure", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			account, quota, partial, err := antigravityObservationCompleteness(test.observation)
			if (err != nil) != test.wantError || account != test.wantAccount || quota != test.wantQuota || partial != test.wantPartial {
				t.Fatalf("completeness = %t/%t/%t, %v", account, quota, partial, err)
			}
			if test.wantError && !errors.Is(err, subscriptionruntime.ErrObservationPayloadInvalid) {
				t.Fatalf("error = %v, want ErrObservationPayloadInvalid", err)
			}
		})
	}
}

func float64Ptr(value float64) *float64 { return &value }
