package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ID0019 = "0019_auto_decision_attribution"

const (
	autoDecisionUsageTable0019       = "auto_decision_usage_stats"
	autoDecisionUsageBuildTable0019  = "auto_decision_usage_stats_0019"
	autoDecisionUsageBackupTable0019 = "auto_decision_usage_stats_0019_old"
)

var autoDecisionLogColumns0019 = []string{"DecisionGroupID", "DecisionChannelID", "DecisionCredentialID"}

type autoLog0019 struct {
	DecisionGroupID      uint   `gorm:"not null;default:0"`
	DecisionChannelID    string `gorm:"type:varchar(64);not null;default:''"`
	DecisionCredentialID uint   `gorm:"not null;default:0"`
}

func (autoLog0019) TableName() string { return "request_logs" }

type autoUsage0019 struct {
	BucketStartMS        int64  `gorm:"primaryKey;not null"`
	AccessKeyID          uint   `gorm:"primaryKey;not null"`
	GroupID              uint   `gorm:"primaryKey;not null;default:0"`
	ChannelID            string `gorm:"type:varchar(64);primaryKey;not null;default:''"`
	CredentialID         uint   `gorm:"primaryKey;not null;default:0"`
	Model                string `gorm:"type:varchar(512);primaryKey;not null"`
	EstimatedCostNanoUSD int64  `gorm:"not null;default:0;check:chk_auto_decision_usage_0019_cost,estimated_cost_nano_usd >= 0"`
	UnpricedRequestCount int64  `gorm:"not null;default:0;check:chk_auto_decision_usage_0019_unpriced,unpriced_request_count >= 0"`
	PricingPartialCount  int64  `gorm:"not null;default:0;check:chk_auto_decision_usage_0019_partial,pricing_partial_count >= 0"`
}

func (autoUsage0019) TableName() string { return "auto_decision_usage_stats" }

type autoUsageBuild0019 autoUsage0019

func (autoUsageBuild0019) TableName() string { return autoDecisionUsageBuildTable0019 }

func Up0019(db *gorm.DB) error {
	if err := ValidateRecoverable0019(db); err != nil {
		return err
	}
	for _, name := range autoDecisionLogColumns0019 {
		if !db.Migrator().HasColumn(&autoLog0019{}, name) {
			if err := db.Migrator().AddColumn(&autoLog0019{}, name); err != nil {
				return fmt.Errorf("add automatic decision attribution column %s: %w", name, err)
			}
		}
	}

	hasCurrent := db.Migrator().HasTable(&autoUsage0019{})
	hasBuild := db.Migrator().HasTable(&autoUsageBuild0019{})
	hasBackup := db.Migrator().HasTable(autoDecisionUsageBackupTable0019)
	if hasCurrent && autoDecisionUsageHasAttribution0019(db) {
		if hasBuild {
			if err := dropAutoDecisionUsageTable0019(db, autoDecisionUsageBuildTable0019, &autoUsageBuild0019{}); err != nil {
				return fmt.Errorf("drop stale automatic decision attribution table: %w", err)
			}
		}
		if hasBackup {
			if err := dropAutoDecisionUsageTable0019(db, autoDecisionUsageBackupTable0019, autoDecisionUsageBackupTable0019); err != nil {
				return fmt.Errorf("drop automatic decision attribution swap backup: %w", err)
			}
		}
		return Validate0019(db)
	}
	if !hasCurrent && hasBuild {
		if err := db.Migrator().RenameTable(autoDecisionUsageBuildTable0019, autoDecisionUsageTable0019); err != nil {
			return fmt.Errorf("finish automatic decision attribution table rename: %w", err)
		}
		if hasBackup {
			if err := dropAutoDecisionUsageTable0019(db, autoDecisionUsageBackupTable0019, autoDecisionUsageBackupTable0019); err != nil {
				return fmt.Errorf("drop automatic decision attribution recovery backup: %w", err)
			}
		}
		return Validate0019(db)
	}
	if !hasCurrent {
		return fmt.Errorf("automatic decision attribution migration requires auto_decision_usage_stats")
	}
	if hasBackup {
		return fmt.Errorf("automatic decision attribution migration found an unexpected swap backup")
	}
	if hasBuild {
		if err := dropAutoDecisionUsageTable0019(db, autoDecisionUsageBuildTable0019, &autoUsageBuild0019{}); err != nil {
			return fmt.Errorf("reset automatic decision attribution build table: %w", err)
		}
	}
	if err := db.Migrator().CreateTable(&autoUsageBuild0019{}); err != nil {
		return fmt.Errorf("create automatic decision attribution build table: %w", err)
	}
	if err := db.Exec(`INSERT INTO auto_decision_usage_stats_0019
		(bucket_start_ms, access_key_id, group_id, channel_id, credential_id, model,
		 estimated_cost_nano_usd, unpriced_request_count, pricing_partial_count)
		SELECT bucket_start_ms, access_key_id, 0, '', 0, model,
		       estimated_cost_nano_usd, unpriced_request_count, pricing_partial_count
		FROM auto_decision_usage_stats`).Error; err != nil {
		return fmt.Errorf("copy automatic decision usage history: %w", err)
	}
	if strings.EqualFold(db.Dialector.Name(), "mysql") {
		if err := db.Exec(
			"RENAME TABLE ? TO ?, ? TO ?",
			clause.Table{Name: autoDecisionUsageTable0019},
			clause.Table{Name: autoDecisionUsageBackupTable0019},
			clause.Table{Name: autoDecisionUsageBuildTable0019},
			clause.Table{Name: autoDecisionUsageTable0019},
		).Error; err != nil {
			return fmt.Errorf("swap automatic decision attribution table: %w", err)
		}
		if err := dropAutoDecisionUsageTable0019(db, autoDecisionUsageBackupTable0019, autoDecisionUsageBackupTable0019); err != nil {
			return fmt.Errorf("drop old automatic decision usage table: %w", err)
		}
	} else {
		if err := dropAutoDecisionUsageTable0019(db, autoDecisionUsageTable0019, &autoUsage0019{}); err != nil {
			return fmt.Errorf("drop old automatic decision usage table: %w", err)
		}
		if err := db.Migrator().RenameTable(autoDecisionUsageBuildTable0019, autoDecisionUsageTable0019); err != nil {
			return fmt.Errorf("rename automatic decision attribution table: %w", err)
		}
	}
	return Validate0019(db)
}

func dropAutoDecisionUsageTable0019(db *gorm.DB, table string, model any) error {
	if strings.EqualFold(db.Dialector.Name(), "mysql") {
		return db.Exec("DROP TABLE IF EXISTS ?", clause.Table{Name: table}).Error
	}
	return db.Migrator().DropTable(model)
}

func ValidateRecoverable0019(db *gorm.DB) error {
	if !db.Migrator().HasTable("request_logs") {
		return fmt.Errorf("automatic decision attribution migration requires request_logs")
	}
	if !db.Migrator().HasTable("auto_decision_usage_stats") &&
		!db.Migrator().HasTable(autoDecisionUsageBuildTable0019) {
		return fmt.Errorf("automatic decision attribution migration requires decision usage history")
	}
	return nil
}

func Validate0019(db *gorm.DB) error {
	for _, name := range autoDecisionLogColumns0019 {
		if !db.Migrator().HasColumn(&autoLog0019{}, name) {
			return fmt.Errorf("automatic decision attribution log column %s missing", name)
		}
	}
	if !db.Migrator().HasTable(&autoUsage0019{}) || !autoDecisionUsageHasAttribution0019(db) {
		return fmt.Errorf("automatic decision usage attribution table is incomplete")
	}
	return nil
}

func autoDecisionUsageHasAttribution0019(db *gorm.DB) bool {
	for _, name := range []string{
		"BucketStartMS", "AccessKeyID", "GroupID", "ChannelID", "CredentialID", "Model",
		"EstimatedCostNanoUSD", "UnpricedRequestCount", "PricingPartialCount",
	} {
		if !db.Migrator().HasColumn(&autoUsage0019{}, name) {
			return false
		}
	}
	return true
}
