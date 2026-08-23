package control

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/platform/epochms"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func normalizeCredentialUpdate(
	request CredentialUpdateRequest,
	encryptionService encryption.Service,
) (status *state.CredentialStatus, weight *int, weightSet bool, proxy *string, proxySet bool, err error) {
	if !request.Status.Set && !request.WeightManual.Set && !request.Proxy.Set {
		return nil, nil, false, nil, false, app_errors.ErrBadRequest
	}
	if request.Status.Set {
		if request.Status.Null ||
			(request.Status.Value != state.CredentialStatusActive && request.Status.Value != state.CredentialStatusDisabled) {
			return nil, nil, false, nil, false, app_errors.ErrValidation
		}
		value := request.Status.Value
		status = &value
	}
	if request.WeightManual.Set {
		weightSet = true
		if !request.WeightManual.Null {
			if request.WeightManual.Value < 1 || request.WeightManual.Value > state.MaxWeight {
				return nil, nil, false, nil, false, app_errors.ErrValidation
			}
			value := request.WeightManual.Value
			weight = &value
		}
	}
	proxy, proxySet, err = normalizeProxyOverride(request.Proxy, encryptionService)
	if err != nil {
		return nil, nil, false, nil, false, err
	}
	return status, weight, weightSet, proxy, proxySet, nil
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
	if normalizeGroupConnectionType(group.ConnectionType) == models.ConnectionTypeSubscription {
		return CredentialRevealResult{}, app_errors.ErrForbidden
	}
	var row models.Credential
	if err := s.db.WithContext(ctx).Select("id", "group_id", "data", "fingerprint", "identity_fingerprint", "secret_version", "auth_state", "status", "weight_manual", "updated_at_ms").
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
	status, weight, weightSet, proxy, proxySet, err := normalizeCredentialUpdate(request, s.encryption)
	if err != nil {
		return CredentialItemResponse{}, err
	}
	var committed models.Credential
	var committedGroup models.Group
	var committedProxy, committedProxyFingerprint string
	err = s.writeCredentialConfig(ctx, groupID, credentialID, func(tx *gorm.DB) error {
		group, err := loadGroupRow(tx, groupID)
		if err != nil {
			return err
		}
		if group.ChannelID == "" {
			return app_errors.ErrValidation
		}
		if request.Proxy.Set && !request.Proxy.Null &&
			!s.channelRegistry.SupportsOutboundProxy(channel.ID(group.ChannelID)) {
			return app_errors.ErrValidation
		}
		committedGroup = group
		if err := tx.Where("id = ? AND group_id = ?", credentialID, groupID).Take(&committed).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return credentialNotFoundError()
			}
			return app_errors.ParseDBError(err)
		}
		view, exists := findRuntimeCredential(s.registry.Snapshot(), credentialID)
		if err := validateCredentialRuntimeRow(group, committed, view, exists); err != nil {
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
		if proxySet {
			committed.ProxyConfig = proxy
			updates["proxy_config"] = proxy
		}
		committed.UpdatedAtMS = updatedAtMS
		committedProxy, committedProxyFingerprint, err = storedProxyIdentity(s.encryption, committed.ProxyConfig)
		if err != nil {
			return err
		}
		if err := tx.Model(&models.Credential{}).Where("id = ? AND group_id = ?", credentialID, groupID).
			Updates(updates).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		return nil
	}, func() error {
		var applyErr error
		entries, snapshotErr := s.registry.SnapshotGroupCredentialEntriesExact(groupID, []uint{credentialID})
		if snapshotErr != nil {
			return dbRegistryMismatch(mismatchMissingRegistry, groupID, credentialID)
		}
		entry := entries[0]
		entry.Status = state.CredentialStatus(committed.Status)
		entry.WeightManual = cloneInt(committed.WeightManual)
		entry.Version = groupCollectionCredentialVersion(committed.SecretVersion)
		entry.IdentityGeneration = groupCollectionCredentialIdentity(
			committed.IdentityFingerprint,
			committedGroup,
		)
		entry.Fingerprint = committed.Fingerprint
		entry.EncryptedValue = committed.Data
		entry.EncryptedProxy = committedProxy
		entry.ProxyFingerprint = committedProxyFingerprint
		applyErr = s.registry.RestoreGroupCredentialEntriesExact(groupID, []state.CredentialEntry{entry})
		if applyErr == nil && proxySet {
			s.retireCredentialRuntime(credentialID)
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
		if err := validateCredentialRuntimeRow(group, row, view, exists); err != nil {
			return err
		}
		if err := tx.Delete(&row).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		return nil
	}, func() error {
		if !s.registry.RemoveCredential(credentialID) {
			return dbRegistryMismatch(mismatchMissingRegistry, groupID, credentialID)
		}
		s.stats.Reset(credentialID)
		s.retireCredentialRuntime(credentialID)
		return nil
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
	if err := validateCredentialRuntimeRow(group, row, view, exists); err != nil {
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
	return s.mapCredentialItem(ctx, row, view, group, s.stats.Snapshot(credentialID, observedAt), observedAt)
}

func validateCredentialRuntimeRow(
	group models.Group,
	row models.Credential,
	view state.CredentialRuntimeView,
	exists bool,
) error {
	groupID := group.ID
	if !exists {
		return dbRegistryMismatch(mismatchMissingRegistry, groupID, row.ID)
	}
	if view.GroupID != groupID {
		return dbRegistryMismatch(mismatchGroupID, groupID, row.ID)
	}
	if view.Status != state.CredentialStatus(row.Status) {
		return dbRegistryMismatch(mismatchStatus, groupID, row.ID)
	}
	if view.AuthState != normalizeRuntimeCredentialAuthState(row.AuthState) {
		return dbRegistryMismatch(mismatchStatus, groupID, row.ID)
	}
	if !equalOptionalWeight(view.WeightManual, row.WeightManual) {
		return dbRegistryMismatch(mismatchWeightManual, groupID, row.ID)
	}
	if view.Version != groupCollectionCredentialVersion(row.SecretVersion) ||
		view.IdentityGeneration != groupCollectionCredentialIdentity(row.IdentityFingerprint, group) {
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
			return s.mapCredentialItem(ctx, row, observation.runtime[credentialID], observation.group,
				s.stats.Snapshot(credentialID, observation.observedAt), observation.observedAt)
		}
	}
	return CredentialItemResponse{}, credentialNotFoundError()
}

func (s *Service) mapCredentialItem(
	ctx context.Context,
	row models.Credential,
	view state.CredentialRuntimeView,
	group models.Group,
	stats health.CredentialStats,
	observedAt time.Time,
) (CredentialItemResponse, error) {
	canonical, identity, err := s.decodeCredential(group, row)
	if err != nil {
		return CredentialItemResponse{}, err
	}
	mask, account, err := s.credentialPresentation(group, row, canonical, identity)
	if err != nil {
		return CredentialItemResponse{}, err
	}
	bucket := classifyHealthKey(state.GroupCatalogView{ID: group.ID, Name: group.Name, Enabled: group.Enabled,
		WeightManual: cloneInt(group.WeightManual)}, view, observedAt)
	item, err := mapCredentialRuntimeItem(mask, row.ID, view, bucket, stats, observedAt)
	if err != nil {
		return CredentialItemResponse{}, err
	}
	item.ConnectionType = string(normalizeGroupConnectionType(group.ConnectionType))
	item.SecretVersion = row.SecretVersion
	item.AuthState = string(row.AuthState)
	item.Account = account
	proxyViews, err := s.credentialProxyViews(ctx, s.db, group, []models.Credential{row})
	if err != nil {
		return CredentialItemResponse{}, err
	}
	item.Proxy = proxyViews[row.ID]
	if normalizeGroupConnectionType(group.ConnectionType) == models.ConnectionTypeSubscription {
		var observation models.CredentialObservation
		result := s.db.WithContext(ctx).Take(&observation, "credential_id = ?", row.ID)
		if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return CredentialItemResponse{}, app_errors.ParseDBError(result.Error)
		}
		item.Observation = presentCredentialObservation(observation, row.IdentityFingerprint)
	}
	return item, nil
}

func normalizeCredentialBatchRequest(request CredentialBatchRequest) ([]uint, bool, error) {
	if request.Action != CredentialBatchEnable && request.Action != CredentialBatchDisable && request.Action != CredentialBatchDelete {
		return nil, false, app_errors.ErrValidation
	}
	if request.Scope == CredentialBatchScopeAll {
		if request.Action == CredentialBatchDelete || len(request.CredentialIDs) != 0 {
			return nil, false, app_errors.ErrValidation
		}
		return nil, true, nil
	}
	if request.Scope != "" {
		return nil, false, app_errors.ErrValidation
	}
	if len(request.CredentialIDs) < 1 || len(request.CredentialIDs) > 100 {
		return nil, false, app_errors.ErrValidation
	}
	ids := append([]uint(nil), request.CredentialIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for index, id := range ids {
		if id == 0 || index > 0 && id == ids[index-1] {
			return nil, false, app_errors.ErrValidation
		}
	}
	return ids, false, nil
}

func (s *Service) BatchGroupCredentials(
	ctx context.Context,
	groupID uint,
	request CredentialBatchRequest,
) (CredentialBatchResponse, error) {
	if groupID == 0 {
		return CredentialBatchResponse{}, app_errors.ErrBadRequest
	}
	ids, all, err := normalizeCredentialBatchRequest(request)
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
	rowsQuery := s.db.WithContext(ctx)
	if all {
		rowsQuery = rowsQuery.Where("group_id = ?", groupID).Order("id ASC")
	} else {
		rowsQuery = rowsQuery.Where("id IN ?", ids)
	}
	if err := rowsQuery.Find(&rows).Error; err != nil {
		return CredentialBatchResponse{}, app_errors.ParseDBError(err)
	}
	if all {
		ids = make([]uint, len(rows))
		for index, row := range rows {
			ids[index] = row.ID
		}
		if len(ids) == 0 {
			return CredentialBatchResponse{
				AffectedCredentialIDs: []uint{},
				Summary:               summarizeGroupRuntimeCredentials(group, s.registry.Snapshot(), s.now().UTC()),
			}, nil
		}
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
		if err := validateCredentialRuntimeRow(group, row, view, exists); err != nil {
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
		desired := make([]state.CredentialEntry, len(before))
		for index, entry := range before {
			desired[index] = entry
			if request.Action == CredentialBatchEnable {
				desired[index].Status = state.CredentialStatusActive
			} else if request.Action == CredentialBatchDisable {
				desired[index].Status = state.CredentialStatusDisabled
			}
		}
		persist := func() error {
			return s.withControlTransaction(ctx, func(tx *gorm.DB) error {
				query := tx.Where("group_id = ?", groupID)
				if !all {
					query = query.Where("id IN ?", ids)
				}
				var result *gorm.DB
				switch request.Action {
				case CredentialBatchEnable:
					result = query.Model(&models.Credential{}).Updates(map[string]any{
						"status": models.CredentialStatusActive,
					})
				case CredentialBatchDisable:
					result = query.Model(&models.Credential{}).Updates(map[string]any{
						"status": models.CredentialStatusDisabled,
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
		}
		if s.applyBatchRegistryMutation == nil {
			mutationErr = app_errors.ErrInternalServer
			return
		}
		if request.Action == CredentialBatchEnable {
			// Enabling expands data-plane authority. Persist it before making
			// the credentials routable; disable/delete intentionally keep the
			// safer runtime-first ordering below.
			if mutationErr = persist(); mutationErr != nil {
				return
			}
			if applyErr := s.applyBatchRegistryMutation(groupID, ids, request.Action); applyErr != nil {
				operationErr := withControlOperationContext(
					newControlOperationError(stageApplyCommittedRegistryMutation),
					groupID,
					0,
				)
				mutationErr = joinCommittedRuntimeRecovery(
					errors.Join(operationErr, applyErr),
					s.recoverCommittedCredentialRegistryGroup(ctx, groupID),
				)
				return
			}
			if restoreErr := s.registry.RestoreGroupCredentialEntriesExact(groupID, desired); restoreErr != nil {
				operationErr := withControlOperationContext(
					newControlOperationError(stageApplyCommittedRegistryMutation),
					groupID,
					0,
				)
				mutationErr = joinCommittedRuntimeRecovery(
					errors.Join(operationErr, restoreErr),
					s.recoverCommittedCredentialRegistryGroup(ctx, groupID),
				)
			}
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
		mutationErr = persist()
		if mutationErr != nil {
			mutationErr = compensateCredentialBatchRegistry(s, groupID, before, mutationErr)
			return
		}
		if request.Action == CredentialBatchDelete {
			for _, id := range ids {
				s.stats.Reset(id)
				s.retireCredentialRuntime(id)
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
