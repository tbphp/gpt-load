package pricing

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestScopeKeyBuildersProduceCanonicalKeys(t *testing.T) {
	tests := []struct {
		name    string
		build   func() (string, error)
		want    string
		wantErr bool
	}{
		{name: "provider", build: func() (string, error) { return ProviderScopeKey("openai") }, want: "provider:openai"},
		{name: "provider dotted slug", build: func() (string, error) { return ProviderScopeKey("wafer.ai") }, want: "provider:wafer.ai"},
		{name: "provider hyphenated slug", build: func() (string, error) { return ProviderScopeKey("amazon-bedrock") }, want: "provider:amazon-bedrock"},
		{name: "group", build: func() (string, error) { return GroupScopeKey(42) }, want: "group:42"},
		{name: "zero group", build: func() (string, error) { return GroupScopeKey(0) }, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.build()
			if (err != nil) != test.wantErr || got != test.want {
				t.Fatalf("scope builder = %q, %v, want %q, error=%t", got, err, test.want, test.wantErr)
			}
		})
	}
}

func TestProviderScopeKeyRejectsNonCanonicalProviderIDs(t *testing.T) {
	for _, providerID := range []string{
		"", "OpenAI", "open:ai", " openai", "openai ", "open ai", ".openai", "openai.",
		"open..ai", "open-_ai", "open_ai", "openai\n", strings.Repeat("a", 247),
	} {
		t.Run(providerID, func(t *testing.T) {
			if key, err := ProviderScopeKey(providerID); err == nil {
				t.Fatalf("ProviderScopeKey(%q) = %q, want error", providerID, key)
			}
		})
	}
}

func TestNewTableRequiresExactGlobalModelIdentity(t *testing.T) {
	firstRule := Rule{Identity: Identity{ModelID: "openai/gpt-4.1"}}
	secondRule := Rule{Identity: Identity{ModelID: "gpt-4.1"}}
	table := mustTable(t, firstRule, secondRule)

	for _, identity := range []Identity{firstRule.Identity, secondRule.Identity} {
		if got, ok := table.Lookup(identity); !ok || got.Identity != identity {
			t.Fatalf("Lookup(%#v) = %#v, %t, want exact hit", identity, got, ok)
		}
	}
	for _, identity := range []Identity{
		{ModelID: "openai/gpt-4"},
		{ModelID: "gpt-4.1-preview"},
	} {
		if got, ok := table.Lookup(identity); ok {
			t.Fatalf("Lookup(%#v) = %#v, true, want exact miss", identity, got)
		}
	}
}

func TestNewTableRejectsInvalidIdentityAndDuplicates(t *testing.T) {
	valid := Identity{ModelID: "gpt-4.1"}
	invalid := []Identity{
		{},
		{ModelID: ""},
		{ModelID: " gpt-4.1"},
		{ModelID: "gpt-4.1 "},
		{ModelID: "gpt-4.1\n"},
		{ModelID: strings.Repeat("m", 256)},
	}
	for _, identity := range invalid {
		t.Run(identity.ModelID, func(t *testing.T) {
			if _, err := NewTable([]Rule{{Identity: identity}}); err == nil {
				t.Fatalf("NewTable() accepted invalid identity %#v", identity)
			}
		})
	}

	if _, err := NewTable([]Rule{{Identity: valid}, {Identity: valid, IsManual: true}}); err == nil {
		t.Fatal("NewTable() accepted duplicate exact identity")
	}
}

func TestNewTableRejectsRepeatedModelID(t *testing.T) {
	_, err := NewTable([]Rule{
		{Identity: Identity{ModelID: "shared-model"}},
		{Identity: Identity{ModelID: "shared-model"}},
	})
	if err == nil {
		t.Fatal("NewTable() accepted two rules for the same upstream model")
	}
}

func TestNewTableValidatesPricesAndContextTiers(t *testing.T) {
	identity := Identity{ModelID: "gpt-4.1"}
	tests := []struct {
		name string
		rule Rule
	}{
		{name: "negative set price", rule: Rule{Identity: identity, Prices: Prices{Input: Price{NanoUSDPerMillion: -1, Set: true}}}},
		{name: "negative hidden price", rule: Rule{Identity: identity, Prices: Prices{Output: Price{NanoUSDPerMillion: -1}}}},
		{name: "negative tier threshold", rule: Rule{Identity: identity, ContextTiers: []ContextTier{{InputThresholdTokens: -1, Prices: Prices{Input: fixedPrice(1)}}}}},
		{name: "tier without price", rule: Rule{Identity: identity, ContextTiers: []ContextTier{{InputThresholdTokens: 0}}}},
		{name: "duplicate tier threshold", rule: Rule{Identity: identity, ContextTiers: []ContextTier{{InputThresholdTokens: 1, Prices: Prices{Input: fixedPrice(1)}}, {InputThresholdTokens: 1, Prices: Prices{Output: fixedPrice(1)}}}}},
		{name: "decreasing tier threshold", rule: Rule{Identity: identity, ContextTiers: []ContextTier{{InputThresholdTokens: 2, Prices: Prices{Input: fixedPrice(1)}}, {InputThresholdTokens: 1, Prices: Prices{Output: fixedPrice(1)}}}}},
		{name: "negative hidden tier price", rule: Rule{Identity: identity, ContextTiers: []ContextTier{{InputThresholdTokens: 0, Prices: Prices{CacheWrite: Price{NanoUSDPerMillion: -1}}}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewTable([]Rule{test.rule}); err == nil {
				t.Fatal("NewTable() accepted invalid rule")
			}
		})
	}

	for _, rule := range []Rule{
		{Identity: identity},
		{Identity: identity, Prices: Prices{Input: fixedPrice(0)}},
		{Identity: identity, ContextTiers: []ContextTier{{InputThresholdTokens: 0, Prices: Prices{Input: fixedPrice(0)}}}},
	} {
		if _, err := NewTable([]Rule{rule}); err != nil {
			t.Fatalf("NewTable() rejected valid rule %#v: %v", rule, err)
		}
	}
}

func TestTableDeepClonesConstructionAndLookup(t *testing.T) {
	rules := []Rule{{
		Identity: Identity{ModelID: "claude:3_5/model"},
		Prices:   Prices{Input: fixedPrice(1)},
		ContextTiers: []ContextTier{{
			InputThresholdTokens: 100,
			Prices:               Prices{Output: fixedPrice(2)},
		}},
		IsManual: true,
	}}
	want := rules[0]
	want.ContextTiers = append([]ContextTier(nil), rules[0].ContextTiers...)
	table := mustTable(t, rules...)

	rules[0].Prices.Input = fixedPrice(99)
	rules[0].ContextTiers[0].InputThresholdTokens = math.MaxInt64
	rules[0].ContextTiers[0].Prices.Output = fixedPrice(99)

	first, ok := table.Lookup(want.Identity)
	if !ok || !reflect.DeepEqual(first, want) {
		t.Fatalf("first Lookup() = %#v, %t, want %#v", first, ok, want)
	}
	first.ContextTiers[0].Prices.Output = fixedPrice(77)
	second, ok := table.Lookup(want.Identity)
	if !ok || !reflect.DeepEqual(second, want) {
		t.Fatalf("second Lookup() = %#v, %t, want immutable %#v", second, ok, want)
	}
}

func mustTable(t *testing.T, rules ...Rule) *Table {
	t.Helper()
	table, err := NewTable(rules)
	if err != nil {
		t.Fatalf("NewTable() error = %v", err)
	}
	return table
}

func fixedPrice(value NanoUSD) Price {
	return Price{NanoUSDPerMillion: value, Set: true}
}
