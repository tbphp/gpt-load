package claude

import (
	"encoding/json"
	"reflect"
	"testing"
)

func floatPointer(value float64) *float64 { return &value }
func stringPointer(value string) *string  { return &value }
func boolPointer(value bool) *bool        { return &value }

func TestNormalizeObservationIncludesAccountAndQuotaWindows(t *testing.T) {
	raw, err := NormalizeObservation(AccountObservation{
		Profile: AccountProfile{
			DisplayName: "Owner", Email: "owner@example.com", OrganizationName: "Example Org",
			OrganizationType: "claude_team", OrganizationRole: "admin", WorkspaceRole: "member",
			OrganizationRateLimitTier: "org-tier", UserRateLimitTier: "user-tier",
			SeatTier: "team_standard", BillingType: "stripe_subscription",
			AccountCreatedAt: "2025-01-02T03:04:05Z", SubscriptionCreatedAt: "2025-02-03T04:05:06Z",
			ExtraUsageEnabled: boolPointer(true),
		},
		Usage: Usage{
			FiveHour: &UsageWindow{
				Utilization: floatPointer(25), ResetsAt: stringPointer("2026-08-16T10:00:00Z"),
			},
			SevenDay: &UsageWindow{
				Utilization: floatPointer(40), ResetsAt: stringPointer("2026-08-23T08:00:00Z"),
			},
			ExtraUsage: &ExtraUsage{
				Enabled: boolPointer(true), MonthlyLimit: floatPointer(100), UsedCredits: floatPointer(12.5),
				Utilization: floatPointer(12.5), Currency: stringPointer("USD"),
			},
			Limits: []ScopedLimit{{
				Kind: "weekly", Group: "opus", Percent: 80,
				ResetsAt: stringPointer("2026-08-23T08:00:00Z"), ModelDisplayName: "Claude Opus",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Account struct {
			DisplayName      string `json:"display_name"`
			OrganizationName string `json:"organization_name"`
			SeatTier         string `json:"seat_tier"`
		} `json:"account_summary"`
		Plan struct {
			Name string `json:"name"`
		} `json:"plan_summary"`
		Windows []quotaWindow `json:"quota_windows"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Account.DisplayName != "Owner" || snapshot.Account.OrganizationName != "Example Org" ||
		snapshot.Account.SeatTier != "team_standard" || snapshot.Plan.Name != "Team" ||
		len(snapshot.Windows) != 4 {
		t.Fatalf("snapshot = %s", raw)
	}
	byID := make(map[string]quotaWindow, len(snapshot.Windows))
	for _, window := range snapshot.Windows {
		byID[window.ID] = window
	}
	if byID["five_hour"].Utilization == nil || *byID["five_hour"].Utilization != 0.25 ||
		byID["five_hour"].Label != "Session · 5h" ||
		byID["five_hour"].ResetAtMS == nil || !byID["five_hour"].IsPrimary ||
		byID["seven_day"].Label != "Weekly · 7d" ||
		byID["extra_usage"].Remaining == nil || *byID["extra_usage"].Remaining != 87.5 ||
		byID["weekly_opus"].Label != "Opus · 7d" ||
		byID["weekly_opus"].Utilization == nil || *byID["weekly_opus"].Utilization != 0.8 {
		t.Fatalf("quota windows = %#v", byID)
	}
}

func TestNormalizeObservationRejectsMalformedResetTime(t *testing.T) {
	_, err := NormalizeObservation(AccountObservation{
		Usage: Usage{FiveHour: &UsageWindow{
			Utilization: floatPointer(25), ResetsAt: stringPointer("not-a-time"),
		}},
	})
	if err == nil {
		t.Fatal("NormalizeObservation() accepted malformed reset time")
	}
}

func TestNormalizeObservationUsesUsageExtraStatus(t *testing.T) {
	enabled := true
	reason := "org_level_disabled"
	raw, err := NormalizeObservation(AccountObservation{
		Profile: AccountProfile{ExtraUsageEnabled: &enabled},
		Usage: Usage{ExtraUsage: &ExtraUsage{
			Enabled: boolPointer(false), DisabledReason: &reason,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Account struct {
			ExtraUsageEnabled        *bool  `json:"extra_usage_enabled"`
			ExtraUsageDisabledReason string `json:"extra_usage_disabled_reason"`
		} `json:"account_summary"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Account.ExtraUsageEnabled == nil || *snapshot.Account.ExtraUsageEnabled ||
		snapshot.Account.ExtraUsageDisabledReason != reason {
		t.Fatalf("account summary = %s", raw)
	}
}

func TestNormalizeObservationKeepsProfileExtraStatusWhenUsageOmitsEnabled(t *testing.T) {
	enabled := true
	raw, err := NormalizeObservation(AccountObservation{
		Profile: AccountProfile{ExtraUsageEnabled: &enabled},
		Usage: Usage{ExtraUsage: &ExtraUsage{
			MonthlyLimit: floatPointer(100),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Account struct {
			ExtraUsageEnabled *bool `json:"extra_usage_enabled"`
		} `json:"account_summary"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Account.ExtraUsageEnabled == nil || !*snapshot.Account.ExtraUsageEnabled {
		t.Fatalf("account summary = %s", raw)
	}
}

func TestNormalizeObservationKeepsQuotaWindowIDsUnique(t *testing.T) {
	raw, err := NormalizeObservation(AccountObservation{Usage: Usage{
		FiveHour: &UsageWindow{Utilization: floatPointer(10)},
		Limits: []ScopedLimit{
			{Kind: "five", Group: "hour", Percent: 20},
			{Kind: "weekly", Group: "opus", Percent: 30, ModelDisplayName: "Opus A"},
			{Kind: "weekly", Group: "opus", Percent: 40, ModelDisplayName: "Opus B"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Windows []quotaWindow `json:"quota_windows"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]struct{}, len(snapshot.Windows))
	for _, window := range snapshot.Windows {
		if _, duplicate := seen[window.ID]; duplicate {
			t.Fatalf("duplicate quota window ID %q: %s", window.ID, raw)
		}
		seen[window.ID] = struct{}{}
	}
	if len(seen) != 4 {
		t.Fatalf("quota windows = %s", raw)
	}
}

func TestNormalizeObservationSuppressesDuplicateSessionAndWeeklyLimits(t *testing.T) {
	reset := "2026-08-18T10:00:00Z"
	raw, err := NormalizeObservation(AccountObservation{Usage: Usage{
		FiveHour: &UsageWindow{Utilization: floatPointer(0), ResetsAt: &reset},
		SevenDay: &UsageWindow{Utilization: floatPointer(85), ResetsAt: &reset},
		Limits: []ScopedLimit{
			{Kind: "rolling", Group: "session", Percent: 0, ResetsAt: &reset},
			{Kind: "rolling", Group: "weekly", Percent: 85, ResetsAt: &reset},
			{Kind: "model", Group: "opus", Percent: 40, ModelDisplayName: "Claude Opus"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Windows []quotaWindow `json:"quota_windows"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(snapshot.Windows))
	for _, window := range snapshot.Windows {
		ids = append(ids, window.ID)
	}
	if !reflect.DeepEqual(ids, []string{"five_hour", "seven_day", "model_opus"}) {
		t.Fatalf("quota window IDs = %v", ids)
	}
}

func TestNormalizeObservationInfersPeriodsForScopedSessionAndWeeklyWindows(t *testing.T) {
	raw, err := NormalizeObservation(AccountObservation{Usage: Usage{Limits: []ScopedLimit{
		{Kind: "rolling", Group: "session", Percent: 20},
		{Kind: "rolling", Group: "weekly", Percent: 30},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Windows []quotaWindow `json:"quota_windows"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	labels := make([]string, 0, len(snapshot.Windows))
	for _, window := range snapshot.Windows {
		labels = append(labels, window.Label)
	}
	if !reflect.DeepEqual(labels, []string{"Session · 5h", "Weekly · 7d"}) {
		t.Fatalf("quota window labels = %q", labels)
	}
}

func TestNormalizeObservationPreservesScopedLimitKinds(t *testing.T) {
	raw, err := NormalizeObservation(AccountObservation{Usage: Usage{Limits: []ScopedLimit{
		{Kind: "weekly", Group: "opus", Percent: 20, ModelDisplayName: "Claude Opus"},
		{Kind: "weekly", Group: "oauth_apps", Percent: 30, SurfaceDisplayName: "OAuth apps"},
		{Kind: "weekly", Group: "special", Percent: 40, ModelDisplayName: "Claude Special"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Windows []quotaWindow `json:"quota_windows"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]quotaWindow, len(snapshot.Windows))
	for _, window := range snapshot.Windows {
		byID[window.ID] = window
	}
	if model := byID["weekly_opus"]; model.Scope != "model" || model.Label != "Opus · 7d" {
		t.Fatalf("model window = %#v", model)
	}
	if surface := byID["weekly_oauth-apps"]; surface.Scope != "surface" || surface.Label != "OAuth apps · 7d" {
		t.Fatalf("surface window = %#v", surface)
	}
	if model := byID["weekly_special"]; model.Scope != "model" || model.Label != "Claude Special · 7d" {
		t.Fatalf("custom model window = %#v", model)
	}
}

func TestNormalizeObservationUsesRateLimitTierAsPlanFallback(t *testing.T) {
	tests := []struct {
		name    string
		profile AccountProfile
		want    string
	}{
		{name: "organization max", profile: AccountProfile{OrganizationRateLimitTier: "default_claude_max_20x"}, want: "Max"},
		{name: "user pro", profile: AccountProfile{UserRateLimitTier: "default_claude_pro"}, want: "Pro"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, err := NormalizeObservation(AccountObservation{Profile: test.profile})
			if err != nil {
				t.Fatal(err)
			}
			var snapshot quotaSnapshot
			if err := json.Unmarshal(raw, &snapshot); err != nil {
				t.Fatal(err)
			}
			if snapshot.Plan.Name != test.want {
				t.Fatalf("plan name = %q, want %q", snapshot.Plan.Name, test.want)
			}
		})
	}
}
