package control

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type GroupKeyBatchAction string

const (
	GroupKeyBatchEnable  GroupKeyBatchAction = "enable"
	GroupKeyBatchDisable GroupKeyBatchAction = "disable"
	GroupKeyBatchDelete  GroupKeyBatchAction = "delete"
)

type GroupKeyBatchRequest struct {
	Action GroupKeyBatchAction `json:"action"`
	KeyIDs []uint              `json:"key_ids"`
}

type GroupKeyBatchResponse struct {
	AffectedIDs []uint                  `json:"affected_ids"`
	Summary     GroupKeySummaryResponse `json:"summary"`
}

func (s *Service) RestoreGroupKey(
	ctx context.Context,
	groupID uint,
	keyID uint,
) (GroupKeyItemResponse, error) {
	if groupID == 0 || keyID == 0 {
		return GroupKeyItemResponse{}, app_errors.ErrBadRequest
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var group models.Group
	if err := s.db.WithContext(ctx).Where("id = ?", groupID).Take(&group).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return GroupKeyItemResponse{}, groupNotFoundError()
		}
		return GroupKeyItemResponse{}, app_errors.ParseDBError(err)
	}
	var row models.UpstreamKey
	if err := s.db.WithContext(ctx).
		Where("id = ? AND group_id = ?", keyID, groupID).
		Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return GroupKeyItemResponse{}, keyNotFoundError()
		}
		return GroupKeyItemResponse{}, app_errors.ParseDBError(err)
	}
	view, exists := findRuntimeKey(s.registry.Snapshot(), keyID)
	if !exists {
		return GroupKeyItemResponse{}, dbRegistryMismatch(mismatchMissingRegistry, groupID, keyID)
	}
	if view.GroupID != groupID {
		return GroupKeyItemResponse{}, dbRegistryMismatch(mismatchGroupID, groupID, keyID)
	}
	if view.Status != state.KeyStatus(row.Status) {
		return GroupKeyItemResponse{}, dbRegistryMismatch(mismatchStatus, groupID, keyID)
	}
	if !equalOptionalWeight(view.WeightManual, row.WeightManual) {
		return GroupKeyItemResponse{}, dbRegistryMismatch(mismatchWeightManual, groupID, keyID)
	}

	observedAt := s.now().UTC()
	bucket := classifyHealthKey(state.GroupCatalogView{
		ID: group.ID, Name: group.Name, Enabled: group.Enabled,
		WeightManual: cloneInt(group.WeightManual),
	}, view, observedAt)
	if bucket != healthBucketCooldown && bucket != healthBucketBlacklisted {
		return GroupKeyItemResponse{}, app_errors.ErrInvalidKeyState
	}
	var restoreErr error
	restore := func() {
		stats := s.stats.Snapshot(keyID, observedAt)
		stats.ConsecutiveFailure = 0
		stats.ConsecutiveProblem = 0
		stats.LastFailureCategory = 0
		stats.LastStatusCode = 0
		weight := calculateAutoWeight(stats)
		if !s.registry.RestoreRuntimeState(keyID, weight) {
			restoreErr = dbRegistryMismatch(mismatchMissingRegistry, groupID, keyID)
			return
		}
		s.stats.ClearProblemState(keyID)
	}
	if s.mutations == nil {
		restore()
	} else {
		s.mutations.Do(keyID, restore)
	}
	if restoreErr != nil {
		return GroupKeyItemResponse{}, restoreErr
	}

	view, exists = findRuntimeKey(s.registry.Snapshot(), keyID)
	if !exists {
		return GroupKeyItemResponse{}, dbRegistryMismatch(mismatchMissingRegistry, groupID, keyID)
	}
	return s.mapGroupKeyItem(row, view, group, s.stats.Snapshot(keyID, observedAt), observedAt)
}

func (s *Service) BatchGroupKeys(
	ctx context.Context,
	groupID uint,
	request GroupKeyBatchRequest,
) (GroupKeyBatchResponse, error) {
	if groupID == 0 {
		return GroupKeyBatchResponse{}, app_errors.ErrBadRequest
	}
	keyIDs, err := normalizeGroupKeyBatchRequest(request)
	if err != nil {
		return GroupKeyBatchResponse{}, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.enforceOperationRecoveryBarrierLocked(ctx, 0); err != nil {
		return GroupKeyBatchResponse{}, err
	}

	var group models.Group
	if err := s.db.WithContext(ctx).Where("id = ?", groupID).Take(&group).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return GroupKeyBatchResponse{}, groupNotFoundError()
		}
		return GroupKeyBatchResponse{}, app_errors.ParseDBError(err)
	}
	var rows []models.UpstreamKey
	if err := s.db.WithContext(ctx).Where("id IN ?", keyIDs).Find(&rows).Error; err != nil {
		return GroupKeyBatchResponse{}, app_errors.ParseDBError(err)
	}
	if len(rows) != len(keyIDs) {
		return GroupKeyBatchResponse{}, keyNotFoundError()
	}
	rowByID := make(map[uint]models.UpstreamKey, len(rows))
	for _, row := range rows {
		if row.GroupID != groupID {
			return GroupKeyBatchResponse{}, keyNotFoundError()
		}
		rowByID[row.ID] = row
	}
	viewByID := make(map[uint]state.KeyRuntimeView)
	for _, view := range s.registry.Snapshot() {
		viewByID[view.ID] = view
	}
	for _, keyID := range keyIDs {
		row := rowByID[keyID]
		view, exists := viewByID[keyID]
		if !exists {
			return GroupKeyBatchResponse{}, dbRegistryMismatch(mismatchMissingRegistry, groupID, keyID)
		}
		if view.GroupID != groupID {
			return GroupKeyBatchResponse{}, dbRegistryMismatch(mismatchGroupID, groupID, keyID)
		}
		if view.Status != state.KeyStatus(row.Status) {
			return GroupKeyBatchResponse{}, dbRegistryMismatch(mismatchStatus, groupID, keyID)
		}
		if !equalOptionalWeight(view.WeightManual, row.WeightManual) {
			return GroupKeyBatchResponse{}, dbRegistryMismatch(mismatchWeightManual, groupID, keyID)
		}
	}

	err = s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		query := tx.Where("group_id = ? AND id IN ?", groupID, keyIDs)
		var result *gorm.DB
		switch request.Action {
		case GroupKeyBatchEnable:
			result = query.Model(&models.UpstreamKey{}).Update("status", models.UpstreamKeyStatusActive)
		case GroupKeyBatchDisable:
			result = query.Model(&models.UpstreamKey{}).Update("status", models.UpstreamKeyStatusDisabled)
		case GroupKeyBatchDelete:
			result = query.Delete(&models.UpstreamKey{})
		}
		if result.Error != nil {
			return app_errors.ParseDBError(result.Error)
		}
		if result.RowsAffected != int64(len(keyIDs)) {
			return fmt.Errorf("batch group key rows affected = %d, want %d: %w", result.RowsAffected, len(keyIDs), app_errors.ErrDatabase)
		}
		return nil
	})
	if err != nil {
		return GroupKeyBatchResponse{}, err
	}

	switch request.Action {
	case GroupKeyBatchEnable:
		err = s.registry.UpdateGroupKeyStatuses(groupID, keyIDs, state.KeyStatusActive)
	case GroupKeyBatchDisable:
		err = s.registry.UpdateGroupKeyStatuses(groupID, keyIDs, state.KeyStatusDisabled)
	case GroupKeyBatchDelete:
		err = s.registry.RemoveGroupKeys(groupID, keyIDs)
	}
	if err != nil {
		return GroupKeyBatchResponse{}, withControlOperationContext(
			newControlOperationError(stageApplyCommittedRegistryMutation),
			groupID,
			0,
		)
	}
	if request.Action == GroupKeyBatchDelete {
		for _, keyID := range keyIDs {
			s.stats.Reset(keyID)
		}
	}

	return GroupKeyBatchResponse{
		AffectedIDs: keyIDs,
		Summary:     summarizeGroupRuntimeKeys(group, s.registry.Snapshot(), s.now().UTC()),
	}, nil
}

func normalizeGroupKeyBatchRequest(request GroupKeyBatchRequest) ([]uint, error) {
	if request.Action != GroupKeyBatchEnable &&
		request.Action != GroupKeyBatchDisable &&
		request.Action != GroupKeyBatchDelete {
		return nil, app_errors.ErrValidation
	}
	if len(request.KeyIDs) < 1 || len(request.KeyIDs) > 100 {
		return nil, app_errors.ErrValidation
	}
	keyIDs := append([]uint(nil), request.KeyIDs...)
	sort.Slice(keyIDs, func(left, right int) bool { return keyIDs[left] < keyIDs[right] })
	for index, keyID := range keyIDs {
		if keyID == 0 || (index > 0 && keyID == keyIDs[index-1]) {
			return nil, app_errors.ErrValidation
		}
	}
	return keyIDs, nil
}

func summarizeGroupRuntimeKeys(
	group models.Group,
	views []state.KeyRuntimeView,
	observedAt time.Time,
) GroupKeySummaryResponse {
	summary := GroupKeySummaryResponse{}
	groupView := state.GroupCatalogView{
		ID: group.ID, Name: group.Name, Enabled: group.Enabled,
		WeightManual: cloneInt(group.WeightManual),
	}
	for _, view := range views {
		if view.GroupID != group.ID {
			continue
		}
		summary.Total++
		switch classifyHealthKey(groupView, view, observedAt) {
		case healthBucketAvailable:
			summary.Available++
		case healthBucketCooldown:
			summary.Cooldown++
		case healthBucketBlacklisted:
			summary.Blacklisted++
		case healthBucketDisabled:
			summary.Disabled++
		}
	}
	return summary
}
