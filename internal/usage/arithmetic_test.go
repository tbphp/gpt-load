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

func TestUsageCheckedAdd(t *testing.T) {
	tests := []struct {
		name        string
		left, right int64
		want        int64
		wantOK      bool
	}{
		{name: "normal", left: 7, right: 5, want: 12, wantOK: true},
		{name: "left zero", left: 0, right: 5, want: 5, wantOK: true},
		{name: "right zero", left: 7, right: 0, want: 7, wantOK: true},
		{name: "negative left", left: -1, right: 5},
		{name: "negative right", left: 7, right: -1},
		{name: "overflow", left: math.MaxInt64, right: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := CheckedAdd(tt.left, tt.right)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("CheckedAdd(%d, %d) = %d, %t, want %d, %t", tt.left, tt.right, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestUsageCheckedTotal(t *testing.T) {
	tests := []struct {
		name   string
		tokens Tokens
		want   int64
		wantOK bool
	}{
		{name: "six buckets", tokens: Tokens{UncachedInput: 1, CacheRead: 2, CacheWrite5M: 3, CacheWrite1H: 4, CacheWriteUnknown: 5, Output: 6}, want: 21, wantOK: true},
		{name: "negative uncached input", tokens: Tokens{UncachedInput: -1}},
		{name: "negative cache read", tokens: Tokens{CacheRead: -1}},
		{name: "negative cache write 5m", tokens: Tokens{CacheWrite5M: -1}},
		{name: "negative cache write 1h", tokens: Tokens{CacheWrite1H: -1}},
		{name: "negative cache write unknown", tokens: Tokens{CacheWriteUnknown: -1}},
		{name: "negative output", tokens: Tokens{Output: -1}},
		{name: "overflow", tokens: Tokens{UncachedInput: math.MaxInt64, Output: 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := CheckedTotal(tt.tokens)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("CheckedTotal(%#v) = %d, %t, want %d, %t", tt.tokens, got, ok, tt.want, tt.wantOK)
			}
		})
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

func TestUsageDiagnosticsAddAndHasEverySupportedCode(t *testing.T) {
	tests := []struct {
		name string
		code DiagnosticCode
	}{
		{name: "unsupported billable detail", code: DiagnosticUnsupportedBillableDetail},
		{name: "cache write defaulted 5m", code: DiagnosticCacheWriteDefaulted5M},
		{name: "negative value", code: DiagnosticNegativeValue},
		{name: "invalid number", code: DiagnosticInvalidNumber},
		{name: "missing required field", code: DiagnosticMissingRequiredField},
		{name: "inconsistent total", code: DiagnosticInconsistentTotal},
		{name: "invalid payload", code: DiagnosticInvalidPayload},
		{name: "invalid event sequence", code: DiagnosticInvalidEventSequence},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var diagnostics Diagnostics
			diagnostics.Add(tt.code)
			if !diagnostics.Has(tt.code) {
				t.Fatalf("Add(%q) was not observable through Has", tt.code)
			}
		})
	}
}

func TestUsageDiagnosticsSetZeroTotalDeltaIsPresent(t *testing.T) {
	var diagnostics Diagnostics
	diagnostics.SetTotalDelta(0)
	if delta, ok := diagnostics.TotalDelta(); !ok || delta != 0 {
		t.Fatalf("TotalDelta() = %d, %t, want 0, true", delta, ok)
	}
}
