package control

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/health"
	"gpt-load/internal/platform/epochms"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func normalizeCredentialUpdate(
	request CredentialUpdateRequest,
) (status *state.CredentialStatus, weight *int, weightSet bool, err error) {
	if !request.Status.Set && !request.WeightManual.Set {
		return nil, nil, false, app_errors.ErrBadRequest
	}
	if request.Status.Set {
		if request.Status.Null ||
			(request.Status.Value != state.CredentialStatusActive && request.Status.Value != state.CredentialStatusDisabled) {
			return nil, nil, false, app_errors.ErrValidation
		}
		value := request.Status.Value
		status = &value
	}
	if request.WeightManual.Set {
		weightSet = true
		if !request.WeightManual.Null {
			if request.WeightManual.Value < 1 || request.WeightManual.Value > state.MaxWeight {
				return nil, nil, false, app_errors.ErrValidation
			}
			value := request.WeightManual.Value
			weight = &value
		}
	}
	return status, weight, weightSet, nil
}

func nextCredentialUpdatedAtMS(now time.Time, previous int64) (int64, error) {
	nowMS, err := epochms.FromTime(now)
	if err != nil {
		return 0, err
	}
	if nowMS < 1 {
		nowMS = 1
	}
	if nowMS <= previous {
		if previous == math.MaxInt64 {
			return 0, fmt.Errorf("credential version exhausted")
		}
		nowMS = previous + 1
	}
	return nowMS, nil
}

func equalOptionalWeight(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func findRuntimeCredential(
	views []state.CredentialRuntimeView,
	credentialID uint,
) (state.CredentialRuntimeView, bool) {
	for _, view := range views {
		if view.ID == credentialID {
			return view, true
		}
	}
	return state.CredentialRuntimeView{}, false
}

func (s *Service) RevealGroupCredential(
	ctx context.Context,
	groupID uint,
	credentialID uint,
) (CredentialRevealResult, error) {
	if groupID == 0 || credentialID == 0 {
		return CredentialRevealResult{}, app_errors.ErrBadRequest
	}
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	group, err := loadGroupRow(s.db.WithContext(ctx), groupID)
	if err != nil {
		return CredentialRevealResult{}, err
	}
	if group.ChannelID == "" {
		return CredentialRevealResult{}, app_errors.ErrValidation
	}
	var row models.Credential
	if err := s.db.WithContext(ctx).Select("id", "group_id", "data", "fingerprint", "status", "weight_manual", "updated_at_ms").
		Where("id = ? AND group_id = ?", credentialID, groupID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return CredentialRevealResult{}, credentialNotFoundError()
		}
		return CredentialRevealResult{}, app_errors.ParseDBError(err)
	}
	credential, _, err := s.decodeCredential(group, row)
	if err != nil {
		return CredentialRevealResult{}, err
	}
	revealedAtMS, err := safeEpochMilliseconds(s.now())
	if err != nil {
		return CredentialRevealResult{}, app_errors.ErrInternalServer
	}
	return CredentialRevealResult{
		CredentialID: row.ID, Credential: append([]byte(nil), credential...), RevealedAtMS: revealedAtMS,
	}, nil
}

func (s *Service) UpdateGroupCredential(
	ctx context.Context,
	groupID uint,
	credentialID uint,
	request CredentialUpdateRequest,
) (CredentialItemResponse, error) {
	if groupID == 0 || credentialID == 0 {
		return CredentialItemResponse{}, app_errors.ErrBadRequest
	}
	status, weight, weightSet, err := normalizeCredentialUpdate(request)
	if err != nil {
		return CredentialItemResponse{}, err
	}
	var committed models.Credential
	err = s.writeCredentialConfig(ctx, groupID, credentialID, func(tx *gorm.DB) error {
		group, err := loadGroupRow(tx, groupID)
		if err != nil {
			return err
		}
		if group.ChannelID == "" {
			return app_errors.ErrValidation
		}
		if err := tx.Where("id = ? AND group_id = ?", credentialID, groupID).Take(&committed).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return credentialNotFoundError()
			}
			return app_errors.ParseDBError(err)
		}
		view, exists := findRuntimeCredential(s.registry.Snapshot(), credentialID)
		if err := validateCredentialRuntimeRow(groupID, committed, view, exists); err != nil {
			return err
		}
		updatedAtMS, err := nextCredentialUpdatedAtMS(s.now(), committed.UpdatedAtMS)
		if err != nil {
			return app_errors.ErrInternalServer
		}
		updates := map[string]any{"updated_at_ms": updatedAtMS}
		if status != nil {
			committed.Status = models.CredentialStatus(*status)
			updates["status"] = committed.Status
		}
		if weightSet {
			committed.WeightManual = cloneInt(weight)
			updates["weight_manual"] = committed.WeightManual
		}
		committed.UpdatedAtMS = updatedAtMS
		if err := tx.Model(&models.Credential{}).Where("id = ? AND group_id = ?", credentialID, groupID).
			Updates(updates).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		return nil
	}, func() error {
		var applyErr error
		apply := func() {
			entries, snapshotErr := s.registry.SnapshotGroupCredentialEntriesExact(groupID, []uint{credentialID})
			if snapshotErr != nil {
				applyErr = dbRegistryMismatch(mismatchMissingRegistry, groupID, credentialID)
				return
			}
			entry := entries[0]
			entry.Status = state.CredentialStatus(committed.Status)
			entry.WeightManual = cloneInt(committed.WeightManual)
			entry.Version = groupCollectionCredentialVersion(committed.UpdatedAtMS)
			entry.IdentityGeneration = groupCollectionCredentialIdentity(committed.Fingerprint)
			entry.Fingerprint = committed.Fingerprint
			entry.EncryptedValue = committed.Data
			applyErr = s.registry.RestoreGroupCredentialEntriesExact(groupID, []state.CredentialEntry{entry})
		}
		if s.mutations == nil {
			apply()
		} else {
			s.mutations.Do(credentialID, apply)
		}
		return applyErr
	})
	if err != nil {
		return CredentialItemResponse{}, err
	}
	return s.loadCredentialItem(ctx, groupID, credentialID)
}

func (s *Service) DeleteGroupCredential(ctx context.Context, groupID, credentialID uint) error {
	if groupID == 0 || credentialID == 0 {
		return app_errors.ErrBadRequest
	}
	return s.writeCredentialConfig(ctx, groupID, credentialID, func(tx *gorm.DB) error {
		group, err := loadGroupRow(tx, groupID)
		if err != nil {
			return err
		}
		if group.ChannelID == "" {
			return app_errors.ErrValidation
		}
		var row models.Credential
		if err := tx.Where("id = ? AND group_id = ?", credentialID, groupID).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return credentialNotFoundError()
			}
			return app_errors.ParseDBError(err)
		}
		view, exists := findRuntimeCredential(s.registry.Snapshot(), credentialID)
		if err := validateCredentialRuntimeRow(groupID, row, view, exists); err != nil {
			return err
		}
		if err := tx.Delete(&row).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		return nil
	}, func() error {
		var applyErr error
		apply := func() {
			if !s.registry.RemoveCredential(credentialID) {
				applyErr = dbRegistryMismatch(mismatchMissingRegistry, groupID, credentialID)
				return
			}
			s.stats.Reset(credentialID)
		}
		if s.mutations == nil {
			apply()
		} else {
			s.mutations.Do(credentialID, apply)
		}
		return applyErr
	})
}

func (s *Service) RestoreGroupCredential(
	ctx context.Context,
	groupID uint,
	credentialID uint,
) (CredentialItemResponse, error) {
	if groupID == 0 || credentialID == 0 {
		return CredentialItemResponse{}, app_errors.ErrBadRequest
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	group, err := loadGroupRow(s.db.WithContext(ctx), groupID)
	if err != nil {
		return CredentialItemResponse{}, err
	}
	if group.ChannelID == "" {
		return CredentialItemResponse{}, app_errors.ErrValidation
	}
	var row models.Credential
	if err := s.db.WithContext(ctx).Where("id = ? AND group_id = ?", credentialID, groupID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return CredentialItemResponse{}, credentialNotFoundError()
		}
		return CredentialItemResponse{}, app_errors.ParseDBError(err)
	}
	view, exists := findRuntimeCredential(s.registry.Snapshot(), credentialID)
	if err := validateCredentialRuntimeRow(groupID, row, view, exists); err != nil {
		return CredentialItemResponse{}, err
	}
	groupView := state.GroupCatalogView{ID: group.ID, Name: group.Name, Enabled: group.Enabled,
		WeightManual: cloneInt(group.WeightManual)}
	var observedAt time.Time
	var restoreErr error
	restore := func() {
		observedAt = s.now().UTC()
		current, exists := findRuntimeCredential(s.registry.Snapshot(), credentialID)
		if !exists {
			restoreErr = dbRegistryMismatch(mismatchMissingRegistry, groupID, credentialID)
			return
		}
		bucket := classifyHealthKey(groupView, current, observedAt)
		if bucket != healthBucketCooldown && bucket != healthBucketBlacklisted {
			restoreErr = app_errors.ErrInvalidCredentialState
			return
		}
		stats := s.stats.Snapshot(credentialID, observedAt)
		stats.ConsecutiveFailure = 0
		stats.ConsecutiveProblem = 0
		stats.LastFailureCategory = 0
		stats.LastStatusCode = 0
		if !s.registry.RestoreRuntimeState(credentialID, calculateAutoWeight(stats)) {
			restoreErr = dbRegistryMismatch(mismatchMissingRegistry, groupID, credentialID)
			return
		}
		s.stats.ClearProblemState(credentialID)
	}
	if s.mutations == nil {
		restore()
	} else {
		s.mutations.Do(credentialID, restore)
	}
	if restoreErr != nil {
		return CredentialItemResponse{}, restoreErr
	}
	view, exists = findRuntimeCredential(s.registry.Snapshot(), credentialID)
	if !exists {
		return CredentialItemResponse{}, dbRegistryMismatch(mismatchMissingRegistry, groupID, credentialID)
	}
	return s.mapCredentialItem(row, view, group, s.stats.Snapshot(credentialID, observedAt), observedAt)
}

func validateCredentialRuntimeRow(
	groupID uint,
	row models.Credential,
	view state.CredentialRuntimeView,
	exists bool,
) error {
	if !exists {
		return dbRegistryMismatch(mismatchMissingRegistry, groupID, row.ID)
	}
	if view.GroupID != groupID {
		return dbRegistryMismatch(mismatchGroupID, groupID, row.ID)
	}
	if view.Status != state.CredentialStatus(row.Status) {
		return dbRegistryMismatch(mismatchStatus, groupID, row.ID)
	}
	if !equalOptionalWeight(view.WeightManual, row.WeightManual) {
		return dbRegistryMismatch(mismatchWeightManual, groupID, row.ID)
	}
	if view.Version != groupCollectionCredentialVersion(row.UpdatedAtMS) ||
		view.IdentityGeneration != groupCollectionCredentialIdentity(row.Fingerprint) {
		return dbRegistryMismatch(mismatchIdentity, groupID, row.ID)
	}
	return nil
}

func (s *Service) loadCredentialItem(ctx context.Context, groupID, credentialID uint) (CredentialItemResponse, error) {
	capture, err := s.captureCredentials(ctx, groupID)
	if err != nil {
		return CredentialItemResponse{}, err
	}
	observation, err := validateCredentialCapture(capture)
	if err != nil {
		return CredentialItemResponse{}, err
	}
	for _, row := range observation.rows {
		if row.ID == credentialID {
			return s.mapCredentialItem(row, observation.runtime[credentialID], observation.group,
				s.stats.Snapshot(credentialID, observation.observedAt), observation.observedAt)
		}
	}
	return CredentialItemResponse{}, credentialNotFoundError()
}

func (s *Service) mapCredentialItem(
	row models.Credential,
	view state.CredentialRuntimeView,
	group models.Group,
	stats health.CredentialStats,
	observedAt time.Time,
) (CredentialItemResponse, error) {
	canonical, _, err := s.decodeCredential(group, row)
	if err != nil {
		return CredentialItemResponse{}, err
	}
	mask, err := maskCanonicalCredential(canonical)
	if err != nil {
		return CredentialItemResponse{}, err
	}
	bucket := classifyHealthKey(state.GroupCatalogView{ID: group.ID, Name: group.Name, Enabled: group.Enabled,
		WeightManual: cloneInt(group.WeightManual)}, view, observedAt)
	return mapCredentialRuntimeItem(mask, row.ID, view, bucket, stats, observedAt)
}

func normalizeCredentialBatchRequest(request CredentialBatchRequest) ([]uint, error) {
	if request.Action != CredentialBatchEnable && request.Action != CredentialBatchDisable && request.Action != CredentialBatchDelete {
		return nil, app_errors.ErrValidation
	}
	if len(request.CredentialIDs) < 1 || len(request.CredentialIDs) > 100 {
		return nil, app_errors.ErrValidation
	}
	ids := append([]uint(nil), request.CredentialIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for index, id := range ids {
		if id == 0 || index > 0 && id == ids[index-1] {
			return nil, app_errors.ErrValidation
		}
	}
	return ids, nil
}

func (s *Service) BatchGroupCredentials(
	ctx context.Context,
	groupID uint,
	request CredentialBatchRequest,
) (CredentialBatchResponse, error) {
	if groupID == 0 {
		return CredentialBatchResponse{}, app_errors.ErrBadRequest
	}
	ids, err := normalizeCredentialBatchRequest(request)
	if err != nil {
		return CredentialBatchResponse{}, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.enforceOperationRecoveryBarrierLocked(ctx, 0); err != nil {
		return CredentialBatchResponse{}, err
	}
	group, err := loadGroupRow(s.db.WithContext(ctx), groupID)
	if err != nil {
		return CredentialBatchResponse{}, err
	}
	if group.ChannelID == "" {
		return CredentialBatchResponse{}, app_errors.ErrValidation
	}
	var rows []models.Credential
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return CredentialBatchResponse{}, app_errors.ParseDBError(err)
	}
	if len(rows) != len(ids) {
		return CredentialBatchResponse{}, credentialNotFoundError()
	}
	rowByID := make(map[uint]models.Credential, len(rows))
	viewByID := make(map[uint]state.CredentialRuntimeView)
	for _, view := range s.registry.Snapshot() {
		viewByID[view.ID] = view
	}
	for _, row := range rows {
		if row.GroupID != groupID {
			return CredentialBatchResponse{}, credentialNotFoundError()
		}
		view, exists := viewByID[row.ID]
		if err := validateCredentialRuntimeRow(groupID, row, view, exists); err != nil {
			return CredentialBatchResponse{}, err
		}
		rowByID[row.ID] = row
	}
	coordinator, ok := s.mutations.(interface{ DoMany([]uint, func()) })
	if !ok {
		return CredentialBatchResponse{}, fmt.Errorf("batch mutation coordinator unavailable: %w", app_errors.ErrInternalServer)
	}
	var mutationErr error
	coordinator.DoMany(ids, func() {
		before, snapshotErr := s.registry.SnapshotGroupCredentialEntriesExact(groupID, ids)
		if snapshotErr != nil {
			mutationErr = fmt.Errorf("snapshot credential registry entries: %w", app_errors.ErrInternalServer)
			return
		}
		nowMS := int64(0)
		for _, id := range ids {
			candidate, versionErr := nextCredentialUpdatedAtMS(s.now(), rowByID[id].UpdatedAtMS)
			if versionErr != nil {
				mutationErr = app_errors.ErrInternalServer
				return
			}
			if candidate > nowMS {
				nowMS = candidate
			}
		}
		desired := make([]state.CredentialEntry, len(before))
		for index, entry := range before {
			desired[index] = entry
			if request.Action == CredentialBatchEnable {
				desired[index].Status = state.CredentialStatusActive
			} else if request.Action == CredentialBatchDisable {
				desired[index].Status = state.CredentialStatusDisabled
			}
			desired[index].Version = uint64(nowMS)
		}
		if s.applyBatchRegistryMutation == nil {
			mutationErr = app_errors.ErrInternalServer
			return
		}
		if applyErr := s.applyBatchRegistryMutation(groupID, ids, request.Action); applyErr != nil {
			mutationErr = withControlOperationContext(newControlOperationError(stageApplyCommittedRegistryMutation), groupID, 0)
			return
		}
		if request.Action != CredentialBatchDelete {
			if restoreErr := s.registry.RestoreGroupCredentialEntriesExact(groupID, desired); restoreErr != nil {
				mutationErr = compensateCredentialBatchRegistry(s, groupID, before, restoreErr)
				return
			}
		}
		mutationErr = s.withControlTransaction(ctx, func(tx *gorm.DB) error {
			query := tx.Where("group_id = ? AND id IN ?", groupID, ids)
			var result *gorm.DB
			switch request.Action {
			case CredentialBatchEnable:
				result = query.Model(&models.Credential{}).Updates(map[string]any{
					"status": models.CredentialStatusActive, "updated_at_ms": nowMS,
				})
			case CredentialBatchDisable:
				result = query.Model(&models.Credential{}).Updates(map[string]any{
					"status": models.CredentialStatusDisabled, "updated_at_ms": nowMS,
				})
			case CredentialBatchDelete:
				result = query.Delete(&models.Credential{})
			}
			if result.Error != nil {
				return app_errors.ParseDBError(result.Error)
			}
			if result.RowsAffected != int64(len(ids)) {
				return fmt.Errorf("batch credential rows affected = %d, want %d: %w", result.RowsAffected, len(ids), app_errors.ErrDatabase)
			}
			return nil
		})
		if mutationErr != nil {
			mutationErr = compensateCredentialBatchRegistry(s, groupID, before, mutationErr)
			return
		}
		if request.Action == CredentialBatchDelete {
			for _, id := range ids {
				s.stats.Reset(id)
			}
		}
	})
	if mutationErr != nil {
		return CredentialBatchResponse{}, mutationErr
	}
	return CredentialBatchResponse{
		AffectedCredentialIDs: ids,
		Summary:               summarizeGroupRuntimeCredentials(group, s.registry.Snapshot(), s.now().UTC()),
	}, nil
}

func (s *Service) applyCredentialBatchRegistryMutation(
	groupID uint,
	credentialIDs []uint,
	action CredentialBatchAction,
) error {
	switch action {
	case CredentialBatchEnable:
		return s.registry.UpdateGroupCredentialStatuses(
			groupID, credentialIDs, state.CredentialStatusActive,
		)
	case CredentialBatchDisable:
		return s.registry.UpdateGroupCredentialStatuses(
			groupID, credentialIDs, state.CredentialStatusDisabled,
		)
	case CredentialBatchDelete:
		return s.registry.RemoveGroupCredentials(groupID, credentialIDs)
	default:
		return fmt.Errorf("unsupported batch credential action %q", action)
	}
}

func compensateCredentialBatchRegistry(
	s *Service,
	groupID uint,
	before []state.CredentialEntry,
	cause error,
) error {
	if s.restoreBatchRegistryEntries == nil {
		return errors.Join(
			cause,
			fmt.Errorf("compensate batch credential Registry mutation: %w", app_errors.ErrInternalServer),
		)
	}
	if err := s.restoreBatchRegistryEntries(groupID, before); err != nil {
		return errors.Join(
			cause,
			fmt.Errorf("compensate batch credential Registry mutation: %w", err),
		)
	}
	return cause
}

func summarizeGroupRuntimeCredentials(
	group models.Group,
	views []state.CredentialRuntimeView,
	observedAt time.Time,
) CredentialSummaryResponse {
	summary := CredentialSummaryResponse{}
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
