package control

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type UpstreamKeyResponse struct {
	ID              uint            `json:"id"`
	GroupID         uint            `json:"group_id"`
	Mask            string          `json:"mask"`
	Status          state.KeyStatus `json:"status"`
	EffectiveStatus string          `json:"effective_status"`
	WeightManual    *int            `json:"weight_manual"`
	WeightAuto      int             `json:"weight_auto"`
	Blacklisted     bool            `json:"blacklisted"`
	CooldownUntil   *time.Time      `json:"cooldown_until"`
	FailureCount    int             `json:"failure_count"`
}

type UpstreamKeyUpdateRequest struct {
	Status       optionalField[state.KeyStatus] `json:"status"`
	WeightManual optionalField[int]             `json:"weight_manual"`
}

type groupKeysCapture struct {
	group      models.Group
	rows       []models.UpstreamKey
	views      []state.KeyRuntimeView
	observedAt time.Time
}

type groupKeysObservation struct {
	group      models.Group
	rows       []models.UpstreamKey
	runtime    map[uint]state.KeyRuntimeView
	observedAt time.Time
}

func equalOptionalWeight(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (s *Service) captureGroupKeys(
	ctx context.Context,
	groupID uint,
) (groupKeysCapture, error) {
	s.writeMu.RLock()

	var group models.Group
	groupErr := s.db.WithContext(ctx).
		Where("id = ?", groupID).Take(&group).Error
	rows := make([]models.UpstreamKey, 0)
	var rowsErr error
	if groupErr == nil {
		rowsErr = s.db.WithContext(ctx).
			Where("group_id = ?", groupID).
			Order("id ASC").
			Find(&rows).Error
	}
	var views []state.KeyRuntimeView
	var observedAt time.Time
	if groupErr == nil && rowsErr == nil {
		views = s.registry.Snapshot()
		observedAt = s.now().UTC()
	}
	s.writeMu.RUnlock()

	if groupErr != nil {
		if errors.Is(groupErr, gorm.ErrRecordNotFound) {
			return groupKeysCapture{}, groupNotFoundError()
		}
		return groupKeysCapture{}, app_errors.ParseDBError(groupErr)
	}
	if rowsErr != nil {
		return groupKeysCapture{}, app_errors.ParseDBError(rowsErr)
	}
	return groupKeysCapture{
		group: group, rows: rows, views: views, observedAt: observedAt,
	}, nil
}

func validateGroupKeysCapture(
	capture groupKeysCapture,
) (groupKeysObservation, error) {
	groupID := capture.group.ID
	byID := make(map[uint]state.KeyRuntimeView, len(capture.views))
	for _, view := range capture.views {
		byID[view.ID] = view
	}
	for _, row := range capture.rows {
		view, exists := byID[row.ID]
		if !exists {
			return groupKeysObservation{}, dbRegistryMismatch(
				mismatchMissingRegistry, groupID, row.ID,
			)
		}
		if view.GroupID != groupID {
			return groupKeysObservation{}, dbRegistryMismatch(
				mismatchGroupID, groupID, row.ID,
			)
		}
		if view.Status != state.KeyStatus(row.Status) {
			return groupKeysObservation{}, dbRegistryMismatch(
				mismatchStatus, groupID, row.ID,
			)
		}
		if !equalOptionalWeight(view.WeightManual, row.WeightManual) {
			return groupKeysObservation{}, dbRegistryMismatch(
				mismatchWeightManual, groupID, row.ID,
			)
		}
	}
	persisted := make(map[uint]struct{}, len(capture.rows))
	for _, row := range capture.rows {
		persisted[row.ID] = struct{}{}
	}
	for _, view := range capture.views {
		if view.GroupID != groupID {
			continue
		}
		if _, exists := persisted[view.ID]; !exists {
			return groupKeysObservation{}, dbRegistryMismatch(
				mismatchExtraRegistry, groupID, view.ID,
			)
		}
	}
	return groupKeysObservation{
		group: capture.group, rows: capture.rows,
		runtime: byID, observedAt: capture.observedAt,
	}, nil
}

func (s *Service) mapGroupKeys(
	observation groupKeysObservation,
) ([]UpstreamKeyResponse, error) {
	group := state.GroupCatalogView{
		ID:           observation.group.ID,
		Name:         observation.group.Name,
		Enabled:      observation.group.Enabled,
		WeightManual: cloneInt(observation.group.WeightManual),
	}
	result := make([]UpstreamKeyResponse, 0, len(observation.rows))
	for _, row := range observation.rows {
		view := observation.runtime[row.ID]
		plaintext, err := s.encryption.Decrypt(row.KeyValue)
		if err != nil {
			return nil, fmt.Errorf(
				"decrypt upstream key %d: %w",
				row.ID,
				app_errors.ErrInternalServer,
			)
		}
		var cooldownUntil *time.Time
		if view.CooldownUntil.After(observation.observedAt) {
			cooldownUntil = optionalUTC(view.CooldownUntil)
		}
		result = append(result, UpstreamKeyResponse{
			ID: row.ID, GroupID: row.GroupID,
			Mask:   utils.MaskAPIKey(plaintext),
			Status: view.Status,
			EffectiveStatus: string(classifyHealthKey(
				group,
				view,
				observation.observedAt,
			)),
			WeightManual:  cloneInt(view.WeightManual),
			WeightAuto:    view.WeightAuto,
			Blacklisted:   view.Blacklisted,
			CooldownUntil: cooldownUntil,
			FailureCount:  view.FailureCount,
		})
	}
	return result, nil
}

func (s *Service) ListGroupKeys(
	ctx context.Context,
	groupID uint,
) ([]UpstreamKeyResponse, error) {
	if groupID == 0 {
		return nil, app_errors.ErrBadRequest
	}
	capture, err := s.captureGroupKeys(ctx, groupID)
	if err != nil {
		return nil, err
	}
	observation, err := validateGroupKeysCapture(capture)
	if err != nil {
		return nil, err
	}
	return s.mapGroupKeys(observation)
}

func normalizeUpstreamKeyUpdate(
	request UpstreamKeyUpdateRequest,
) (status *state.KeyStatus, weight *int, weightSet bool, err error) {
	if !request.Status.Set && !request.WeightManual.Set {
		return nil, nil, false, app_errors.ErrBadRequest
	}
	if request.Status.Set {
		if request.Status.Null ||
			(request.Status.Value != state.KeyStatusActive &&
				request.Status.Value != state.KeyStatusDisabled) {
			return nil, nil, false, app_errors.ErrValidation
		}
		value := request.Status.Value
		status = &value
	}
	if request.WeightManual.Set {
		weightSet = true
		if !request.WeightManual.Null {
			if request.WeightManual.Value < 0 ||
				request.WeightManual.Value > state.MaxWeight {
				return nil, nil, false, app_errors.ErrValidation
			}
			value := request.WeightManual.Value
			weight = &value
		}
	}
	return status, weight, weightSet, nil
}

func findRuntimeKey(
	views []state.KeyRuntimeView,
	keyID uint,
) (state.KeyRuntimeView, bool) {
	for _, view := range views {
		if view.ID == keyID {
			return view, true
		}
	}
	return state.KeyRuntimeView{}, false
}

func (s *Service) UpdateGroupKey(
	ctx context.Context,
	groupID uint,
	keyID uint,
	request UpstreamKeyUpdateRequest,
) (UpstreamKeyResponse, error) {
	if groupID == 0 || keyID == 0 {
		return UpstreamKeyResponse{}, app_errors.ErrBadRequest
	}
	status, weight, weightSet, err := normalizeUpstreamKeyUpdate(request)
	if err != nil {
		return UpstreamKeyResponse{}, err
	}

	var committed models.UpstreamKey
	err = s.writeKeyConfig(
		ctx,
		groupID,
		keyID,
		func(tx *gorm.DB) error {
			var group models.Group
			if err := tx.Select("id").
				Where("id = ?", groupID).
				Take(&group).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return groupNotFoundError()
				}
				return app_errors.ParseDBError(err)
			}
			if err := tx.Where("id = ? AND group_id = ?", keyID, groupID).
				Take(&committed).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return keyNotFoundError()
				}
				return app_errors.ParseDBError(err)
			}
			updates := make(map[string]any, 2)
			if status != nil {
				committed.Status = models.UpstreamKeyStatus(*status)
				updates["status"] = committed.Status
			}
			if weightSet {
				committed.WeightManual = cloneInt(weight)
				updates["weight_manual"] = committed.WeightManual
			}
			if err := tx.Model(&models.UpstreamKey{}).
				Where("id = ? AND group_id = ?", keyID, groupID).
				Updates(updates).Error; err != nil {
				return app_errors.ParseDBError(err)
			}
			return nil
		},
		func() error {
			view, exists := findRuntimeKey(s.registry.Snapshot(), keyID)
			if exists && view.GroupID != groupID {
				return fmt.Errorf("Registry key belongs to another Group")
			}
			if !exists {
				return s.registry.ApplyImport(groupID, []state.KeyEntry{{
					ID: keyID, GroupID: groupID,
					Status:         state.KeyStatus(committed.Status),
					WeightManual:   cloneInt(committed.WeightManual),
					WeightAuto:     state.DefaultWeight,
					EncryptedValue: committed.KeyValue,
				}})
			}
			return s.registry.UpdateKeyConfig(
				keyID,
				state.KeyStatus(committed.Status),
				committed.WeightManual,
			)
		},
	)
	if err != nil {
		return UpstreamKeyResponse{}, err
	}

	responses, err := s.ListGroupKeys(ctx, groupID)
	if err != nil {
		return UpstreamKeyResponse{}, err
	}
	for _, response := range responses {
		if response.ID == keyID {
			return response, nil
		}
	}
	return UpstreamKeyResponse{}, keyNotFoundError()
}

func (s *Service) DeleteGroupKey(
	ctx context.Context,
	groupID uint,
	keyID uint,
) error {
	if groupID == 0 || keyID == 0 {
		return app_errors.ErrBadRequest
	}
	return s.writeKeyConfig(
		ctx,
		groupID,
		keyID,
		func(tx *gorm.DB) error {
			var group models.Group
			if err := tx.Select("id").
				Where("id = ?", groupID).
				Take(&group).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return groupNotFoundError()
				}
				return app_errors.ParseDBError(err)
			}
			var row models.UpstreamKey
			if err := tx.Select("id").
				Where("id = ? AND group_id = ?", keyID, groupID).
				Take(&row).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return keyNotFoundError()
				}
				return app_errors.ParseDBError(err)
			}
			if err := tx.Delete(&row).Error; err != nil {
				return app_errors.ParseDBError(err)
			}
			return nil
		},
		func() error {
			s.registry.RemoveKey(keyID)
			return nil
		},
	)
}
