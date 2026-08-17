package requestlog

import (
	"context"
	"fmt"
	"math"

	"gorm.io/gorm"

	"gpt-load/internal/platform/epochms"
	"gpt-load/internal/storage/dbtx"
	"gpt-load/internal/storage/models"
)

type CredentialWindowUsageSource string

const (
	CredentialWindowUsageSourceRequestLogs CredentialWindowUsageSource = "request_logs"
	CredentialWindowUsageSourceHourlyStats CredentialWindowUsageSource = "usage_stats"
)

type CredentialWindowUsageQuery struct {
	CredentialID uint
	FromMS       int64
	ToMS         int64
	Source       CredentialWindowUsageSource
}

type CredentialWindowUsage struct {
	UsageAggregate
	Source       CredentialWindowUsageSource
	DataComplete bool
	LastUsedAtMS *int64
}

func (service *Service) QueryCredentialWindowUsage(
	ctx context.Context,
	input CredentialWindowUsageQuery,
) (CredentialWindowUsage, error) {
	if service == nil || service.db == nil {
		return CredentialWindowUsage{}, fmt.Errorf("query credential window usage: database is nil")
	}
	if input.CredentialID == 0 || input.FromMS < 0 || input.ToMS <= input.FromMS {
		return CredentialWindowUsage{}, fmt.Errorf("query credential window usage: invalid scope")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	switch input.Source {
	case CredentialWindowUsageSourceRequestLogs, CredentialWindowUsageSourceHourlyStats:
	default:
		return CredentialWindowUsage{}, fmt.Errorf(
			"query credential window usage: unsupported source %q",
			input.Source,
		)
	}

	var result CredentialWindowUsage
	err := dbtx.Run(ctx, service.db, dbtx.Options{
		Mode:           dbtx.ReadSnapshot,
		CleanupTimeout: usageRollbackTimeout,
		Operation:      "credential window usage read transaction",
	}, func(connection *gorm.DB) error {
		var queryErr error
		switch input.Source {
		case CredentialWindowUsageSourceRequestLogs:
			result, queryErr = service.queryCredentialRequestLogUsage(connection, input)
		case CredentialWindowUsageSourceHourlyStats:
			result, queryErr = service.queryCredentialHourlyUsage(connection, input)
		}
		return queryErr
	})
	if err != nil {
		return CredentialWindowUsage{}, fmt.Errorf("query credential window usage: %w", err)
	}
	return result, nil
}

func (service *Service) queryCredentialRequestLogUsage(
	db *gorm.DB,
	input CredentialWindowUsageQuery,
) (CredentialWindowUsage, error) {
	var row credentialRequestLogUsageRow
	query := db.Model(&models.RequestLog{}).
		Where("credential_id = ?", input.CredentialID).
		Where("completed_at_ms >= ? AND completed_at_ms < ?", input.FromMS, input.ToMS)
	if err := query.Select(credentialRequestLogUsageSelect).Find(&row).Error; err != nil {
		return CredentialWindowUsage{}, fmt.Errorf("query request logs: %w", err)
	}
	if err := validateUsageAggregate(row.UsageAggregate); err != nil {
		return CredentialWindowUsage{}, fmt.Errorf("validate request log usage: %w", err)
	}
	return CredentialWindowUsage{
		UsageAggregate: row.UsageAggregate,
		Source:         CredentialWindowUsageSourceRequestLogs,
		DataComplete:   service.requestLogWindowRetained(input.FromMS),
		LastUsedAtMS:   row.LastUsedAtMS,
	}, nil
}

func (service *Service) queryCredentialHourlyUsage(
	db *gorm.DB,
	input CredentialWindowUsageQuery,
) (CredentialWindowUsage, error) {
	fullHoursFromMS, err := alignHourUp(input.FromMS)
	if err != nil {
		return CredentialWindowUsage{}, fmt.Errorf("align window start: %w", err)
	}
	fullHoursToMS, err := alignHourUp(input.ToMS)
	if err != nil {
		return CredentialWindowUsage{}, fmt.Errorf("align window end: %w", err)
	}
	result := CredentialWindowUsage{
		Source:       CredentialWindowUsageSourceHourlyStats,
		DataComplete: true,
	}
	if fullHoursFromMS < fullHoursToMS {
		scope := db.Model(&models.UsageStat{}).
			Where("credential_id = ?", input.CredentialID).
			Where("bucket_start_ms >= ? AND bucket_start_ms < ?", fullHoursFromMS, fullHoursToMS)
		if err := validateUsageStatIntegrity(scope); err != nil {
			return CredentialWindowUsage{}, err
		}
		aggregate, err := queryUsageSummary(scope)
		if err != nil {
			return CredentialWindowUsage{}, err
		}
		result.UsageAggregate = aggregate
		var latest struct {
			LastUsedAtMS *int64 `gorm:"column:last_used_at_ms"`
		}
		if err := scope.Select("MAX(bucket_start_ms) AS last_used_at_ms").Find(&latest).Error; err != nil {
			return CredentialWindowUsage{}, fmt.Errorf("query latest usage bucket: %w", err)
		}
		result.LastUsedAtMS = latest.LastUsedAtMS
	}

	boundaryToMS := fullHoursFromMS
	if boundaryToMS > input.ToMS {
		boundaryToMS = input.ToMS
	}
	if input.FromMS < boundaryToMS {
		boundary, err := service.queryCredentialRequestLogUsage(db, CredentialWindowUsageQuery{
			CredentialID: input.CredentialID,
			FromMS:       input.FromMS,
			ToMS:         boundaryToMS,
			Source:       CredentialWindowUsageSourceRequestLogs,
		})
		if err != nil {
			return CredentialWindowUsage{}, err
		}
		result.UsageAggregate, err = addUsageAggregates(
			result.UsageAggregate,
			boundary.UsageAggregate,
		)
		if err != nil {
			return CredentialWindowUsage{}, fmt.Errorf("merge boundary usage: %w", err)
		}
		result.DataComplete = result.DataComplete && boundary.DataComplete
		if boundary.LastUsedAtMS != nil &&
			(result.LastUsedAtMS == nil || *boundary.LastUsedAtMS > *result.LastUsedAtMS) {
			result.LastUsedAtMS = boundary.LastUsedAtMS
		}
	}
	return result, nil
}

func (service *Service) requestLogWindowRetained(fromMS int64) bool {
	if service.retentionPolicy == nil {
		return false
	}
	cutoffMS, err := retentionCutoffMS(service.now().UTC().UnixMilli(), service.retentionPolicy.RequestLogRetentionDays())
	return err == nil && fromMS >= cutoffMS
}

func alignHourUp(value int64) (int64, error) {
	aligned, err := epochms.AlignDown(value, epochms.MillisecondsPerHour)
	if err != nil {
		return 0, err
	}
	if aligned == value {
		return value, nil
	}
	if aligned > math.MaxInt64-epochms.MillisecondsPerHour {
		return 0, fmt.Errorf("aligned time overflow")
	}
	return aligned + epochms.MillisecondsPerHour, nil
}

type credentialRequestLogUsageRow struct {
	UsageAggregate
	LastUsedAtMS *int64 `gorm:"column:last_used_at_ms"`
}

const credentialRequestLogUsageSelect = "" +
	"COUNT(*) AS request_count, " +
	"COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END), 0) AS success_count, " +
	"COALESCE(SUM(CASE WHEN status <> 'success' THEN 1 ELSE 0 END), 0) AS failure_count, " +
	"COALESCE(SUM(uncached_input_tokens), 0) AS uncached_input_tokens, " +
	"COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens, " +
	"COALESCE(SUM(cache_write_5m_tokens), 0) AS cache_write5_m_tokens, " +
	"COALESCE(SUM(cache_write_1h_tokens), 0) AS cache_write1_h_tokens, " +
	"COALESCE(SUM(cache_write_unknown_tokens), 0) AS cache_write_unknown_tokens, " +
	"COALESCE(SUM(output_tokens), 0) AS output_tokens, " +
	"COALESCE(SUM(estimated_cost_nano_usd), 0) AS estimated_cost_nano_usd, " +
	"COALESCE(SUM(CASE WHEN usage_state = 'missing' THEN 1 ELSE 0 END), 0) AS usage_missing_count, " +
	"COALESCE(SUM(CASE WHEN usage_state = 'partial' THEN 1 ELSE 0 END), 0) AS partial_count, " +
	"COALESCE(SUM(CASE WHEN cost_state = 'unpriced' THEN 1 ELSE 0 END), 0) AS unpriced_request_count, " +
	"COALESCE(SUM(CASE WHEN pricing_completeness = 'partial' THEN 1 ELSE 0 END), 0) AS pricing_partial_count, " +
	"MAX(completed_at_ms) AS last_used_at_ms"
