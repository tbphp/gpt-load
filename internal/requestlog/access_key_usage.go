package requestlog

import (
	"fmt"

	"gorm.io/gorm"

	"gpt-load/internal/platform/epochms"
)

// ReadAccessKeyUsage 在调用方的读快照内批量汇总密钥用量，复用用量页的范围和金额校验。
func ReadAccessKeyUsage(db *gorm.DB, query UsageQuery) (map[uint]UsageAggregate, error) {
	if _, err := validateUsageQuery(query); err != nil {
		return nil, err
	}
	if query.Granularity == UsageGranularityMinute {
		return nil, fmt.Errorf("access key collection requires hourly aggregates")
	}
	if err := validateUsageIntegrity(usageStatScope(db, query), epochms.MillisecondsPerHour); err != nil {
		return nil, err
	}
	var rows []struct {
		AccessKeyID uint
		UsageAggregate
	}
	if err := usageStatScope(db, query).Select("access_key_id, " + usageAggregateSelect).Group("access_key_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[uint]UsageAggregate, len(rows))
	for _, row := range rows {
		if err := validateUsageAggregate(row.UsageAggregate); err != nil {
			return nil, err
		}
		result[row.AccessKeyID] = row.UsageAggregate
	}
	return result, nil
}
