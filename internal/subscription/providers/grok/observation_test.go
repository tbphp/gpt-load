package grok

import (
	"encoding/json"
	"testing"

	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestNormalizeObservationBuildsPlanAndDisplayReadyWindows(t *testing.T) {
	usagePercent := 25.0
	monthlyLimit := 15000.0
	used := 3000.0
	onDemandCap := 5000.0
	onDemandUsed := 1000.0
	raw, err := NormalizeObservation("owner@example.com", AccountObservation{
		Billing: BillingObservation{
			PeriodType: "weekly", PeriodStart: "2026-08-12T00:00:00Z", PeriodEnd: "2026-08-19T00:00:00Z",
			UsagePercent:      &usagePercent,
			ProductUsage:      []ProductUsage{{Product: "GrokBuild", UsagePercent: float64Pointer(10)}},
			MonthlyLimitCents: &monthlyLimit, UsedCents: &used,
			OnDemandCapCents: &onDemandCap, OnDemandUsedCents: &onDemandUsed,
			BillingPeriodStart: "2026-08-01T00:00:00Z", BillingPeriodEnd: "2026-09-01T00:00:00Z",
		},
		AccountQuotaObserved: true, SurfaceQuotaObserved: true, CreditQuotaObserved: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot providerobservation.Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Plan.Name != "SuperGrok" || snapshot.Plan.Level != providerobservation.PlanLevelPremium ||
		snapshot.Account == nil || snapshot.Account.Email != "owner@example.com" {
		t.Fatalf("snapshot header = %#v", snapshot)
	}
	windows := make(map[string]providerobservation.QuotaWindow, len(snapshot.QuotaWindows))
	for _, window := range snapshot.QuotaWindows {
		windows[window.ID] = window
	}
	if windows["weekly"].Label != "Weekly · 7d" || windows["weekly"].Scope != quotaScopeAccount ||
		windows["grokbuild"].Label != "Grok Build · 7d" || windows["grokbuild"].Scope != quotaScopeSurface ||
		windows["included_usage"].Scope != quotaScopeCredits || windows["pay_as_you_go"].Scope != quotaScopeCredits {
		t.Fatalf("quota windows = %#v", snapshot.QuotaWindows)
	}
}

func TestNormalizeObservationMapsHeavyAndUnknownPlansSafely(t *testing.T) {
	for _, test := range []struct {
		limit float64
		name  string
		level providerobservation.PlanLevel
	}{
		{limit: 0, name: "Free", level: providerobservation.PlanLevelFree},
		{limit: 150000, name: "SuperGrok Heavy", level: providerobservation.PlanLevelElite},
		{limit: 10000, name: "Grok Paid", level: providerobservation.PlanLevelStandard},
	} {
		raw, err := NormalizeObservation("owner@example.com", AccountObservation{
			Billing: BillingObservation{MonthlyLimitCents: &test.limit}, CreditQuotaObserved: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		var snapshot providerobservation.Snapshot
		if json.Unmarshal(raw, &snapshot) != nil || snapshot.Plan.Name != test.name || snapshot.Plan.Level != test.level {
			t.Fatalf("plan = %#v", snapshot.Plan)
		}
	}
}

func float64Pointer(value float64) *float64 { return &value }
