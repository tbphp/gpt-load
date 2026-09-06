package subscription

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"gpt-load/internal/subscription/providers/codex"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

const codexSourceQuotaPayload = `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":80,"limit_window_seconds":604800,"reset_at":1800000000}},"additional_rate_limits":[{"metered_feature":"codex_bengalfox","limit_name":"GPT-5.3-Codex-Spark","rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000,"reset_at":1799900000}}}]}`

func TestPassiveQuotaMatchesSourceAndPeriodAcrossSlots(t *testing.T) {
	raw, err := codex.NormalizeQuota([]byte(codexSourceQuotaPayload), nil)
	if err != nil {
		t.Fatal(err)
	}
	var active providerobservation.Snapshot
	if err := json.Unmarshal(raw, &active); err != nil {
		t.Fatal(err)
	}
	patches := codex.NormalizePassiveQuotaWindows(map[string]string{
		// 本次请求计费到普通额度，通用组报告账号自身窗口，Spark 另有命名空间。
		"X-Codex-Active-Limit":                       "premium",
		"X-Codex-Primary-Used-Percent":               "5",
		"X-Codex-Primary-Window-Minutes":             "300",
		"X-Codex-Secondary-Used-Percent":             "100",
		"X-Codex-Secondary-Window-Minutes":           "10080",
		"X-Codex-Secondary-Reset-At":                 "1800000600",
		"X-Codex-Bengalfox-Secondary-Used-Percent":   "30",
		"X-Codex-Bengalfox-Secondary-Window-Minutes": "300",
		"X-Codex-Bengalfox-Secondary-Reset-At":       "1799900600",
	}, time.Unix(1799890000, 0))
	merged, err := mergePassiveQuotaSnapshot(raw, patches)
	if err != nil {
		t.Fatal(err)
	}
	if !merged.Matched || !merged.Changed || len(merged.Windows) != 2 {
		t.Fatalf("matched=%v changed=%v windows=%s, want both existing windows refreshed", merged.Matched, merged.Changed, merged.Encoded)
	}
	for index, expected := range []struct {
		used  float64
		reset int64
		state string
	}{{100, 1800000600000, "exhausted"}, {30, 1799900600000, "available"}} {
		got, previous := merged.Windows[index], active.QuotaWindows[index]
		if got.ID != previous.ID || got.SourceID != previous.SourceID ||
			got.Label != previous.Label || got.Scope != previous.Scope ||
			!reflect.DeepEqual(got.WindowSeconds, previous.WindowSeconds) ||
			got.Used == nil || *got.Used != expected.used ||
			got.ResetAtMS == nil || *got.ResetAtMS != expected.reset || got.State != expected.state {
			t.Fatalf("wrong target or metadata overwritten: %s", merged.Encoded)
		}
	}
	var encoded providerobservation.Snapshot
	if err := json.Unmarshal(merged.Encoded, &encoded); err != nil ||
		!reflect.DeepEqual(encoded.QuotaWindows, merged.Windows) {
		t.Fatalf("encoded quota windows differ: %s, %v", merged.Encoded, err)
	}
}

func TestPassiveQuotaSourceMatchingRejectsUncertainTargets(t *testing.T) {
	period, otherPeriod := int64(18000), int64(604800)
	used, utilization := 50.0, 0.5
	patch := providerobservation.QuotaWindow{
		ID: "primary", SourceID: "codex", WindowSeconds: &period,
		Used: &used, Utilization: &utilization, State: "exhausted",
	}
	window := providerobservation.QuotaWindow{
		ID: "primary", SourceID: "codex", WindowSeconds: &period, State: "available",
	}
	for _, name := range []string{
		"different source same ID and period", "unknown source", "missing patch period",
		"missing stored period", "legacy snapshot", "unidentified patch",
		"ambiguous stored windows", "duplicate response windows", "period conflict",
	} {
		t.Run(name, func(t *testing.T) {
			windows, patches := []providerobservation.QuotaWindow{window}, []providerobservation.QuotaWindow{patch}
			switch name {
			case "different source same ID and period":
				patches[0].SourceID = "codex_bengalfox"
			case "unknown source":
				patches[0].SourceID = "codex_unknown"
			case "missing patch period":
				patches[0].WindowSeconds = nil
			case "missing stored period":
				windows[0].WindowSeconds = nil
			case "legacy snapshot":
				windows[0].SourceID = ""
			case "unidentified patch":
				patches[0].SourceID = ""
			case "ambiguous stored windows":
				second := window
				second.ID = "secondary"
				windows = append(windows, second)
			case "duplicate response windows":
				second := patch
				second.ID = "secondary"
				patches = append(patches, second)
			case "period conflict":
				patches[0].WindowSeconds = &otherPeriod
			}
			raw, err := json.Marshal(providerobservation.Snapshot{QuotaWindows: windows})
			if err != nil {
				t.Fatal(err)
			}
			merged, err := mergePassiveQuotaSnapshot(raw, patches)
			if err != nil {
				t.Fatal(err)
			}
			if merged.Matched || merged.Changed || !bytes.Equal(merged.Encoded, raw) {
				t.Fatalf("uncertain sample changed data or freshness: matched=%v changed=%v snapshot=%s", merged.Matched, merged.Changed, merged.Encoded)
			}
		})
	}
}

// 复现维护者现场：普通 7d 用尽后请求 Spark，上游只在通用组里回本次计费到的
// Spark 周窗口。按普通额度收下会把 7d 刷成满额，必须落到 Spark 自己的窗口上。
func TestPassiveQuotaMeteredRequestDoesNotRefillAccountWeekly(t *testing.T) {
	raw, err := codex.NormalizeQuota([]byte(`{"plan_type":"pro",
		"rate_limit":{"primary_window":{"used_percent":100,"limit_window_seconds":604800,"reset_at":1788700000}},
		"additional_rate_limits":[{"metered_feature":"codex_bengalfox","limit_name":"GPT-5.3-Codex-Spark",
			"rate_limit":{"primary_window":{"used_percent":0,"limit_window_seconds":18000,"reset_at":1788678033},
				"secondary_window":{"used_percent":0,"limit_window_seconds":604800,"reset_at":1789222487}}}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var active providerobservation.Snapshot
	if err := json.Unmarshal(raw, &active); err != nil {
		t.Fatal(err)
	}
	if len(active.QuotaWindows) != 3 || active.QuotaWindows[0].State != "exhausted" {
		t.Fatalf("unexpected active snapshot: %s", raw)
	}

	patches := codex.NormalizePassiveQuotaWindows(map[string]string{
		"X-Codex-Active-Limit":             "codex_bengalfox",
		"X-Codex-Secondary-Used-Percent":   "10",
		"X-Codex-Secondary-Window-Minutes": "10080",
		"X-Codex-Secondary-Reset-At":       "1789222487",
	}, time.Unix(1788660000, 0))
	merged, err := mergePassiveQuotaSnapshot(raw, patches)
	if err != nil {
		t.Fatal(err)
	}
	if !merged.Matched || !merged.Changed || len(merged.Windows) != 3 {
		t.Fatalf("merge = %#v, want only the Spark weekly window refreshed", merged)
	}
	want := append([]providerobservation.QuotaWindow(nil), active.QuotaWindows...)
	used, remaining, utilization := 10.0, 90.0, 0.1
	want[2].Used, want[2].Remaining, want[2].Utilization = &used, &remaining, &utilization
	if !reflect.DeepEqual(merged.Windows, want) {
		t.Fatalf("account weekly window was refilled by the Spark copy: %s", merged.Encoded)
	}
}
