package migrations

import (
	"fmt"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
)

const ID0021 = "0021_request_audit"

type auditLog0021 struct {
	RequestAudit             models.JSON `gorm:"type:json"`
	AuditCostNanoUSD         int64       `gorm:"column:audit_cost_nano_usd;not null;default:0"`
	AuditPricingCompleteness string      `gorm:"type:varchar(32);not null;default:'not_applicable'"`
}

func (auditLog0021) TableName() string { return "request_logs" }

type auditParent0021 struct {
	ID string `gorm:"type:varchar(36);primaryKey;not null"`
}

func (auditParent0021) TableName() string { return "request_logs" }

type auditUsage0021 struct {
	Request              auditParent0021 `gorm:"foreignKey:RequestID;references:ID;constraint:OnDelete:CASCADE"`
	RequestID            string          `gorm:"type:varchar(36);primaryKey;not null"`
	Sequence             int             `gorm:"primaryKey;not null"`
	CompletedAtMS        int64           `gorm:"column:completed_at_ms;not null;index:idx_request_audit_usage_completed"`
	AccessKeyID          uint            `gorm:"not null"`
	GroupID              uint            `gorm:"not null"`
	ChannelID            string          `gorm:"type:varchar(64);not null"`
	CredentialID         uint            `gorm:"not null"`
	Model                string          `gorm:"type:varchar(512);not null"`
	EstimatedCostNanoUSD int64           `gorm:"not null;check:chk_request_audit_usage_cost,estimated_cost_nano_usd >= 0"`
	UnpricedRequestCount int64           `gorm:"not null;default:0"`
	PricingPartialCount  int64           `gorm:"not null;default:0"`
	PricingCompleteness  string          `gorm:"type:varchar(32);not null"`
}

func (auditUsage0021) TableName() string { return "request_audit_usages" }

var auditColumns0021 = []string{"RequestAudit", "AuditCostNanoUSD", "AuditPricingCompleteness"}

func Up0021(db *gorm.DB) error {
	if err := ValidateRecoverable0021(db); err != nil {
		return err
	}
	for _, name := range auditColumns0021 {
		if !db.Migrator().HasColumn(&auditLog0021{}, name) {
			if err := db.Migrator().AddColumn(&auditLog0021{}, name); err != nil {
				return err
			}
		}
	}
	if !db.Migrator().HasTable(&auditUsage0021{}) {
		if err := db.Migrator().CreateTable(&auditUsage0021{}); err != nil {
			return err
		}
	}
	if !db.Migrator().HasIndex(&auditUsage0021{}, "idx_request_audit_usage_completed") {
		if err := db.Migrator().CreateIndex(&auditUsage0021{}, "idx_request_audit_usage_completed"); err != nil {
			return err
		}
	}
	if !db.Migrator().HasConstraint(&auditUsage0021{}, "Request") {
		if err := db.Migrator().CreateConstraint(&auditUsage0021{}, "Request"); err != nil {
			return err
		}
	}
	return Validate0021(db)
}
func ValidateRecoverable0021(db *gorm.DB) error {
	if !db.Migrator().HasTable("request_logs") {
		return fmt.Errorf("request audit requires request_logs")
	}
	if db.Migrator().HasTable(&auditUsage0021{}) {
		return validateAuditTable0021(db, false)
	}
	return nil
}
func Validate0021(db *gorm.DB) error {
	for _, name := range auditColumns0021 {
		if !db.Migrator().HasColumn(&auditLog0021{}, name) {
			return fmt.Errorf("audit column %s missing", name)
		}
	}
	return validateAuditTable0021(db, true)
}
func validateAuditTable0021(db *gorm.DB, requireIndex bool) error {
	if !db.Migrator().HasTable(&auditUsage0021{}) {
		return fmt.Errorf("audit usage table missing")
	}
	for _, name := range []string{"RequestID", "Sequence", "CompletedAtMS", "AccessKeyID", "GroupID", "ChannelID", "CredentialID", "Model", "EstimatedCostNanoUSD", "PricingCompleteness", "UnpricedRequestCount", "PricingPartialCount"} {
		if !db.Migrator().HasColumn(&auditUsage0021{}, name) {
			return fmt.Errorf("audit usage column %s missing", name)
		}
	}
	if requireIndex && !db.Migrator().HasConstraint(&auditUsage0021{}, "Request") {
		return fmt.Errorf("audit usage request constraint missing")
	}
	if requireIndex && !db.Migrator().HasIndex(&auditUsage0021{}, "idx_request_audit_usage_completed") {
		return fmt.Errorf("audit usage completion index missing")
	}
	return nil
}
