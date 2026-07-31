package models

// AccessKey is an encrypted client credential and its persisted access policy.
type AccessKey struct {
	ID                      uint   `gorm:"primaryKey;autoIncrement"`
	Name                    string `gorm:"type:varchar(255);not null"`
	KeyValue                string `gorm:"type:text;not null"`
	KeyHash                 string `gorm:"type:varchar(128);not null;uniqueIndex"`
	KeySuffix               string `gorm:"type:char(4);not null;check:chk_access_key_suffix,key_suffix GLOB '[0-9a-f][0-9a-f][0-9a-f][0-9a-f]'"`
	Status                  string `gorm:"type:varchar(32);not null;default:'active';check:chk_access_key_status,status IN ('active','disabled')"`
	Filters                 JSON   `gorm:"type:json"`
	RPMLimit                int64  `gorm:"not null;default:0"`
	DailyCostLimitNanoUSD   int64  `gorm:"column:daily_cost_limit_nano_usd;not null;default:0;check:chk_access_key_daily_cost_limit_nano,daily_cost_limit_nano_usd >= 0"`
	MonthlyCostLimitNanoUSD int64  `gorm:"column:monthly_cost_limit_nano_usd;not null;default:0;check:chk_access_key_monthly_cost_limit_nano,monthly_cost_limit_nano_usd >= 0"`
	CreatedAtMS             int64  `gorm:"column:created_at_ms;not null;autoCreateTime:milli"`
	UpdatedAtMS             int64  `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli"`
}
