package codex

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestNormalizeQuotaNormalizesPlanAndStandardWindows(t *testing.T) {
	raw, err := NormalizeQuota([]byte(`{
		"plan_type":"pro",
		"rate_limit":{
			"primary_window":{
				"label":"Five Hour Limit Remaining",
				"limit_window_seconds":18000,
				"used_percent":25
			},
			"secondary_window":{
				"label":"Weekly Limit Remaining",
				"limit_window_seconds":604800,
				"used_percent":40
			}
		}
	}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot quotaSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Plan.Name != "Pro 20x" || snapshot.Plan.Level != "elite" {
		t.Fatalf("plan = %#v", snapshot.Plan)
	}
	labels := make([]string, 0, len(snapshot.QuotaWindows))
	labelKeys := make([]string, 0, len(snapshot.QuotaWindows))
	for _, window := range snapshot.QuotaWindows {
		labels = append(labels, window.Label)
		labelKeys = append(labelKeys, window.LabelKey)
	}
	if !reflect.DeepEqual(labels, []string{"7d", "5h"}) ||
		!reflect.DeepEqual(labelKeys, []string{"weekly", "session"}) {
		t.Fatalf("window labels = %q, keys = %q", labels, labelKeys)
	}
}

func TestNormalizePassiveQuotaWindowsMapsPrimaryAndSecondary(t *testing.T) {
	observedAt := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	windows := NormalizePassiveQuotaWindows(map[string]string{
		"X-Codex-Primary-Used-Percent":     "55",
		"X-Codex-Primary-Window-Minutes":   "300",
		"X-Codex-Primary-Reset-At":         "1800000000",
		"X-Codex-Secondary-Used-Percent":   "10",
		"X-Codex-Secondary-Window-Minutes": "10080",
	}, observedAt)
	if len(windows) != 2 {
		t.Fatalf("windows = %#v, want 2 entries", windows)
	}
	byID := map[string]quotaWindow{}
	for _, window := range windows {
		byID[window.ID] = window
	}
	primary, ok := byID["primary"]
	if !ok || primary.Scope != "account" || primary.Used == nil || *primary.Used != 55 ||
		primary.Utilization == nil || *primary.Utilization != 0.55 || primary.State != "available" ||
		primary.WindowSeconds == nil || *primary.WindowSeconds != 300*60 ||
		primary.ResetAtMS == nil || *primary.ResetAtMS != 1800000000*1000 {
		t.Fatalf("primary window = %#v", primary)
	}
	secondary, ok := byID["secondary"]
	if !ok || secondary.Used == nil || *secondary.Used != 10 || secondary.ResetAtMS != nil {
		t.Fatalf("secondary window = %#v", secondary)
	}
}

func TestNormalizePassiveQuotaWindowsAppliesAllowedFalseAsExhausted(t *testing.T) {
	windows := NormalizePassiveQuotaWindows(map[string]string{
		"X-Codex-Primary-Used-Percent": "10",
		"X-Codex-Allowed":              "false",
	}, time.Now())
	if len(windows) != 1 || windows[0].State != "exhausted" {
		t.Fatalf("windows = %#v, want exhausted primary", windows)
	}
}

func TestNormalizePassiveQuotaWindowsComputesResetFromRelativeSeconds(t *testing.T) {
	observedAt := time.Unix(1000, 0).UTC()
	windows := NormalizePassiveQuotaWindows(map[string]string{
		"X-Codex-Primary-Used-Percent":        "5",
		"X-Codex-Primary-Reset-After-Seconds": "60",
	}, observedAt)
	if len(windows) != 1 || windows[0].ResetAtMS == nil || *windows[0].ResetAtMS != 1060*1000 {
		t.Fatalf("windows = %#v, want reset_at_ms 1060000", windows)
	}
}

func TestNormalizePassiveQuotaWindowsRejectsNaNAndInf(t *testing.T) {
	for _, value := range []string{"NaN", "Inf", "-Inf", "not-a-number"} {
		windows := NormalizePassiveQuotaWindows(map[string]string{
			"X-Codex-Primary-Used-Percent": value,
		}, time.Now())
		if len(windows) != 0 {
			t.Fatalf("value %q produced windows = %#v, want none", value, windows)
		}
	}
}

func TestNormalizePassiveQuotaWindowsIgnoresUnrelatedHeaders(t *testing.T) {
	windows := NormalizePassiveQuotaWindows(map[string]string{
		"X-Codex-Plan-Type":       "pro",
		"X-Codex-Credits-Balance": "10",
		"Retry-After":             "30",
	}, time.Now())
	if len(windows) != 0 {
		t.Fatalf("windows = %#v, want none", windows)
	}
}

func TestNormalizeQuotaNormalizesProLitePlan(t *testing.T) {
	raw, err := NormalizeQuota([]byte(`{"plan_type":"pro_lite"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot quotaSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Plan.Name != "Pro 5x" || snapshot.Plan.Level != "premium" {
		t.Fatalf("plan = %#v", snapshot.Plan)
	}
}

func TestNormalizeQuotaKeepsAvailableResetCreditsWithoutExpiry(t *testing.T) {
	raw, err := NormalizeQuota(
		[]byte(`{"plan_type":"pro"}`),
		[]byte(`{"items":[
			{"status":"available","reset_type":"codex_rate_limits"},
			{"status":"available","reset_type":"codex_rate_limits","expires_at":"2026-08-23T12:00:00Z"},
			{"status":"consumed","reset_type":"codex_rate_limits","expires_at":"2026-08-22T12:00:00Z"}
		]}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot quotaSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.ResetCreditsAvailable == nil || *snapshot.ResetCreditsAvailable != 2 {
		t.Fatalf("available reset credits = %#v, want 2", snapshot.ResetCreditsAvailable)
	}
	if len(snapshot.ResetCredits) != 2 || snapshot.ResetCredits[0].ExpiresAtMS == nil ||
		*snapshot.ResetCredits[0].ExpiresAtMS != 1_787_486_400_000 ||
		snapshot.ResetCredits[1].ExpiresAtMS != nil {
		t.Fatalf("reset credits = %#v", snapshot.ResetCredits)
	}
}
