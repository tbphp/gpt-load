package observation

// MergeQuotaWindow overlays patch's Header-confirmed fields onto previous,
// preserving whatever the patch left unset. Both must share the same ID;
// callers are responsible for matching windows before calling this.
func MergeQuotaWindow(previous, patch QuotaWindow) QuotaWindow {
	merged := previous
	merged.ID = patch.ID
	merged.Label = patch.Label
	merged.LabelKey = patch.LabelKey
	merged.Scope = patch.Scope
	merged.Unit = patch.Unit
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
