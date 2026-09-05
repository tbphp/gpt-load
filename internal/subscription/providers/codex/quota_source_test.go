package codex

import (
	"encoding/json"
	"testing"
	"time"
)

func TestQuotaSourcesMatchWithoutDependingOnDisplayName(t *testing.T) {
	raw, err := NormalizeQuota([]byte(`{
		"rate_limit":{"primary_window":{"used_percent":80,"limit_window_seconds":604800}},
		"additional_rate_limits":[{
			"metered_feature":" CODEX-FUTURE-TIER ","limit_name":"Display name",
			"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000}}
		}]
	}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var active quotaSnapshot
	if err := json.Unmarshal(raw, &active); err != nil {
		t.Fatal(err)
	}
	if len(active.QuotaWindows) != 2 || active.QuotaWindows[0].SourceID != "codex" ||
		active.QuotaWindows[1].SourceID != "codex_future_tier" {
		t.Fatalf("active sources = %#v", active.QuotaWindows)
	}
	// 名称变化不影响来源；Active-Limit 不应把普通窗口改绑到当前受限来源。
	// 周窗口从主动 primary 变为响应头 secondary。
	passive := NormalizePassiveQuotaWindows(map[string]string{
		"X-Codex-Active-Limit":                       "codex_future_tier",
		"X-Codex-Future-Tier-Limit-Name":             "Renamed display",
		"X-Codex-Secondary-Used-Percent":             "90",
		"X-Codex-Secondary-Window-Minutes":           "10080",
		"X-Codex-Future-Tier-Primary-Used-Percent":   "20",
		"X-Codex-Future-Tier-Primary-Window-Minutes": "300",
	}, time.Now())
	if len(passive) != 2 || passive[0].SourceID != active.QuotaWindows[0].SourceID ||
		passive[1].SourceID != active.QuotaWindows[1].SourceID {
		t.Fatalf("passive sources = %#v", passive)
	}
}

func TestQuotaSourceIsNotInferredFromLimitName(t *testing.T) {
	raw, err := NormalizeQuota([]byte(`{"additional_rate_limits":[{
		"limit_name":"codex_bengalfox",
		"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000}}
	}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var active quotaSnapshot
	if err := json.Unmarshal(raw, &active); err != nil {
		t.Fatal(err)
	}
	if len(active.QuotaWindows) != 1 || active.QuotaWindows[0].SourceID != "" {
		t.Fatalf("missing metered_feature must remain display-only: %#v", active)
	}
}

func TestPassiveQuotaSourceMayContainWindowNames(t *testing.T) {
	windows := NormalizePassiveQuotaWindows(map[string]string{
		"X-Codex-Secondary-Primary-Used-Percent":       "25",
		"X-Codex-Secondary-Primary-Window-Minutes":     "90",
		"X-Codex-Secondary-Limit-Name":                 "Other limit",
		"X-Codex-Primary-Over-Secondary-Limit-Percent": "95",
	}, time.Now())
	if len(windows) != 1 || windows[0].SourceID != "codex_secondary" ||
		windows[0].Used == nil || *windows[0].Used != 25 ||
		windows[0].WindowSeconds == nil || *windows[0].WindowSeconds != 5400 {
		t.Fatalf("window name in source was parsed as a slot: %#v", windows)
	}
}
