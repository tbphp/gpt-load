package requestlog

import (
	"context"
	"fmt"
	"time"

	"gpt-load/internal/platform/epochms"
	"gpt-load/internal/storage/models"
)

type RetentionPolicyProvider interface {
	RequestLogRetentionDays() int
}

const (
	retentionBatchSize     = 1000
	usageStatRetentionDays = 35
)

// Sweep removes request logs and hourly usage aggregates strictly older than
// their respective retention boundaries. Failures are isolated from the data
// plane.
func (service *Service) Sweep(ctx context.Context, now time.Time) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return
	}

	days := service.retentionPolicy.RequestLogRetentionDays()
	nowMS, err := epochms.FromTime(now)
	if err != nil {
		service.recordRetentionDeleteFailure(now)
		return
	}
	requestLogCutoffMS, err := retentionCutoffMS(nowMS, days)
	if err != nil {
		service.recordRetentionDeleteFailure(now)
		return
	}
	usageStatCutoffMS, err := retentionCutoffMS(nowMS, usageStatRetentionDays)
	if err != nil {
		service.recordRetentionDeleteFailure(now)
		return
	}
	usageStatCutoffMS, err = epochms.AlignDown(
		usageStatCutoffMS,
		epochms.MillisecondsPerHour,
	)
	if err != nil {
		service.recordRetentionDeleteFailure(now)
		return
	}

	if !service.deleteExpiredRequestLogs(ctx, requestLogCutoffMS, now) {
		return
	}
	service.deleteExpiredUsageStats(ctx, usageStatCutoffMS, now)
}

func retentionCutoffMS(nowMS int64, days int) (int64, error) {
	if days < 0 {
		return 0, fmt.Errorf("retention days must be non-negative")
	}
	retentionMS := int64(days) * epochms.MillisecondsPerDay
	if retentionMS > nowMS {
		return 0, nil
	}
	return nowMS - retentionMS, nil
}

func (service *Service) deleteExpiredRequestLogs(
	ctx context.Context,
	cutoffMS int64,
	now time.Time,
) bool {
	for {
		if ctx.Err() != nil {
			return false
		}

		var ids []string
		result := service.db.WithContext(ctx).
			Model(&models.RequestLog{}).
			Where("completed_at_ms < ?", cutoffMS).
			Order("completed_at_ms ASC").
			Order("id ASC").
			Limit(retentionBatchSize).
			Pluck("id", &ids)
		if result.Error != nil {
			if ctx.Err() == nil {
				service.recordRetentionDeleteFailure(now)
			}
			return false
		}
		if len(ids) == 0 {
			return true
		}

		result = service.db.WithContext(ctx).
			Where("id IN ?", ids).
			Delete(&models.RequestLog{})
		if result.Error != nil {
			if ctx.Err() == nil {
				service.recordRetentionDeleteFailure(now)
			}
			return false
		}
		if len(ids) < retentionBatchSize {
			return true
		}
	}
}

func (service *Service) deleteExpiredUsageStats(
	ctx context.Context,
	cutoffMS int64,
	now time.Time,
) bool {
	for {
		if ctx.Err() != nil {
			return false
		}

		var ids []uint
		result := service.db.WithContext(ctx).
			Model(&models.UsageStat{}).
			Where("bucket_start_ms < ?", cutoffMS).
			Order("bucket_start_ms ASC").
			Order("id ASC").
			Limit(retentionBatchSize).
			Pluck("id", &ids)
		if result.Error != nil {
			if ctx.Err() == nil {
				service.recordRetentionDeleteFailure(now)
			}
			return false
		}
		if len(ids) == 0 {
			return true
		}

		result = service.db.WithContext(ctx).
			Where("id IN ?", ids).
			Delete(&models.UsageStat{})
		if result.Error != nil {
			if ctx.Err() == nil {
				service.recordRetentionDeleteFailure(now)
			}
			return false
		}
		if len(ids) < retentionBatchSize {
			return true
		}
	}
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
