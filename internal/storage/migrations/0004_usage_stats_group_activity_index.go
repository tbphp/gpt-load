package migrations

import (
	"fmt"

	"gorm.io/gorm"
)

const (
	// ID0004 adds the index used to find each Group's most recent usage hour.
	ID0004 = "0004_usage_stats_group_activity_index"

	usageStatsGroupBucketIndex0004 = "idx_usage_stats_group_bucket"
)

type usageStat0004 struct {
	GroupID       uint  `gorm:"column:group_id;index:idx_usage_stats_group_bucket,priority:1"`
	BucketStartMS int64 `gorm:"column:bucket_start_ms;index:idx_usage_stats_group_bucket,priority:2,sort:desc"`
}

func (usageStat0004) TableName() string {
	return "usage_stats"
}

// Up0004 adds a Group-first activity index without changing usage data.
func Up0004(db *gorm.DB) error {
	if !db.Migrator().HasTable(&usageStat0004{}) {
		return fmt.Errorf("add usage stats group activity index: table %q is missing", usageStat0004{}.TableName())
	}
	if db.Migrator().HasIndex(&usageStat0004{}, usageStatsGroupBucketIndex0004) {
		return nil
	}
	if err := db.Migrator().CreateIndex(&usageStat0004{}, usageStatsGroupBucketIndex0004); err != nil {
		return fmt.Errorf("add usage stats group activity index: %w", err)
	}
	return nil
}

// ValidateRecoverable0004 accepts either side of this idempotent index creation.
func ValidateRecoverable0004(db *gorm.DB) error {
	if !db.Migrator().HasTable(&usageStat0004{}) {
		return fmt.Errorf("validate recoverable usage stats group activity index: table %q is missing", usageStat0004{}.TableName())
	}
	return nil
}

// Validate0004 verifies the activity lookup index is present.
func Validate0004(db *gorm.DB) error {
	if !db.Migrator().HasTable(&usageStat0004{}) {
		return fmt.Errorf("validate usage stats group activity index: table %q is missing", usageStat0004{}.TableName())
	}
	if !db.Migrator().HasIndex(&usageStat0004{}, usageStatsGroupBucketIndex0004) {
		return fmt.Errorf("validate usage stats group activity index: index %q is missing", usageStatsGroupBucketIndex0004)
	}
	return nil
}
