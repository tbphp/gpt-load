package requestlog

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"gpt-load/internal/accessquota"
	"gpt-load/internal/storage/dbtx"
	"gpt-load/internal/storage/models"
)

type accessQuotaCheckpointWriter interface {
	WriteSnapshots(context.Context, []accessquota.RestoredState) error
}

type gormAccessQuotaCheckpointWriter struct {
	db *gorm.DB
}

func (writer *gormAccessQuotaCheckpointWriter) WriteSnapshots(
	ctx context.Context,
	snapshots []accessquota.RestoredState,
) error {
	if len(snapshots) == 0 {
		return nil
	}
	if writer == nil || writer.db == nil {
		return fmt.Errorf("write access key cost limit checkpoints: database is nil")
	}
	return dbtx.Run(ctx, writer.db, dbtx.Options{
		Mode:           dbtx.Write,
		CleanupTimeout: requestLogTransactionCleanupTimeout,
		Operation:      "access key cost limit checkpoint transaction",
	}, func(tx *gorm.DB) error {
		for _, snapshot := range snapshots {
			if _, err := persistAccessQuotaSnapshot(tx, snapshot); err != nil {
				return err
			}
		}
		return nil
	})
}

type checkpointPersistOutcome string

const (
	checkpointPersistApplied        checkpointPersistOutcome = "applied"
	checkpointPersistAlreadyCurrent checkpointPersistOutcome = "already_current"
	checkpointPersistStaleRevision  checkpointPersistOutcome = "stale_revision"
	checkpointPersistRuleDeleted    checkpointPersistOutcome = "rule_deleted"
)

func persistAccessQuotaSnapshot(
	tx *gorm.DB,
	snapshot accessquota.RestoredState,
) (checkpointPersistOutcome, error) {
	if snapshot.AccessKeyID == 0 || snapshot.RuleID == 0 || snapshot.RuleRevision == 0 ||
		snapshot.SnapshotVersion == 0 || snapshot.UsedNanoUSD < 0 ||
		(snapshot.WindowStartedAtMS == nil) != (snapshot.WindowEndsAtMS == nil) {
		return "", fmt.Errorf("persist access key cost limit rule %d: invalid snapshot", snapshot.RuleID)
	}
	result := tx.Model(&models.AccessKeyCostLimitState{}).
		Where(
			"rule_id = ? AND rule_revision = ? AND snapshot_version < ?",
			snapshot.RuleID,
			snapshot.RuleRevision,
			snapshot.SnapshotVersion,
		).
		Updates(map[string]any{
			"used_nano_usd":        snapshot.UsedNanoUSD,
			"window_started_at_ms": snapshot.WindowStartedAtMS,
			"window_ends_at_ms":    snapshot.WindowEndsAtMS,
			"window_generation":    snapshot.WindowGeneration,
			"snapshot_version":     snapshot.SnapshotVersion,
		})
	if result.Error != nil {
		return "", fmt.Errorf("update access key cost limit rule %d checkpoint: %w", snapshot.RuleID, result.Error)
	}
	if result.RowsAffected == 1 {
		return checkpointPersistApplied, nil
	}
	if result.RowsAffected != 0 {
		return "", fmt.Errorf("update access key cost limit rule %d checkpoint: changed %d rows", snapshot.RuleID, result.RowsAffected)
	}

	var rule models.AccessKeyCostLimitRule
	if err := tx.Select("id", "access_key_id", "rule_revision").First(&rule, snapshot.RuleID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return checkpointPersistRuleDeleted, nil
		}
		return "", fmt.Errorf("classify access key cost limit rule %d: %w", snapshot.RuleID, err)
	}
	if rule.AccessKeyID != snapshot.AccessKeyID {
		return "", fmt.Errorf("classify access key cost limit rule %d: access key mismatch", snapshot.RuleID)
	}
	if rule.RuleRevision != snapshot.RuleRevision {
		return checkpointPersistStaleRevision, nil
	}

	var state models.AccessKeyCostLimitState
	if err := tx.First(&state, snapshot.RuleID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", fmt.Errorf("classify access key cost limit rule %d: state is missing", snapshot.RuleID)
		}
		return "", fmt.Errorf("classify access key cost limit rule %d state: %w", snapshot.RuleID, err)
	}
	if state.RuleRevision != snapshot.RuleRevision {
		return "", fmt.Errorf("classify access key cost limit rule %d: state revision mismatch", snapshot.RuleID)
	}
	if state.SnapshotVersion >= snapshot.SnapshotVersion {
		return checkpointPersistAlreadyCurrent, nil
	}
	return "", fmt.Errorf(
		"classify access key cost limit rule %d: database version %d is behind incoming %d after no-op update",
		snapshot.RuleID,
		state.SnapshotVersion,
		snapshot.SnapshotVersion,
	)
}
