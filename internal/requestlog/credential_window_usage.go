package requestlog

import (
	"context"
	"fmt"
	"math"

	"gorm.io/gorm"

	"gpt-load/internal/execution"
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

type CredentialModelUsage struct {
	Model string
	UsageAggregate
	LastUsedAtMS *int64
}

type CredentialWindowUsageByModel struct {
	Source       CredentialWindowUsageSource
	DataComplete bool
	Models       []CredentialModelUsage
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

func (service *Service) QueryCredentialWindowUsageByModel(
	ctx context.Context,
	input CredentialWindowUsageQuery,
) (CredentialWindowUsageByModel, error) {
	if service == nil || service.db == nil {
		return CredentialWindowUsageByModel{}, fmt.Errorf("query credential window usage by model: database is nil")
	}
	if input.CredentialID == 0 || input.FromMS < 0 || input.ToMS <= input.FromMS {
		return CredentialWindowUsageByModel{}, fmt.Errorf("query credential window usage by model: invalid scope")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	switch input.Source {
	case CredentialWindowUsageSourceHourlyStats:
	default:
		return CredentialWindowUsageByModel{}, fmt.Errorf(
			"query credential window usage by model: unsupported source %q",
			input.Source,
		)
	}

	var result CredentialWindowUsageByModel
	err := dbtx.Run(ctx, service.db, dbtx.Options{
		Mode:           dbtx.ReadSnapshot,
		CleanupTimeout: usageRollbackTimeout,
		Operation:      "credential window usage by model read transaction",
	}, func(connection *gorm.DB) error {
		var queryErr error
		result, queryErr = service.queryCredentialHourlyUsageByModel(connection, input)
		return queryErr
	})
	if err != nil {
		return CredentialWindowUsageByModel{}, fmt.Errorf("query credential window usage by model: %w", err)
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
		Where("operation <> ?", string(execution.OperationWebSearch)).
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
	fullHoursToMS, err := epochms.AlignDown(input.ToMS, epochms.MillisecondsPerHour)
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

	mergeBoundary := func(fromMS, toMS int64) error {
		if fromMS >= toMS {
			return nil
		}
		boundary, err := service.queryCredentialRequestLogUsage(db, CredentialWindowUsageQuery{
			CredentialID: input.CredentialID,
			FromMS:       fromMS,
			ToMS:         toMS,
			Source:       CredentialWindowUsageSourceRequestLogs,
		})
		if err != nil {
			return err
		}
		result.UsageAggregate, err = addUsageAggregates(
			result.UsageAggregate,
			boundary.UsageAggregate,
		)
		if err != nil {
			return fmt.Errorf("merge boundary usage: %w", err)
		}
		result.DataComplete = result.DataComplete && boundary.DataComplete
		if boundary.LastUsedAtMS != nil &&
			(result.LastUsedAtMS == nil || *boundary.LastUsedAtMS > *result.LastUsedAtMS) {
			result.LastUsedAtMS = boundary.LastUsedAtMS
		}
		return nil
	}

	startBoundaryToMS := fullHoursFromMS
	if startBoundaryToMS > input.ToMS {
		startBoundaryToMS = input.ToMS
	}
	if err := mergeBoundary(input.FromMS, startBoundaryToMS); err != nil {
		return CredentialWindowUsage{}, err
	}
	endBoundaryFromMS := fullHoursToMS
	if endBoundaryFromMS < startBoundaryToMS {
		endBoundaryFromMS = startBoundaryToMS
	}
	if err := mergeBoundary(endBoundaryFromMS, input.ToMS); err != nil {
		return CredentialWindowUsage{}, err
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

func (service *Service) scanCredentialRequestLogUsageByModel(
	db *gorm.DB,
	input CredentialWindowUsageQuery,
) (map[string]CredentialWindowUsage, error) {
	var rows []credentialModelUsageRow
	query := db.Model(&models.RequestLog{}).
		Where("credential_id = ?", input.CredentialID).
		Where("operation <> ?", string(execution.OperationWebSearch)).
		Where("completed_at_ms >= ? AND completed_at_ms < ?", input.FromMS, input.ToMS)
	if err := query.
		Select("upstream_model AS model, " + credentialRequestLogUsageSelect).
		Group("upstream_model").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query request logs by model: %w", err)
	}
	result := make(map[string]CredentialWindowUsage, len(rows))
	for _, row := range rows {
		if err := validateUsageAggregate(row.UsageAggregate); err != nil {
			return nil, fmt.Errorf("validate request log usage for %q: %w", row.Model, err)
		}
		if row.Model == "" {
			continue
		}
		result[row.Model] = CredentialWindowUsage{
			UsageAggregate: row.UsageAggregate,
			LastUsedAtMS:   row.LastUsedAtMS,
		}
	}
	return result, nil
}

func (service *Service) queryCredentialHourlyUsageByModel(
	db *gorm.DB,
	input CredentialWindowUsageQuery,
) (CredentialWindowUsageByModel, error) {
	fullHoursFromMS, err := alignHourUp(input.FromMS)
	if err != nil {
		return CredentialWindowUsageByModel{}, fmt.Errorf("align window start: %w", err)
	}
	fullHoursToMS, err := epochms.AlignDown(input.ToMS, epochms.MillisecondsPerHour)
	if err != nil {
		return CredentialWindowUsageByModel{}, fmt.Errorf("align window end: %w", err)
	}
	result := make(map[string]CredentialWindowUsage)
	dataComplete := true
	if fullHoursFromMS < fullHoursToMS {
		scope := db.Model(&models.UsageStat{}).
			Where("credential_id = ?", input.CredentialID).
			Where("bucket_start_ms >= ? AND bucket_start_ms < ?", fullHoursFromMS, fullHoursToMS)
		if err := validateUsageStatIntegrity(scope); err != nil {
			return CredentialWindowUsageByModel{}, err
		}
		var rows []credentialModelUsageRow
		if err := scope.
			Select("model, MAX(bucket_start_ms) AS last_used_at_ms, " + usageAggregateSelect).
			Group("model").
			Find(&rows).Error; err != nil {
			return CredentialWindowUsageByModel{}, fmt.Errorf("query hourly usage by model: %w", err)
		}
		for _, row := range rows {
			if err := validateUsageAggregate(row.UsageAggregate); err != nil {
				return CredentialWindowUsageByModel{}, fmt.Errorf("validate hourly usage for %q: %w", row.Model, err)
			}
			if row.Model == "" {
				continue
			}
			result[row.Model] = CredentialWindowUsage{
				UsageAggregate: row.UsageAggregate,
				LastUsedAtMS:   row.LastUsedAtMS,
			}
		}
	}

	mergeBoundary := func(fromMS, toMS int64) error {
		if fromMS >= toMS {
			return nil
		}
		boundary, err := service.scanCredentialRequestLogUsageByModel(db, CredentialWindowUsageQuery{
			CredentialID: input.CredentialID,
			FromMS:       fromMS,
			ToMS:         toMS,
			Source:       CredentialWindowUsageSourceRequestLogs,
		})
		if err != nil {
			return err
		}
		retained := service.requestLogWindowRetained(fromMS)
		dataComplete = dataComplete && retained
		return mergeModelWindowUsage(result, boundary)
	}

	startBoundaryToMS := fullHoursFromMS
	if startBoundaryToMS > input.ToMS {
		startBoundaryToMS = input.ToMS
	}
	if err := mergeBoundary(input.FromMS, startBoundaryToMS); err != nil {
		return CredentialWindowUsageByModel{}, err
	}
	endBoundaryFromMS := fullHoursToMS
	if endBoundaryFromMS < startBoundaryToMS {
		endBoundaryFromMS = startBoundaryToMS
	}
	if err := mergeBoundary(endBoundaryFromMS, input.ToMS); err != nil {
		return CredentialWindowUsageByModel{}, err
	}
	return credentialWindowUsageByModelFromMap(
		CredentialWindowUsageSourceHourlyStats,
		dataComplete,
		result,
	), nil
}

func mergeModelWindowUsage(
	dst map[string]CredentialWindowUsage,
	src map[string]CredentialWindowUsage,
) error {
	for model, incoming := range src {
		existing, ok := dst[model]
		if !ok {
			dst[model] = incoming
			continue
		}
		merged, err := addUsageAggregates(existing.UsageAggregate, incoming.UsageAggregate)
		if err != nil {
			return fmt.Errorf("merge %q usage: %w", model, err)
		}
		existing.UsageAggregate = merged
		if incoming.LastUsedAtMS != nil &&
			(existing.LastUsedAtMS == nil || *incoming.LastUsedAtMS > *existing.LastUsedAtMS) {
			existing.LastUsedAtMS = incoming.LastUsedAtMS
		}
		dst[model] = existing
	}
	return nil
}

func credentialWindowUsageByModelFromMap(
	source CredentialWindowUsageSource,
	dataComplete bool,
	rows map[string]CredentialWindowUsage,
) CredentialWindowUsageByModel {
	models := make([]CredentialModelUsage, 0, len(rows))
	for model, usage := range rows {
		models = append(models, CredentialModelUsage{
			Model:          model,
			UsageAggregate: usage.UsageAggregate,
			LastUsedAtMS:   usage.LastUsedAtMS,
		})
	}
	return CredentialWindowUsageByModel{
		Source:       source,
		DataComplete: dataComplete,
		Models:       models,
	}
}

type credentialRequestLogUsageRow struct {
	UsageAggregate
	LastUsedAtMS *int64 `gorm:"column:last_used_at_ms"`
}

type credentialModelUsageRow struct {
	Model string `gorm:"column:model"`
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
