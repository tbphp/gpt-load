package control

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"gpt-load/internal/platform/canonicaljson"
	"gpt-load/internal/platform/epochms"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/storage/models"
)

const (
	operationResultRetention        = 7 * 24 * time.Hour
	operationRecoveryInitialBackoff = 250 * time.Millisecond
	operationRecoveryMaximumBackoff = 30 * time.Second
	operationCompactionInterval     = time.Hour
)

type recoveryPendingData struct {
	OperationID   string        `json:"operation_id"`
	OperationKind operationKind `json:"operation_kind"`
	FailedStage   string        `json:"failed_stage"`
	RetryAfterMS  int64         `json:"retry_after_ms"`
}

// DrainCommittedOperations restores all known post-commit side effects in
// commit order. It is called during startup before the process listens.
func (s *Service) DrainCommittedOperations(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.recoverPendingOperationsLocked(ctx, 0)
	return err
}

func (s *Service) recoverPendingOperationsLocked(
	ctx context.Context,
	beforeCommitSequence uint64,
) (*models.ControlOperation, error) {
	query := s.db.WithContext(ctx).
		Where("completed_at_ms IS NULL").
		Order("commit_sequence ASC")
	if beforeCommitSequence != 0 {
		query = query.Where("commit_sequence < ?", beforeCommitSequence)
	}
	var operations []models.ControlOperation
	if err := query.Find(&operations).Error; err != nil {
		return nil, app_errors.ParseDBError(err)
	}
	for index := range operations {
		operation := &operations[index]
		if err := s.recoverOperationLocked(ctx, operation); err != nil {
			return operation, err
		}
	}
	return nil, nil
}

func (s *Service) enforceOperationRecoveryBarrierLocked(
	ctx context.Context,
	beforeCommitSequence uint64,
) error {
	blocked, err := s.recoverPendingOperationsLocked(ctx, beforeCommitSequence)
	if err == nil {
		return nil
	}
	if blocked == nil {
		return err
	}
	return s.recoveryPendingError(*blocked)
}

func (s *Service) recoveryPendingError(
	operation models.ControlOperation,
) error {
	return app_errors.NewAPIErrorWithData(
		app_errors.ErrControlRecoveryPending,
		recoveryPendingData{
			OperationID:   operation.OperationID,
			OperationKind: operationKind(operation.OperationKind),
			FailedStage:   operation.FailedStage,
			RetryAfterMS:  operationRecoveryInitialBackoff.Milliseconds(),
		},
	)
}

// CompactCompletedOperations removes replay payloads after the guaranteed
// retention period while preserving the permanent request comparator.
func (s *Service) CompactCompletedOperations(
	ctx context.Context,
	now time.Time,
) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.compactCompletedOperationsLocked(ctx, now)
}

func (s *Service) compactCompletedOperationsLocked(
	ctx context.Context,
	now time.Time,
) (int64, error) {
	now = now.UTC()
	cutoff := now.Add(-operationResultRetention)
	nowMS, err := epochms.FromTime(now)
	if err != nil {
		return 0, app_errors.ErrInternalServer
	}
	cutoffMS, err := epochms.FromTime(cutoff)
	if err != nil {
		cutoffMS = 0
	}
	var rowsAffected int64
	err = s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&models.ControlOperation{}).
			Where("completed_at_ms IS NOT NULL").
			Where("completed_at_ms <= ?", cutoffMS).
			Where("compacted_at_ms IS NULL").
			Updates(map[string]any{
				"canonical_result":     gorm.Expr("NULL"),
				"required_stages":      gorm.Expr("NULL"),
				"last_completed_stage": "",
				"failed_stage":         "",
				"compacted_at_ms":      nowMS,
				"updated_at_ms":        nowMS,
			})
		if result.Error != nil {
			return app_errors.ParseDBError(result.Error)
		}
		rowsAffected = result.RowsAffected
		return nil
	})
	return rowsAffected, err
}

// RunOperationRecovery drains failures signaled by mutation requests and
// periodically compacts terminal results. Retries are bounded and stop with
// the application context.
func (s *Service) RunOperationRecovery(ctx context.Context) {
	compactionTicker := time.NewTicker(operationCompactionInterval)
	defer compactionTicker.Stop()

	var retryTimer *time.Timer
	var retry <-chan time.Time
	backoff := operationRecoveryInitialBackoff
	stopRetry := func() {
		if retryTimer != nil {
			if !retryTimer.Stop() {
				select {
				case <-retryTimer.C:
				default:
				}
			}
		}
		retryTimer = nil
		retry = nil
	}
	defer stopRetry()

	drain := func() {
		if err := s.DrainCommittedOperations(ctx); err != nil {
			utils.LogPlaneBestEffort(
				logrus.StandardLogger(),
				logrus.WarnLevel,
				utils.LogPlaneControl,
				logrus.Fields{
					"retry_after_ms": backoff.Milliseconds(),
				},
				"Operation recovery remains pending",
			)
			stopRetry()
			retryTimer = time.NewTimer(backoff)
			retry = retryTimer.C
			backoff = nextOperationRecoveryBackoff(backoff)
			return
		}
		stopRetry()
		backoff = operationRecoveryInitialBackoff
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.operationRecoveryWake:
			drain()
		case <-retry:
			retryTimer = nil
			retry = nil
			drain()
		case now := <-compactionTicker.C:
			if _, err := s.CompactCompletedOperations(ctx, now); err != nil &&
				ctx.Err() == nil {
				utils.LogPlaneBestEffort(
					logrus.StandardLogger(),
					logrus.WarnLevel,
					utils.LogPlaneControl,
					nil,
					"Operation result compaction failed",
				)
			}
		}
	}
}

func nextOperationRecoveryBackoff(current time.Duration) time.Duration {
	if current >= operationRecoveryMaximumBackoff/2 {
		return operationRecoveryMaximumBackoff
	}
	return current * 2
}

func (s *Service) wakeOperationRecovery() {
	if s.operationRecoveryWake == nil {
		return
	}
	select {
	case s.operationRecoveryWake <- struct{}{}:
	default:
	}
}

func validateRecoverableOperation(operation *models.ControlOperation) error {
	if operation == nil ||
		operation.DigestVersion != 1 ||
		len(operation.RequestDigest) != 32 ||
		!operationKind(operation.OperationKind).valid() ||
		operation.CompactedAtMS != nil {
		return fmt.Errorf("invalid durable operation comparator: %w", app_errors.ErrInternalServer)
	}
	if err := validateIdempotencyKey(operation.OperationID); err != nil {
		return fmt.Errorf("invalid durable operation id: %w", app_errors.ErrInternalServer)
	}
	if err := validateIdempotencyKey(operation.IdempotencyKey); err != nil {
		return fmt.Errorf("invalid durable idempotency key: %w", app_errors.ErrInternalServer)
	}
	if err := validateOperationResourceIdentity(
		operationKind(operation.OperationKind),
		operation.ResourceIdentity,
	); err != nil {
		return fmt.Errorf("invalid durable resource identity: %w", app_errors.ErrInternalServer)
	}
	if len(operation.CanonicalResult) == 0 {
		return fmt.Errorf("missing durable operation result: %w", app_errors.ErrInternalServer)
	}
	canonical, err := canonicaljson.Canonicalize(operation.CanonicalResult)
	if err != nil || !bytes.Equal(canonical, operation.CanonicalResult) {
		return fmt.Errorf("invalid durable operation result: %w", app_errors.ErrInternalServer)
	}
	return nil
}

func operationGroupID(operation *models.ControlOperation) (uint, error) {
	kind := operationKind(operation.OperationKind)
	if kind != operationKindGroupCreate &&
		kind != operationKindCredentialImport {
		return 0, fmt.Errorf("operation %q has no group registry stage", kind)
	}
	return parseResourceIdentity(operation.ResourceIdentity, "group")
}

func validateOperationResourceIdentity(kind operationKind, identity string) error {
	switch kind {
	case operationKindAccessKeyCreate:
		_, err := parseResourceIdentity(identity, "access-key")
		return err
	case operationKindGroupCreate, operationKindCredentialImport:
		_, err := parseResourceIdentity(identity, "group")
		return err
	default:
		return fmt.Errorf("unsupported operation kind")
	}
}

func parseResourceIdentity(identity, prefix string) (uint, error) {
	wantPrefix := prefix + ":"
	if !strings.HasPrefix(identity, wantPrefix) {
		return 0, fmt.Errorf("resource identity must start with %q", wantPrefix)
	}
	raw := strings.TrimPrefix(identity, wantPrefix)
	if raw == "" || raw[0] == '0' {
		return 0, fmt.Errorf("resource identity id must be canonical base10")
	}
	value, err := strconv.ParseUint(raw, 10, strconv.IntSize)
	if err != nil || value == 0 ||
		strconv.FormatUint(value, 10) != raw {
		return 0, fmt.Errorf("resource identity id must be canonical base10")
	}
	return uint(value), nil
}
