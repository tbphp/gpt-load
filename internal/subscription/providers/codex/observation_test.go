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

func passiveWindowsByID(windows []quotaWindow) map[string]quotaWindow {
	result := make(map[string]quotaWindow, len(windows))
	for _, window := range windows {
		result[window.ID] = window
	}
	return result
}

func TestNormalizePassiveQuotaWindowsMapsAdditionalLimitNamespaces(t *testing.T) {
	// The same additional limit the active JSON payload reports as
	// limit_name "GPT 5.3 Codex Spark" arrives on the HTTP path under a short
	// namespace plus an X-Codex-<ns>-Limit-Name header.
	windows := NormalizePassiveQuotaWindows(map[string]string{
		"X-Codex-Primary-Used-Percent":               "20",
		"X-Codex-Bengalfox-Limit-Name":               "GPT 5.3 Codex Spark",
		"X-Codex-Bengalfox-Primary-Used-Percent":     "33",
		"X-Codex-Bengalfox-Primary-Window-Minutes":   "300",
		"X-Codex-Bengalfox-Secondary-Used-Percent":   "44",
		"X-Codex-Bengalfox-Secondary-Window-Minutes": "10080",
	}, time.Now())

	byID := passiveWindowsByID(windows)
	// Account-scope windows keep working alongside the additional namespace.
	if _, ok := byID["primary"]; !ok {
		t.Fatalf("windows = %#v, want the account-scope primary window", windows)
	}
	spark, ok := byID["gpt-5-3-codex-spark-primary"]
	if !ok || spark.Scope != "GPT 5.3 Codex Spark" ||
		spark.Used == nil || *spark.Used != 33 {
		t.Fatalf("additional primary window = %#v (all=%#v)", spark, windows)
	}
	sparkWeekly, ok := byID["gpt-5-3-codex-spark-secondary"]
	if !ok || sparkWeekly.Used == nil || *sparkWeekly.Used != 44 {
		t.Fatalf("additional secondary window = %#v", sparkWeekly)
	}
}

func TestNormalizePassiveQuotaWindowsAdditionalIDMatchesActiveJSON(t *testing.T) {
	// The passive namespace ID must equal the ID the active JSON path builds
	// for the same limit_name, otherwise the merge silently never applies.
	raw, err := NormalizeQuota([]byte(`{
		"plan_type":"pro",
		"additional_rate_limits":[{
			"limit_name":"GPT 5.3 Codex Spark",
			"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000}}
		}]
	}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot quotaSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	activeIDs := passiveWindowsByID(snapshot.QuotaWindows)

	passive := NormalizePassiveQuotaWindows(map[string]string{
		"X-Codex-Bengalfox-Limit-Name":           "GPT 5.3 Codex Spark",
		"X-Codex-Bengalfox-Primary-Used-Percent": "33",
	}, time.Now())
	if len(passive) != 1 {
		t.Fatalf("passive windows = %#v, want exactly one", passive)
	}
	if _, ok := activeIDs[passive[0].ID]; !ok {
		t.Fatalf("passive ID %q not produced by the active JSON path (active=%v)",
			passive[0].ID, mapKeys(activeIDs))
	}
}

func TestNormalizePassiveQuotaWindowsSkipsNamespaceWithoutLimitName(t *testing.T) {
	// Without a Limit-Name there is no way to build the same stable ID the
	// active path uses, so the namespace must be ignored entirely.
	windows := NormalizePassiveQuotaWindows(map[string]string{
		"X-Codex-Mysteryns-Primary-Used-Percent": "33",
	}, time.Now())
	if len(windows) != 0 {
		t.Fatalf("windows = %#v, want none without a Limit-Name", windows)
	}
}

func TestNormalizePassiveQuotaWindowsOmitsLabelMetadata(t *testing.T) {
	// Labels are derived from the window period, which a partial response may
	// not carry. Leaving them empty keeps the active observation's metadata.
	windows := NormalizePassiveQuotaWindows(map[string]string{
		"X-Codex-Primary-Used-Percent": "50",
	}, time.Now())
	if len(windows) != 1 || windows[0].Label != "" || windows[0].LabelKey != "" {
		t.Fatalf("windows = %#v, want empty Label/LabelKey", windows)
	}
}

func TestNormalizePassiveQuotaWindowsRejectsNonPositivePeriodAndReset(t *testing.T) {
	for _, test := range []struct {
		name   string
		signal map[string]string
	}{
		{"zero reset", map[string]string{"X-Codex-Primary-Used-Percent": "50", "X-Codex-Primary-Reset-At": "0"}},
		{"zero window", map[string]string{"X-Codex-Primary-Used-Percent": "50", "X-Codex-Primary-Window-Minutes": "0"}},
		{"overflowing reset", map[string]string{"X-Codex-Primary-Used-Percent": "50", "X-Codex-Primary-Reset-At": "92233720368547758"}},
		{"negative reset-after", map[string]string{"X-Codex-Primary-Used-Percent": "50", "X-Codex-Primary-Reset-After-Seconds": "-999"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			windows := NormalizePassiveQuotaWindows(test.signal, time.Unix(1000, 0).UTC())
			if len(windows) != 1 {
				t.Fatalf("windows = %#v, want one usage-only window", windows)
			}
			if windows[0].ResetAtMS != nil {
				t.Fatalf("reset_at_ms = %d, want nil so a valid stored value is not overwritten", *windows[0].ResetAtMS)
			}
			if windows[0].WindowSeconds != nil {
				t.Fatalf("window_seconds = %d, want nil so a valid stored value is not overwritten", *windows[0].WindowSeconds)
			}
		})
	}
}

func TestNormalizePassiveQuotaWindowsRejectsOutOfRangeUsedPercent(t *testing.T) {
	for _, value := range []string{"-1", "101"} {
		windows := NormalizePassiveQuotaWindows(map[string]string{
			"X-Codex-Primary-Used-Percent": value,
		}, time.Now())
		if len(windows) != 0 {
			t.Fatalf("used-percent %q produced %#v, want none", value, windows)
		}
	}
}

func mapKeys(values map[string]quotaWindow) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
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
