package requestlog

import (
	"context"
	"strconv"
	"time"

	"gpt-load/internal/storage/models"
)

const RetentionSettingKey = "request_log_retention_days"

const (
	defaultRetentionDays = int64(7)
	minRetentionDays     = int64(1)
	maxRetentionDays     = int64(365)
	retentionBatchSize   = 1000
)

// Sweep removes request logs strictly older than the configured retention
// boundary. Configuration and database failures are intentionally isolated
// from the data-plane request path.
func (service *Service) Sweep(ctx context.Context, now time.Time) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return
	}

	days, valid, err := service.loadRetentionDays(ctx)
	if err != nil {
		if ctx.Err() == nil {
			service.recordRetentionDeleteFailure(now)
		}
		return
	}
	if !valid {
		service.recordRetentionInvalidSetting(now)
		return
	}

	cutoff := now.UTC().Add(-time.Duration(days) * 24 * time.Hour)
	for {
		if ctx.Err() != nil {
			return
		}

		var ids []string
		result := service.db.WithContext(ctx).
			Model(&models.RequestLog{}).
			Where("created_at < ?", cutoff).
			Order("created_at ASC").
			Order("id ASC").
			Limit(retentionBatchSize).
			Pluck("id", &ids)
		if result.Error != nil {
			if ctx.Err() == nil {
				service.recordRetentionDeleteFailure(now)
			}
			return
		}
		if len(ids) == 0 {
			return
		}

		result = service.db.WithContext(ctx).
			Where("id IN ?", ids).
			Delete(&models.RequestLog{})
		if result.Error != nil {
			if ctx.Err() == nil {
				service.recordRetentionDeleteFailure(now)
			}
			return
		}
		if len(ids) < retentionBatchSize {
			return
		}
	}
}

func (service *Service) loadRetentionDays(ctx context.Context) (int64, bool, error) {
	var setting models.SystemSetting
	result := service.db.WithContext(ctx).
		Where("key = ?", RetentionSettingKey).
		Find(&setting)
	if result.Error != nil {
		return 0, false, result.Error
	}
	if result.RowsAffected == 0 {
		return defaultRetentionDays, true, nil
	}

	days, err := strconv.ParseInt(setting.Value, 10, 64)
	if err != nil ||
		strconv.FormatInt(days, 10) != setting.Value ||
		days < minRetentionDays ||
		days > maxRetentionDays {
		return 0, false, nil
	}
	return days, true, nil
}

func (service *Service) recordRetentionInvalidSetting(now time.Time) {
	service.retentionInvalidTotal.Add(1)
	service.recordRetentionFailureAt(now)
	service.warn("retention_invalid_setting", 0)
}

func (service *Service) recordRetentionDeleteFailure(now time.Time) {
	service.retentionDeleteTotal.Add(1)
	service.recordRetentionFailureAt(now)
	service.warn("retention_delete_failure", 0)
}

func (service *Service) recordRetentionFailureAt(now time.Time) {
	service.statsMu.Lock()
	service.lastRetentionFailureAt = now
	service.statsMu.Unlock()
}
