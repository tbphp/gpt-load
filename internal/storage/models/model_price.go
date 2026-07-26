package models

import "time"

// ModelPrice stores prices in USD per one million tokens for an upstream model pattern.
type ModelPrice struct {
	ID                uint     `gorm:"primaryKey;autoIncrement"`
	Pattern           string   `gorm:"type:varchar(255);not null;uniqueIndex"`
	InputPrice        *float64 `gorm:"column:input_price"`
	OutputPrice       *float64 `gorm:"column:output_price"`
	CacheReadPrice    *float64 `gorm:"column:cache_read_price"`
	CacheWrite5MPrice *float64 `gorm:"column:cache_write_5m_price"`
	CacheWrite1HPrice *float64 `gorm:"column:cache_write_1h_price"`
	Source            string   `gorm:"type:varchar(32);not null;check:chk_model_price_source,source = 'user'"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
