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
	keys       []state.CredentialRuntimeView
}

type runtimeHealthObservation struct {
	runtimeObservation
	problemCiphertexts map[uint]string
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

func (service *Service) captureRuntimeHealthObservation() (
	runtimeHealthObservation,
	error,
) {
	if service == nil || service.manager == nil || service.registry == nil ||
		service.now == nil {
		return runtimeHealthObservation{}, fmt.Errorf(
			"capture runtime health observation: %w",
			app_errors.ErrInternalServer,
		)
	}

	service.writeMu.RLock()
	defer service.writeMu.RUnlock()

	snapshot := service.manager.Current()
	if snapshot == nil {
		return runtimeHealthObservation{}, fmt.Errorf(
			"capture runtime health observation: Snapshot is nil: %w",
			app_errors.ErrInternalServer,
		)
	}
	keys := service.registry.Snapshot()
	observedAt := service.now().UTC()
	problemCiphertexts := make(map[uint]string)
	for _, key := range keys {
		group, exists := snapshot.GroupCatalog[key.GroupID]
		if !exists {
			return runtimeHealthObservation{}, fmt.Errorf(
				"capture runtime health observation: key %d group %d missing from catalog: %w",
				key.ID,
				key.GroupID,
				app_errors.ErrInternalServer,
			)
		}
		bucket := classifyHealthKey(group, key, observedAt)
		if bucket != healthBucketCooldown && bucket != healthBucketBlacklisted {
			continue
		}
		ciphertext, exists := service.registry.EncryptedCredentialData(key.ID)
		if !exists || ciphertext == "" {
			return runtimeHealthObservation{}, fmt.Errorf(
				"capture runtime health observation: key %d ciphertext unavailable: %w",
				key.ID,
				app_errors.ErrInternalServer,
			)
		}
		problemCiphertexts[key.ID] = ciphertext
	}

	return runtimeHealthObservation{
		runtimeObservation: runtimeObservation{
			observedAt: observedAt,
			snapshot:   snapshot,
			keys:       keys,
		},
		problemCiphertexts: problemCiphertexts,
	}, nil
}
