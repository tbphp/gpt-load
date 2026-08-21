package codex

import (
	"encoding/json"
	"reflect"
	"testing"
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
