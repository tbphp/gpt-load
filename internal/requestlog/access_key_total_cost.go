package requestlog

import (
	"context"
	"fmt"
	"math"

	"gorm.io/gorm"

	"gpt-load/internal/platform/epochms"
	"gpt-load/internal/storage/dbtx"
)

// QueryAccessKeyTotalCost 从长期保留的小时汇总读取累计成本，不受明细留存或周期额度重置影响。
func (service *Service) QueryAccessKeyTotalCost(ctx context.Context, accessKeyID uint) (int64, error) {
	if service == nil || service.db == nil || accessKeyID == 0 {
		return 0, fmt.Errorf("query access key total cost: invalid database or key scope")
	}
	var cost int64
	err := dbtx.Run(ctx, service.db, dbtx.Options{
		Mode: dbtx.ReadSnapshot, CleanupTimeout: usageRollbackTimeout, Operation: "access key total cost read",
	}, func(tx *gorm.DB) error {
		scope := usageStatScope(tx, UsageQuery{ToMS: math.MaxInt64, AccessKeyID: &accessKeyID})
		if err := validateUsageIntegrity(scope, epochms.MillisecondsPerHour); err != nil {
			return err
		}
		summary, err := queryUsageSummary(scope.Session(&gorm.Session{}))
		if err != nil {
			return err
		}
		cost = summary.EstimatedCostNanoUSD
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("query access key total cost: %w", err)
	}
	return cost, nil
}
