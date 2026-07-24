package container

import "gpt-load/internal/state"

type retentionSnapshotProvider struct {
	manager *state.Manager
}

func (provider retentionSnapshotProvider) RequestLogRetentionDays() int {
	snapshot := provider.manager.Current()
	if snapshot == nil {
		return state.DefaultRuntimeSettings().RequestLogRetentionDays
	}
	return snapshot.Settings.RequestLogRetentionDays
}
