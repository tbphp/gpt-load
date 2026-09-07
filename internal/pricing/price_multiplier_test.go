package pricing

import (
	"encoding/json"
	"testing"
)

func TestPriceMultiplierParsesExactDecimalsAndFormatsCanonically(t *testing.T) {
	for _, test := range []struct {
		input     string
		want      PriceMultiplier
		canonical string
	}{
		{"0", 0, "0"},
		{"0.000000", 0, "0"},
		{"1", DefaultPriceMultiplier, "1"},
		{"0001.200000", 1_200_000, "1.2"},
		{"0.000001", 1, "0.000001"},
		{"0.123456", 123_456, "0.123456"},
		{"999.999999", 999_999_999, "999.999999"},
		{"1000.000000", 1_000_000_000, "1000"},
	} {
		t.Run(test.input, func(t *testing.T) {
			got, err := ParsePriceMultiplier(test.input)
			if err != nil || got != test.want || !got.Valid() {
				t.Fatalf("ParsePriceMultiplier(%q) = %d, %v; want %d valid", test.input, got, err, test.want)
			}
			if got := FormatPriceMultiplier(got); got != test.canonical {
				t.Fatalf("FormatPriceMultiplier() = %q, want %q", got, test.canonical)
			}
			encoded, err := json.Marshal(got)
			if err != nil || string(encoded) != `"`+test.canonical+`"` {
				t.Fatalf("Marshal() = %s, %v", encoded, err)
			}
			var decoded PriceMultiplier
			if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != got {
				t.Fatalf("Unmarshal() = %d, %v", decoded, err)
			}
		})
	}
}

func TestPriceMultiplierRejectsInvalidDecimalsAndJSONTypes(t *testing.T) {
	for _, input := range []string{
		"", " ", " 1", "1 ", "1\n", "-1", "-0", "+1", ".5", "1.", "1..1", "1e2",
		"1E2", "NaN", "Infinity", "０", "0.0000001", "1.0000000", "1000.000001", "1001", "9223372036854775807",
	} {
		t.Run(input, func(t *testing.T) {
			if value, err := ParsePriceMultiplier(input); err == nil {
				t.Fatalf("ParsePriceMultiplier(%q) = %d, want error", input, value)
			}
			encoded, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			var decoded PriceMultiplier
			if err := json.Unmarshal(encoded, &decoded); err == nil {
				t.Fatalf("Unmarshal(%s) accepted invalid multiplier", encoded)
			}
		})
	}
	for _, input := range []string{"null", "0", "1.2", "true", "[]", "{}"} {
		var decoded PriceMultiplier
		if err := json.Unmarshal([]byte(input), &decoded); err == nil {
			t.Fatalf("Unmarshal(%s) accepted a non-string", input)
		}
	}
	for _, value := range []PriceMultiplier{-1, 1_000_000_001} {
		if value.Valid() {
			t.Fatalf("Valid(%d) accepted invalid multiplier", value)
		}
		if _, err := json.Marshal(value); err == nil {
			t.Fatalf("Marshal(%d) accepted invalid multiplier", value)
		}
	}
}

func TestPriceMultipliersRequireBothExplicitJSONFactors(t *testing.T) {
	for _, input := range []string{
		`null`, `{}`, `{"group":"0"}`, `{"access_key":"0"}`,
		`{"group":null,"access_key":"1"}`, `{"group":"1","access_key":null}`,
		`{"group":"1","access_key":"1","extra":"1"}`,
	} {
		var multipliers PriceMultipliers
		if err := json.Unmarshal([]byte(input), &multipliers); err == nil {
			t.Fatalf("Unmarshal(%s) accepted incomplete multipliers", input)
		}
	}
	var multipliers PriceMultipliers
	if err := json.Unmarshal([]byte(`{"group":"0","access_key":"1.200000"}`), &multipliers); err != nil {
		t.Fatal(err)
	}
	if multipliers != (PriceMultipliers{Group: 0, AccessKey: 1_200_000}) {
		t.Fatalf("decoded multipliers = %#v", multipliers)
	}
}
