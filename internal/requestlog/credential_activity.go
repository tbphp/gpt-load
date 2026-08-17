package requestlog

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"gpt-load/internal/execution"
	"gpt-load/internal/platform/epochms"
	"gpt-load/internal/storage/dbtx"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

const (
	maxCredentialActivityIDs      = 100
	credentialActivityMaxWindowMS = 24 * epochms.MillisecondsPerHour
)

type CredentialActivityQuery struct {
	CredentialIDs []uint
	FromMS        int64
	ToMS          int64
}

type CredentialActivity struct {
	CredentialID uint
	LastUsedAtMS *int64
	SuccessCount int64
	FailureCount int64
	DataComplete bool
}

// QueryCredentialActivity returns the account-card activity for the requested
// credentials without issuing one query per account. Full hours come from the
// durable hourly aggregate; only the oldest partial hour reads attempt rows.
func (service *Service) QueryCredentialActivity(
	ctx context.Context,
	input CredentialActivityQuery,
) (map[uint]CredentialActivity, error) {
	if service == nil || service.db == nil {
		return nil, fmt.Errorf("query credential activity: database is nil")
	}
	credentialIDs, err := validateCredentialActivityQuery(input)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	hourlyFromMS, err := alignHourUp(input.FromMS)
	if err != nil {
		return nil, fmt.Errorf("query credential activity: align window start: %w", err)
	}
	hourlyToMS, err := alignHourUp(input.ToMS)
	if err != nil {
		return nil, fmt.Errorf("query credential activity: align window end: %w", err)
	}
	boundaryToMS := hourlyFromMS
	if boundaryToMS > input.ToMS {
		boundaryToMS = input.ToMS
	}
	hasHourlyWindow := hourlyFromMS < hourlyToMS
	hasBoundaryWindow := input.FromMS < boundaryToMS
	dataComplete := (!hasHourlyWindow || service.hourlyWindowRetained(hourlyFromMS)) &&
		(!hasBoundaryWindow || service.requestLogWindowRetained(input.FromMS))

	result := make(map[uint]CredentialActivity, len(credentialIDs))
	for _, credentialID := range credentialIDs {
		result[credentialID] = CredentialActivity{
			CredentialID: credentialID,
			DataComplete: dataComplete,
		}
	}

	err = dbtx.Run(ctx, service.db, dbtx.Options{
		Mode:           dbtx.ReadSnapshot,
		CleanupTimeout: usageRollbackTimeout,
		Operation:      "credential activity read transaction",
	}, func(connection *gorm.DB) error {
		if err := queryCredentialLatestActivity(
			connection, credentialIDs, input.ToMS, result,
		); err != nil {
			return err
		}
		if hasHourlyWindow {
			if err := queryCredentialHourlyActivity(
				connection, credentialIDs, hourlyFromMS, hourlyToMS, result,
			); err != nil {
				return err
			}
		}
		if hasBoundaryWindow {
			if err := queryCredentialBoundaryActivity(
				connection, credentialIDs, input.FromMS, boundaryToMS, result,
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("query credential activity: %w", err)
	}
	return result, nil
}

func validateCredentialActivityQuery(input CredentialActivityQuery) ([]uint, error) {
	if len(input.CredentialIDs) == 0 || len(input.CredentialIDs) > maxCredentialActivityIDs ||
		input.FromMS < 0 || input.ToMS <= input.FromMS ||
		input.ToMS-input.FromMS > credentialActivityMaxWindowMS {
		return nil, fmt.Errorf("query credential activity: invalid scope")
	}
	credentialIDs := make([]uint, 0, len(input.CredentialIDs))
	seen := make(map[uint]struct{}, len(input.CredentialIDs))
	for _, credentialID := range input.CredentialIDs {
		if credentialID == 0 {
			return nil, fmt.Errorf("query credential activity: invalid scope")
		}
		if _, exists := seen[credentialID]; exists {
			continue
		}
		seen[credentialID] = struct{}{}
		credentialIDs = append(credentialIDs, credentialID)
	}
	return credentialIDs, nil
}

type credentialLatestActivityRow struct {
	CredentialID uint   `gorm:"column:credential_id"`
	LastUsedAtMS *int64 `gorm:"column:last_used_at_ms"`
}

func queryCredentialLatestActivity(
	db *gorm.DB,
	credentialIDs []uint,
	toMS int64,
	result map[uint]CredentialActivity,
) error {
	var query strings.Builder
	args := make([]any, 0, len(credentialIDs)*3)
	for index, credentialID := range credentialIDs {
		if index > 0 {
			query.WriteString(" UNION ALL ")
		}
		query.WriteString(`SELECT ? AS credential_id, (
			SELECT completed_at_ms
			FROM request_log_attempts
			WHERE credential_id = ? AND completed_at_ms < ? AND dispatch_state <> ?
			ORDER BY completed_at_ms DESC
			LIMIT 1
		) AS last_used_at_ms`)
		args = append(args, credentialID, credentialID, toMS, string(execution.DispatchLocal))
	}
	var rows []credentialLatestActivityRow
	if err := db.Raw(query.String(), args...).Scan(&rows).Error; err != nil {
		return fmt.Errorf("query latest credential attempts: %w", err)
	}
	for _, row := range rows {
		activity, exists := result[row.CredentialID]
		if !exists {
			continue
		}
		activity.LastUsedAtMS = row.LastUsedAtMS
		result[row.CredentialID] = activity
	}
	return nil
}

type credentialActivityCountRow struct {
	CredentialID uint  `gorm:"column:credential_id"`
	SuccessCount int64 `gorm:"column:success_count"`
	FailureCount int64 `gorm:"column:failure_count"`
}

func queryCredentialHourlyActivity(
	db *gorm.DB,
	credentialIDs []uint,
	fromMS int64,
	toMS int64,
	result map[uint]CredentialActivity,
) error {
	var rows []credentialActivityCountRow
	if err := db.Model(&models.CredentialAttemptStat{}).
		Select("credential_id, COALESCE(SUM(success_count), 0) AS success_count, "+
			"COALESCE(SUM(failure_count), 0) AS failure_count").
		Where("credential_id IN ?", credentialIDs).
		Where("bucket_start_ms >= ? AND bucket_start_ms < ?", fromMS, toMS).
		Group("credential_id").
		Find(&rows).Error; err != nil {
		return fmt.Errorf("query hourly credential attempts: %w", err)
	}
	return mergeCredentialActivityCounts(result, rows)
}

func queryCredentialBoundaryActivity(
	db *gorm.DB,
	credentialIDs []uint,
	fromMS int64,
	toMS int64,
	result map[uint]CredentialActivity,
) error {
	var rows []credentialActivityCountRow
	if err := db.Model(&models.RequestLogAttempt{}).
		Select("credential_id, "+
			"COALESCE(SUM(CASE WHEN failure_category = ? THEN 1 ELSE 0 END), 0) AS success_count, "+
			"COALESCE(SUM(CASE WHEN failure_category <> ? AND failure_category <> ? THEN 1 ELSE 0 END), 0) AS failure_count",
			string(telemetry.FailureCategoryOK),
			string(telemetry.FailureCategoryOK),
			string(telemetry.FailureCategoryDownstreamCancel),
		).
		Where("credential_id IN ?", credentialIDs).
		Where("dispatch_state <> ?", string(execution.DispatchLocal)).
		Where("completed_at_ms >= ? AND completed_at_ms < ?", fromMS, toMS).
		Group("credential_id").
		Find(&rows).Error; err != nil {
		return fmt.Errorf("query credential attempt boundary: %w", err)
	}
	return mergeCredentialActivityCounts(result, rows)
}

func mergeCredentialActivityCounts(
	result map[uint]CredentialActivity,
	rows []credentialActivityCountRow,
) error {
	for _, row := range rows {
		activity, exists := result[row.CredentialID]
		if !exists {
			continue
		}
		if row.SuccessCount < 0 || row.FailureCount < 0 {
			return fmt.Errorf("validate credential activity: negative count")
		}
		successCount, ok := usage.CheckedAdd(activity.SuccessCount, row.SuccessCount)
		if !ok {
			return fmt.Errorf("aggregate credential activity success count: checked addition failed")
		}
		failureCount, ok := usage.CheckedAdd(activity.FailureCount, row.FailureCount)
		if !ok {
			return fmt.Errorf("aggregate credential activity failure count: checked addition failed")
		}
		activity.SuccessCount = successCount
		activity.FailureCount = failureCount
		result[row.CredentialID] = activity
	}
	return nil
}
