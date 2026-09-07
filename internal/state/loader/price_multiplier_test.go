package loader_test

import (
	"fmt"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/pricing"
	"gpt-load/internal/state"
	"gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

func TestBuildCompileInputLoadsPersistedPriceMultipliers(t *testing.T) {
	for _, test := range []struct {
		name   string
		stored *int64
		want   pricing.PriceMultiplier
	}{
		{name: "default", want: pricing.DefaultPriceMultiplier},
		{name: "zero", stored: multiplierMicros(0), want: 0},
		{name: "fraction", stored: multiplierMicros(123_456), want: 123_456},
		{name: "maximum", stored: multiplierMicros(1_000_000_000), want: 1_000_000_000},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openMigratedDatabase(t)
			group := models.Group{
				Name: "multiplier", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`),
				Models: models.JSON(`[]`), PriceMultiplierMicros: test.stored,
			}
			key := models.AccessKey{
				Name: "multiplier", KeyValue: "cipher", KeyHash: "multiplier-hash", KeySuffix: "1234",
				Status: string(state.AccessKeyStatusActive), PriceMultiplierMicros: test.stored,
			}
			mustCreate(t, db, &group)
			mustCreate(t, db, &key)
			input, err := loader.BuildCompileInput(t.Context(), db)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := state.Compile(input)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Groups[group.ID].PriceMultiplier != test.want ||
				snapshot.AccessKeysByID[key.ID].PriceMultiplier != test.want ||
				snapshot.AccessKeysByHash[key.KeyHash].PriceMultiplier != test.want {
				t.Fatalf("loaded multipliers do not equal %d", test.want)
			}
		})
	}
}

func TestBuildCompileInputRejectsInvalidPersistedPriceMultipliers(t *testing.T) {
	db := openMigratedDatabase(t)
	group := models.Group{
		Name: "invalid multiplier", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`), Models: models.JSON(`[]`),
	}
	key := models.AccessKey{
		Name: "invalid multiplier", KeyValue: "cipher", KeyHash: "invalid-multiplier", KeySuffix: "1234",
	}
	mustCreate(t, db, &group)
	mustCreate(t, db, &key)
	if err := db.Exec("PRAGMA ignore_check_constraints = ON").Error; err != nil {
		t.Fatal(err)
	}
	for _, resource := range []struct {
		model any
		name  string
	}{
		{model: &group, name: "group"},
		{model: &key, name: "access key"},
	} {
		for _, invalid := range []int64{-1, 1_000_000_001} {
			t.Run(fmt.Sprintf("%s/%d", resource.name, invalid), func(t *testing.T) {
				if err := db.Model(resource.model).UpdateColumn("price_multiplier_micros", invalid).Error; err != nil {
					t.Fatal(err)
				}
				if _, err := loader.BuildCompileInput(t.Context(), db); err == nil ||
					!strings.Contains(err.Error(), resource.name) || !strings.Contains(err.Error(), "price multiplier") {
					t.Fatalf("invalid persisted multiplier error = %v", err)
				}
			})
		}
		if err := db.Model(resource.model).UpdateColumn("price_multiplier_micros", int64(pricing.DefaultPriceMultiplier)).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func multiplierMicros(value int64) *int64 { return &value }
