package pricing

import (
	"math"
	"testing"
)

func TestParseUSDConvertsDecimalUSDPerMillionToNanoUSDExactly(t *testing.T) {
	tests := []struct {
		value string
		want  NanoUSD
	}{
		{value: "0", want: 0},
		{value: "0.000000001", want: 1},
		{value: "1.25", want: 1_250_000_000},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got, err := ParseUSD(tt.value)
			if err != nil {
				t.Fatalf("ParseUSD(%q) error = %v", tt.value, err)
			}
			if got != tt.want {
				t.Fatalf("ParseUSD(%q) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}

func TestParseUSDRejectsInvalidOrUnrepresentablePrices(t *testing.T) {
	for _, value := range []string{"-1", "1e3", "0.0000000001", "", " ", "92233720368.54775808"} {
		t.Run(value, func(t *testing.T) {
			if _, err := ParseUSD(value); err == nil {
				t.Fatalf("ParseUSD(%q) accepted an invalid price", value)
			}
		})
	}
}

func TestFormatUSDUsesPlainDecimalWithoutInsignificantZeros(t *testing.T) {
	tests := []struct {
		value NanoUSD
		want  string
	}{
		{value: 0, want: "0"},
		{value: 1, want: "0.000000001"},
		{value: 1_250_000_000, want: "1.25"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := FormatUSD(tt.value); got != tt.want {
				t.Fatalf("FormatUSD(%d) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestQuoteComponentUsesIntegerHalfUpRounding(t *testing.T) {
	tests := []struct {
		name       string
		tokens     int64
		price      NanoUSD
		multiplier Multiplier
		want       NanoUSD
	}{
		{name: "half rounds up", tokens: 500_000, price: 1, multiplier: Multiplier{Numerator: 1, Denominator: 1}, want: 1},
		{name: "exact two times multiplier", tokens: 1_000_000, price: 7, multiplier: Multiplier{Numerator: 2, Denominator: 1}, want: 14},
		{name: "exact three halves multiplier", tokens: 1_000_000, price: 7, multiplier: Multiplier{Numerator: 3, Denominator: 2}, want: 11},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := QuoteComponent(tt.tokens, tt.price, tt.multiplier)
			if !ok || got != tt.want {
				t.Fatalf("QuoteComponent(%d, %d, %+v) = %d, %t, want %d, true", tt.tokens, tt.price, tt.multiplier, got, ok, tt.want)
			}
		})
	}
}

func TestQuoteComponentRejectsFinalOverflowAndInvalidInputs(t *testing.T) {
	if _, ok := QuoteComponent(math.MaxInt64, NanoUSD(math.MaxInt64), Multiplier{Numerator: 1_000_000, Denominator: 1}); ok {
		t.Fatal("QuoteComponent() accepted a result above int64")
	}
	for _, test := range []struct {
		name       string
		tokens     int64
		price      NanoUSD
		multiplier Multiplier
	}{
		{name: "negative tokens", tokens: -1, price: 1, multiplier: Multiplier{Numerator: 1, Denominator: 1}},
		{name: "negative price", tokens: 1, price: -1, multiplier: Multiplier{Numerator: 1, Denominator: 1}},
		{name: "zero numerator", tokens: 1, price: 1, multiplier: Multiplier{Numerator: 0, Denominator: 1}},
		{name: "zero denominator", tokens: 1, price: 1, multiplier: Multiplier{Numerator: 1, Denominator: 0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, ok := QuoteComponent(test.tokens, test.price, test.multiplier); ok {
				t.Fatal("QuoteComponent() accepted invalid input")
			}
		})
	}
}

func TestCheckedAddNanoUSDRejectsNegativeAndOverflowAmounts(t *testing.T) {
	tests := []struct {
		name        string
		left, right NanoUSD
		want        NanoUSD
		wantOK      bool
	}{
		{name: "adds valid amounts", left: 7, right: 5, want: 12, wantOK: true},
		{name: "negative left", left: -1, right: 5},
		{name: "negative right", left: 7, right: -1},
		{name: "overflow", left: NanoUSD(math.MaxInt64), right: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := CheckedAddNanoUSD(tt.left, tt.right)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("CheckedAddNanoUSD(%d, %d) = %d, %t, want %d, %t", tt.left, tt.right, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
