package models

// AccessKey is an encrypted client credential and its persisted access policy.
type AccessKey struct {
	PriceMultiplierMicros   *int64  `gorm:"column:price_multiplier_micros;type:bigint;not null;default:1000000;check:chk_access_key_price_multiplier,price_multiplier_micros >= 0 AND price_multiplier_micros <= 1000000000"`
	ID                      uint    `gorm:"primaryKey;autoIncrement"`
	Name                    string  `gorm:"type:varchar(255);not null"`
	KeyValue                string  `gorm:"type:text;not null"`
	KeyHash                 string  `gorm:"type:varchar(128);not null;uniqueIndex"`
	KeyPrefix               *string `gorm:"type:varchar(6);not null;default:'sk-gl-';check:chk_access_key_prefix,length(key_prefix) IN (0,6)"`
	KeySuffix               string  `gorm:"type:char(4);not null;check:chk_access_key_suffix,length(key_suffix) = 4"`
	Status                  string  `gorm:"type:varchar(32);not null;default:'active';check:chk_access_key_status,status IN ('active','disabled')"`
	Filters                 JSON    `gorm:"type:json"`
	RPMLimit                int64   `gorm:"not null;default:0"`
	DailyCostLimitNanoUSD   int64   `gorm:"column:daily_cost_limit_nano_usd;not null;default:0;check:chk_access_key_daily_cost_limit_nano,daily_cost_limit_nano_usd >= 0"`
	MonthlyCostLimitNanoUSD int64   `gorm:"column:monthly_cost_limit_nano_usd;not null;default:0;check:chk_access_key_monthly_cost_limit_nano,monthly_cost_limit_nano_usd >= 0"`
	ExpiresAtMS             *int64  `gorm:"column:expires_at_ms"`
	CreatedAtMS             int64   `gorm:"column:created_at_ms;not null;autoCreateTime:milli;check:chk_access_key_created_at,created_at_ms >= 0"`
	UpdatedAtMS             int64   `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_access_key_updated_at,updated_at_ms >= 0"`
}
