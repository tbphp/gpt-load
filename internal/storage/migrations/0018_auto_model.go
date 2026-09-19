package migrations

import (
	"fmt"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
)

const ID0018 = "0018_auto_model"

var autoModelColumns0018 = []string{"AutoDecision", "DecisionModel", "DecisionCostNanoUSD", "DecisionPricingCompleteness"}

// 冻结本次迁移的字段，后续运行模型变化不能改写历史 DDL。
type autoLog0018 struct {
	AutoDecision                models.JSON `gorm:"type:json"`
	DecisionModel               string      `gorm:"type:varchar(512);not null;default:''"`
	DecisionCostNanoUSD         int64       `gorm:"column:decision_cost_nano_usd;not null;default:0"`
	DecisionPricingCompleteness string      `gorm:"type:varchar(32);not null;default:'not_applicable'"`
}

func (autoLog0018) TableName() string { return "request_logs" }

type autoUsage0018 struct {
	BucketStartMS        int64  `gorm:"primaryKey;not null"`
	AccessKeyID          uint   `gorm:"primaryKey;not null"`
	Model                string `gorm:"type:varchar(512);primaryKey;not null"`
	EstimatedCostNanoUSD int64  `gorm:"not null;default:0;check:chk_auto_decision_usage_cost,estimated_cost_nano_usd >= 0"`
	UnpricedRequestCount int64  `gorm:"not null;default:0;check:chk_auto_decision_usage_unpriced,unpriced_request_count >= 0"`
	PricingPartialCount  int64  `gorm:"not null;default:0;check:chk_auto_decision_usage_partial,pricing_partial_count >= 0"`
}

func (autoUsage0018) TableName() string { return "auto_decision_usage_stats" }

func Up0018(db *gorm.DB) error {
	if err := ValidateRecoverable0018(db); err != nil {
		return err
	}
	for _, name := range autoModelColumns0018 {
		if !db.Migrator().HasColumn(&autoLog0018{}, name) {
			if err := db.Migrator().AddColumn(&autoLog0018{}, name); err != nil {
				return fmt.Errorf("add automatic model request log column %s: %w", name, err)
			}
		}
	}
	for _, table := range []any{&autoUsage0018{}} {
		if !db.Migrator().HasTable(table) {
			if err := db.Migrator().CreateTable(table); err != nil {
				return err
			}
		}
		statement := &gorm.Statement{DB: db}
		if err := statement.Parse(table); err != nil {
			return err
		}
		for _, index := range statement.Schema.ParseIndexes() {
			if !db.Migrator().HasIndex(table, index.Name) {
				if err := db.Migrator().CreateIndex(table, index.Name); err != nil {
					return err
				}
			}
		}
	}
	return Validate0018(db)
}

func ValidateRecoverable0018(db *gorm.DB) error {
	if !db.Migrator().HasTable("request_logs") {
		return fmt.Errorf("automatic model migration requires request_logs")
	}
	return nil
}

func Validate0018(db *gorm.DB) error {
	for _, name := range autoModelColumns0018 {
		if !db.Migrator().HasColumn(&autoLog0018{}, name) {
			return fmt.Errorf("automatic model log column %s missing", name)
		}
	}
	for _, definition := range []struct {
		model  any
		fields []string
	}{
		{&autoUsage0018{}, []string{"BucketStartMS", "AccessKeyID", "Model", "EstimatedCostNanoUSD", "UnpricedRequestCount", "PricingPartialCount"}},
	} {
		if !db.Migrator().HasTable(definition.model) {
			return fmt.Errorf("automatic model table missing")
		}
		for _, name := range definition.fields {
			if !db.Migrator().HasColumn(definition.model, name) {
				return fmt.Errorf("automatic model field %s missing", name)
			}
		}
	}
	return nil
}
