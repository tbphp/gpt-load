package usage

import (
	"math"
	"testing"
)

func TestUsageSubtractCached(t *testing.T) {
	tests := []struct {
		name              string
		inclusive, cached int64
		want              int64
		wantOK            bool
	}{
		{name: "normal", inclusive: 100, cached: 20, want: 80, wantOK: true},
		{name: "equal", inclusive: 20, cached: 20, want: 0, wantOK: true},
		{name: "cached exceeds prompt", inclusive: 20, cached: 21, want: 0},
		{name: "negative inclusive", inclusive: -1, cached: 0, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := SubtractCached(tt.inclusive, tt.cached)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("SubtractCached(%d, %d) = %d, %t, want %d, %t", tt.inclusive, tt.cached, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestUsageCheckedAddAndTotalRejectOverflow(t *testing.T) {
	if got, ok := CheckedAdd(math.MaxInt64, 1); ok || got != 0 {
		t.Fatalf("CheckedAdd overflow = %d, %t", got, ok)
	}
	tokens := Tokens{UncachedInput: math.MaxInt64, Output: 1}
	if got, ok := CheckedTotal(tokens); ok || got != 0 {
		t.Fatalf("CheckedTotal overflow = %d, %t", got, ok)
	}
}

func TestUsageDiagnosticsAreFixedAndMergeLatestDelta(t *testing.T) {
	var first Diagnostics
	first.Add(DiagnosticNegativeValue)
	first.SetTotalDelta(3)
	var second Diagnostics
	second.Add(DiagnosticInvalidNumber)
	second.SetTotalDelta(-2)
	first.Merge(second)

	if !first.Has(DiagnosticNegativeValue) ||
		!first.Has(DiagnosticInvalidNumber) ||
		!first.Has(DiagnosticInconsistentTotal) {
		t.Fatalf("merged diagnostics = %#v", first)
	}
	if delta, ok := first.TotalDelta(); !ok || delta != -2 {
		t.Fatalf("TotalDelta() = %d, %t, want -2, true", delta, ok)
	}
	first.Add(DiagnosticCode("unknown"))
	if first.Has(DiagnosticCode("unknown")) {
		t.Fatalf("unknown diagnostic code was retained: %#v", first)
	}
}
