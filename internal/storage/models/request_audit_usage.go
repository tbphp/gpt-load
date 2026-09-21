package models

// RequestAuditUsage attributes each audit call without counting another client request.
type RequestAuditUsage struct {
	RequestID            string `gorm:"type:varchar(36);primaryKey;not null"`
	Sequence             int    `gorm:"primaryKey;not null"`
	CompletedAtMS        int64  `gorm:"column:completed_at_ms;not null;index:idx_request_audit_usage_completed"`
	AccessKeyID          uint   `gorm:"not null"`
	GroupID              uint   `gorm:"not null"`
	ChannelID            string `gorm:"type:varchar(64);not null"`
	CredentialID         uint   `gorm:"not null"`
	Model                string `gorm:"type:varchar(512);not null"`
	EstimatedCostNanoUSD int64  `gorm:"not null;check:chk_request_audit_usage_cost,estimated_cost_nano_usd >= 0"`
	UnpricedRequestCount int64  `gorm:"not null;default:0"`
	PricingPartialCount  int64  `gorm:"not null;default:0"`
	PricingCompleteness  string `gorm:"type:varchar(32);not null"`
}
