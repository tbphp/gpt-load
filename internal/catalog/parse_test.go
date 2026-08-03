package catalog

import (
	"strings"
	"testing"

	"gpt-load/internal/pricing"
)

func TestParseModelsDevUsesExactDecimalsAndCanonicalTiers(t *testing.T) {
	raw := `{
		"openai": {
			"id": "openai",
			"name": "OpenAI",
			"api": "https://api.openai.com/v1/",
			"npm": "@ai-sdk/openai",
			"models": {
				"gpt-x": {
					"id": "gpt-x",
					"name": "GPT X",
					"cost": {
						"input": 0.000000001,
						"output": 8,
						"cache_read": 0.5,
						"cache_write": 1.25,
						"reasoning": 99,
						"context_over_200k": {"input": 999},
						"tiers": [
							{"tier":{"type":"context","size":200000},"input":4},
							{"tier":{"type":"context","size":400000},"output":12,"cache_read":0}
						]
					},
					"context_over_200k": {"input": 777}
				},
				"free-model": {
					"id": "free-model",
					"name": "Free Model",
					"cost": {"input": 0}
				},
				"exponent-model": {
					"id": "exponent-model",
					"name": "Exponent Model",
					"cost": {"input": 1e-9, "output": 1e3}
				},
				"unknown-cost": {
					"id": "unknown-cost",
					"name": "Unknown Cost",
					"cost": {"reasoning": 2, "audio": 3, "context_over_200k": {"input": 4}}
				}
			}
		}
	}`

	snapshot := mustParse(t, raw)
	provider := snapshot.Providers["openai"]
	if provider.ID != "openai" || provider.Name != "OpenAI" ||
		provider.APIURL != "https://api.openai.com/v1/" || provider.NPM != "@ai-sdk/openai" {
		t.Fatalf("provider = %#v", provider)
	}

	cost := provider.Models["gpt-x"].Cost
	if cost == nil {
		t.Fatal("gpt-x cost = nil")
	}
	assertPrice(t, "input", cost.Prices.Input, 1, true)
	assertPrice(t, "output", cost.Prices.Output, 8_000_000_000, true)
	assertPrice(t, "cache read", cost.Prices.CacheRead, 500_000_000, true)
	assertPrice(t, "cache write", cost.Prices.CacheWrite, 1_250_000_000, true)
	if len(cost.ContextTiers) != 2 {
		t.Fatalf("context tiers = %#v, want two canonical tiers", cost.ContextTiers)
	}
	if cost.ContextTiers[0].InputThresholdTokens != 200_000 {
		t.Fatalf("first threshold = %d, want 200000", cost.ContextTiers[0].InputThresholdTokens)
	}
	assertPrice(t, "first tier input", cost.ContextTiers[0].Prices.Input, 4_000_000_000, true)
	assertPrice(t, "first tier missing output", cost.ContextTiers[0].Prices.Output, 0, false)
	if cost.ContextTiers[1].InputThresholdTokens != 400_000 {
		t.Fatalf("second threshold = %d, want 400000", cost.ContextTiers[1].InputThresholdTokens)
	}
	assertPrice(t, "second tier output", cost.ContextTiers[1].Prices.Output, 12_000_000_000, true)
	assertPrice(t, "second tier explicit free cache read", cost.ContextTiers[1].Prices.CacheRead, 0, true)
	assertPrice(t, "second tier missing input", cost.ContextTiers[1].Prices.Input, 0, false)

	free := provider.Models["free-model"].Cost
	if free == nil {
		t.Fatal("explicit zero cost normalized to nil")
	}
	assertPrice(t, "explicit zero", free.Prices.Input, 0, true)
	exponent := provider.Models["exponent-model"].Cost
	if exponent == nil {
		t.Fatal("exact exponent cost normalized to nil")
	}
	assertPrice(t, "negative-nine exponent", exponent.Prices.Input, 1, true)
	assertPrice(t, "positive exponent", exponent.Prices.Output, 1_000_000_000_000, true)
	if got := provider.Models["unknown-cost"].Cost; got != nil {
		t.Fatalf("unsupported-only cost = %#v, want nil", got)
	}
}

func TestParseModelsDevRejectsInvalidIdentityPricesAndTiers(t *testing.T) {
	validProvider := func(model string) string {
		return `{"openai":{"id":"openai","name":"OpenAI","models":{"gpt-x":` + model + `}}}`
	}
	validModel := `{"id":"gpt-x","name":"GPT X"}`

	tests := []struct {
		name string
		raw  string
	}{
		{name: "duplicate provider key", raw: `{"openai":{"id":"openai","name":"One","models":{}},"openai":{"id":"openai","name":"Two","models":{}}}`},
		{name: "duplicate provider field", raw: `{"openai":{"id":"wrong","id":"openai","name":"OpenAI","models":{}}}`},
		{name: "provider id case alias", raw: `{"openai":{"id":"wrong","ID":"openai","name":"OpenAI","models":{}}}`},
		{name: "duplicate model key", raw: `{"openai":{"id":"openai","name":"OpenAI","models":{"gpt-x":{"id":"gpt-x","name":"One"},"gpt-x":{"id":"gpt-x","name":"Two"}}}}`},
		{name: "duplicate model field", raw: validProvider(`{"id":"wrong","id":"gpt-x","name":"GPT X"}`)},
		{name: "model id case alias", raw: validProvider(`{"id":"wrong","ID":"gpt-x","name":"GPT X"}`)},
		{name: "duplicate cost field", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"input":1,"input":2}}`)},
		{name: "cost input case alias", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"input":1,"Input":2}}`)},
		{name: "cost tiers case alias", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"tiers":[],"Tiers":[{"tier":{"type":"context","size":1},"input":1}]}}`)},
		{name: "tier type case alias", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"tiers":[{"tier":{"type":"other","Type":"context","size":1},"input":1}]}}`)},
		{name: "tier size case alias", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"tiers":[{"tier":{"type":"context","size":2,"Size":1},"input":1}]}}`)},
		{name: "tier input case alias", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"tiers":[{"tier":{"type":"context","size":1},"input":1,"Input":2}]}}`)},
		{name: "provider key id mismatch", raw: `{"openai":{"id":"anthropic","name":"OpenAI","models":{}}}`},
		{name: "invalid provider slug", raw: `{"OpenAI":{"id":"OpenAI","name":"OpenAI","models":{}}}`},
		{name: "empty provider name", raw: `{"openai":{"id":"openai","name":" ","models":{}}}`},
		{name: "model key id mismatch", raw: validProvider(`{"id":"gpt-y","name":"GPT X"}`)},
		{name: "empty model id", raw: `{"openai":{"id":"openai","name":"OpenAI","models":{"":{"id":"","name":"GPT X"}}}}`},
		{name: "model id surrounding whitespace", raw: `{"openai":{"id":"openai","name":"OpenAI","models":{" gpt-x":{"id":" gpt-x","name":"GPT X"}}}}`},
		{name: "model id control", raw: `{"openai":{"id":"openai","name":"OpenAI","models":{"gpt\nx":{"id":"gpt\nx","name":"GPT X"}}}}`},
		{name: "model id too long", raw: `{"openai":{"id":"openai","name":"OpenAI","models":{"` + strings.Repeat("m", 256) + `":{"id":"` + strings.Repeat("m", 256) + `","name":"GPT X"}}}}`},
		{name: "empty model name", raw: validProvider(`{"id":"gpt-x","name":""}`)},
		{name: "negative price", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"input":-1}}`)},
		{name: "string price", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"input":"1.25"}}`)},
		{name: "exponent precision overflow price", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"input":1e-10}}`)},
		{name: "exponent range overflow price", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"input":1e100}}`)},
		{name: "precision overflow price", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"input":0.0000000001}}`)},
		{name: "range overflow price", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"input":92233720368.54775808}}`)},
		{name: "malformed price", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"input":01}}`)},
		{name: "unsupported tier type", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"tiers":[{"tier":{"type":"tokens","size":1},"input":1}]}}`)},
		{name: "missing tier type", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"tiers":[{"tier":{"size":1},"input":1}]}}`)},
		{name: "missing tier size", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"tiers":[{"tier":{"type":"context"},"input":1}]}}`)},
		{name: "negative tier size", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"tiers":[{"tier":{"type":"context","size":-1},"input":1}]}}`)},
		{name: "fractional tier size", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"tiers":[{"tier":{"type":"context","size":1.5},"input":1}]}}`)},
		{name: "string tier size", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"tiers":[{"tier":{"type":"context","size":"200000"},"input":1}]}}`)},
		{name: "tier size overflow", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"tiers":[{"tier":{"type":"context","size":9223372036854775808},"input":1}]}}`)},
		{name: "duplicate tier threshold", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"tiers":[{"tier":{"type":"context","size":1},"input":1},{"tier":{"type":"context","size":1},"output":2}]}}`)},
		{name: "decreasing tier threshold", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"tiers":[{"tier":{"type":"context","size":2},"input":1},{"tier":{"type":"context","size":1},"output":2}]}}`)},
		{name: "tier without supported price", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"tiers":[{"tier":{"type":"context","size":1},"reasoning":2}]}}`)},
		{name: "negative tier price", raw: validProvider(`{"id":"gpt-x","name":"GPT X","cost":{"tiers":[{"tier":{"type":"context","size":1},"cache_write":-1}]}}`)},
		{name: "trailing JSON", raw: validProvider(validModel) + `{}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Parse(strings.NewReader(test.raw)); err == nil {
				t.Fatalf("Parse() accepted invalid input: %s", test.raw)
			}
		})
	}
}

func TestParseReturnsFreshOwnedMapsAndSlices(t *testing.T) {
	raw := `{"openai":{"id":"openai","name":"OpenAI","models":{"gpt-x":{"id":"gpt-x","name":"GPT X","cost":{"tiers":[{"tier":{"type":"context","size":1},"input":1}]}}}}}`
	first := mustParse(t, raw)
	second := mustParse(t, raw)

	firstProvider := first.Providers["openai"]
	firstProvider.Name = "mutated"
	firstModel := firstProvider.Models["gpt-x"]
	firstModel.Cost.ContextTiers[0].Prices.Input.NanoUSDPerMillion = 99
	firstProvider.Models["gpt-x"] = firstModel
	first.Providers["openai"] = firstProvider

	secondProvider := second.Providers["openai"]
	if secondProvider.Name != "OpenAI" || secondProvider.Models["gpt-x"].Cost.ContextTiers[0].Prices.Input.NanoUSDPerMillion != 1_000_000_000 {
		t.Fatalf("second parse shared mutable storage: %#v", secondProvider)
	}
}

func mustParse(t *testing.T, raw string) *Snapshot {
	t.Helper()
	snapshot, err := Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return snapshot
}

func assertPrice(t *testing.T, name string, got pricing.Price, value pricing.NanoUSD, set bool) {
	t.Helper()
	if got.NanoUSDPerMillion != value || got.Set != set {
		t.Fatalf("%s = %#v, want value=%d set=%t", name, got, value, set)
	}
}
