package models

// RequestLog is the durable request-level audit and usage record.
type RequestLog struct {
	ID                      string              `gorm:"type:varchar(36);primaryKey;not null;index:idx_request_logs_completed_id,priority:2,sort:desc;index:idx_request_logs_access_completed_id,priority:3,sort:desc;index:idx_request_logs_status_completed_id,priority:3,sort:desc;index:idx_request_logs_model_completed_id,priority:3,sort:desc;index:idx_request_logs_upstream_model_completed_id,priority:3,sort:desc"`
	CompletedAtMS           int64               `gorm:"column:completed_at_ms;not null;check:chk_request_log_completed_at,completed_at_ms >= 0;index:idx_request_logs_completed_id,priority:1,sort:desc;index:idx_request_logs_access_completed_id,priority:2,sort:desc;index:idx_request_logs_status_completed_id,priority:2,sort:desc;index:idx_request_logs_model_completed_id,priority:2,sort:desc;index:idx_request_logs_upstream_model_completed_id,priority:2,sort:desc"`
	AccessKeyID             uint                `gorm:"not null;index:idx_request_logs_access_completed_id,priority:1"`
	GroupID                 uint                `gorm:"not null;default:0"`
	ChannelID               string              `gorm:"type:varchar(64);not null;default:''"`
	CredentialID            uint                `gorm:"not null;default:0"`
	Protocol                string              `gorm:"type:varchar(32);not null"`
	Operation               string              `gorm:"type:varchar(64);not null;default:''"`
	ClientModel             string              `gorm:"type:varchar(255);not null;index:idx_request_logs_model_completed_id,priority:1"`
	UpstreamModel           string              `gorm:"type:varchar(255);not null;index:idx_request_logs_upstream_model_completed_id,priority:1"`
	UpstreamReportedModel   string              `gorm:"type:varchar(255);not null;default:''"`
	ModelConsistency        string              `gorm:"type:varchar(32);not null;default:'not_applicable';check:chk_request_log_model_consistency,model_consistency IN ('not_applicable','match','unknown','mismatch')"`
	Status                  string              `gorm:"type:varchar(32);not null;check:chk_request_log_status,status IN ('success','error','incomplete','canceled');index:idx_request_logs_status_completed_id,priority:1"`
	StatusCode              int                 `gorm:"not null"`
	Stream                  bool                `gorm:"not null;default:false"`
	FirstResponseMs         *int64              `gorm:"column:first_response_ms;check:chk_request_log_first_response,first_response_ms IS NULL OR first_response_ms >= 0"`
	DurationMs              int64               `gorm:"not null;check:chk_request_log_duration,duration_ms >= 0"`
	AttemptCount            int                 `gorm:"not null;default:0;check:chk_request_log_attempt_count,attempt_count >= 0"`
	ErrorCode               string              `gorm:"type:varchar(64);not null;default:''"`
	ErrorSummary            string              `gorm:"type:text;not null"`
	AffinityHit             bool                `gorm:"not null;default:false"`
	ReasoningMode           string              `gorm:"type:varchar(64);not null;default:''"`
	ReasoningEffort         string              `gorm:"type:varchar(64);not null;default:''"`
	ReasoningBudgetTokens   *int64              `gorm:"column:reasoning_budget_tokens"`
	UncachedInputTokens     int64               `gorm:"column:uncached_input_tokens;not null;default:0;check:chk_request_log_uncached_input,uncached_input_tokens >= 0"`
	OutputTokens            int64               `gorm:"not null;default:0;check:chk_request_log_output,output_tokens >= 0"`
	CacheReadTokens         int64               `gorm:"not null;default:0;check:chk_request_log_cache_read,cache_read_tokens >= 0"`
	CacheWrite5MTokens      int64               `gorm:"column:cache_write_5m_tokens;not null;default:0;check:chk_request_log_cache_write_5m,cache_write_5m_tokens >= 0"`
	CacheWrite1HTokens      int64               `gorm:"column:cache_write_1h_tokens;not null;default:0;check:chk_request_log_cache_write_1h,cache_write_1h_tokens >= 0"`
	CacheWriteUnknownTokens int64               `gorm:"column:cache_write_unknown_tokens;not null;default:0;check:chk_request_log_cache_write_unknown,cache_write_unknown_tokens >= 0"`
	EstimatedCostNanoUSD    int64               `gorm:"column:estimated_cost_nano_usd;not null;default:0;check:chk_request_log_cost_nano,estimated_cost_nano_usd >= 0"`
	UsageState              string              `gorm:"type:varchar(32);not null;default:'not_applicable';check:chk_request_log_usage_state,usage_state IN ('complete','partial','missing','not_applicable')"`
	CostState               string              `gorm:"type:varchar(32);not null;default:'not_applicable';check:chk_request_log_cost_state,cost_state IN ('priced','unpriced','not_applicable')"`
	PricingCompleteness     string              `gorm:"type:varchar(32);not null;default:'not_applicable';check:chk_request_log_pricing_completeness,pricing_completeness IN ('complete','partial','unavailable','not_applicable');check:chk_request_log_usage_pricing_state,(usage_state = 'not_applicable' AND cost_state = 'not_applicable' AND pricing_completeness = 'not_applicable' AND estimated_cost_nano_usd = 0) OR (usage_state = 'missing' AND cost_state = 'unpriced' AND pricing_completeness = 'unavailable' AND estimated_cost_nano_usd = 0) OR (usage_state IN ('complete','partial') AND ((cost_state = 'unpriced' AND pricing_completeness = 'unavailable' AND estimated_cost_nano_usd = 0) OR (cost_state = 'priced' AND pricing_completeness IN ('complete','partial'))))"`
	AttemptRows             []RequestLogAttempt `gorm:"-"`
}

// RequestLogAttempt is one durable upstream attempt belonging to a client
// request. Identity fields are snapshots and intentionally do not reference
// mutable Group or Credential catalog rows.
type RequestLogAttempt struct {
	RequestID             string      `gorm:"type:varchar(36);primaryKey;not null;index:idx_request_log_attempts_group_completed_request,priority:3;index:idx_request_log_attempts_channel_completed_request,priority:3;index:idx_request_log_attempts_credential_completed_request,priority:3;index:idx_request_log_attempts_model_completed_request,priority:3;index:idx_request_log_attempts_status_completed_request,priority:3;index:idx_request_log_attempts_failure_completed_request,priority:3;index:idx_request_log_attempts_error_completed_request,priority:3"`
	Sequence              int         `gorm:"primaryKey;not null;check:chk_request_log_attempt_sequence,sequence > 0"`
	CompletedAtMS         int64       `gorm:"column:completed_at_ms;not null;check:chk_request_log_attempt_completed_at,completed_at_ms >= 0;index:idx_request_log_attempts_group_completed_request,priority:2,sort:desc;index:idx_request_log_attempts_credential_completed_request,priority:2,sort:desc;index:idx_request_log_attempts_channel_completed_request,priority:2,sort:desc;index:idx_request_log_attempts_model_completed_request,priority:2,sort:desc;index:idx_request_log_attempts_status_completed_request,priority:2,sort:desc;index:idx_request_log_attempts_failure_completed_request,priority:2,sort:desc;index:idx_request_log_attempts_error_completed_request,priority:2,sort:desc"`
	GroupID               uint        `gorm:"not null;check:chk_request_log_attempt_group,group_id > 0;index:idx_request_log_attempts_group_completed_request,priority:1"`
	GroupName             string      `gorm:"type:varchar(255);not null"`
	ChannelID             string      `gorm:"type:varchar(64);not null;default:'';index:idx_request_log_attempts_channel_completed_request,priority:1"`
	CredentialID          uint        `gorm:"not null;check:chk_request_log_attempt_credential,credential_id > 0;index:idx_request_log_attempts_credential_completed_request,priority:1"`
	Operation             string      `gorm:"type:varchar(64);not null;default:''"`
	RouteMode             string      `gorm:"type:varchar(32);not null;default:''"`
	UpstreamModel         string      `gorm:"type:varchar(255);not null;default:''"`
	UpstreamRequestID     string      `gorm:"type:varchar(255);not null;default:''"`
	DispatchState         string      `gorm:"type:varchar(32);not null;default:''"`
	ResponseStarted       bool        `gorm:"not null;default:false"`
	UpstreamAPI           string      `gorm:"type:varchar(64);not null;default:''"`
	ReasoningMode         string      `gorm:"type:varchar(64);not null;default:''"`
	ReasoningEffort       string      `gorm:"type:varchar(64);not null;default:''"`
	ReasoningBudgetTokens *int64      `gorm:"column:reasoning_budget_tokens"`
	StatusCode            int         `gorm:"not null;index:idx_request_log_attempts_status_completed_request,priority:1"`
	DurationMs            int64       `gorm:"not null;check:chk_request_log_attempt_duration,duration_ms >= 0"`
	FailureCategory       string      `gorm:"type:varchar(32);not null;check:chk_request_log_attempt_failure_category,failure_category IN ('ok','rate_limited','model_unavailable','invalid_key','upstream_host_error','client_error','conversion_unsupported','downstream_cancel','ambiguous');index:idx_request_log_attempts_failure_completed_request,priority:1"`
	Action                string      `gorm:"type:varchar(32);not null;check:chk_request_log_attempt_action,action IN ('terminate','retry','cooldown_credential','fail_credential','skip_group')"`
	WillRetry             bool        `gorm:"not null;default:false"`
	ErrorCode             string      `gorm:"type:varchar(64);not null;default:'';index:idx_request_log_attempts_error_completed_request,priority:1"`
	ErrorSummary          string      `gorm:"type:text;not null"`
	Committed             bool        `gorm:"not null;default:false"`
	PricingReceipt        JSON        `gorm:"type:json"`
	RequestLog            *RequestLog `gorm:"foreignKey:RequestID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

// UsageAggregationJournal is the request-idempotent input for hourly usage
// aggregation. It is staged, applied, and committed in the same transaction as
// its RequestLog, and intentionally excludes request and error payloads.
type UsageAggregationJournal struct {
	RequestID               string `gorm:"column:request_id;type:varchar(36);primaryKey;not null"`
	BucketStartMS           int64  `gorm:"column:bucket_start_ms;not null;check:chk_usage_journal_bucket, bucket_start_ms >= 0;index:idx_usage_aggregation_journal_pending_bucket,priority:2"`
	AccessKeyID             uint   `gorm:"not null"`
	GroupID                 uint   `gorm:"not null"`
	ChannelID               string `gorm:"type:varchar(64);not null;default:''"`
	CredentialID            uint   `gorm:"not null;default:0"`
	Model                   string `gorm:"type:varchar(255);not null"`
	RequestCount            int64  `gorm:"not null;check:chk_usage_journal_request_count,request_count = 1;check:chk_usage_journal_request_outcome,request_count = success_count + failure_count"`
	SuccessCount            int64  `gorm:"not null;check:chk_usage_journal_success_count,success_count >= 0"`
	FailureCount            int64  `gorm:"not null;check:chk_usage_journal_failure_count,failure_count >= 0"`
	UncachedInputTokens     int64  `gorm:"column:uncached_input_tokens;not null;check:chk_usage_journal_uncached_input,uncached_input_tokens >= 0"`
	OutputTokens            int64  `gorm:"not null;check:chk_usage_journal_output,output_tokens >= 0"`
	CacheReadTokens         int64  `gorm:"not null;check:chk_usage_journal_cache_read,cache_read_tokens >= 0"`
	CacheWrite5MTokens      int64  `gorm:"column:cache_write_5m_tokens;not null;check:chk_usage_journal_cache_write_5m,cache_write_5m_tokens >= 0"`
	CacheWrite1HTokens      int64  `gorm:"column:cache_write_1h_tokens;not null;check:chk_usage_journal_cache_write_1h,cache_write_1h_tokens >= 0"`
	CacheWriteUnknownTokens int64  `gorm:"column:cache_write_unknown_tokens;not null;check:chk_usage_journal_cache_write_unknown,cache_write_unknown_tokens >= 0"`
	EstimatedCostNanoUSD    int64  `gorm:"column:estimated_cost_nano_usd;not null;check:chk_usage_journal_cost_nano,estimated_cost_nano_usd >= 0"`
	UsageMissingCount       int64  `gorm:"not null;check:chk_usage_journal_usage_missing,usage_missing_count >= 0"`
	PartialCount            int64  `gorm:"not null;check:chk_usage_journal_partial,partial_count >= 0"`
	UnpricedRequestCount    int64  `gorm:"not null;check:chk_usage_journal_unpriced,unpriced_request_count >= 0"`
	PricingPartialCount     int64  `gorm:"not null;check:chk_usage_journal_pricing_partial,pricing_partial_count >= 0"`
	Applied                 bool   `gorm:"not null;default:false;check:chk_usage_journal_applied,applied IN (TRUE, FALSE);index:idx_usage_aggregation_journal_pending_bucket,priority:1"`
}

func (UsageAggregationJournal) TableName() string {
	return "usage_aggregation_journal"
}

// UsageStat is an hourly aggregate by access key, channel, upstream group,
// credential, and upstream model.
type UsageStat struct {
	ID                      uint   `gorm:"primaryKey;autoIncrement"`
	BucketStartMS           int64  `gorm:"column:bucket_start_ms;not null;check:chk_usage_stat_bucket,bucket_start_ms >= 0;uniqueIndex:idx_usage_stats_identity,priority:1"`
	AccessKeyID             uint   `gorm:"not null;uniqueIndex:idx_usage_stats_identity,priority:2"`
	ChannelID               string `gorm:"type:varchar(64);not null;default:'';uniqueIndex:idx_usage_stats_identity,priority:3"`
	GroupID                 uint   `gorm:"not null;uniqueIndex:idx_usage_stats_identity,priority:4"`
	CredentialID            uint   `gorm:"not null;default:0;uniqueIndex:idx_usage_stats_identity,priority:5"`
	Model                   string `gorm:"type:varchar(255);not null;uniqueIndex:idx_usage_stats_identity,priority:6"`
	RequestCount            int64  `gorm:"not null;default:0;check:chk_usage_stat_request_count,request_count >= 0;check:chk_usage_stat_request_outcome,request_count = success_count + failure_count"`
	SuccessCount            int64  `gorm:"not null;default:0;check:chk_usage_stat_success_count,success_count >= 0"`
	FailureCount            int64  `gorm:"not null;default:0;check:chk_usage_stat_failure_count,failure_count >= 0"`
	UncachedInputTokens     int64  `gorm:"column:uncached_input_tokens;not null;default:0;check:chk_usage_stat_uncached_input,uncached_input_tokens >= 0"`
	OutputTokens            int64  `gorm:"not null;default:0;check:chk_usage_stat_output,output_tokens >= 0"`
	CacheReadTokens         int64  `gorm:"not null;default:0;check:chk_usage_stat_cache_read,cache_read_tokens >= 0"`
	CacheWrite5MTokens      int64  `gorm:"column:cache_write_5m_tokens;not null;default:0;check:chk_usage_stat_cache_write_5m,cache_write_5m_tokens >= 0"`
	CacheWrite1HTokens      int64  `gorm:"column:cache_write_1h_tokens;not null;default:0;check:chk_usage_stat_cache_write_1h,cache_write_1h_tokens >= 0"`
	CacheWriteUnknownTokens int64  `gorm:"column:cache_write_unknown_tokens;not null;default:0;check:chk_usage_stat_cache_write_unknown,cache_write_unknown_tokens >= 0"`
	EstimatedCostNanoUSD    int64  `gorm:"not null;default:0;check:chk_usage_stat_cost_nano,estimated_cost_nano_usd >= 0"`
	UsageMissingCount       int64  `gorm:"not null;default:0;check:chk_usage_stat_usage_missing,usage_missing_count >= 0"`
	PartialCount            int64  `gorm:"not null;default:0;check:chk_usage_stat_partial,partial_count >= 0"`
	UnpricedRequestCount    int64  `gorm:"not null;default:0;check:chk_usage_stat_unpriced,unpriced_request_count >= 0"`
	PricingPartialCount     int64  `gorm:"not null;default:0;check:chk_usage_stat_pricing_partial,pricing_partial_count >= 0"`
}
