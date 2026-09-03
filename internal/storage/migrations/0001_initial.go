package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// These migration-local models freeze the unpublished v2 baseline. Runtime
// storage models may evolve only through later migrations.
type initialJSON []byte

type initialGroup struct {
	ID              uint        `gorm:"primaryKey;autoIncrement"`
	Name            string      `gorm:"type:varchar(255);not null;uniqueIndex"`
	ChannelID       string      `gorm:"type:varchar(64);not null"`
	ConnectionType  string      `gorm:"type:varchar(32);not null;default:'api_key';check:chk_group_connection_type,connection_type IN ('api_key','subscription')"`
	Params          initialJSON `gorm:"type:json;not null"`
	Models          initialJSON `gorm:"type:json;not null"`
	WeightManual    *int
	ValidationModel *string             `gorm:"type:varchar(255)"`
	Overrides       initialJSON         `gorm:"type:json"`
	Enabled         bool                `gorm:"not null;default:true"`
	Credentials     []initialCredential `gorm:"foreignKey:GroupID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	CreatedAtMS     int64               `gorm:"column:created_at_ms;not null;autoCreateTime:milli;check:chk_group_created_at,created_at_ms >= 0"`
	UpdatedAtMS     int64               `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_group_updated_at,updated_at_ms >= 0"`
}

func (initialGroup) TableName() string { return "groups" }

type initialCredential struct {
	ID                  uint   `gorm:"primaryKey;autoIncrement"`
	GroupID             uint   `gorm:"not null;uniqueIndex:idx_credentials_group_fingerprint,priority:1;uniqueIndex:idx_credentials_group_identity,priority:1"`
	Data                string `gorm:"type:text;not null"`
	Fingerprint         string `gorm:"type:varchar(128);not null;uniqueIndex:idx_credentials_group_fingerprint,priority:2"`
	IdentityFingerprint string `gorm:"type:varchar(128);not null;default:'';uniqueIndex:idx_credentials_group_identity,priority:2"`
	SecretVersion       uint64 `gorm:"not null;default:1;check:chk_credential_secret_version,secret_version > 0"`
	AuthState           string `gorm:"type:varchar(32);not null;default:'ready';check:chk_credential_auth_state,auth_state IN ('ready','refreshing','reauthorization_required','outcome_unknown')"`
	AuthErrorCode       string `gorm:"type:varchar(64);not null;default:''"`
	Status              string `gorm:"type:varchar(32);not null;default:'active';check:chk_credential_status,status IN ('active','disabled')"`
	WeightManual        *int
	Group               *initialGroup `gorm:"foreignKey:GroupID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	CreatedAtMS         int64         `gorm:"column:created_at_ms;not null;autoCreateTime:milli;check:chk_credential_created_at,created_at_ms >= 0"`
	UpdatedAtMS         int64         `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_credential_updated_at,updated_at_ms >= 0"`
}

func (initialCredential) TableName() string { return "credentials" }

type initialAccessKey struct {
	ID                      uint        `gorm:"primaryKey;autoIncrement"`
	Name                    string      `gorm:"type:varchar(255);not null"`
	KeyValue                string      `gorm:"type:text;not null"`
	KeyHash                 string      `gorm:"type:varchar(128);not null;uniqueIndex"`
	KeySuffix               string      `gorm:"type:char(4);not null;check:chk_access_key_suffix,length(key_suffix) = 4 AND substr(key_suffix, 1, 1) IN ('0','1','2','3','4','5','6','7','8','9','a','b','c','d','e','f') AND substr(key_suffix, 2, 1) IN ('0','1','2','3','4','5','6','7','8','9','a','b','c','d','e','f') AND substr(key_suffix, 3, 1) IN ('0','1','2','3','4','5','6','7','8','9','a','b','c','d','e','f') AND substr(key_suffix, 4, 1) IN ('0','1','2','3','4','5','6','7','8','9','a','b','c','d','e','f')"`
	Status                  string      `gorm:"type:varchar(32);not null;default:'active';check:chk_access_key_status,status IN ('active','disabled')"`
	Filters                 initialJSON `gorm:"type:json"`
	RPMLimit                int64       `gorm:"not null;default:0"`
	DailyCostLimitNanoUSD   int64       `gorm:"column:daily_cost_limit_nano_usd;not null;default:0;check:chk_access_key_daily_cost_limit_nano,daily_cost_limit_nano_usd >= 0"`
	MonthlyCostLimitNanoUSD int64       `gorm:"column:monthly_cost_limit_nano_usd;not null;default:0;check:chk_access_key_monthly_cost_limit_nano,monthly_cost_limit_nano_usd >= 0"`
	CreatedAtMS             int64       `gorm:"column:created_at_ms;not null;autoCreateTime:milli;check:chk_access_key_created_at,created_at_ms >= 0"`
	UpdatedAtMS             int64       `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_access_key_updated_at,updated_at_ms >= 0"`
}

func (initialAccessKey) TableName() string { return "access_keys" }

type initialRequestLog struct {
	ID                      string                     `gorm:"type:varchar(36);primaryKey;not null;index:idx_request_logs_completed_id,priority:2,sort:desc;index:idx_request_logs_access_completed_id,priority:3,sort:desc;index:idx_request_logs_status_completed_id,priority:3,sort:desc;index:idx_request_logs_model_completed_id,priority:3,sort:desc;index:idx_request_logs_upstream_model_completed_id,priority:3,sort:desc;index:idx_request_logs_credential_completed_id,priority:3,sort:desc"`
	CompletedAtMS           int64                      `gorm:"column:completed_at_ms;not null;check:chk_request_log_completed_at,completed_at_ms >= 0;index:idx_request_logs_completed_id,priority:1,sort:desc;index:idx_request_logs_access_completed_id,priority:2,sort:desc;index:idx_request_logs_status_completed_id,priority:2,sort:desc;index:idx_request_logs_model_completed_id,priority:2,sort:desc;index:idx_request_logs_upstream_model_completed_id,priority:2,sort:desc;index:idx_request_logs_credential_completed_id,priority:2,sort:desc"`
	AccessKeyID             uint                       `gorm:"not null;index:idx_request_logs_access_completed_id,priority:1"`
	GroupID                 uint                       `gorm:"not null;default:0"`
	ChannelID               string                     `gorm:"type:varchar(64);not null;default:''"`
	CredentialID            uint                       `gorm:"not null;default:0;index:idx_request_logs_credential_completed_id,priority:1"`
	Protocol                string                     `gorm:"type:varchar(32);not null"`
	Operation               string                     `gorm:"type:varchar(64);not null;default:''"`
	ClientModel             string                     `gorm:"type:varchar(255);not null;index:idx_request_logs_model_completed_id,priority:1"`
	UpstreamModel           string                     `gorm:"type:varchar(255);not null;index:idx_request_logs_upstream_model_completed_id,priority:1"`
	UpstreamReportedModel   string                     `gorm:"type:varchar(255);not null;default:''"`
	ModelConsistency        string                     `gorm:"type:varchar(32);not null;default:'not_applicable';check:chk_request_log_model_consistency,model_consistency IN ('not_applicable','match','unknown','mismatch')"`
	Status                  string                     `gorm:"type:varchar(32);not null;check:chk_request_log_status,status IN ('success','error','incomplete','canceled');index:idx_request_logs_status_completed_id,priority:1"`
	StatusCode              int                        `gorm:"not null"`
	Stream                  bool                       `gorm:"not null;default:false"`
	FirstResponseMs         *int64                     `gorm:"column:first_response_ms;check:chk_request_log_first_response,first_response_ms IS NULL OR first_response_ms >= 0"`
	DurationMs              int64                      `gorm:"not null;check:chk_request_log_duration,duration_ms >= 0"`
	AttemptCount            int                        `gorm:"not null;default:0;check:chk_request_log_attempt_count,attempt_count >= 0"`
	ErrorCode               string                     `gorm:"type:varchar(64);not null;default:''"`
	ErrorSummary            string                     `gorm:"type:text;not null"`
	AffinityHit             bool                       `gorm:"not null;default:false"`
	ReasoningMode           string                     `gorm:"type:varchar(64);not null;default:''"`
	ReasoningEffort         string                     `gorm:"type:varchar(64);not null;default:''"`
	ReasoningBudgetTokens   *int64                     `gorm:"column:reasoning_budget_tokens"`
	UncachedInputTokens     int64                      `gorm:"column:uncached_input_tokens;not null;default:0;check:chk_request_log_uncached_input,uncached_input_tokens >= 0"`
	OutputTokens            int64                      `gorm:"not null;default:0;check:chk_request_log_output,output_tokens >= 0"`
	CacheReadTokens         int64                      `gorm:"not null;default:0;check:chk_request_log_cache_read,cache_read_tokens >= 0"`
	CacheWrite5MTokens      int64                      `gorm:"column:cache_write_5m_tokens;not null;default:0;check:chk_request_log_cache_write_5m,cache_write_5m_tokens >= 0"`
	CacheWrite1HTokens      int64                      `gorm:"column:cache_write_1h_tokens;not null;default:0;check:chk_request_log_cache_write_1h,cache_write_1h_tokens >= 0"`
	CacheWriteUnknownTokens int64                      `gorm:"column:cache_write_unknown_tokens;not null;default:0;check:chk_request_log_cache_write_unknown,cache_write_unknown_tokens >= 0"`
	EstimatedCostNanoUSD    int64                      `gorm:"column:estimated_cost_nano_usd;not null;default:0;check:chk_request_log_cost_nano,estimated_cost_nano_usd >= 0"`
	UsageState              string                     `gorm:"type:varchar(32);not null;default:'not_applicable';check:chk_request_log_usage_state,usage_state IN ('complete','partial','missing','not_applicable')"`
	CostState               string                     `gorm:"type:varchar(32);not null;default:'not_applicable';check:chk_request_log_cost_state,cost_state IN ('priced','unpriced','not_applicable')"`
	PricingCompleteness     string                     `gorm:"type:varchar(32);not null;default:'not_applicable';check:chk_request_log_pricing_completeness,pricing_completeness IN ('complete','partial','unavailable','not_applicable');check:chk_request_log_usage_pricing_state,(usage_state = 'not_applicable' AND cost_state = 'not_applicable' AND pricing_completeness = 'not_applicable' AND estimated_cost_nano_usd = 0) OR (usage_state = 'missing' AND cost_state = 'unpriced' AND pricing_completeness = 'unavailable' AND estimated_cost_nano_usd = 0) OR (usage_state IN ('complete','partial') AND ((cost_state = 'unpriced' AND pricing_completeness = 'unavailable' AND estimated_cost_nano_usd = 0) OR (cost_state = 'priced' AND pricing_completeness IN ('complete','partial'))))"`
	AttemptRows             []initialRequestLogAttempt `gorm:"-"`
}

func (initialRequestLog) TableName() string { return "request_logs" }

type initialRequestLogAttempt struct {
	RequestID             string             `gorm:"type:varchar(36);primaryKey;not null;index:idx_request_log_attempts_group_completed_request,priority:3;index:idx_request_log_attempts_channel_completed_request,priority:3;index:idx_request_log_attempts_credential_completed_request,priority:3;index:idx_request_log_attempts_model_completed_request,priority:3;index:idx_request_log_attempts_status_completed_request,priority:3;index:idx_request_log_attempts_failure_completed_request,priority:3;index:idx_request_log_attempts_error_completed_request,priority:3"`
	Sequence              int                `gorm:"primaryKey;not null;check:chk_request_log_attempt_sequence,sequence > 0"`
	CompletedAtMS         int64              `gorm:"column:completed_at_ms;not null;check:chk_request_log_attempt_completed_at,completed_at_ms >= 0;index:idx_request_log_attempts_group_completed_request,priority:2,sort:desc;index:idx_request_log_attempts_credential_completed_request,priority:2,sort:desc;index:idx_request_log_attempts_channel_completed_request,priority:2,sort:desc;index:idx_request_log_attempts_model_completed_request,priority:2,sort:desc;index:idx_request_log_attempts_status_completed_request,priority:2,sort:desc;index:idx_request_log_attempts_failure_completed_request,priority:2,sort:desc;index:idx_request_log_attempts_error_completed_request,priority:2,sort:desc"`
	GroupID               uint               `gorm:"not null;check:chk_request_log_attempt_group,group_id > 0;index:idx_request_log_attempts_group_completed_request,priority:1"`
	GroupName             string             `gorm:"type:varchar(255);not null"`
	ChannelID             string             `gorm:"type:varchar(64);not null;default:'';index:idx_request_log_attempts_channel_completed_request,priority:1"`
	CredentialID          uint               `gorm:"not null;check:chk_request_log_attempt_credential,credential_id > 0;index:idx_request_log_attempts_credential_completed_request,priority:1"`
	Operation             string             `gorm:"type:varchar(64);not null;default:''"`
	RouteMode             string             `gorm:"type:varchar(32);not null;default:''"`
	UpstreamModel         string             `gorm:"type:varchar(255);not null;default:''"`
	UpstreamRequestID     string             `gorm:"type:varchar(255);not null;default:''"`
	DispatchState         string             `gorm:"type:varchar(32);not null;default:''"`
	ResponseStarted       bool               `gorm:"not null;default:false"`
	UpstreamProtocol      string             `gorm:"type:varchar(32);not null;default:''"`
	ReasoningMode         string             `gorm:"type:varchar(64);not null;default:''"`
	ReasoningEffort       string             `gorm:"type:varchar(64);not null;default:''"`
	ReasoningBudgetTokens *int64             `gorm:"column:reasoning_budget_tokens"`
	StatusCode            int                `gorm:"not null;index:idx_request_log_attempts_status_completed_request,priority:1"`
	DurationMs            int64              `gorm:"not null;check:chk_request_log_attempt_duration,duration_ms >= 0"`
	FailureCategory       string             `gorm:"type:varchar(32);not null;check:chk_request_log_attempt_failure_category,failure_category IN ('ok','rate_limited','model_unavailable','invalid_key','upstream_host_error','client_error','conversion_unsupported','downstream_cancel','ambiguous');index:idx_request_log_attempts_failure_completed_request,priority:1"`
	Action                string             `gorm:"type:varchar(32);not null;check:chk_request_log_attempt_action,action IN ('terminate','retry','cooldown_credential','fail_credential','skip_group')"`
	WillRetry             bool               `gorm:"not null;default:false"`
	ErrorCode             string             `gorm:"type:varchar(64);not null;default:'';index:idx_request_log_attempts_error_completed_request,priority:1"`
	ErrorSummary          string             `gorm:"type:text;not null"`
	Committed             bool               `gorm:"not null;default:false"`
	PricingReceipt        initialJSON        `gorm:"type:json"`
	RequestLog            *initialRequestLog `gorm:"foreignKey:RequestID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

func (initialRequestLogAttempt) TableName() string { return "request_log_attempts" }

type initialUsageAggregationJournal struct {
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

func (initialUsageAggregationJournal) TableName() string { return "usage_aggregation_journal" }

type initialUsageStat struct {
	ID                      uint   `gorm:"primaryKey;autoIncrement"`
	BucketStartMS           int64  `gorm:"column:bucket_start_ms;not null;check:chk_usage_stat_bucket,bucket_start_ms >= 0;uniqueIndex:idx_usage_stats_identity,priority:1;index:idx_usage_stats_credential_bucket,priority:2"`
	AccessKeyID             uint   `gorm:"not null;uniqueIndex:idx_usage_stats_identity,priority:2"`
	ChannelID               string `gorm:"type:varchar(64);not null;default:'';uniqueIndex:idx_usage_stats_identity,priority:3"`
	GroupID                 uint   `gorm:"not null;uniqueIndex:idx_usage_stats_identity,priority:4"`
	CredentialID            uint   `gorm:"not null;default:0;uniqueIndex:idx_usage_stats_identity,priority:5;index:idx_usage_stats_credential_bucket,priority:1"`
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

func (initialUsageStat) TableName() string { return "usage_stats" }

type initialModelPrice struct {
	ID                                     uint        `gorm:"primaryKey;autoIncrement"`
	ChannelID                              string      `gorm:"type:varchar(64);not null;uniqueIndex:idx_model_prices_channel_model,priority:1"`
	ModelID                                string      `gorm:"type:varchar(255);not null;uniqueIndex:idx_model_prices_channel_model,priority:2"`
	InputPriceNanoUSDPerMillionTokens      *int64      `gorm:"column:input_price_nano_usd_per_million_tokens;check:chk_model_price_input_nano,input_price_nano_usd_per_million_tokens IS NULL OR input_price_nano_usd_per_million_tokens >= 0"`
	OutputPriceNanoUSDPerMillionTokens     *int64      `gorm:"column:output_price_nano_usd_per_million_tokens;check:chk_model_price_output_nano,output_price_nano_usd_per_million_tokens IS NULL OR output_price_nano_usd_per_million_tokens >= 0"`
	CacheReadPriceNanoUSDPerMillionTokens  *int64      `gorm:"column:cache_read_price_nano_usd_per_million_tokens;check:chk_model_price_cache_read_nano,cache_read_price_nano_usd_per_million_tokens IS NULL OR cache_read_price_nano_usd_per_million_tokens >= 0"`
	CacheWritePriceNanoUSDPerMillionTokens *int64      `gorm:"column:cache_write_price_nano_usd_per_million_tokens;check:chk_model_price_cache_write_nano,cache_write_price_nano_usd_per_million_tokens IS NULL OR cache_write_price_nano_usd_per_million_tokens >= 0"`
	ContextPriceTiers                      initialJSON `gorm:"type:json"`
	ModePriceSchedules                     initialJSON `gorm:"column:mode_price_schedules;type:json"`
	IsManual                               bool        `gorm:"not null;default:false"`
	CreatedAtMS                            int64       `gorm:"column:created_at_ms;not null;autoCreateTime:milli;check:chk_model_price_created_at,created_at_ms >= 0"`
	UpdatedAtMS                            int64       `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_model_price_updated_at,updated_at_ms >= 0"`
}

func (initialModelPrice) TableName() string { return "model_prices" }

type initialSystemSetting struct {
	Key         string `gorm:"type:varchar(255);primaryKey;not null"`
	Value       string `gorm:"type:text;not null"`
	UpdatedAtMS int64  `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_system_setting_updated_at,updated_at_ms >= 0"`
}

func (initialSystemSetting) TableName() string { return "system_settings" }

type initialJob struct {
	ID           string      `gorm:"type:varchar(36);primaryKey;not null"`
	Type         string      `gorm:"type:varchar(64);not null;index"`
	Status       string      `gorm:"type:varchar(32);not null;default:'pending';index"`
	Payload      initialJSON `gorm:"type:json"`
	Result       initialJSON `gorm:"type:json"`
	Error        string      `gorm:"type:text"`
	CreatedAtMS  int64       `gorm:"column:created_at_ms;not null;index;autoCreateTime:milli;check:chk_job_created_at,created_at_ms >= 0"`
	StartedAtMS  *int64      `gorm:"column:started_at_ms;check:chk_job_started_at,started_at_ms IS NULL OR started_at_ms >= 0"`
	FinishedAtMS *int64      `gorm:"column:finished_at_ms;check:chk_job_finished_at,finished_at_ms IS NULL OR finished_at_ms >= 0"`
}

func (initialJob) TableName() string { return "jobs" }

type initialControlOperation struct {
	CommitSequence     uint64 `gorm:"primaryKey;autoIncrement"`
	OperationID        string `gorm:"type:char(36);not null;uniqueIndex"`
	IdempotencyKey     string `gorm:"type:char(36);not null;uniqueIndex"`
	DigestVersion      uint   `gorm:"not null;check:chk_control_operation_digest_version,digest_version > 0"`
	RequestDigest      []byte `gorm:"not null;check:chk_control_operation_digest,length(request_digest) = 32"`
	OperationKind      string `gorm:"type:varchar(32);not null"`
	ResourceIdentity   string `gorm:"type:varchar(64);not null"`
	CanonicalResult    []byte
	RequiredStages     initialJSON `gorm:"type:json"`
	LastCompletedStage string      `gorm:"type:varchar(32)"`
	FailedStage        string      `gorm:"type:varchar(32)"`
	CompletedAtMS      *int64      `gorm:"column:completed_at_ms;index;check:chk_control_operation_completed_at,completed_at_ms IS NULL OR completed_at_ms >= 0"`
	CompactedAtMS      *int64      `gorm:"column:compacted_at_ms;check:chk_control_operation_compacted_at,compacted_at_ms IS NULL OR compacted_at_ms >= 0"`
	CreatedAtMS        int64       `gorm:"column:created_at_ms;not null;autoCreateTime:milli;check:chk_control_operation_created_at,created_at_ms >= 0"`
	UpdatedAtMS        int64       `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_control_operation_updated_at,updated_at_ms >= 0"`
}

func (initialControlOperation) TableName() string { return "control_operations" }

type initialCredentialStage struct {
	ID                   string      `gorm:"type:varchar(36);primaryKey;not null"`
	ChannelID            string      `gorm:"type:varchar(64);not null"`
	ConnectionType       string      `gorm:"type:varchar(32);not null;check:chk_credential_stage_connection_type,connection_type = 'subscription'"`
	AuthorizationMethod  string      `gorm:"type:varchar(32);not null;check:chk_credential_stage_authorization_method,authorization_method IN ('browser_oauth','device_oauth','oauth_file','self_discovery')"`
	Status               string      `gorm:"type:varchar(32);not null;check:chk_credential_stage_status,status IN ('pending_authorization','exchanging','ready','consumed','failed','cancelled','expired','outcome_unknown');index:idx_credential_stages_status_expires,priority:1"`
	EncryptedPayload     string      `gorm:"type:text;not null"`
	PayloadSchemaVersion uint        `gorm:"not null;default:1;check:chk_credential_stage_payload_schema,payload_schema_version > 0"`
	SafeSummaryJSON      initialJSON `gorm:"column:safe_summary_json;type:json;not null"`
	IdentityFingerprint  string      `gorm:"type:varchar(128);not null;default:''"`
	OAuthStateHash       *string     `gorm:"column:oauth_state_hash;type:varchar(128);uniqueIndex:idx_credential_stages_oauth_state"`
	ExpiresAtMS          int64       `gorm:"column:expires_at_ms;not null;check:chk_credential_stage_expires_at,expires_at_ms >= 0;index:idx_credential_stages_status_expires,priority:2"`
	ConsumedAtMS         *int64      `gorm:"column:consumed_at_ms;check:chk_credential_stage_consumed_at,consumed_at_ms IS NULL OR consumed_at_ms >= 0"`
	ConsumedGroupID      *uint
	ErrorCode            string `gorm:"type:varchar(64);not null;default:''"`
	CreatedAtMS          int64  `gorm:"column:created_at_ms;not null;check:chk_credential_stage_created_at,created_at_ms >= 0"`
	UpdatedAtMS          int64  `gorm:"column:updated_at_ms;not null;check:chk_credential_stage_updated_at,updated_at_ms >= 0;index:idx_credential_stages_status_expires,priority:3"`
}

func (initialCredentialStage) TableName() string { return "credential_stages" }

type initialCredentialObservation struct {
	CredentialID                 uint               `gorm:"primaryKey;not null"`
	Credential                   *initialCredential `gorm:"foreignKey:CredentialID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	IdentityFingerprint          string             `gorm:"type:varchar(128);not null"`
	SchemaVersion                uint               `gorm:"not null;default:1;check:chk_credential_observation_schema,schema_version > 0"`
	ObservationVersion           uint64             `gorm:"not null;default:1;check:chk_credential_observation_version,observation_version > 0"`
	SnapshotJSON                 initialJSON        `gorm:"column:snapshot_json;type:json;not null"`
	State                        string             `gorm:"type:varchar(32);not null;check:chk_credential_observation_state,state IN ('fresh','stale','refreshing','error','unavailable')"`
	ObservedAtMS                 *int64             `gorm:"column:observed_at_ms;check:chk_credential_observation_observed_at,observed_at_ms IS NULL OR observed_at_ms >= 0"`
	FreshUntilMS                 *int64             `gorm:"column:fresh_until_ms;check:chk_credential_observation_fresh_until,fresh_until_ms IS NULL OR fresh_until_ms >= 0"`
	LastAttemptAtMS              *int64             `gorm:"column:last_attempt_at_ms;check:chk_credential_observation_last_attempt,last_attempt_at_ms IS NULL OR last_attempt_at_ms >= 0"`
	NextAllowedAtMS              *int64             `gorm:"column:next_allowed_at_ms;check:chk_credential_observation_next_allowed,next_allowed_at_ms IS NULL OR next_allowed_at_ms >= 0"`
	LastAuthRefreshSecretVersion *uint64            `gorm:"column:last_auth_refresh_secret_version"`
	LastErrorCode                string             `gorm:"type:varchar(64);not null;default:''"`
	UpdatedAtMS                  int64              `gorm:"column:updated_at_ms;not null;check:chk_credential_observation_updated_at,updated_at_ms >= 0"`
}

func (initialCredentialObservation) TableName() string { return "credential_observations" }

type initialCredentialResetOperation struct {
	IdempotencyKey  string      `gorm:"column:idempotency_key;type:char(36);primaryKey;not null"`
	RequestDigest   []byte      `gorm:"column:request_digest;not null"`
	GroupID         uint        `gorm:"column:group_id;not null;index:idx_credential_reset_operations_credential,priority:1"`
	CredentialID    uint        `gorm:"column:credential_id;not null;index:idx_credential_reset_operations_credential,priority:2"`
	RedeemRequestID string      `gorm:"column:redeem_request_id;type:char(36);not null;uniqueIndex"`
	State           string      `gorm:"type:varchar(32);not null;check:chk_credential_reset_operation_state,state IN ('prepared','succeeded','rejected','outcome_unknown')"`
	ResultJSON      initialJSON `gorm:"column:result_json;type:json"`
	ErrorCode       string      `gorm:"column:error_code;type:varchar(64);not null;default:''"`
	CreatedAtMS     int64       `gorm:"column:created_at_ms;not null;check:chk_credential_reset_operation_created_at,created_at_ms >= 0"`
	UpdatedAtMS     int64       `gorm:"column:updated_at_ms;not null;check:chk_credential_reset_operation_updated_at,updated_at_ms >= 0"`
	CompletedAtMS   *int64      `gorm:"column:completed_at_ms;check:chk_credential_reset_operation_completed_at,completed_at_ms IS NULL OR completed_at_ms >= 0"`
}

func (initialCredentialResetOperation) TableName() string {
	return "credential_reset_operations"
}

type initialCredentialAttemptStat struct {
	ID            uint  `gorm:"primaryKey;autoIncrement;index:idx_credential_attempt_stats_bucket_id,priority:2"`
	CredentialID  uint  `gorm:"not null;check:chk_credential_attempt_stat_credential,credential_id > 0;uniqueIndex:idx_credential_attempt_stats_identity,priority:1"`
	BucketStartMS int64 `gorm:"column:bucket_start_ms;not null;check:chk_credential_attempt_stat_bucket,bucket_start_ms >= 0;uniqueIndex:idx_credential_attempt_stats_identity,priority:2;index:idx_credential_attempt_stats_bucket_id,priority:1"`
	SuccessCount  int64 `gorm:"not null;default:0;check:chk_credential_attempt_stat_success_count,success_count >= 0"`
	FailureCount  int64 `gorm:"not null;default:0;check:chk_credential_attempt_stat_failure_count,failure_count >= 0"`
}

func (initialCredentialAttemptStat) TableName() string { return "credential_attempt_stats" }

// ID0001 is the immutable identifier of the first GPT-Load 2.0 migration.
const ID0001 = "0001_initial"

// Up0001 creates the complete schema frozen at the migration-chain baseline.
func Up0001(db *gorm.DB) error {
	if err := db.AutoMigrate(SchemaModels0001()...); err != nil {
		return fmt.Errorf("create initial schema: %w", err)
	}
	if db.Dialector.Name() == "mysql" {
		// MySQL's database default is commonly case-insensitive. Model IDs are
		// wire identities, so preserve exact casing within the composite key.
		if err := db.Exec(
			"ALTER TABLE model_prices MODIFY model_id varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL",
		).Error; err != nil {
			return fmt.Errorf("configure exact MySQL model price identity: %w", err)
		}
	}
	return nil
}

// SchemaModels0001 returns the migration-local models in deterministic DDL order.
func SchemaModels0001() []any {
	return []any{
		&initialGroup{},
		&initialCredential{},
		&initialAccessKey{},
		&initialRequestLog{},
		&initialRequestLogAttempt{},
		&initialUsageAggregationJournal{},
		&initialUsageStat{},
		&initialModelPrice{},
		&initialSystemSetting{},
		&initialJob{},
		&initialControlOperation{},
		&initialCredentialStage{},
		&initialCredentialObservation{},
		&initialCredentialResetOperation{},
		&initialCredentialAttemptStat{},
	}
}

// TableNames0001 returns the application tables created by this migration.
func TableNames0001() []string {
	return []string{
		"groups",
		"credentials",
		"access_keys",
		"request_logs",
		"request_log_attempts",
		"usage_aggregation_journal",
		"usage_stats",
		"model_prices",
		"system_settings",
		"jobs",
		"control_operations",
		"credential_stages",
		"credential_observations",
		"credential_reset_operations",
		"credential_attempt_stats",
	}
}

type initialSchemaDefinition struct {
	model       any
	table       string
	columns     map[string]struct{}
	indexes     []string
	constraints []string
}

func initialSchemaDefinitions(db *gorm.DB) ([]initialSchemaDefinition, error) {
	definitions := make([]initialSchemaDefinition, 0, len(SchemaModels0001()))
	for _, model := range SchemaModels0001() {
		statement := &gorm.Statement{DB: db}
		if err := statement.Parse(model); err != nil {
			return nil, fmt.Errorf("parse initial schema model: %w", err)
		}
		definition := initialSchemaDefinition{
			model:   model,
			table:   statement.Schema.Table,
			columns: make(map[string]struct{}),
		}
		for _, field := range statement.Schema.Fields {
			if field.DBName != "" {
				definition.columns[strings.ToLower(field.DBName)] = struct{}{}
			}
		}
		for _, index := range statement.Schema.ParseIndexes() {
			definition.indexes = append(definition.indexes, index.Name)
		}
		for name := range statement.Schema.ParseCheckConstraints() {
			definition.constraints = append(definition.constraints, name)
		}
		for name := range statement.Schema.ParseUniqueConstraints() {
			definition.constraints = append(definition.constraints, name)
		}
		for _, relationship := range statement.Schema.Relationships.Relations {
			if constraint := relationship.ParseConstraint(); constraint != nil {
				definition.constraints = append(definition.constraints, constraint.Name)
			}
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

// ValidateRecoverable0001 rejects unsafe partial MySQL initialization while
// ignoring tables owned by external applications or database extensions.
func ValidateRecoverable0001(db *gorm.DB) error {
	definitions, err := initialSchemaDefinitions(db)
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		if !db.Migrator().HasTable(definition.model) {
			continue
		}
		table := definition.table
		var count int64
		if err := db.Table(table).Count(&count).Error; err != nil {
			return fmt.Errorf("count interrupted baseline table %q: %w", table, err)
		}
		if count != 0 {
			return fmt.Errorf("table %q contains data", table)
		}
		columns, err := db.Migrator().ColumnTypes(table)
		if err != nil {
			return fmt.Errorf("inspect interrupted baseline table %q: %w", table, err)
		}
		for _, column := range columns {
			if _, expected := definition.columns[strings.ToLower(column.Name())]; !expected {
				return fmt.Errorf("table %q contains unexpected column %q", table, column.Name())
			}
		}
	}
	return nil
}

// Validate0001 verifies the tables, columns, indexes, and constraints owned by 0001.
func Validate0001(db *gorm.DB) error {
	definitions, err := initialSchemaDefinitions(db)
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		if !db.Migrator().HasTable(definition.model) {
			return fmt.Errorf("validate initial schema: table %q is missing", definition.table)
		}
		for column := range definition.columns {
			if !db.Migrator().HasColumn(definition.model, column) {
				return fmt.Errorf(
					"validate initial schema: column %q.%q is missing",
					definition.table,
					column,
				)
			}
		}
		for _, index := range definition.indexes {
			if !db.Migrator().HasIndex(definition.model, index) {
				return fmt.Errorf(
					"validate initial schema: index %q on %q is missing",
					index,
					definition.table,
				)
			}
		}
		for _, constraint := range definition.constraints {
			if !db.Migrator().HasConstraint(definition.model, constraint) {
				return fmt.Errorf(
					"validate initial schema: constraint %q on %q is missing",
					constraint,
					definition.table,
				)
			}
		}
	}
	return nil
}
