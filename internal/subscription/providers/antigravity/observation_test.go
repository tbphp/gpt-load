package antigravity

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func TestNormalizeObservationPreservesQuotaGroupNamesAndValues(t *testing.T) {
	raw, err := NormalizeObservation("", AccountObservation{
		PlanID: "g1-pro-tier",
		QuotaGroups: []QuotaGroup{
			{DisplayName: "Gemini Models", Buckets: []QuotaBucket{
				{ID: "gemini-weekly", DisplayName: "Weekly Limit Remaining", Window: "weekly", RemainingFraction: float64Ptr(0.778), ResetTime: "2026-10-23T23:04:59Z"},
				{ID: "gemini-5h", DisplayName: "Five Hour Limit Remaining", Window: "5h", RemainingFraction: float64Ptr(0.981), ResetTime: "2026-10-23T10:31:56Z"},
			}},
			{DisplayName: "Claude and GPT models", Buckets: []QuotaBucket{
				{ID: "3p-weekly", DisplayName: "Weekly Limit Remaining", Window: "weekly", RemainingFraction: float64Ptr(0.018), ResetTime: "2026-10-23T12:41:29Z"},
				{ID: "3p-5h", DisplayName: "Five Hour Limit Remaining", Window: "5h", RemainingFraction: float64Ptr(1), ResetTime: "2026-10-23T13:59:56Z"},
			}},
			{DisplayName: "Gemini 4 Argon", Buckets: []QuotaBucket{
				{ID: "gemini-argon-wtu-weekly", DisplayName: "Weekly Limit Remaining", Window: "weekly", RemainingFraction: float64Ptr(1), ResetTime: "2026-10-30T08:59:56Z"},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot quotaSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	want := []struct {
		id        string
		label     string
		remaining float64
		seconds   int64
		resetAtMS int64
	}{
		{"gemini-weekly", "Gemini · 7d", 77.8, 604800, 1792796699000},
		{"gemini-5h", "Gemini · 5h", 98.1, 18000, 1792751516000},
		{"3p-weekly", "Claude/GPT · 7d", 1.8, 604800, 1792759289000},
		{"3p-5h", "Claude/GPT · 5h", 100, 18000, 1792763996000},
		{"gemini-argon-wtu-weekly", "Gemini 4 Argon · 7d", 100, 604800, 1793350796000},
	}
	if len(snapshot.QuotaWindows) != len(want) {
		t.Fatalf("quota windows = %d, want %d", len(snapshot.QuotaWindows), len(want))
	}
	for index, expected := range want {
		window := snapshot.QuotaWindows[index]
		if window.ID != expected.id || window.Label != expected.label ||
			window.Scope != "model" || window.Unit != "percent" || window.SourceID != "" ||
			window.State != "available" || window.IsPrimary != (index == 0) {
			t.Errorf("window %d identity/presentation = %+v", index, window)
		}
		if window.Remaining == nil || math.Abs(*window.Remaining-expected.remaining) > 1e-9 ||
			window.Used == nil || math.Abs(*window.Used-(100-expected.remaining)) > 1e-9 ||
			window.Limit == nil || *window.Limit != 100 ||
			window.Utilization == nil || math.Abs(*window.Utilization-(1-expected.remaining/100)) > 1e-9 ||
			window.WindowSeconds == nil || *window.WindowSeconds != expected.seconds ||
			window.ResetAtMS == nil || *window.ResetAtMS != expected.resetAtMS {
			t.Errorf("window %s quota fields = %+v", expected.id, window)
		}
	}
}

func TestNormalizeObservationPreservesSpecificQuotaGroupNames(t *testing.T) {
	for _, name := range []string{"Gemini 4 WTU", "Claude Opus", "GPT Research", "Custom API Pool"} {
		t.Run(name, func(t *testing.T) {
			raw, err := NormalizeObservation("", AccountObservation{QuotaGroups: []QuotaGroup{{
				DisplayName: " " + name + " ",
				Buckets:     []QuotaBucket{{ID: "weekly", Window: "weekly", RemainingFraction: float64Ptr(1)}},
			}}})
			if err != nil {
				t.Fatal(err)
			}
			var snapshot quotaSnapshot
			if err := json.Unmarshal(raw, &snapshot); err != nil {
				t.Fatal(err)
			}
			if len(snapshot.QuotaWindows) != 1 || snapshot.QuotaWindows[0].Label != name+" · 7d" {
				t.Fatalf("quota windows = %+v", snapshot.QuotaWindows)
			}
		})
	}
}

func TestNormalizeObservationDisambiguatesQuotaLabels(t *testing.T) {
	for _, test := range []struct {
		name        string
		bucketNames []string
		wantLabels  []string
	}{
		{
			name:        "distinct bucket names",
			bucketNames: []string{"Standard Pool", "Special WTU Pool"},
			wantLabels:  []string{"Gemini (Standard Pool) · 7d", "Gemini (Special WTU Pool) · 7d"},
		},
		{
			name:        "identical bucket names",
			bucketNames: []string{"Weekly Limit Remaining", "Weekly Limit Remaining"},
			wantLabels:  []string{"Gemini (gemini-weekly) · 7d", "Gemini (gemini-special-weekly) · 7d"},
		},
		{
			name:        "missing bucket names",
			bucketNames: []string{"", ""},
			wantLabels:  []string{"Gemini (gemini-weekly) · 7d", "Gemini (gemini-special-weekly) · 7d"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			buckets := []QuotaBucket{
				{ID: "gemini-weekly", DisplayName: test.bucketNames[0], Window: "weekly", RemainingFraction: float64Ptr(0.75)},
				{ID: "gemini-special-weekly", DisplayName: test.bucketNames[1], Window: "weekly", RemainingFraction: float64Ptr(0.5)},
				{ID: "gemini-5h", DisplayName: "Five Hour Limit Remaining", Window: "5h", RemainingFraction: float64Ptr(1)},
			}
			// 上游返回顺序改变时，同一个桶仍应得到同一个名称。
			for _, order := range [][]QuotaBucket{buckets, {buckets[2], buckets[1], buckets[0]}} {
				raw, err := NormalizeObservation("", AccountObservation{QuotaGroups: []QuotaGroup{{
					DisplayName: "Gemini Models", Buckets: order,
				}}})
				if err != nil {
					t.Fatal(err)
				}
				var snapshot quotaSnapshot
				if err := json.Unmarshal(raw, &snapshot); err != nil {
					t.Fatal(err)
				}
				labels := make(map[string]string, len(snapshot.QuotaWindows))
				for _, window := range snapshot.QuotaWindows {
					labels[window.ID] = window.Label
				}
				want := map[string]string{
					"gemini-weekly":         test.wantLabels[0],
					"gemini-special-weekly": test.wantLabels[1],
					"gemini-5h":             "Gemini · 5h",
				}
				if !reflect.DeepEqual(labels, want) {
					t.Errorf("labels = %v, want %v", labels, want)
				}
			}
		})
	}
}
