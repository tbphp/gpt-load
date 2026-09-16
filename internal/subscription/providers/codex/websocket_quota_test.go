package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWebsocketQuotaComparisonUsesSharedClock(t *testing.T) {
	for _, test := range []struct {
		name         string
		topReset     string
		otherReset   string
		accountCount int
	}{
		{"absolute copy", `"reset_at":1800000000`, `"reset_at":1800000001`, 0},
		{"absolute distinct", `"reset_at":1800000000`, `"reset_at":1800000003`, 1},
		{"relative copy", `"reset_after_seconds":100`, `"reset_after_seconds":101`, 0},
		{"relative distinct", `"reset_after_seconds":100`, `"reset_after_seconds":103`, 1},
		{"mixed clocks", `"reset_at":1800000000`, `"reset_after_seconds":100`, 0},
		{"shared relative clock", `"reset_at":1800000000,"reset_after_seconds":100`, `"reset_after_seconds":100`, 0},
		{"shared absolute clock", `"reset_at":1800000000,"reset_after_seconds":100`, `"reset_at":1800000000`, 0},
		{"conflicting duplicate evidence", `"reset_at":1800000000,"reset_after_seconds":100`, `"reset_at":1800000003,"reset_after_seconds":100`, 0},
		{"missing clock", `"reset_at":1800000000`, `"reset_at":null`, 0},
		{"invalid absolute with relative copy", `"reset_at":"bad","reset_after_seconds":100`, `"reset_after_seconds":100`, 0},
	} {
		for _, swap := range []bool{false, true} {
			for _, delay := range []int64{0, 5} {
				t.Run(fmt.Sprintf("%s/swap=%t/delay=%d", test.name, swap, delay), func(t *testing.T) {
					top, other := test.topReset, test.otherReset
					if swap {
						top, other = other, top
					}
					payload := []byte(fmt.Sprintf(`{
						"type":"codex.rate_limits",
						"rate_limits":{"primary":{"used_percent":5,"window_minutes":10080,%s}},
						"additional_rate_limits":{"Spark":{"secondary":{"used_percent":20,"window_minutes":10080,%s}}}
					}`, top, other))
					windows := NormalizeWebsocketQuotaWindows(payload, time.Unix(1799999900+delay, 0))
					account, named := 0, 0
					for _, window := range windows {
						if window.SourceID == "codex" {
							account++
						} else if window.SourceName == "Spark" && window.Used != nil && *window.Used == 20 {
							named++
						}
					}
					if account != test.accountCount || named != 1 {
						t.Fatalf("account=%d named=%d, want account=%d named=1", account, named, test.accountCount)
					}
				})
			}
		}
	}
}

func TestWebsocketQuotaNonQuotaEventsHaveBoundedAllocations(t *testing.T) {
	text := strings.Repeat("x", 1<<20)
	for _, payload := range [][]byte{
		[]byte(`{"type":"response.output_text.delta","delta":"` + text + `"}`),
		[]byte(`{"delta":"` + text + `","type":"response.output_text.delta"}`),
	} {
		allocs := testing.AllocsPerRun(5, func() {
			if len(NormalizeWebsocketQuotaWindows(payload, time.Time{})) != 0 {
				t.Fatal("non-quota event produced quota")
			}
		})
		if allocs > 4 {
			t.Fatalf("non-quota event allocated %.0f objects, want a lightweight type check (at most 4)", allocs)
		}
	}
}

func BenchmarkWebsocketQuotaNonQuotaEvent(b *testing.B) {
	text := strings.Repeat("x", 1<<20)
	for name, payload := range map[string][]byte{
		"type_first": []byte(`{"type":"response.output_text.delta","delta":"` + text + `"}`),
		"type_last":  []byte(`{"delta":"` + text + `","type":"response.output_text.delta"}`),
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				NormalizeWebsocketQuotaWindows(payload, time.Time{})
			}
		})
	}
}

func TestWebsocketQuotaDropsSameEventCopyWithinResetPrecision(t *testing.T) {
	payload, err := os.ReadFile("testdata/quota-ws-spark.json")
	if err != nil {
		t.Fatal(err)
	}
	// 同一事件里的副本允许 1 秒 reset_at 精度差；用量值不参与窗口身份判断。
	var event map[string]any
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	weekly := event["rate_limits"].(map[string]any)["secondary"].(map[string]any)
	weekly["used_percent"] = float64(42)
	weekly["reset_at"] = weekly["reset_at"].(float64) + 1
	payload, err = json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	windows := NormalizeWebsocketQuotaWindows(payload, time.Unix(1000, 0))
	if len(windows) != 2 {
		t.Fatalf("windows = %#v, want only named Spark windows", windows)
	}
	for _, window := range windows {
		if window.SourceID != "" || window.SourceName != "GPT-5.3-Codex-Spark" {
			t.Fatalf("unexpected Spark source: id=%q name=%q", window.SourceID, window.SourceName)
		}
	}
}

// testdata 为 2026-09-12 对同一账号实测得到的额度字段，不包含身份和凭据。
func TestWebsocketQuotaCapturedAccountAndSparkEvents(t *testing.T) {
	for _, test := range []struct {
		file         string
		accountCount int
	}{
		{"quota-ws-account.json", 1},
		{"quota-ws-spark.json", 0},
	} {
		t.Run(test.file, func(t *testing.T) {
			payload, err := os.ReadFile("testdata/" + test.file)
			if err != nil {
				t.Fatal(err)
			}
			windows := NormalizeWebsocketQuotaWindows(payload, time.Unix(1789204875, 0))
			if len(windows) != test.accountCount+2 {
				t.Fatalf("windows=%#v", windows)
			}
			account, spark := 0, 0
			for _, window := range windows {
				if window.SourceID == "codex" {
					account++
					if test.file == "quota-ws-account.json" && (*window.Used != 6 || *window.Remaining != 94 || *window.WindowSeconds != 604800) {
						t.Fatalf("account quota=%#v", window)
					}
				} else if window.SourceName == "GPT-5.3-Codex-Spark" && window.SourceID == "" {
					spark++
					if *window.Used != 0 || *window.Remaining != 100 || *window.ResetAtMS <= 0 {
						t.Fatalf("Spark quota=%#v", window)
					}
				}
				if window.Label != "" || window.Scope != "" || window.Unit != "" {
					t.Fatalf("passive event rewrites presentation: %#v", window)
				}
			}
			if account != test.accountCount || spark != 2 {
				t.Fatalf("account=%d spark=%d", account, spark)
			}
		})
	}
}

func TestWebsocketQuotaKeepsExplicitSourceAndExistingConversions(t *testing.T) {
	observedAt := time.Unix(1000, 0)
	windows := NormalizeWebsocketQuotaWindows([]byte(`{
		"type":"codex.rate_limits","metered_limit_name":"CODEX-BENGALFOX",
		"rate_limits":{"allowed":false,"secondary":{"used_percent":1,"window_minutes":10080,
			"reset_at":-1,"reset_after_seconds":60}}
	}`), observedAt)
	if len(windows) != 1 || windows[0].SourceID != "codex_bengalfox" ||
		*windows[0].Used != 1 || *windows[0].Remaining != 99 || *windows[0].Utilization != .01 ||
		*windows[0].WindowSeconds != 604800 || *windows[0].ResetAtMS != 1060000 || windows[0].State != "exhausted" {
		t.Fatalf("windows=%#v", windows)
	}
}

func TestWebsocketQuotaRejectsUnusableOrAmbiguousSignals(t *testing.T) {
	for _, payload := range []string{
		`{"type":"response.completed","response":{"usage":{"input_tokens":5}}}`,
		`{"type":"codex.rate_limits"}`,
		`{"type":"codex.rate_limits","rate_limits":{"primary":{"used_percent":101,"window_minutes":300}}}`,
		`{"type":"codex.rate_limits","rate_limits":{"primary":{"used_percent":1,"window_minutes":0}}}`,
		`{"type":"codex.rate_limits","rate_limits":{"primary":{"used_percent":1}}}`,
		`{"type":"codex.rate_limits","rate_limits":{"primary":{"used_percent":1,"window_minutes":300}}} {}`,
	} {
		if got := NormalizeWebsocketQuotaWindows([]byte(payload), time.Unix(1000, 0)); len(got) != 0 {
			t.Fatalf("invalid event produced quota: %s => %#v", payload, got)
		}
	}
}

func TestWebsocketQuotaDropsMeteredCopyEvenWhenSlotsDiffer(t *testing.T) {
	windows := NormalizeWebsocketQuotaWindows([]byte(`{
		"type":"codex.rate_limits",
		"rate_limits":{"primary":{"used_percent":5,"window_minutes":10080,"reset_at":1800000000}},
		"additional_rate_limits":{"Spark":{"secondary":{"used_percent":5,"window_minutes":10080,"reset_at":1800000000}}}
	}`), time.Unix(1000, 0))
	if len(windows) != 1 || windows[0].SourceID != "" || windows[0].SourceName != "Spark" {
		t.Fatalf("metered copy reached the account pool: %#v", windows)
	}
}

func TestWebsocketQuotaTreatsMissingOrEmptyAdditionalLimitsAsAccount(t *testing.T) {
	for _, additional := range []string{"", `,"additional_rate_limits":null`, `,"additional_rate_limits":{}`, `,"additional_rate_limits":{"Unknown":null}`} {
		payload := []byte(`{"type":"codex.rate_limits","rate_limits":{"primary":{"used_percent":1,"window_minutes":300,"reset_at":1800000000}}` + additional + `}`)
		windows := NormalizeWebsocketQuotaWindows(payload, time.Unix(1000, 0))
		if len(windows) != 1 || windows[0].SourceID != "codex" {
			t.Fatalf("additional=%q windows=%#v, want one account window", additional, windows)
		}
	}
	windows := NormalizeWebsocketQuotaWindows([]byte(`{
		"type":"codex.rate_limits",
		"rate_limits":{"primary":{"used_percent":1,"window_minutes":300}}
	}`), time.Unix(1000, 0))
	if len(windows) != 1 || windows[0].SourceID != "codex" || windows[0].ResetAtMS != nil {
		t.Fatalf("top-level account window without reset was rejected: %#v", windows)
	}
}

func TestWebsocketQuotaComparesEveryAdditionalWindow(t *testing.T) {
	for _, test := range []struct {
		name         string
		topReset     int64
		accountCount int
	}{
		{"matches second source", 1800000000, 0},
		{"differs from every source", 1800000003, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			windows := NormalizeWebsocketQuotaWindows([]byte(fmt.Sprintf(`{
				"type":"codex.rate_limits",
				"rate_limits":{"primary":{"used_percent":40,"window_minutes":10080,"reset_at":%d}},
				"additional_rate_limits":{
					"FiveHour":{"primary":{"used_percent":10,"window_minutes":300,"reset_at":1700000000}},
					"Weekly":{"secondary":{"used_percent":20,"window_minutes":10080,"reset_at":1800000000}}
				}
			}`, test.topReset)), time.Unix(1000, 0))
			account := 0
			for _, window := range windows {
				if window.SourceID == "codex" {
					account++
				}
			}
			if len(windows) != 2+test.accountCount || account != test.accountCount {
				t.Fatalf("windows=%#v, want account count %d", windows, test.accountCount)
			}
		})
	}
}

func TestWebsocketQuotaSkipsTopLevelWhenSamePeriodAdditionalCannotBeCompared(t *testing.T) {
	for _, payload := range []string{
		`{"type":"codex.rate_limits","rate_limits":{"primary":{"used_percent":40,"window_minutes":10080,"reset_at":1800000000}},"additional_rate_limits":{"Unknown":{"secondary":{"used_percent":20,"window_minutes":10080}}}}`,
		`{"type":"codex.rate_limits","rate_limits":{"primary":{"used_percent":40,"window_minutes":10080}},"additional_rate_limits":{"Unknown":{"secondary":{"used_percent":20,"window_minutes":10080,"reset_at":1800000000}}}}`,
	} {
		windows := NormalizeWebsocketQuotaWindows([]byte(payload), time.Unix(1000, 0))
		if len(windows) != 1 || windows[0].SourceName != "Unknown" || windows[0].SourceID != "" {
			t.Fatalf("ambiguous top-level window was retained: %#v", windows)
		}
	}
}

func TestWebsocketQuotaMalformedAdditionalWindowDoesNotMakeTopLevelAccount(t *testing.T) {
	for _, test := range []struct {
		name   string
		window string
	}{
		{"missing period", `{"used_percent":20,"reset_at":1800000000}`},
		{"missing usage", `{"window_minutes":10080,"reset_at":1800000000}`},
		{"null usage", `{"used_percent":null,"window_minutes":10080,"reset_at":1800000000}`},
		{"out of range usage", `{"used_percent":101,"window_minutes":10080,"reset_at":1800000000}`},
		{"invalid period", `{"used_percent":20,"window_minutes":0,"reset_at":1800000000}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := []byte(fmt.Sprintf(`{
				"type":"codex.rate_limits",
				"rate_limits":{"primary":{"used_percent":5,"window_minutes":10080,"reset_at":1800000000}},
				"additional_rate_limits":{"Spark":{"primary":%s}}
			}`, test.window))
			windows := NormalizeWebsocketQuotaWindows(payload, time.Unix(1000, 0))
			if len(windows) != 0 {
				t.Fatalf("malformed additional window produced an update: %#v", windows)
			}
		})
	}
}

func TestWebsocketQuotaIsolatesMalformedWindowFields(t *testing.T) {
	windows := NormalizeWebsocketQuotaWindows([]byte(`{
		"type":"codex.rate_limits","metered_limit_name":"premium",
		"rate_limits":{
			"primary":{"used_percent":5,"window_minutes":10080,"reset_at":1800000000},
			"secondary":{"used_percent":"bad","window_minutes":300,"reset_at":1700000000}
		},
		"additional_rate_limits":{
			"Bad":{"primary":{"used_percent":"bad","window_minutes":10080,"reset_at":1800000000}},
			"Good":{"allowed":"bad","primary":{"used_percent":20,"window_minutes":300,"reset_at":1700000000}}
		}
	}`), time.Unix(1000, 0))
	if len(windows) != 2 {
		t.Fatalf("windows=%#v, want valid top-level and named windows", windows)
	}
	if windows[0].SourceID != codexAccountQuotaSourceID || windows[0].Used == nil || *windows[0].Used != 5 {
		t.Fatalf("valid top-level window was lost: %#v", windows)
	}
	if windows[1].SourceName != "Good" || windows[1].SourceID != "" || windows[1].Used == nil || *windows[1].Used != 20 {
		t.Fatalf("valid additional window was lost: %#v", windows)
	}
}

func TestWebsocketQuotaDeduplicatesTopLevelPerWindow(t *testing.T) {
	windows := NormalizeWebsocketQuotaWindows([]byte(`{
		"type":"codex.rate_limits",
		"rate_limits":{
			"primary":{"used_percent":40,"window_minutes":300,"reset_at":1700000000},
			"secondary":{"used_percent":50,"window_minutes":10080,"reset_at":1800000000}
		},
		"additional_rate_limits":{"Spark":{
			"primary":{"used_percent":10,"window_minutes":300,"reset_at":1700000000},
			"secondary":{"used_percent":20,"window_minutes":10080,"reset_at":1800000100}
		}}
	}`), time.Unix(1000, 0))
	account := 0
	for _, window := range windows {
		if window.SourceID == "codex" {
			account++
			if window.WindowSeconds == nil || *window.WindowSeconds != 604800 {
				t.Fatalf("wrong top-level window retained: %#v", window)
			}
		}
	}
	if len(windows) != 3 || account != 1 {
		t.Fatalf("windows=%#v, want one account window and two named windows", windows)
	}
}

func TestWebsocketQuotaKeepsExplicitDifferentSourcesWithEqualValues(t *testing.T) {
	windows := NormalizeWebsocketQuotaWindows([]byte(`{
		"type":"codex.rate_limits","metered_limit_name":"premium",
		"rate_limits":{"primary":{"used_percent":0,"window_minutes":10080,"reset_at":1800000000}},
		"additional_rate_limits":{"Spark":{"secondary":{"used_percent":0,"window_minutes":10080,"reset_at":1800000000}}}
	}`), time.Unix(1000, 0))
	if len(windows) != 2 || windows[0].SourceID != "codex" || windows[1].SourceName != "Spark" {
		t.Fatalf("equal values erased an explicitly identified source: %#v", windows)
	}
}
