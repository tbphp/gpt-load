package observation

import "testing"

func floatPtr(value float64) *float64 { return &value }
func int64Ptr(value int64) *int64     { return &value }

func TestMergeQuotaWindowKeepsUnsetFieldsFromPrevious(t *testing.T) {
	previous := QuotaWindow{
		ID: "primary", Label: "Session", Scope: "account", Unit: "percent",
		Used: floatPtr(10), Limit: floatPtr(100), Remaining: floatPtr(90), Utilization: floatPtr(0.1),
		ResetAtMS: int64Ptr(1000), WindowSeconds: int64Ptr(300), State: "available",
	}
	patch := QuotaWindow{
		ID: "primary", Label: "Session", Scope: "account", Unit: "percent",
		Used: floatPtr(55), Limit: floatPtr(100), Remaining: floatPtr(45), Utilization: floatPtr(0.55),
		State: "available",
	}

	merged := MergeQuotaWindow(previous, patch)

	if *merged.Used != 55 || *merged.Utilization != 0.55 {
		t.Fatalf("merged usage = %#v", merged)
	}
	if merged.ResetAtMS == nil || *merged.ResetAtMS != 1000 {
		t.Fatalf("merged reset_at_ms = %#v, want preserved 1000", merged.ResetAtMS)
	}
	if merged.WindowSeconds == nil || *merged.WindowSeconds != 300 {
		t.Fatalf("merged window_seconds = %#v, want preserved 300", merged.WindowSeconds)
	}
}

func TestMergeQuotaWindowAppliesPatchResetAndState(t *testing.T) {
	previous := QuotaWindow{
		ID: "primary", Scope: "account", Unit: "percent",
		ResetAtMS: int64Ptr(1000), WindowSeconds: int64Ptr(300), State: "available",
	}
	patch := QuotaWindow{
		ID: "primary", Scope: "account", Unit: "percent",
		ResetAtMS: int64Ptr(2000), State: "exhausted",
	}

	merged := MergeQuotaWindow(previous, patch)

	if merged.ResetAtMS == nil || *merged.ResetAtMS != 2000 {
		t.Fatalf("merged reset_at_ms = %#v, want patch 2000", merged.ResetAtMS)
	}
	if merged.State != "exhausted" {
		t.Fatalf("merged state = %q, want exhausted", merged.State)
	}
	if merged.WindowSeconds == nil || *merged.WindowSeconds != 300 {
		t.Fatalf("merged window_seconds = %#v, want preserved 300", merged.WindowSeconds)
	}
}

func TestMergeQuotaWindowKeepsPreviousMetadataWhenPatchOmitsIt(t *testing.T) {
	previous := QuotaWindow{
		ID: "primary", Label: "5h", LabelKey: "session", Scope: "account", Unit: "percent",
		State: "available",
	}
	patch := QuotaWindow{ID: "primary", Used: floatPtr(10), Utilization: floatPtr(0.1)}

	merged := MergeQuotaWindow(previous, patch)

	if merged.Label != "5h" || merged.LabelKey != "session" ||
		merged.Scope != "account" || merged.Unit != "percent" {
		t.Fatalf("merged metadata = %#v, want previous label/label_key/scope/unit preserved", merged)
	}
	if merged.Used == nil || *merged.Used != 10 {
		t.Fatalf("merged usage = %#v, want the patch value applied", merged.Used)
	}
}

func TestMergeQuotaWindowAppliesPatchMetadataWhenPresent(t *testing.T) {
	previous := QuotaWindow{ID: "primary", Label: "old", LabelKey: "old-key", Scope: "model", Unit: "credits"}
	patch := QuotaWindow{ID: "primary", Label: "5h", LabelKey: "session", Scope: "account", Unit: "percent"}

	merged := MergeQuotaWindow(previous, patch)

	if merged.Label != "5h" || merged.LabelKey != "session" ||
		merged.Scope != "account" || merged.Unit != "percent" {
		t.Fatalf("merged metadata = %#v, want the patch values applied", merged)
	}
}

func TestMergeQuotaWindowIgnoresUnknownPatchState(t *testing.T) {
	previous := QuotaWindow{ID: "primary", State: "exhausted"}
	patch := QuotaWindow{ID: "primary", State: "unknown"}

	merged := MergeQuotaWindow(previous, patch)

	if merged.State != "exhausted" {
		t.Fatalf("merged state = %q, want preserved exhausted", merged.State)
	}
}

func TestMergeQuotaWindowResetOnlyPatchInvalidatesObservedAtOnResetChange(t *testing.T) {
	previous := QuotaWindow{
		ID: "primary", Scope: "account", Unit: "percent",
		Used: floatPtr(10), Limit: floatPtr(100), Remaining: floatPtr(90), Utilization: floatPtr(0.1),
		ResetAtMS: int64Ptr(1000), WindowSeconds: int64Ptr(300), State: "available",
		ObservedAtMS: int64Ptr(500),
	}
	// Reset-only patch with changed ResetAtMS: must keep display fields, but invalidate ObservedAtMS to nil
	patch := QuotaWindow{
		ID: "primary", Scope: "account",
		ResetAtMS: int64Ptr(2000),
	}

	merged := MergeQuotaWindow(previous, patch)

	if merged.ObservedAtMS != nil {
		t.Fatalf("merged.ObservedAtMS = %v, want nil", merged.ObservedAtMS)
	}
	if merged.Used == nil || *merged.Used != 10 || merged.Utilization == nil || *merged.Utilization != 0.1 {
		t.Fatalf("merged display fields modified unexpectedly: %#v", merged)
	}
	if merged.ResetAtMS == nil || *merged.ResetAtMS != 2000 {
		t.Fatalf("merged.ResetAtMS = %v, want 2000", merged.ResetAtMS)
	}
}

func TestMergeQuotaWindowUtilizationPatchUpdatesObservedAt(t *testing.T) {
	previous := QuotaWindow{
		ID: "primary", Scope: "account",
		Used: floatPtr(10), Limit: floatPtr(100), Remaining: floatPtr(90), Utilization: floatPtr(0.1),
		ResetAtMS: int64Ptr(1000), ObservedAtMS: int64Ptr(500),
	}
	patch := QuotaWindow{
		ID: "primary", Scope: "account",
		Used: floatPtr(20), Limit: floatPtr(100), Remaining: floatPtr(80), Utilization: floatPtr(0.2),
		ObservedAtMS: int64Ptr(700),
	}

	merged := MergeQuotaWindow(previous, patch)

	if merged.ObservedAtMS == nil || *merged.ObservedAtMS != 700 {
		t.Fatalf("merged.ObservedAtMS = %v, want 700", merged.ObservedAtMS)
	}
	if merged.Utilization == nil || *merged.Utilization != 0.2 {
		t.Fatalf("merged.Utilization = %v, want 0.2", merged.Utilization)
	}
}
