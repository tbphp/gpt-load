package models

// ModelPrice stores prices in USD per one million tokens for an upstream model pattern.
type ModelPrice struct {
	ID                                       uint   `gorm:"primaryKey;autoIncrement"`
	Pattern                                  string `gorm:"type:varchar(255);not null;uniqueIndex"`
	InputPriceNanoUSDPerMillionTokens        *int64 `gorm:"column:input_price_nano_usd_per_million_tokens;check:chk_model_price_input_nano,input_price_nano_usd_per_million_tokens IS NULL OR input_price_nano_usd_per_million_tokens >= 0"`
	OutputPriceNanoUSDPerMillionTokens       *int64 `gorm:"column:output_price_nano_usd_per_million_tokens;check:chk_model_price_output_nano,output_price_nano_usd_per_million_tokens IS NULL OR output_price_nano_usd_per_million_tokens >= 0"`
	CacheReadPriceNanoUSDPerMillionTokens    *int64 `gorm:"column:cache_read_price_nano_usd_per_million_tokens;check:chk_model_price_cache_read_nano,cache_read_price_nano_usd_per_million_tokens IS NULL OR cache_read_price_nano_usd_per_million_tokens >= 0"`
	CacheWrite5MPriceNanoUSDPerMillionTokens *int64 `gorm:"column:cache_write_5m_price_nano_usd_per_million_tokens;check:chk_model_price_cache_write_5m_nano,cache_write_5m_price_nano_usd_per_million_tokens IS NULL OR cache_write_5m_price_nano_usd_per_million_tokens >= 0"`
	CacheWrite1HPriceNanoUSDPerMillionTokens *int64 `gorm:"column:cache_write_1h_price_nano_usd_per_million_tokens;check:chk_model_price_cache_write_1h_nano,cache_write_1h_price_nano_usd_per_million_tokens IS NULL OR cache_write_1h_price_nano_usd_per_million_tokens >= 0"`
	Source                                   string `gorm:"type:varchar(32);not null;check:chk_model_price_source,source = 'user'"`
	CreatedAtMS                              int64  `gorm:"column:created_at_ms;not null;autoCreateTime:milli"`
	UpdatedAtMS                              int64  `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli"`
}
