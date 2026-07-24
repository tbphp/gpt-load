package control

import (
	"fmt"
	"time"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
)

type runtimeObservation struct {
	observedAt time.Time
	snapshot   *state.ConfigSnapshot
	keys       []state.KeyRuntimeView
}

func (service *Service) captureRuntimeObservation() (runtimeObservation, error) {
	if service == nil || service.manager == nil || service.registry == nil ||
		service.now == nil {
		return runtimeObservation{}, fmt.Errorf(
			"capture runtime observation: %w",
			app_errors.ErrInternalServer,
		)
	}
	service.writeMu.RLock()
	snapshot := service.manager.Current()
	if snapshot == nil {
		service.writeMu.RUnlock()
		return runtimeObservation{}, fmt.Errorf(
			"capture runtime observation: Snapshot is nil: %w",
			app_errors.ErrInternalServer,
		)
	}
	keys := service.registry.Snapshot()
	observedAt := service.now().UTC()
	service.writeMu.RUnlock()

	for _, key := range keys {
		if _, exists := snapshot.GroupCatalog[key.GroupID]; !exists {
			return runtimeObservation{}, fmt.Errorf(
				"capture runtime observation: key %d group %d missing from catalog: %w",
				key.ID,
				key.GroupID,
				app_errors.ErrInternalServer,
			)
		}
	}
	return runtimeObservation{
		observedAt: observedAt,
		snapshot:   snapshot,
		keys:       keys,
	}, nil
}
