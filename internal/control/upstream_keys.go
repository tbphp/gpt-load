package control

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/health"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type UpstreamKeyUpdateRequest struct {
	Status       optionalField[state.KeyStatus] `json:"status"`
	WeightManual optionalField[int]             `json:"weight_manual"`
}

type GroupKeyRevealResult struct {
	ID           uint   `json:"id"`
	Key          string `json:"key"`
	RevealedAtMS int64  `json:"revealed_at_ms"`
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

func (s *Service) mapGroupKeyItem(
	row models.UpstreamKey,
	view state.KeyRuntimeView,
	group models.Group,
	stats health.KeyStats,
	observedAt time.Time,
) (GroupKeyItemResponse, error) {
	plaintext, err := s.encryption.Decrypt(row.KeyValue)
	if err != nil {
		return GroupKeyItemResponse{}, fmt.Errorf(
			"decrypt group key %d: %w",
			row.ID,
			app_errors.ErrInternalServer,
		)
	}
	mask, err := maskGroupKeyCollection(plaintext)
	if err != nil {
		return GroupKeyItemResponse{}, err
	}
	bucket := classifyHealthKey(state.GroupCatalogView{
		ID: group.ID, Name: group.Name, Enabled: group.Enabled,
		WeightManual: cloneInt(group.WeightManual),
	}, view, observedAt)
	return mapGroupKeyCollectionItem(mask, row.ID, view, bucket, stats, observedAt)
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
			if request.WeightManual.Value < 1 ||
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

func (s *Service) RevealGroupKey(
	ctx context.Context,
	groupID uint,
	keyID uint,
) (GroupKeyRevealResult, error) {
	if groupID == 0 || keyID == 0 {
		return GroupKeyRevealResult{}, app_errors.ErrBadRequest
	}
	var group models.Group
	if err := s.db.WithContext(ctx).
		Select("id").
		Where("id = ?", groupID).
		Take(&group).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return GroupKeyRevealResult{}, groupNotFoundError()
		}
		return GroupKeyRevealResult{}, app_errors.ParseDBError(err)
	}
	var row struct {
		ID       uint
		KeyValue string
	}
	if err := s.db.WithContext(ctx).
		Model(&models.UpstreamKey{}).
		Select("id", "key_value").
		Where("id = ? AND group_id = ?", keyID, groupID).
		Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return GroupKeyRevealResult{}, keyNotFoundError()
		}
		return GroupKeyRevealResult{}, app_errors.ParseDBError(err)
	}
	plaintext, err := s.encryption.Decrypt(row.KeyValue)
	if err != nil {
		return GroupKeyRevealResult{}, fmt.Errorf(
			"reveal group key %d: %w",
			row.ID,
			app_errors.ErrInternalServer,
		)
	}
	if _, err := maskGroupKeyCollection(plaintext); err != nil {
		return GroupKeyRevealResult{}, fmt.Errorf(
			"reveal group key %d: %w",
			row.ID,
			app_errors.ErrInternalServer,
		)
	}
	revealedAtMS, err := safeEpochMilliseconds(s.now())
	if err != nil {
		return GroupKeyRevealResult{}, app_errors.ErrInternalServer
	}
	return GroupKeyRevealResult{
		ID:           row.ID,
		Key:          plaintext,
		RevealedAtMS: revealedAtMS,
	}, nil
}

func (s *Service) UpdateGroupKey(
	ctx context.Context,
	groupID uint,
	keyID uint,
	request UpstreamKeyUpdateRequest,
) (GroupKeyItemResponse, error) {
	if groupID == 0 || keyID == 0 {
		return GroupKeyItemResponse{}, app_errors.ErrBadRequest
	}
	status, weight, weightSet, err := normalizeUpstreamKeyUpdate(request)
	if err != nil {
		return GroupKeyItemResponse{}, err
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
		return GroupKeyItemResponse{}, err
	}

	capture, err := s.captureGroupKeys(ctx, groupID)
	if err != nil {
		return GroupKeyItemResponse{}, err
	}
	observation, err := validateGroupKeysCapture(capture)
	if err != nil {
		return GroupKeyItemResponse{}, err
	}
	for _, row := range observation.rows {
		if row.ID == keyID {
			return s.mapGroupKeyItem(
				row,
				observation.runtime[keyID],
				observation.group,
				s.stats.Snapshot(keyID, observation.observedAt),
				observation.observedAt,
			)
		}
	}
	return GroupKeyItemResponse{}, keyNotFoundError()
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
