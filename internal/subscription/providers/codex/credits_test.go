package codex

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNormalizeQuotaCreditSummary(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload string
		balance string
		present bool
	}{
		{name: "balance", payload: `{"credits":{"balance":"62500.25","has_credits":true,"unlimited":false}}`, balance: "62500.25", present: true},
		{name: "zero", payload: `{"credits":{"balance":"0","has_credits":false,"unlimited":false}}`, balance: "0", present: true},
		{name: "numeric balance", payload: `{"credits":{"balance":12.5}}`, balance: "12.5", present: true},
		{name: "unlimited", payload: `{"credits":{"unlimited":true}}`, present: true},
		{name: "legacy", payload: `{"plan_type":"pro"}`},
		{name: "null", payload: `{"credits":null}`},
		{name: "invalid balance", payload: `{"credits":{"balance":"NaN"}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := NormalizeQuota([]byte(test.payload), nil)
			if err != nil {
				t.Fatal(err)
			}
			var snapshot map[string]json.RawMessage
			if err := json.Unmarshal(raw, &snapshot); err != nil {
				t.Fatal(err)
			}
			credits, present := snapshot["credits"]
			if present != test.present {
				t.Fatalf("credit presence = %v, want %v: %s", present, test.present, raw)
			}
			if !present {
				return
			}
			var detail struct {
				Balance string `json:"balance"`
			}
			if err := json.Unmarshal(credits, &detail); err != nil {
				t.Fatal(err)
			}
			if detail.Balance != test.balance {
				t.Fatalf("balance = %q, want %q", detail.Balance, test.balance)
			}
		})
	}
}

func TestPassiveCreditSourcesPreserveZeroAndOptionalFields(t *testing.T) {
	at := time.UnixMilli(2000)
	fromHTTP := NormalizePassiveCredits(map[string]string{
		"X-Codex-Credits-Balance": "0", "x-codex-credits-has-credits": "False", "X-Codex-Credits-Unlimited": "False",
	}, at)
	fromWS := NormalizeWebsocketCredits([]byte(`{"type":"codex.rate_limits","credits":{"balance":"0","has_credits":false,"unlimited":false}}`), at)
	for _, credits := range []*quotaSnapshot{
		{Credits: fromHTTP}, {Credits: fromWS},
	} {
		value := credits.Credits
		if value == nil || value.Balance != "0" || value.HasCredits == nil || *value.HasCredits || value.Unlimited == nil || *value.Unlimited || *value.ObservedAtMS != at.UnixMilli() {
			t.Fatalf("passive zero credit summary = %#v", value)
		}
	}
	if NormalizePassiveCredits(nil, at) != nil ||
		NormalizePassiveCredits(map[string]string{"X-Codex-Credits-Balance": "Infinity"}, at) != nil ||
		NormalizeWebsocketCredits([]byte(`{"type":"response.created","credits":{"balance":"9"}}`), at) != nil ||
		NormalizeWebsocketCredits([]byte(`{"type":"codex.rate_limits","credits":null}`), at) != nil {
		t.Fatal("missing or invalid credit data produced a summary")
	}
}
