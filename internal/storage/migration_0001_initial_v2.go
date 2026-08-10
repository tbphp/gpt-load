package storage

import (
	"fmt"
	"strings"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
)

// These migration-local models freeze the unpublished v2 baseline. Runtime
// storage models may evolve only through later migrations.
type initialV2Group struct {
	ID              uint        `gorm:"primaryKey;autoIncrement"`
	Name            string      `gorm:"type:varchar(255);not null;uniqueIndex"`
	ChannelID       string      `gorm:"type:varchar(64);not null"`
	Params          models.JSON `gorm:"type:json;not null"`
	Models          models.JSON `gorm:"type:json;not null"`
	WeightManual    *int
	ValidationModel *string               `gorm:"type:varchar(255)"`
	Overrides       models.JSON           `gorm:"type:json"`
	Enabled         bool                  `gorm:"not null;default:true"`
	Credentials     []initialV2Credential `gorm:"foreignKey:GroupID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	CreatedAtMS     int64                 `gorm:"column:created_at_ms;not null;autoCreateTime:milli;check:chk_group_created_at,created_at_ms >= 0"`
	UpdatedAtMS     int64                 `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_group_updated_at,updated_at_ms >= 0"`
}

func (initialV2Group) TableName() string { return "groups" }

type initialV2Credential struct {
	ID           uint   `gorm:"primaryKey;autoIncrement"`
	GroupID      uint   `gorm:"not null;uniqueIndex:idx_credentials_group_fingerprint,priority:1"`
	Data         string `gorm:"type:text;not null"`
	Fingerprint  string `gorm:"type:varchar(128);not null;uniqueIndex:idx_credentials_group_fingerprint,priority:2"`
	Status       string `gorm:"type:varchar(32);not null;default:'active';check:chk_credential_status,status IN ('active','disabled')"`
	WeightManual *int
	Group        *initialV2Group `gorm:"foreignKey:GroupID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	CreatedAtMS  int64           `gorm:"column:created_at_ms;not null;autoCreateTime:milli;check:chk_credential_created_at,created_at_ms >= 0"`
	UpdatedAtMS  int64           `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_credential_updated_at,updated_at_ms >= 0"`
}

func (initialV2Credential) TableName() string { return "credentials" }

type initialV2AccessKey struct {
	ID                      uint        `gorm:"primaryKey;autoIncrement"`
	Name                    string      `gorm:"type:varchar(255);not null"`
	KeyValue                string      `gorm:"type:text;not null"`
	KeyHash                 string      `gorm:"type:varchar(128);not null;uniqueIndex"`
	KeySuffix               string      `gorm:"type:char(4);not null;check:chk_access_key_suffix,length(key_suffix) = 4 AND substr(key_suffix, 1, 1) IN ('0','1','2','3','4','5','6','7','8','9','a','b','c','d','e','f') AND substr(key_suffix, 2, 1) IN ('0','1','2','3','4','5','6','7','8','9','a','b','c','d','e','f') AND substr(key_suffix, 3, 1) IN ('0','1','2','3','4','5','6','7','8','9','a','b','c','d','e','f') AND substr(key_suffix, 4, 1) IN ('0','1','2','3','4','5','6','7','8','9','a','b','c','d','e','f')"`
	Status                  string      `gorm:"type:varchar(32);not null;default:'active';check:chk_access_key_status,status IN ('active','disabled')"`
	Filters                 models.JSON `gorm:"type:json"`
	RPMLimit                int64       `gorm:"not null;default:0"`
	DailyCostLimitNanoUSD   int64       `gorm:"column:daily_cost_limit_nano_usd;not null;default:0;check:chk_access_key_daily_cost_limit_nano,daily_cost_limit_nano_usd >= 0"`
	MonthlyCostLimitNanoUSD int64       `gorm:"column:monthly_cost_limit_nano_usd;not null;default:0;check:chk_access_key_monthly_cost_limit_nano,monthly_cost_limit_nano_usd >= 0"`
	CreatedAtMS             int64       `gorm:"column:created_at_ms;not null;autoCreateTime:milli;check:chk_access_key_created_at,created_at_ms >= 0"`
	UpdatedAtMS             int64       `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_access_key_updated_at,updated_at_ms >= 0"`
}

func (initialV2AccessKey) TableName() string { return "access_keys" }

type initialV2RequestLog struct {
	ID                      string                       `gorm:"type:varchar(36);primaryKey;not null;index:idx_request_logs_completed_id,priority:2,sort:desc;index:idx_request_logs_access_completed_id,priority:3,sort:desc;index:idx_request_logs_status_completed_id,priority:3,sort:desc;index:idx_request_logs_model_completed_id,priority:3,sort:desc;index:idx_request_logs_upstream_model_completed_id,priority:3,sort:desc"`
	CompletedAtMS           int64                        `gorm:"column:completed_at_ms;not null;check:chk_request_log_completed_at,completed_at_ms >= 0;index:idx_request_logs_completed_id,priority:1,sort:desc;index:idx_request_logs_access_completed_id,priority:2,sort:desc;index:idx_request_logs_status_completed_id,priority:2,sort:desc;index:idx_request_logs_model_completed_id,priority:2,sort:desc;index:idx_request_logs_upstream_model_completed_id,priority:2,sort:desc"`
	AccessKeyID             uint                         `gorm:"not null;index:idx_request_logs_access_completed_id,priority:1"`
	GroupID                 uint                         `gorm:"not null;default:0"`
	ChannelID               string                       `gorm:"type:varchar(64);not null;default:''"`
	CredentialID            uint                         `gorm:"not null;default:0"`
	Protocol                string                       `gorm:"type:varchar(32);not null"`
	ClientModel             string                       `gorm:"type:varchar(255);not null;index:idx_request_logs_model_completed_id,priority:1"`
	UpstreamModel           string                       `gorm:"type:varchar(255);not null;index:idx_request_logs_upstream_model_completed_id,priority:1"`
	UpstreamReportedModel   string                       `gorm:"type:varchar(255);not null;default:''"`
	ModelConsistency        string                       `gorm:"type:varchar(32);not null;default:'not_applicable';check:chk_request_log_model_consistency,model_consistency IN ('not_applicable','match','unknown','mismatch')"`
	Status                  string                       `gorm:"type:varchar(32);not null;check:chk_request_log_status,status IN ('success','error','incomplete','canceled');index:idx_request_logs_status_completed_id,priority:1"`
	StatusCode              int                          `gorm:"not null"`
	Stream                  bool                         `gorm:"not null;default:false"`
	FirstResponseMs         *int64                       `gorm:"column:first_response_ms;check:chk_request_log_first_response,first_response_ms IS NULL OR first_response_ms >= 0"`
	DurationMs              int64                        `gorm:"not null;check:chk_request_log_duration,duration_ms >= 0"`
	AttemptCount            int                          `gorm:"not null;default:0;check:chk_request_log_attempt_count,attempt_count >= 0"`
	ErrorCode               string                       `gorm:"type:varchar(64);not null;default:''"`
	ErrorSummary            string                       `gorm:"type:text;not null"`
	AffinityHit             bool                         `gorm:"not null;default:false"`
	ReasoningMode           string                       `gorm:"type:varchar(64);not null;default:''"`
	ReasoningEffort         string                       `gorm:"type:varchar(64);not null;default:''"`
	ReasoningBudgetTokens   *int64                       `gorm:"column:reasoning_budget_tokens"`
	UncachedInputTokens     int64                        `gorm:"column:uncached_input_tokens;not null;default:0;check:chk_request_log_uncached_input,uncached_input_tokens >= 0"`
	OutputTokens            int64                        `gorm:"not null;default:0;check:chk_request_log_output,output_tokens >= 0"`
	CacheReadTokens         int64                        `gorm:"not null;default:0;check:chk_request_log_cache_read,cache_read_tokens >= 0"`
	CacheWrite5MTokens      int64                        `gorm:"column:cache_write_5m_tokens;not null;default:0;check:chk_request_log_cache_write_5m,cache_write_5m_tokens >= 0"`
	CacheWrite1HTokens      int64                        `gorm:"column:cache_write_1h_tokens;not null;default:0;check:chk_request_log_cache_write_1h,cache_write_1h_tokens >= 0"`
	CacheWriteUnknownTokens int64                        `gorm:"column:cache_write_unknown_tokens;not null;default:0;check:chk_request_log_cache_write_unknown,cache_write_unknown_tokens >= 0"`
	EstimatedCostNanoUSD    int64                        `gorm:"column:estimated_cost_nano_usd;not null;default:0;check:chk_request_log_cost_nano,estimated_cost_nano_usd >= 0"`
	UsageState              string                       `gorm:"type:varchar(32);not null;default:'not_applicable';check:chk_request_log_usage_state,usage_state IN ('complete','partial','missing','not_applicable')"`
	CostState               string                       `gorm:"type:varchar(32);not null;default:'not_applicable';check:chk_request_log_cost_state,cost_state IN ('priced','unpriced','not_applicable')"`
	PricingCompleteness     string                       `gorm:"type:varchar(32);not null;default:'not_applicable';check:chk_request_log_pricing_completeness,pricing_completeness IN ('complete','partial','unavailable','not_applicable');check:chk_request_log_usage_pricing_state,(usage_state = 'not_applicable' AND cost_state = 'not_applicable' AND pricing_completeness = 'not_applicable' AND estimated_cost_nano_usd = 0) OR (usage_state = 'missing' AND cost_state = 'unpriced' AND pricing_completeness = 'unavailable' AND estimated_cost_nano_usd = 0) OR (usage_state IN ('complete','partial') AND ((cost_state = 'unpriced' AND pricing_completeness = 'unavailable' AND estimated_cost_nano_usd = 0) OR (cost_state = 'priced' AND pricing_completeness IN ('complete','partial'))))"`
	AttemptRows             []initialV2RequestLogAttempt `gorm:"-"`
}

func (initialV2RequestLog) TableName() string { return "request_logs" }

type initialV2RequestLogAttempt struct {
	RequestID         string               `gorm:"type:varchar(36);primaryKey;not null;index:idx_request_log_attempts_group_completed_request,priority:3;index:idx_request_log_attempts_channel_completed_request,priority:3;index:idx_request_log_attempts_credential_completed_request,priority:3;index:idx_request_log_attempts_model_completed_request,priority:3;index:idx_request_log_attempts_status_completed_request,priority:3;index:idx_request_log_attempts_failure_completed_request,priority:3;index:idx_request_log_attempts_error_completed_request,priority:3"`
	Sequence          int                  `gorm:"primaryKey;not null;check:chk_request_log_attempt_sequence,sequence > 0"`
	CompletedAtMS     int64                `gorm:"column:completed_at_ms;not null;check:chk_request_log_attempt_completed_at,completed_at_ms >= 0;index:idx_request_log_attempts_group_completed_request,priority:2,sort:desc;index:idx_request_log_attempts_credential_completed_request,priority:2,sort:desc;index:idx_request_log_attempts_channel_completed_request,priority:2,sort:desc;index:idx_request_log_attempts_model_completed_request,priority:2,sort:desc;index:idx_request_log_attempts_status_completed_request,priority:2,sort:desc;index:idx_request_log_attempts_failure_completed_request,priority:2,sort:desc;index:idx_request_log_attempts_error_completed_request,priority:2,sort:desc"`
	GroupID           uint                 `gorm:"not null;check:chk_request_log_attempt_group,group_id > 0;index:idx_request_log_attempts_group_completed_request,priority:1"`
	GroupName         string               `gorm:"type:varchar(255);not null"`
	ChannelID         string               `gorm:"type:varchar(64);not null;default:'';index:idx_request_log_attempts_channel_completed_request,priority:1"`
	CredentialID      uint                 `gorm:"not null;check:chk_request_log_attempt_credential,credential_id > 0;index:idx_request_log_attempts_credential_completed_request,priority:1"`
	Operation         string               `gorm:"type:varchar(64);not null;default:''"`
	RouteMode         string               `gorm:"type:varchar(32);not null;default:''"`
	UpstreamModel     string               `gorm:"type:varchar(255);not null;default:''"`
	UpstreamRequestID string               `gorm:"type:varchar(255);not null;default:''"`
	DispatchState     string               `gorm:"type:varchar(32);not null;default:''"`
	ResponseStarted   bool                 `gorm:"not null;default:false"`
	StatusCode        int                  `gorm:"not null;index:idx_request_log_attempts_status_completed_request,priority:1"`
	DurationMs        int64                `gorm:"not null;check:chk_request_log_attempt_duration,duration_ms >= 0"`
	FailureCategory   string               `gorm:"type:varchar(32);not null;check:chk_request_log_attempt_failure_category,failure_category IN ('ok','rate_limited','model_unavailable','invalid_key','upstream_host_error','client_error','downstream_cancel','ambiguous');index:idx_request_log_attempts_failure_completed_request,priority:1"`
	Action            string               `gorm:"type:varchar(32);not null;check:chk_request_log_attempt_action,action IN ('terminate','retry','cooldown_credential','fail_credential','skip_group')"`
	WillRetry         bool                 `gorm:"not null;default:false"`
	ErrorCode         string               `gorm:"type:varchar(64);not null;default:'';index:idx_request_log_attempts_error_completed_request,priority:1"`
	ErrorSummary      string               `gorm:"type:text;not null"`
	Committed         bool                 `gorm:"not null;default:false"`
	PricingReceipt    models.JSON          `gorm:"type:json"`
	RequestLog        *initialV2RequestLog `gorm:"foreignKey:RequestID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

func (initialV2RequestLogAttempt) TableName() string { return "request_log_attempts" }

type initialV2UsageAggregationJournal struct {
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

func (initialV2UsageAggregationJournal) TableName() string { return "usage_aggregation_journal" }

type initialV2UsageStat struct {
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

func (initialV2UsageStat) TableName() string { return "usage_stats" }

type initialV2ModelPrice struct {
	ID                                     uint        `gorm:"primaryKey;autoIncrement"`
	ChannelID                              string      `gorm:"type:varchar(64);not null;uniqueIndex:idx_model_prices_channel_model,priority:1"`
	ModelID                                string      `gorm:"type:varchar(255);not null;uniqueIndex:idx_model_prices_channel_model,priority:2"`
	InputPriceNanoUSDPerMillionTokens      *int64      `gorm:"column:input_price_nano_usd_per_million_tokens;check:chk_model_price_input_nano,input_price_nano_usd_per_million_tokens IS NULL OR input_price_nano_usd_per_million_tokens >= 0"`
	OutputPriceNanoUSDPerMillionTokens     *int64      `gorm:"column:output_price_nano_usd_per_million_tokens;check:chk_model_price_output_nano,output_price_nano_usd_per_million_tokens IS NULL OR output_price_nano_usd_per_million_tokens >= 0"`
	CacheReadPriceNanoUSDPerMillionTokens  *int64      `gorm:"column:cache_read_price_nano_usd_per_million_tokens;check:chk_model_price_cache_read_nano,cache_read_price_nano_usd_per_million_tokens IS NULL OR cache_read_price_nano_usd_per_million_tokens >= 0"`
	CacheWritePriceNanoUSDPerMillionTokens *int64      `gorm:"column:cache_write_price_nano_usd_per_million_tokens;check:chk_model_price_cache_write_nano,cache_write_price_nano_usd_per_million_tokens IS NULL OR cache_write_price_nano_usd_per_million_tokens >= 0"`
	ContextPriceTiers                      models.JSON `gorm:"type:json"`
	IsManual                               bool        `gorm:"not null;default:false"`
	CreatedAtMS                            int64       `gorm:"column:created_at_ms;not null;autoCreateTime:milli;check:chk_model_price_created_at,created_at_ms >= 0"`
	UpdatedAtMS                            int64       `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_model_price_updated_at,updated_at_ms >= 0"`
}

func (initialV2ModelPrice) TableName() string { return "model_prices" }

type initialV2SystemSetting struct {
	Key         string `gorm:"type:varchar(255);primaryKey;not null"`
	Value       string `gorm:"type:text;not null"`
	UpdatedAtMS int64  `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_system_setting_updated_at,updated_at_ms >= 0"`
}

func (initialV2SystemSetting) TableName() string { return "system_settings" }

type initialV2Job struct {
	ID           string      `gorm:"type:varchar(36);primaryKey;not null"`
	Type         string      `gorm:"type:varchar(64);not null;index"`
	Status       string      `gorm:"type:varchar(32);not null;default:'pending';index"`
	Payload      models.JSON `gorm:"type:json"`
	Result       models.JSON `gorm:"type:json"`
	Error        string      `gorm:"type:text"`
	CreatedAtMS  int64       `gorm:"column:created_at_ms;not null;index;autoCreateTime:milli;check:chk_job_created_at,created_at_ms >= 0"`
	StartedAtMS  *int64      `gorm:"column:started_at_ms;check:chk_job_started_at,started_at_ms IS NULL OR started_at_ms >= 0"`
	FinishedAtMS *int64      `gorm:"column:finished_at_ms;check:chk_job_finished_at,finished_at_ms IS NULL OR finished_at_ms >= 0"`
}

func (initialV2Job) TableName() string { return "jobs" }

type initialV2ControlOperation struct {
	CommitSequence     uint64 `gorm:"primaryKey;autoIncrement"`
	OperationID        string `gorm:"type:char(36);not null;uniqueIndex"`
	IdempotencyKey     string `gorm:"type:char(36);not null;uniqueIndex"`
	DigestVersion      uint   `gorm:"not null;check:chk_control_operation_digest_version,digest_version > 0"`
	RequestDigest      []byte `gorm:"not null;check:chk_control_operation_digest,length(request_digest) = 32"`
	OperationKind      string `gorm:"type:varchar(32);not null"`
	ResourceIdentity   string `gorm:"type:varchar(64);not null"`
	CanonicalResult    []byte
	RequiredStages     models.JSON `gorm:"type:json"`
	LastCompletedStage string      `gorm:"type:varchar(32)"`
	FailedStage        string      `gorm:"type:varchar(32)"`
	CompletedAtMS      *int64      `gorm:"column:completed_at_ms;index;check:chk_control_operation_completed_at,completed_at_ms IS NULL OR completed_at_ms >= 0"`
	CompactedAtMS      *int64      `gorm:"column:compacted_at_ms;check:chk_control_operation_compacted_at,compacted_at_ms IS NULL OR compacted_at_ms >= 0"`
	CreatedAtMS        int64       `gorm:"column:created_at_ms;not null;autoCreateTime:milli;check:chk_control_operation_created_at,created_at_ms >= 0"`
	UpdatedAtMS        int64       `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_control_operation_updated_at,updated_at_ms >= 0"`
}

func (initialV2ControlOperation) TableName() string { return "control_operations" }

// createInitialV2Tables builds the final pre-release v2 baseline from the same
// frozen schema used for the unpublished 2.0 baseline. The baseline
// intentionally accepts only an empty database; future changes append a new
// migration.
func createInitialV2Tables(db *gorm.DB) error {
	if err := db.AutoMigrate(initialV2SchemaModels()...); err != nil {
		return fmt.Errorf("create initial v2 schema: %w", err)
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

func initialV2SchemaModels() []any {
	return []any{
		&initialV2Group{},
		&initialV2Credential{},
		&initialV2AccessKey{},
		&initialV2RequestLog{},
		&initialV2RequestLogAttempt{},
		&initialV2UsageAggregationJournal{},
		&initialV2UsageStat{},
		&initialV2ModelPrice{},
		&initialV2SystemSetting{},
		&initialV2Job{},
		&initialV2ControlOperation{},
	}
}

func initialV2TableNames() []string {
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
	}
}

type initialV2SchemaDefinition struct {
	model       any
	table       string
	columns     map[string]struct{}
	indexes     []string
	constraints []string
}

func initialV2SchemaDefinitions(db *gorm.DB) ([]initialV2SchemaDefinition, error) {
	definitions := make([]initialV2SchemaDefinition, 0, len(initialV2SchemaModels()))
	for _, model := range initialV2SchemaModels() {
		statement := &gorm.Statement{DB: db}
		if err := statement.Parse(model); err != nil {
			return nil, fmt.Errorf("parse initial v2 schema model: %w", err)
		}
		definition := initialV2SchemaDefinition{
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

func validateRecoverableInitialV2Schema(db *gorm.DB) error {
	definitions, err := initialV2SchemaDefinitions(db)
	if err != nil {
		return err
	}
	byTable := make(map[string]initialV2SchemaDefinition, len(definitions))
	for _, definition := range definitions {
		byTable[strings.ToLower(definition.table)] = definition
	}
	tables, err := db.Migrator().GetTables()
	if err != nil {
		return fmt.Errorf("list interrupted baseline tables: %w", err)
	}
	for _, table := range tables {
		if strings.EqualFold(table, migrationLedgerTable) || isDatabaseSystemTable(db, table) {
			continue
		}
		definition, known := byTable[strings.ToLower(table)]
		if !known {
			return fmt.Errorf("unexpected table %q", table)
		}
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

func validateInitialV2Schema(db *gorm.DB) error {
	definitions, err := initialV2SchemaDefinitions(db)
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		if !db.Migrator().HasTable(definition.model) {
			return fmt.Errorf("validate initial v2 schema: table %q is missing", definition.table)
		}
		for column := range definition.columns {
			if !db.Migrator().HasColumn(definition.model, column) {
				return fmt.Errorf(
					"validate initial v2 schema: column %q.%q is missing",
					definition.table,
					column,
				)
			}
		}
		for _, index := range definition.indexes {
			if !db.Migrator().HasIndex(definition.model, index) {
				return fmt.Errorf(
					"validate initial v2 schema: index %q on %q is missing",
					index,
					definition.table,
				)
			}
		}
		for _, constraint := range definition.constraints {
			if !db.Migrator().HasConstraint(definition.model, constraint) {
				return fmt.Errorf(
					"validate initial v2 schema: constraint %q on %q is missing",
					constraint,
					definition.table,
				)
			}
		}
	}
	return nil
}

// validateMigrationForeignKeys performs the existing SQLite integrity check.
// MySQL and PostgreSQL enforce the same foreign-key definitions during normal
// writes; their catalogs do not expose SQLite's PRAGMA interface.
func validateMigrationForeignKeys(db *gorm.DB) error {
	if db == nil || db.Dialector == nil || db.Dialector.Name() != "sqlite" {
		return nil
	}
	var violations []struct {
		Table string
		RowID int64 `gorm:"column:rowid"`
	}
	if err := db.Raw("PRAGMA foreign_key_check").Scan(&violations).Error; err != nil {
		return fmt.Errorf("validate migration foreign keys: %w", err)
	}
	if len(violations) != 0 {
		return fmt.Errorf("validate migration foreign keys: %d violation(s)", len(violations))
	}
	return nil
}
