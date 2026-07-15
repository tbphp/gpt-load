package models

import "time"

// ModelPrice stores prices in USD per one million tokens for an upstream model pattern.
type ModelPrice struct {
	ID                uint    `gorm:"primaryKey;autoIncrement"`
	Pattern           string  `gorm:"type:varchar(255);not null;index"`
	InputPrice        float64 `gorm:"not null;default:0"`
	OutputPrice       float64 `gorm:"not null;default:0"`
	CacheReadPrice    float64 `gorm:"not null;default:0"`
	CacheWrite5MPrice float64 `gorm:"column:cache_write_5m_price;not null;default:0"`
	CacheWrite1HPrice float64 `gorm:"column:cache_write_1h_price;not null;default:0"`
	Source            string  `gorm:"type:varchar(32);not null;index"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
