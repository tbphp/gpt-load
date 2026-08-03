package usage

import (
	"math"
	"testing"
)

func TestUsageAccumulatorStateTable(t *testing.T) {
	zero := int64(0)
	seven := int64(7)
	tests := []struct {
		name       string
		applicable bool
		patch      Patch
		wantState  State
		want       Tokens
	}{
		{name: "not applicable", patch: Patch{Output: &seven, Final: true, Diagnostics: diagnostic(DiagnosticInvalidNumber)}, wantState: StateNotApplicable},
		{name: "missing", applicable: true, wantState: StateMissing},
		{name: "partial", applicable: true, patch: Patch{Output: &seven}, wantState: StatePartial, want: Tokens{Output: 7}},
		{name: "complete zero", applicable: true, patch: Patch{Output: &zero, Final: true}, wantState: StateComplete},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var accumulator Accumulator
			if err := accumulator.MergePatch(tt.patch); err != nil {
				t.Fatal(err)
			}
			result, ok := accumulator.Finalize(tt.applicable)
			if !ok || result.State != tt.wantState || result.Tokens != tt.want {
				t.Fatalf("Finalize(%t) = %#v, %t, want state %q and tokens %#v", tt.applicable, result, ok, tt.wantState, tt.want)
			}
			if !tt.applicable && result.Diagnostics != (Diagnostics{}) {
				t.Fatalf("Finalize(false) diagnostics = %#v, want zero diagnostics", result.Diagnostics)
			}
		})
	}
}

func TestUsageAccumulatorReplaceSnapshotClearsAbsentFieldsAndDiagnostics(t *testing.T) {
	one, two := int64(1), int64(2)
	var stale Diagnostics
	stale.Add(DiagnosticNegativeValue)
	var accumulator Accumulator
	if err := accumulator.ReplaceSnapshot(Patch{
		UncachedInput: &one, Output: &one, Diagnostics: stale,
	}); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.ReplaceSnapshot(Patch{Output: &two, Final: true}); err != nil {
		t.Fatal(err)
	}
	result, ok := accumulator.Finalize(true)
	if !ok || result.Tokens != (Tokens{Output: 2}) || result.Diagnostics.Has(DiagnosticNegativeValue) {
		t.Fatalf("final result = %#v, %t", result, ok)
	}
}

func TestUsageAccumulatorReplaceSnapshotClearsPresence(t *testing.T) {
	one := int64(1)
	var accumulator Accumulator
	if err := accumulator.ReplaceSnapshot(Patch{Output: &one}); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.ReplaceSnapshot(Patch{Final: true}); err != nil {
		t.Fatal(err)
	}
	result, ok := accumulator.Finalize(true)
	if !ok || result.State != StateMissing || result.Tokens != (Tokens{}) {
		t.Fatalf("replacement without token fields = %#v, %t, want missing with zero tokens", result, ok)
	}
}

func TestUsageAccumulatorReplaceSnapshotExplicitZeroIsPresent(t *testing.T) {
	zero := int64(0)
	var accumulator Accumulator
	if err := accumulator.ReplaceSnapshot(Patch{Output: &zero, Final: true}); err != nil {
		t.Fatal(err)
	}
	result, ok := accumulator.Finalize(true)
	if !ok || result.State != StateComplete || result.Tokens != (Tokens{}) {
		t.Fatalf("explicit zero replacement = %#v, %t, want complete with zero tokens", result, ok)
	}
}

func TestUsageAccumulatorMergePatchPreservesAbsentFieldsAndStickyFinality(t *testing.T) {
	one, two, three, four := int64(1), int64(2), int64(3), int64(4)
	var first Diagnostics
	first.Add(DiagnosticNegativeValue)
	var second Diagnostics
	second.Add(DiagnosticInvalidNumber)
	var accumulator Accumulator
	if err := accumulator.MergePatch(Patch{UncachedInput: &one, Output: &one, Diagnostics: first}); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.MergePatch(Patch{CacheRead: &two, CacheWriteUnknown: &four, Output: &three, Final: true, Diagnostics: second}); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.MergePatch(Patch{}); err != nil {
		t.Fatal(err)
	}
	result, ok := accumulator.Finalize(true)
	if !ok || result.State != StateComplete || result.Tokens != (Tokens{UncachedInput: 1, CacheRead: 2, CacheWriteUnknown: 4, Output: 3}) ||
		!result.Diagnostics.Has(DiagnosticNegativeValue) || !result.Diagnostics.Has(DiagnosticInvalidNumber) {
		t.Fatalf("merged result = %#v, %t", result, ok)
	}
}

func TestUsageAccumulatorRejectsNegativePatchWithoutMutation(t *testing.T) {
	one, negative, nine := int64(1), int64(-1), int64(9)
	var accumulator Accumulator
	if err := accumulator.MergePatch(Patch{Output: &one}); err != nil {
		t.Fatal(err)
	}
	var diagnostics Diagnostics
	diagnostics.Add(DiagnosticInvalidNumber)
	if err := accumulator.MergePatch(Patch{UncachedInput: &negative, Output: &nine, Final: true, Diagnostics: diagnostics}); err != ErrNegativePatch {
		t.Fatalf("MergePatch negative = %v, want %v", err, ErrNegativePatch)
	}
	result, ok := accumulator.Finalize(true)
	if !ok || result.State != StatePartial || result.Tokens != (Tokens{Output: 1}) || result.Diagnostics.Has(DiagnosticInvalidNumber) {
		t.Fatalf("state after rejected patch = %#v, %t", result, ok)
	}
}

func TestUsageAccumulatorRejectsNegativeUnknownCacheWriteWithoutMutation(t *testing.T) {
	one, negative := int64(1), int64(-1)
	var accumulator Accumulator
	if err := accumulator.MergePatch(Patch{Output: &one}); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.MergePatch(Patch{CacheWriteUnknown: &negative}); err != ErrNegativePatch {
		t.Fatalf("MergePatch negative unknown cache write = %v, want %v", err, ErrNegativePatch)
	}
	result, ok := accumulator.Finalize(true)
	if !ok || result.Tokens != (Tokens{Output: 1}) {
		t.Fatalf("state after rejected patch = %#v, %t", result, ok)
	}
}

func TestUsageAccumulatorReplaceSnapshotRejectsNegativePatchWithoutMutation(t *testing.T) {
	one, negative := int64(1), int64(-1)
	var accumulator Accumulator
	if err := accumulator.ReplaceSnapshot(Patch{Output: &one, Final: true}); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.ReplaceSnapshot(Patch{Output: &negative}); err != ErrNegativePatch {
		t.Fatalf("ReplaceSnapshot negative = %v, want %v", err, ErrNegativePatch)
	}
	result, ok := accumulator.Finalize(true)
	if !ok || result.State != StateComplete || result.Tokens != (Tokens{Output: 1}) {
		t.Fatalf("state after rejected replacement = %#v, %t", result, ok)
	}
}

func TestUsageAccumulatorMergeRejectsCrossFieldOverflowWithoutMutation(t *testing.T) {
	maximum, one := int64(math.MaxInt64), int64(1)
	var accumulator Accumulator
	if err := accumulator.MergePatch(Patch{UncachedInput: &maximum}); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.MergePatch(Patch{Output: &one, Final: true}); err == nil {
		t.Fatal("MergePatch cross-field overflow error = nil")
	}
	result, ok := accumulator.Finalize(true)
	if !ok || result.State != StatePartial || result.Tokens != (Tokens{UncachedInput: math.MaxInt64}) {
		t.Fatalf("state after rejected merge = %#v, %t", result, ok)
	}
}

func TestUsageAccumulatorReplaceRejectsCrossFieldOverflowWithoutMutation(t *testing.T) {
	maximum, one := int64(math.MaxInt64), int64(1)
	var accumulator Accumulator
	if err := accumulator.ReplaceSnapshot(Patch{Output: &one, Final: true}); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.ReplaceSnapshot(Patch{
		UncachedInput: &maximum,
		Output:        &one,
	}); err == nil {
		t.Fatal("ReplaceSnapshot cross-field overflow error = nil")
	}
	result, ok := accumulator.Finalize(true)
	if !ok || result.State != StateComplete || result.Tokens != (Tokens{Output: 1}) {
		t.Fatalf("state after rejected replacement = %#v, %t", result, ok)
	}
}

func TestUsageAccumulatorReplaceSnapshotReplacesFinality(t *testing.T) {
	one, two := int64(1), int64(2)
	var accumulator Accumulator
	if err := accumulator.ReplaceSnapshot(Patch{Output: &one, Final: true}); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.ReplaceSnapshot(Patch{Output: &two}); err != nil {
		t.Fatal(err)
	}
	result, ok := accumulator.Finalize(true)
	if !ok || result.State != StatePartial || result.Tokens != (Tokens{Output: 2}) {
		t.Fatalf("replaced result = %#v, %t", result, ok)
	}
}

func TestUsageAccumulatorFinalizePublishesOnce(t *testing.T) {
	var accumulator Accumulator
	first, ok := accumulator.Finalize(true)
	if !ok || first.State != StateMissing {
		t.Fatalf("first Finalize = %#v, %t", first, ok)
	}
	second, ok := accumulator.Finalize(true)
	if ok || second != (Result{}) {
		t.Fatalf("second Finalize = %#v, %t, want zero result and false", second, ok)
	}
}

func TestUsageAccumulatorRejectsWritesAfterFinalize(t *testing.T) {
	value := int64(1)
	var accumulator Accumulator
	if _, ok := accumulator.Finalize(true); !ok {
		t.Fatal("first Finalize returned false")
	}
	if err := accumulator.MergePatch(Patch{Output: &value}); err != ErrFinalized {
		t.Fatalf("MergePatch after Finalize = %v, want %v", err, ErrFinalized)
	}
	if err := accumulator.ReplaceSnapshot(Patch{Output: &value}); err != ErrFinalized {
		t.Fatalf("ReplaceSnapshot after Finalize = %v, want %v", err, ErrFinalized)
	}
}

func diagnostic(code DiagnosticCode) Diagnostics {
	var diagnostics Diagnostics
	diagnostics.Add(code)
	return diagnostics
}
