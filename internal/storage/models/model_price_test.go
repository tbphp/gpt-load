package models

import (
	"encoding/json"
	"testing"
)

func TestNormalizeModePriceSchedulesCanonicalizesCompleteSchedules(t *testing.T) {
	raw := JSON(`{"fast":{"prices":{"input_price_nano_usd_per_million_tokens":7,"output_price_nano_usd_per_million_tokens":null,"cache_read_price_nano_usd_per_million_tokens":null,"cache_write_price_nano_usd_per_million_tokens":null},"context_tiers":[]}}`)
	normalized, err := NormalizeModePriceSchedules(raw)
	if err != nil {
		t.Fatal(err)
	}
	var schedules map[string]ModePriceSchedule
	if err := json.Unmarshal(normalized, &schedules); err != nil {
		t.Fatal(err)
	}
	fast := schedules["fast"]
	if fast.Prices.InputPriceNanoUSDPerMillionTokens == nil ||
		*fast.Prices.InputPriceNanoUSDPerMillionTokens != 7 ||
		len(fast.ContextPriceTiers) != 0 {
		t.Fatalf("normalized schedules = %#v", schedules)
	}

	empty, err := NormalizeModePriceSchedules(JSON(`{}`))
	if err != nil || empty != nil {
		t.Fatalf("empty schedules = %q, %v", empty, err)
	}
}

func TestNormalizeModePriceSchedulesRejectsInvalidContracts(t *testing.T) {
	for _, raw := range []JSON{
		JSON(`[]`),
		JSON(`{"standard":{"prices":{"input_price_nano_usd_per_million_tokens":1}}}`),
		JSON(`{"Fast Mode":{"prices":{"input_price_nano_usd_per_million_tokens":1}}}`),
		JSON(`{"fast":{"prices":null,"context_tiers":[]}}`),
		JSON(`{"fast":{"prices":{"input_price_nano_usd_per_million_tokens":null,"output_price_nano_usd_per_million_tokens":null,"cache_read_price_nano_usd_per_million_tokens":null,"cache_write_price_nano_usd_per_million_tokens":null},"context_tiers":[]}}`),
		JSON(`{"fast":{"prices":{"input_price_nano_usd_per_million_tokens":-1},"context_tiers":[]}}`),
		JSON(`{"fast":{"prices":{"input_price_nano_usd_per_million_tokens":1},"context_tiers":[{"threshold_tokens":1}]}}`),
		JSON(`{"fast":{"prices":{"input_price_nano_usd_per_million_tokens":1},"context_tiers":[{"threshold_tokens":1,"input_price_nano_usd_per_million_tokens":2}]}}`),
	} {
		if _, err := NormalizeModePriceSchedules(raw); err == nil {
			t.Fatalf("NormalizeModePriceSchedules(%s) error = nil", raw)
		}
	}
}
