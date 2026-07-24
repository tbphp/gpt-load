package requestlog

import (
	"context"
	"time"

	"gpt-load/internal/storage/models"
)

type RetentionPolicyProvider interface {
	RequestLogRetentionDays() int
}

const retentionBatchSize = 1000

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

	days := service.retentionPolicy.RequestLogRetentionDays()
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
