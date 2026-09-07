package codex

import (
	"encoding/json"
	"testing"
	"time"
)

// 上游只在通用 X-Codex-Primary/Secondary-* 组里报告本次请求计费到的那份额度。
// 请求 Spark 时这一组装的是 Spark 的窗口，按普通账号额度收下会覆盖普通 7d。
func TestPassiveQuotaGenericWindowsFollowActiveLimit(t *testing.T) {
	weekly := map[string]string{
		"X-Codex-Secondary-Used-Percent":   "0",
		"X-Codex-Secondary-Window-Minutes": "10080",
		"X-Codex-Secondary-Reset-At":       "1789222487",
	}
	sparkNamespace := map[string]string{
		"X-Codex-Bengalfox-Primary-Used-Percent":   "20",
		"X-Codex-Bengalfox-Primary-Window-Minutes": "300",
	}
	for _, test := range []struct {
		name        string
		activeLimit string
		namespaced  bool
		wantSources []string
		wantPeriods []int64
	}{
		{"spark request reports only the metered window", "codex_bengalfox", false,
			[]string{"codex_bengalfox"}, []int64{604800}},
		{"account request keeps the account source", "premium", false,
			[]string{"codex"}, []int64{604800}},
		{"account alias keeps the account source", "codex", false,
			[]string{"codex"}, []int64{604800}},
		// 同来源但不同周期，两个窗口各自对应一份额度，都要刷新。
		{"metered copy covers another period", "codex_bengalfox", true,
			[]string{"codex_bengalfox", "codex_bengalfox"}, []int64{604800, 18000}},
		{"legacy response without an active limit", "", false,
			[]string{"codex"}, []int64{604800}},
		{"unidentified copy alongside a metered namespace", "", true,
			[]string{"codex_bengalfox"}, []int64{18000}},
	} {
		t.Run(test.name, func(t *testing.T) {
			signals := map[string]string{}
			for key, value := range weekly {
				signals[key] = value
			}
			if test.activeLimit != "" {
				signals["X-Codex-Active-Limit"] = test.activeLimit
			}
			if test.namespaced {
				for key, value := range sparkNamespace {
					signals[key] = value
				}
			}

			windows := NormalizePassiveQuotaWindows(signals, time.Now())
			if len(windows) != len(test.wantSources) {
				t.Fatalf("windows = %#v, want %d from %v", windows, len(test.wantSources), test.wantSources)
			}
			for index, want := range test.wantSources {
				window := windows[index]
				if window.SourceID != want {
					t.Fatalf("window %d source = %q, want %q (all=%#v)", index, window.SourceID, want, windows)
				}
				if window.WindowSeconds == nil || *window.WindowSeconds != test.wantPeriods[index] {
					t.Fatalf("window %d period = %v, want %d (all=%#v)",
						index, window.WindowSeconds, test.wantPeriods[index], windows)
				}
			}
		})
	}
}

// 同来源同周期才是真正的重复：这时通用副本必须让位，否则两份数据指向同一个
// 窗口，合并层会判定为歧义而把两者一起丢弃。
func TestPassiveQuotaMeteredCopyDoesNotCompeteWithItsNamespace(t *testing.T) {
	windows := NormalizePassiveQuotaWindows(map[string]string{
		"X-Codex-Active-Limit":                       "codex_bengalfox",
		"X-Codex-Primary-Used-Percent":               "20",
		"X-Codex-Primary-Window-Minutes":             "300",
		"X-Codex-Bengalfox-Primary-Used-Percent":     "20",
		"X-Codex-Bengalfox-Primary-Window-Minutes":   "300",
		"X-Codex-Bengalfox-Secondary-Used-Percent":   "40",
		"X-Codex-Bengalfox-Secondary-Window-Minutes": "10080",
	}, time.Now())
	if len(windows) != 2 {
		t.Fatalf("windows = %#v, want only the namespaced pair", windows)
	}
	for _, window := range windows {
		if window.SourceID != "codex_bengalfox" || window.WindowSeconds == nil {
			t.Fatalf("unexpected window: %#v (all=%#v)", window, windows)
		}
	}
	if *windows[0].WindowSeconds != 18000 || *windows[1].WindowSeconds != 604800 {
		t.Fatalf("duplicate or missing period: %#v", windows)
	}
}

// Active-Limit 的取值与主动查询的 metered_feature 是同一个来源标识，
// 改绑通用组时不再经过响应头命名空间的前缀推导。
func TestPassiveQuotaActiveLimitMatchesActiveMeteredFeature(t *testing.T) {
	raw, err := NormalizeQuota([]byte(`{
		"rate_limit":{"primary_window":{"used_percent":100,"limit_window_seconds":604800,"reset_at":1788700000}},
		"additional_rate_limits":[{"metered_feature":"codex_bengalfox","limit_name":"GPT-5.3-Codex-Spark",
			"rate_limit":{"secondary_window":{"used_percent":0,"limit_window_seconds":604800,"reset_at":1789222487}}}]
	}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var active quotaSnapshot
	if err := json.Unmarshal(raw, &active); err != nil {
		t.Fatal(err)
	}
	windows := NormalizePassiveQuotaWindows(map[string]string{
		"X-Codex-Active-Limit":             "codex_bengalfox",
		"X-Codex-Secondary-Used-Percent":   "0",
		"X-Codex-Secondary-Window-Minutes": "10080",
		"X-Codex-Secondary-Reset-At":       "1789222487",
	}, time.Now())
	if len(windows) != 1 {
		t.Fatalf("windows = %#v, want the rebound Spark window", windows)
	}
	spark := active.QuotaWindows[1]
	if windows[0].SourceID != spark.SourceID {
		t.Fatalf("passive source = %q, want the active metered feature %q", windows[0].SourceID, spark.SourceID)
	}
	if windows[0].WindowSeconds == nil || spark.WindowSeconds == nil ||
		*windows[0].WindowSeconds != *spark.WindowSeconds {
		t.Fatalf("passive period does not align with the active window: %#v", windows[0])
	}
}

// 去重只针对通用组与独立命名空间之间的重复报告。通用组内部的两个槽位始终是
// 两份不同的数据，即使周期相同也不能互相判定为副本而双双消失，是否可用交给
// 合并层判定。
func TestPassiveQuotaDeduplicationIgnoresSiblingGenericWindows(t *testing.T) {
	windows := NormalizePassiveQuotaWindows(map[string]string{
		"X-Codex-Active-Limit":             "premium",
		"X-Codex-Primary-Used-Percent":     "10",
		"X-Codex-Primary-Window-Minutes":   "300",
		"X-Codex-Secondary-Used-Percent":   "20",
		"X-Codex-Secondary-Window-Minutes": "300",
	}, time.Now())
	if len(windows) != 2 {
		t.Fatalf("windows = %#v, want both generic slots preserved", windows)
	}
	for index, window := range windows {
		if window.SourceID != "codex" || window.WindowSeconds == nil || *window.WindowSeconds != 18000 {
			t.Fatalf("window %d = %#v", index, window)
		}
	}
	if windows[0].Used == nil || *windows[0].Used != 10 ||
		windows[1].Used == nil || *windows[1].Used != 20 {
		t.Fatalf("generic slots lost their own values: %#v", windows)
	}
}
