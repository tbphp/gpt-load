package codex

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestWebsocketQuotaDoesNotGuessAccountSourceWhenMeteredValuesDiffer(t *testing.T) {
	payload, err := os.ReadFile("testdata/quota-ws-spark.json")
	if err != nil {
		t.Fatal(err)
	}
	// 同一事件的顶层与具名额度存在数值差异，也不能把无来源顶层归属普通账号。
	var event map[string]any
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	weekly := event["rate_limits"].(map[string]any)["secondary"].(map[string]any)
	weekly["reset_at"] = weekly["reset_at"].(float64) + 1
	payload, err = json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	windows := NormalizeWebsocketQuotaWindows(payload, time.Unix(1000, 0))
	if len(windows) != 2 {
		t.Fatalf("windows = %#v, want both Spark windows", windows)
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
		{"quota-ws-account.json", 0},
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
					if *window.Used != 6 || *window.Remaining != 94 || *window.WindowSeconds != 604800 {
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
		`{"type":"codex.rate_limits","rate_limits":{"primary":{"used_percent":1,"window_minutes":300}},"additional_rate_limits":{"Unknown":null}}`,
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
