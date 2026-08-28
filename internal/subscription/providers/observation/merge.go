package observation

// MergeQuotaWindow overlays patch's Header-confirmed fields onto previous,
// preserving whatever the patch left unset. Both must share the same ID;
// callers are responsible for matching windows before calling this.
//
// Metadata strings are only overwritten when the patch actually carries one:
// Label, Scope, Unit, and State are required by the stored snapshot, so an
// empty patch value must never blank a window that already has them. The
// usage numbers move as one group keyed on Utilization, so a partial patch
// cannot leave Used and Remaining describing different observations.
func MergeQuotaWindow(previous, patch QuotaWindow) QuotaWindow {
	merged := previous
	merged.ID = patch.ID
	if patch.Label != "" {
		merged.Label = patch.Label
	}
	if patch.LabelKey != "" {
		merged.LabelKey = patch.LabelKey
	}
	if patch.Scope != "" {
		merged.Scope = patch.Scope
	}
	if patch.Unit != "" {
		merged.Unit = patch.Unit
	}
	if patch.Utilization != nil {
		merged.Used, merged.Limit = patch.Used, patch.Limit
		merged.Remaining, merged.Utilization = patch.Remaining, patch.Utilization
	}
	if patch.ResetAtMS != nil {
		merged.ResetAtMS = patch.ResetAtMS
	}
	if patch.WindowSeconds != nil {
		merged.WindowSeconds = patch.WindowSeconds
	}
	if patch.State != "" && patch.State != "unknown" {
		merged.State = patch.State
	}
	return merged
}
