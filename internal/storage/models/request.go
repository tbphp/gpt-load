package models

import "time"

// RequestLog is the durable request-level audit and usage record.
type RequestLog struct {
	ID                 string    `gorm:"type:varchar(36);primaryKey;not null"`
	CreatedAt          time.Time `gorm:"not null;index"`
	AccessKeyID        uint      `gorm:"not null;index"`
	Protocol           string    `gorm:"type:varchar(32);not null;index"`
	ClientModel        string    `gorm:"type:varchar(255);not null;index"`
	UpstreamModel      string    `gorm:"type:varchar(255);not null;index"`
	Status             string    `gorm:"type:varchar(32);not null;index"`
	StatusCode         int       `gorm:"not null"`
	DurationMs         int64     `gorm:"not null"`
	AffinityHit        bool      `gorm:"not null;default:false"`
	InputTokens        int64     `gorm:"not null;default:0"`
	OutputTokens       int64     `gorm:"not null;default:0"`
	CacheReadTokens    int64     `gorm:"not null;default:0"`
	CacheWrite5MTokens int64     `gorm:"column:cache_write_5m_tokens;not null;default:0"`
	CacheWrite1HTokens int64     `gorm:"column:cache_write_1h_tokens;not null;default:0"`
	Cost               float64   `gorm:"not null;default:0"`
	Attempts           JSON      `gorm:"type:json"`
}

// UsageStat is an hourly aggregate by upstream group and upstream model.
type UsageStat struct {
	ID                 uint      `gorm:"primaryKey;autoIncrement"`
	HourBucket         time.Time `gorm:"not null;uniqueIndex:idx_usage_stats_hour_group_model,priority:1"`
	GroupID            uint      `gorm:"not null;uniqueIndex:idx_usage_stats_hour_group_model,priority:2"`
	Model              string    `gorm:"type:varchar(255);not null;uniqueIndex:idx_usage_stats_hour_group_model,priority:3"`
	RequestCount       int64     `gorm:"not null;default:0"`
	SuccessCount       int64     `gorm:"not null;default:0"`
	FailureCount       int64     `gorm:"not null;default:0"`
	InputTokens        int64     `gorm:"not null;default:0"`
	OutputTokens       int64     `gorm:"not null;default:0"`
	CacheReadTokens    int64     `gorm:"not null;default:0"`
	CacheWrite5MTokens int64     `gorm:"column:cache_write_5m_tokens;not null;default:0"`
	CacheWrite1HTokens int64     `gorm:"column:cache_write_1h_tokens;not null;default:0"`
	Cost               float64   `gorm:"not null;default:0"`
}
