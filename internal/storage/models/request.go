package models

// RequestLog is the durable request-level audit and usage record.
type RequestLog struct {
	ID                      string              `gorm:"type:varchar(36);primaryKey;not null;index:idx_request_logs_completed_id,priority:2,sort:desc;index:idx_request_logs_access_completed_id,priority:3,sort:desc;index:idx_request_logs_status_completed_id,priority:3,sort:desc;index:idx_request_logs_model_completed_id,priority:3,sort:desc;index:idx_request_logs_upstream_model_completed_id,priority:3,sort:desc"`
	CompletedAtMS           int64               `gorm:"column:completed_at_ms;not null;index:idx_request_logs_completed_id,priority:1,sort:desc;index:idx_request_logs_access_completed_id,priority:2,sort:desc;index:idx_request_logs_status_completed_id,priority:2,sort:desc;index:idx_request_logs_model_completed_id,priority:2,sort:desc;index:idx_request_logs_upstream_model_completed_id,priority:2,sort:desc"`
	AccessKeyID             uint                `gorm:"not null;index:idx_request_logs_access_completed_id,priority:1"`
	GroupID                 uint                `gorm:"not null;default:0"`
	Protocol                string              `gorm:"type:varchar(32);not null"`
	ClientModel             string              `gorm:"type:varchar(255);not null;index:idx_request_logs_model_completed_id,priority:1"`
	UpstreamModel           string              `gorm:"type:varchar(255);not null;index:idx_request_logs_upstream_model_completed_id,priority:1"`
	Status                  string              `gorm:"type:varchar(32);not null;index:idx_request_logs_status_completed_id,priority:1"`
	StatusCode              int                 `gorm:"not null"`
	Stream                  bool                `gorm:"not null;default:false"`
	FirstResponseMs         *int64              `gorm:"column:first_response_ms"`
	DurationMs              int64               `gorm:"not null"`
	AttemptCount            int                 `gorm:"not null;default:0"`
	ErrorCode               string              `gorm:"type:varchar(64);not null;default:''"`
	ErrorSummary            string              `gorm:"type:text;not null;default:''"`
	AffinityHit             bool                `gorm:"not null;default:false"`
	ReasoningMode           string              `gorm:"type:varchar(64);not null;default:''"`
	ReasoningEffort         string              `gorm:"type:varchar(64);not null;default:''"`
	ReasoningBudgetTokens   *int64              `gorm:"column:reasoning_budget_tokens"`
	UncachedInputTokens     int64               `gorm:"column:uncached_input_tokens;not null;default:0"`
	OutputTokens            int64               `gorm:"not null;default:0"`
	CacheReadTokens         int64               `gorm:"not null;default:0"`
	CacheWrite5MTokens      int64               `gorm:"column:cache_write_5m_tokens;not null;default:0"`
	CacheWrite1HTokens      int64               `gorm:"column:cache_write_1h_tokens;not null;default:0"`
	CacheWriteUnknownTokens int64               `gorm:"column:cache_write_unknown_tokens;not null;default:0"`
	EstimatedCostNanoUSD    int64               `gorm:"column:estimated_cost_nano_usd;not null;default:0;check:chk_request_log_cost_nano,estimated_cost_nano_usd >= 0"`
	UsageState              string              `gorm:"type:varchar(32);not null;default:'not_applicable';check:chk_request_log_usage_state,usage_state IN ('complete','partial','missing','not_applicable')"`
	CostState               string              `gorm:"type:varchar(32);not null;default:'not_applicable';check:chk_request_log_cost_state,cost_state IN ('priced','unpriced','not_applicable')"`
	PricingCompleteness     string              `gorm:"type:varchar(32);not null;default:'not_applicable';check:chk_request_log_pricing_completeness,pricing_completeness IN ('complete','partial','unavailable','not_applicable')"`
	AttemptRows             []RequestLogAttempt `gorm:"-"`
}

// RequestLogAttempt is one durable upstream attempt belonging to a client
// request. Identity fields are snapshots and intentionally do not reference
// mutable Group or Key catalog rows.
type RequestLogAttempt struct {
	RequestID       string `gorm:"type:varchar(36);primaryKey;not null"`
	Sequence        int    `gorm:"primaryKey;not null"`
	CompletedAtMS   int64  `gorm:"column:completed_at_ms;not null"`
	GroupID         uint   `gorm:"not null"`
	GroupName       string `gorm:"type:varchar(255);not null"`
	KeyID           uint   `gorm:"not null"`
	UpstreamModel   string `gorm:"type:varchar(255);not null;default:''"`
	StatusCode      int    `gorm:"not null"`
	DurationMs      int64  `gorm:"not null"`
	FailureCategory string `gorm:"type:varchar(32);not null"`
	Action          string `gorm:"type:varchar(32);not null"`
	WillRetry       bool   `gorm:"not null;default:false"`
	ErrorCode       string `gorm:"type:varchar(64);not null;default:''"`
	ErrorSummary    string `gorm:"type:text;not null;default:''"`
	PricingReceipt  JSON   `gorm:"type:json"`
}

// UsageAggregationJournal is the request-idempotent input for hourly usage
// aggregation. It is staged, applied, and committed in the same transaction as
// its RequestLog, and intentionally excludes request and error payloads.
type UsageAggregationJournal struct {
	RequestID               string `gorm:"column:request_id;type:varchar(36);primaryKey;not null"`
	BucketStartMS           int64  `gorm:"column:bucket_start_ms;not null"`
	AccessKeyID             uint   `gorm:"not null"`
	GroupID                 uint   `gorm:"not null"`
	Model                   string `gorm:"type:varchar(255);not null"`
	RequestCount            int64  `gorm:"not null"`
	SuccessCount            int64  `gorm:"not null"`
	FailureCount            int64  `gorm:"not null"`
	UncachedInputTokens     int64  `gorm:"column:uncached_input_tokens;not null"`
	OutputTokens            int64  `gorm:"not null"`
	CacheReadTokens         int64  `gorm:"not null"`
	CacheWrite5MTokens      int64  `gorm:"column:cache_write_5m_tokens;not null"`
	CacheWrite1HTokens      int64  `gorm:"column:cache_write_1h_tokens;not null"`
	CacheWriteUnknownTokens int64  `gorm:"column:cache_write_unknown_tokens;not null"`
	EstimatedCostNanoUSD    int64  `gorm:"column:estimated_cost_nano_usd;not null"`
	UsageMissingCount       int64  `gorm:"not null"`
	PartialCount            int64  `gorm:"not null"`
	UnpricedRequestCount    int64  `gorm:"not null"`
	PricingPartialCount     int64  `gorm:"not null"`
	Applied                 bool   `gorm:"not null;default:false"`
}

func (UsageAggregationJournal) TableName() string {
	return "usage_aggregation_journal"
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
