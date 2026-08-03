package models

// RequestLog is the durable request-level audit and usage record.
type RequestLog struct {
	ID                      string `gorm:"type:varchar(36);primaryKey;not null;index:idx_request_logs_completed_id,priority:2,sort:desc;index:idx_request_logs_access_completed_id,priority:3,sort:desc;index:idx_request_logs_status_completed_id,priority:3,sort:desc;index:idx_request_logs_model_completed_id,priority:3,sort:desc"`
	CompletedAtMS           int64  `gorm:"column:completed_at_ms;not null;index:idx_request_logs_completed_id,priority:1,sort:desc;index:idx_request_logs_access_completed_id,priority:2,sort:desc;index:idx_request_logs_status_completed_id,priority:2,sort:desc;index:idx_request_logs_model_completed_id,priority:2,sort:desc"`
	AccessKeyID             uint   `gorm:"not null;index:idx_request_logs_access_completed_id,priority:1"`
	GroupID                 uint   `gorm:"not null;default:0"`
	Protocol                string `gorm:"type:varchar(32);not null"`
	ClientModel             string `gorm:"type:varchar(255);not null;index:idx_request_logs_model_completed_id,priority:1"`
	UpstreamModel           string `gorm:"type:varchar(255);not null"`
	Status                  string `gorm:"type:varchar(32);not null;index:idx_request_logs_status_completed_id,priority:1"`
	StatusCode              int    `gorm:"not null"`
	DurationMs              int64  `gorm:"not null"`
	ErrorCode               string `gorm:"type:varchar(64);not null;default:''"`
	ErrorSummary            string `gorm:"type:text;not null;default:''"`
	AffinityHit             bool   `gorm:"not null;default:false"`
	UncachedInputTokens     int64  `gorm:"column:uncached_input_tokens;not null;default:0"`
	OutputTokens            int64  `gorm:"not null;default:0"`
	CacheReadTokens         int64  `gorm:"not null;default:0"`
	CacheWrite5MTokens      int64  `gorm:"column:cache_write_5m_tokens;not null;default:0"`
	CacheWrite1HTokens      int64  `gorm:"column:cache_write_1h_tokens;not null;default:0"`
	CacheWriteUnknownTokens int64  `gorm:"column:cache_write_unknown_tokens;not null;default:0"`
	EstimatedCostNanoUSD    int64  `gorm:"column:estimated_cost_nano_usd;not null;default:0;check:chk_request_log_cost_nano,estimated_cost_nano_usd >= 0"`
	UsageState              string `gorm:"type:varchar(32);not null;default:'not_applicable';check:chk_request_log_usage_state,usage_state IN ('complete','partial','missing','not_applicable')"`
	CostState               string `gorm:"type:varchar(32);not null;default:'not_applicable';check:chk_request_log_cost_state,cost_state IN ('priced','unpriced','not_applicable')"`
	PricingCompleteness     string `gorm:"type:varchar(32);not null;default:'not_applicable';check:chk_request_log_pricing_completeness,pricing_completeness IN ('complete','partial','unavailable','not_applicable')"`
	Attempts                JSON   `gorm:"type:json"`
}

// UsageStat is an hourly aggregate by access key, upstream group, and upstream model.
type UsageStat struct {
	ID                      uint   `gorm:"primaryKey;autoIncrement"`
	BucketStartMS           int64  `gorm:"column:bucket_start_ms;not null;uniqueIndex:idx_usage_stats_bucket_access_group_model,priority:1"`
	AccessKeyID             uint   `gorm:"not null;uniqueIndex:idx_usage_stats_bucket_access_group_model,priority:2"`
	GroupID                 uint   `gorm:"not null;uniqueIndex:idx_usage_stats_bucket_access_group_model,priority:3"`
	Model                   string `gorm:"type:varchar(255);not null;uniqueIndex:idx_usage_stats_bucket_access_group_model,priority:4"`
	RequestCount            int64  `gorm:"not null;default:0"`
	SuccessCount            int64  `gorm:"not null;default:0"`
	FailureCount            int64  `gorm:"not null;default:0"`
	UncachedInputTokens     int64  `gorm:"column:uncached_input_tokens;not null;default:0"`
	OutputTokens            int64  `gorm:"not null;default:0"`
	CacheReadTokens         int64  `gorm:"not null;default:0"`
	CacheWrite5MTokens      int64  `gorm:"column:cache_write_5m_tokens;not null;default:0"`
	CacheWrite1HTokens      int64  `gorm:"column:cache_write_1h_tokens;not null;default:0"`
	CacheWriteUnknownTokens int64  `gorm:"column:cache_write_unknown_tokens;not null;default:0"`
	EstimatedCostNanoUSD    int64  `gorm:"not null;default:0;check:chk_usage_stat_cost_nano,estimated_cost_nano_usd >= 0"`
	UsageMissingCount       int64  `gorm:"not null;default:0"`
	PartialCount            int64  `gorm:"not null;default:0"`
	UnpricedRequestCount    int64  `gorm:"not null;default:0"`
	PricingPartialCount     int64  `gorm:"not null;default:0"`
}
