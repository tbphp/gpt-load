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
	for _, window := range snapshot.QuotaWindows {
		labels = append(labels, window.Label)
	}
	if !reflect.DeepEqual(labels, []string{"Weekly · 7d", "Session · 5h"}) {
		t.Fatalf("window labels = %q", labels)
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
