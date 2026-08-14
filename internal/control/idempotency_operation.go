package control

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"gorm.io/gorm"

	"gpt-load/internal/platform/canonicaljson"
	"gpt-load/internal/platform/epochms"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

type operationStage string

const (
	operationStageDBCommitted       operationStage = "db_committed"
	operationStagePricesPublished   operationStage = "prices_published"
	operationStageRegistryApplied   operationStage = "registry_applied"
	operationStageSnapshotPublished operationStage = "snapshot_published"
	operationStageCompleted         operationStage = "completed"
)

type operationTransaction = *gorm.DB

type idempotentMutationResult struct {
	ResourceIdentity string
	CanonicalResult  []byte
	Ephemeral        any
}

type idempotentOperationInput struct {
	IdempotencyKey       string
	DigestVersion        uint
	RequestDigest        [32]byte
	Kind                 operationKind
	CredentialMutationID uint
	PrepareMutation      func()
	Mutate               func(operationTransaction) (idempotentMutationResult, error)
}

type idempotentOperationResult struct {
	OperationID      string
	ResourceIdentity string
	CanonicalResult  []byte
	Replayed         bool
	Ephemeral        any
}

type operationErrorData struct {
	OperationID   string        `json:"operation_id"`
	OperationKind operationKind `json:"operation_kind"`
}

type operationExpiredData struct {
	OperationID      string        `json:"operation_id"`
	OperationKind    operationKind `json:"operation_kind"`
	ResourceIdentity string        `json:"resource_identity"`
	CompletedAtMS    int64         `json:"completed_at_ms"`
}

func operationRequiredStages(kind operationKind) ([]operationStage, error) {
	var stages []operationStage
	switch kind {
	case operationKindAccessKeyCreate:
		stages = []operationStage{
			operationStageDBCommitted,
			operationStageSnapshotPublished,
			operationStageCompleted,
		}
	case operationKindGroupCreate:
		stages = []operationStage{
			operationStageDBCommitted,
			operationStagePricesPublished,
			operationStageRegistryApplied,
			operationStageSnapshotPublished,
			operationStageCompleted,
		}
	case operationKindCredentialImport:
		stages = []operationStage{
			operationStageDBCommitted,
			operationStageRegistryApplied,
			operationStageCompleted,
		}
	default:
		return nil, fmt.Errorf("unsupported control operation kind %q", kind)
	}
	return append([]operationStage(nil), stages...), nil
}

func validateIdempotencyKey(value string) error {
	if len(value) != 36 ||
		value[8] != '-' ||
		value[13] != '-' ||
		value[18] != '-' ||
		value[23] != '-' ||
		value[14] != '4' {
		return fmt.Errorf("idempotency key must be a canonical lowercase UUID v4")
	}
	switch value[19] {
	case '8', '9', 'a', 'b':
	default:
		return fmt.Errorf("idempotency key must be a canonical lowercase UUID v4")
	}
	for index, character := range []byte(value) {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return fmt.Errorf("idempotency key must be a canonical lowercase UUID v4")
		}
	}
	return nil
}

func newOperationID(random io.Reader) (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", fmt.Errorf("generate operation id: %w", err)
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" +
		encoded[8:12] + "-" +
		encoded[12:16] + "-" +
		encoded[16:20] + "-" +
		encoded[20:32], nil
}

func (s *Service) executeIdempotentOperation(
	ctx context.Context,
	input idempotentOperationInput,
) (idempotentOperationResult, error) {
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return idempotentOperationResult{}, app_errors.ErrInvalidIdempotencyKey
	}
	if input.DigestVersion != 1 || !input.Kind.valid() || input.Mutate == nil {
		return idempotentOperationResult{}, app_errors.ErrInternalServer
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var existing models.ControlOperation
	query := s.db.WithContext(ctx).
		Where("idempotency_key = ?", input.IdempotencyKey).
		Take(&existing)
	if query.Error == nil {
		if err := validateOperationComparator(&existing, input); err != nil {
			return idempotentOperationResult{}, err
		}
		if recoveryErr := s.enforceOperationRecoveryBarrierLocked(
			ctx,
			existing.CommitSequence,
		); recoveryErr != nil {
			return idempotentOperationResult{}, recoveryErr
		}
		return s.replayIdempotentOperationLocked(ctx, &existing, input)
	}
	if !errors.Is(query.Error, gorm.ErrRecordNotFound) {
		return idempotentOperationResult{}, app_errors.ParseDBError(query.Error)
	}
	if recoveryErr := s.enforceOperationRecoveryBarrierLocked(ctx, 0); recoveryErr != nil {
		return idempotentOperationResult{}, recoveryErr
	}
	if input.PrepareMutation != nil {
		input.PrepareMutation()
	}

	requiredStages, err := operationRequiredStages(input.Kind)
	if err != nil {
		return idempotentOperationResult{}, app_errors.ErrInternalServer
	}
	encodedStages, err := json.Marshal(requiredStages)
	if err != nil {
		return idempotentOperationResult{}, app_errors.ErrInternalServer
	}
	operationID, err := newOperationID(s.operationRandom)
	if err != nil {
		return idempotentOperationResult{}, app_errors.ErrInternalServer
	}

	var mutationResult idempotentMutationResult
	var operation models.ControlOperation
	mutate := func() {
		err = s.withControlTransaction(ctx, func(tx *gorm.DB) error {
			var mutationErr error
			mutationResult, mutationErr = input.Mutate(tx)
			if mutationErr != nil {
				return mutationErr
			}
			if mutationResult.ResourceIdentity == "" || len(mutationResult.CanonicalResult) == 0 {
				return fmt.Errorf(
					"idempotent mutation result is incomplete: %w",
					app_errors.ErrInternalServer,
				)
			}
			canonicalResult, canonicalErr := canonicaljson.Canonicalize(
				mutationResult.CanonicalResult,
			)
			if canonicalErr != nil || !bytes.Equal(canonicalResult, mutationResult.CanonicalResult) {
				return fmt.Errorf(
					"idempotent mutation result is not canonical: %w",
					app_errors.ErrInternalServer,
				)
			}
			nowMS, timeErr := epochms.FromTime(s.now())
			if timeErr != nil {
				return app_errors.ErrInternalServer
			}
			operation = models.ControlOperation{
				OperationID:        operationID,
				IdempotencyKey:     input.IdempotencyKey,
				DigestVersion:      input.DigestVersion,
				RequestDigest:      append([]byte(nil), input.RequestDigest[:]...),
				OperationKind:      string(input.Kind),
				ResourceIdentity:   mutationResult.ResourceIdentity,
				CanonicalResult:    append([]byte(nil), mutationResult.CanonicalResult...),
				RequiredStages:     models.JSON(encodedStages),
				LastCompletedStage: string(operationStageDBCommitted),
				CreatedAtMS:        nowMS,
				UpdatedAtMS:        nowMS,
			}
			if err := tx.Create(&operation).Error; err != nil {
				return app_errors.ParseDBError(err)
			}
			return nil
		})
	}
	if input.CredentialMutationID != 0 && s.mutations != nil {
		s.mutations.Do(input.CredentialMutationID, mutate)
	} else {
		mutate()
	}
	if err != nil {
		return idempotentOperationResult{}, err
	}

	if err := s.recoverOperationLocked(ctx, &operation); err != nil {
		s.wakeOperationRecovery()
		return idempotentOperationResult{}, s.operationIncompleteError(operation)
	}
	return idempotentOperationResult{
		OperationID:      operation.OperationID,
		ResourceIdentity: operation.ResourceIdentity,
		CanonicalResult:  append([]byte(nil), operation.CanonicalResult...),
		Ephemeral:        mutationResult.Ephemeral,
	}, nil
}

func (s *Service) replayIdempotentOperationLocked(
	ctx context.Context,
	operation *models.ControlOperation,
	input idempotentOperationInput,
) (idempotentOperationResult, error) {
	if err := validateOperationComparator(operation, input); err != nil {
		return idempotentOperationResult{}, err
	}
	if operation.CompactedAtMS != nil {
		if operation.CompletedAtMS == nil {
			return idempotentOperationResult{}, app_errors.ErrInternalServer
		}
		if err := validateSafeMilliseconds(*operation.CompletedAtMS); err != nil {
			return idempotentOperationResult{}, app_errors.ErrInternalServer
		}
		return idempotentOperationResult{}, app_errors.NewAPIErrorWithData(
			app_errors.ErrIdempotencyResultExpired,
			operationExpiredData{
				OperationID:      operation.OperationID,
				OperationKind:    operationKind(operation.OperationKind),
				ResourceIdentity: operation.ResourceIdentity,
				CompletedAtMS:    *operation.CompletedAtMS,
			},
		)
	}
	if operation.LastCompletedStage != string(operationStageCompleted) {
		if err := s.recoverOperationLocked(ctx, operation); err != nil {
			s.wakeOperationRecovery()
			return idempotentOperationResult{}, s.operationIncompleteError(*operation)
		}
	}
	if operation.CompletedAtMS == nil || len(operation.CanonicalResult) == 0 {
		return idempotentOperationResult{}, app_errors.ErrInternalServer
	}
	return idempotentOperationResult{
		OperationID:      operation.OperationID,
		ResourceIdentity: operation.ResourceIdentity,
		CanonicalResult:  append([]byte(nil), operation.CanonicalResult...),
		Replayed:         true,
	}, nil
}

func validateOperationComparator(
	operation *models.ControlOperation,
	input idempotentOperationInput,
) error {
	if operation.DigestVersion != 1 ||
		input.DigestVersion != 1 ||
		len(operation.RequestDigest) != len(input.RequestDigest) ||
		!operationKind(operation.OperationKind).valid() {
		return app_errors.ErrInternalServer
	}
	if subtle.ConstantTimeCompare(operation.RequestDigest, input.RequestDigest[:]) != 1 {
		return app_errors.NewAPIErrorWithData(
			app_errors.ErrIdempotencyKeyReused,
			operationErrorData{
				OperationID:   operation.OperationID,
				OperationKind: operationKind(operation.OperationKind),
			},
		)
	}
	return nil
}

func (s *Service) recoverOperationLocked(
	ctx context.Context,
	operation *models.ControlOperation,
) error {
	if err := validateRecoverableOperation(operation); err != nil {
		return err
	}
	kind := operationKind(operation.OperationKind)
	wantStages, err := operationRequiredStages(kind)
	if err != nil {
		return err
	}
	var storedStages []operationStage
	if err := json.Unmarshal(operation.RequiredStages, &storedStages); err != nil ||
		!sameOperationStages(storedStages, wantStages) {
		return fmt.Errorf("invalid durable operation stage plan")
	}
	currentIndex := -1
	for index, stage := range storedStages {
		if string(stage) == operation.LastCompletedStage {
			currentIndex = index
			break
		}
	}
	if currentIndex < 0 {
		return fmt.Errorf("invalid durable operation current stage")
	}

	for index := currentIndex + 1; index < len(storedStages); index++ {
		stage := storedStages[index]
		var stageErr error
		switch stage {
		case operationStagePricesPublished:
			var table *pricing.Table
			table, stageErr = loadPriceTable(ctx, s.db)
			if stageErr == nil {
				s.priceRuntime.Publish(table)
			}
		case operationStageRegistryApplied:
			stageErr = s.recoverRegistryOperation(ctx, operation)
		case operationStageSnapshotPublished:
			compileInput, inputErr := stateloader.BuildCompileInput(ctx, s.db, s.channelRegistry)
			if inputErr != nil {
				stageErr = inputErr
			} else {
				var matches bool
				matches, stageErr = s.manager.Matches(compileInput)
				if stageErr == nil && !matches {
					_, stageErr = s.publishSnapshot(compileInput)
				}
			}
		case operationStageCompleted:
		default:
			stageErr = fmt.Errorf("unsupported operation stage %q", stage)
		}
		if stageErr != nil {
			_ = s.recordOperationFailureLocked(ctx, operation, stage)
			return fmt.Errorf("recover operation stage %s", stage)
		}
		if err := s.advanceOperationStageLocked(ctx, operation, stage); err != nil {
			_ = s.recordOperationFailureLocked(ctx, operation, stage)
			return err
		}
	}
	return nil
}

func (s *Service) recoverRegistryOperation(
	ctx context.Context,
	operation *models.ControlOperation,
) error {
	groupID, err := operationGroupID(operation)
	if err != nil {
		return err
	}
	var credentialIDs []uint
	if err := s.db.WithContext(ctx).Model(&models.Credential{}).
		Where("group_id = ?", groupID).Order("id ASC").Pluck("id", &credentialIDs).Error; err != nil {
		return app_errors.ParseDBError(err)
	}
	var reconcileErr error
	reconcile := func() {
		entries, buildErr := stateloader.BuildGroupCredentialEntries(ctx, s.db, groupID)
		if buildErr != nil {
			reconcileErr = buildErr
			return
		}
		if s.registry.MatchesGroup(groupID, entries) {
			return
		}
		_, reconcileErr = s.reconcileRegistryGroup(groupID, entries)
	}
	if err := s.doCredentialMutations(credentialIDs, reconcile); err != nil {
		return err
	}
	return reconcileErr
}

func (s *Service) advanceOperationStageLocked(
	ctx context.Context,
	operation *models.ControlOperation,
	stage operationStage,
) error {
	if s.beforeAdvanceOperationStage != nil {
		if err := s.beforeAdvanceOperationStage(ctx, operation, stage); err != nil {
			return err
		}
	}
	previous := operation.LastCompletedStage
	nowMS, timeErr := epochms.FromTime(s.now())
	if timeErr != nil {
		return app_errors.ErrInternalServer
	}
	updates := map[string]any{
		"last_completed_stage": string(stage),
		"failed_stage":         "",
		"updated_at_ms":        nowMS,
	}
	if stage == operationStageCompleted {
		updates["completed_at_ms"] = nowMS
	}
	err := s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&models.ControlOperation{}).
			Where(
				"commit_sequence = ? AND last_completed_stage = ?",
				operation.CommitSequence,
				previous,
			).
			Updates(updates)
		if result.Error != nil {
			return app_errors.ParseDBError(result.Error)
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("advance operation stage: durable stage changed")
		}
		return nil
	})
	if err != nil {
		return err
	}
	operation.LastCompletedStage = string(stage)
	operation.FailedStage = ""
	operation.UpdatedAtMS = nowMS
	if stage == operationStageCompleted {
		operation.CompletedAtMS = &nowMS
	}
	return nil
}

func (s *Service) recordOperationFailureLocked(
	ctx context.Context,
	operation *models.ControlOperation,
	stage operationStage,
) error {
	nowMS, timeErr := epochms.FromTime(s.now())
	if timeErr != nil {
		return app_errors.ErrInternalServer
	}
	err := s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Model(&models.ControlOperation{}).
			Where("commit_sequence = ?", operation.CommitSequence).
			Updates(map[string]any{
				"failed_stage":  string(stage),
				"updated_at_ms": nowMS,
			}).Error
	})
	if err == nil {
		operation.FailedStage = string(stage)
		operation.UpdatedAtMS = nowMS
	}
	return err
}

func (s *Service) operationIncompleteError(
	operation models.ControlOperation,
) error {
	return app_errors.NewAPIErrorWithData(
		app_errors.ErrControlOperationIncomplete,
		struct {
			OperationID        string        `json:"operation_id"`
			OperationKind      operationKind `json:"operation_kind"`
			LastCompletedStage string        `json:"last_completed_stage"`
			FailedStage        string        `json:"failed_stage"`
			CanReconcile       bool          `json:"can_reconcile"`
		}{
			OperationID:        operation.OperationID,
			OperationKind:      operationKind(operation.OperationKind),
			LastCompletedStage: operation.LastCompletedStage,
			FailedStage:        operation.FailedStage,
			CanReconcile:       true,
		},
	)
}

func sameOperationStages(left, right []operationStage) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
