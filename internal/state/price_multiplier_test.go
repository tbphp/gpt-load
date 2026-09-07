package state

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/pricing"
)

func TestCompilePriceMultiplierDefaultsAndSnapshotOwnership(t *testing.T) {
	t.Parallel()
	input := CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []GroupConfig{{
			ID: 1, ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Enabled: true,
		}},
		AccessKeys: []AccessKeyConfig{{ID: 1, KeyHash: "multiplier-key", Status: AccessKeyStatusActive}},
	}
	defaults, err := Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Groups[1].PriceMultiplier != pricing.DefaultPriceMultiplier ||
		defaults.AccessKeysByID[1].PriceMultiplier != pricing.DefaultPriceMultiplier ||
		defaults.AccessKeysByHash["multiplier-key"].PriceMultiplier != pricing.DefaultPriceMultiplier {
		t.Fatal("omitted compile multiplier did not default to one")
	}
	groupMultiplier, keyMultiplier := pricing.PriceMultiplier(0), pricing.PriceMultiplier(1_234_567)
	input.Groups[0].PriceMultiplier = &groupMultiplier
	input.AccessKeys[0].PriceMultiplier = &keyMultiplier
	snapshot, err := Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	groupMultiplier, keyMultiplier = pricing.PriceMultiplier(1_000_000_000), pricing.PriceMultiplier(0)
	if snapshot.Groups[1].PriceMultiplier != 0 || snapshot.AccessKeysByID[1].PriceMultiplier != 1_234_567 ||
		snapshot.AccessKeysByHash["multiplier-key"].PriceMultiplier != 1_234_567 {
		t.Fatal("compiled multipliers retained mutable input pointers")
	}
	if defaults.Groups[1].PriceMultiplier != pricing.DefaultPriceMultiplier ||
		defaults.AccessKeysByID[1].PriceMultiplier != pricing.DefaultPriceMultiplier {
		t.Fatal("compilation changed a previous snapshot")
	}
	for _, invalid := range []pricing.PriceMultiplier{-1, 1_000_000_001} {
		input.Groups[0].PriceMultiplier = &invalid
		input.AccessKeys[0].PriceMultiplier = nil
		if _, err := Compile(input); err == nil {
			t.Fatalf("accepted invalid group multiplier %d", invalid)
		}
		input.Groups[0].PriceMultiplier = nil
		input.AccessKeys[0].PriceMultiplier = &invalid
		if _, err := Compile(input); err == nil {
			t.Fatalf("accepted invalid access key multiplier %d", invalid)
		}
	}
}
