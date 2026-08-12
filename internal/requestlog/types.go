package requestlog

import (
	"errors"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/reasoning"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

var (
	ErrAlreadyStarted = errors.New("request log service is already started")
	ErrNotRestartable = errors.New("request log service cannot be restarted")
)

type lifecycleState string

const (
	lifecycleNew      lifecycleState = "new"
	lifecycleRunning  lifecycleState = "running"
	lifecycleStopping lifecycleState = "stopping"
	lifecycleStopped  lifecycleState = "stopped"
)

type Attempt struct {
	Sequence          int                       `json:"sequence"`
	GroupID           uint                      `json:"group_id"`
	GroupName         string                    `json:"group_name"`
	ChannelID         channel.ID                `json:"channel_id"`
	CredentialID      uint                      `json:"credential_id"`
	Operation         execution.Operation       `json:"operation"`
	RouteMode         channel.RouteMode         `json:"route_mode"`
	UpstreamModel     string                    `json:"upstream_model"`
	UpstreamRequestID string                    `json:"upstream_request_id"`
	DispatchState     execution.DispatchState   `json:"dispatch_state"`
	ResponseStarted   bool                      `json:"response_started"`
	UpstreamAPI       execution.UpstreamAPI     `json:"upstream_api"`
	Reasoning         reasoning.Config          `json:"reasoning"`
	StatusCode        int                       `json:"status_code"`
	DurationMs        int64                     `json:"duration_ms"`
	FailureCategory   telemetry.FailureCategory `json:"failure_category"`
	Action            telemetry.Action          `json:"action"`
	WillRetry         bool                      `json:"will_retry"`
	ErrorCode         string                    `json:"error_code"`
	ErrorSummary      string                    `json:"error_summary"`
	Committed         bool                      `json:"committed"`
	PricingReceipt    *pricing.Receipt          `json:"pricing_receipt,omitempty"`
}

type RetryState string

const (
	RetryStateRetried    RetryState = "retried"
	RetryStateNotRetried RetryState = "not_retried"
)

type Cursor struct {
	CompletedAtMS int64
	RequestID     string
}

type ListQuery struct {
	FromMS              *int64
	ToMS                *int64
	GroupID             *uint
	ChannelID           channel.ID
	ClientModel         string
	UpstreamModel       string
	AccessKeyID         *uint
	Status              telemetry.RequestStatus
	RequestID           string
	Protocol            protocol.Protocol
	Stream              *bool
	FinalStatusCode     *int
	UsageState          usage.State
	CostState           pricing.CostState
	PricingCompleteness pricing.Completeness
	CachePresent        *bool
	CredentialID        *uint
	AttemptStatusCode   *int
	FailureCategory     telemetry.FailureCategory
	AttemptErrorCode    string
	RetryState          RetryState
	RetryCountMin       *int
	RetryCountMax       *int
	FirstResponseMinMS  *int64
	FirstResponseMaxMS  *int64
	DurationMinMS       *int64
	DurationMaxMS       *int64
	InputTokensMin      *int64
	InputTokensMax      *int64
	OutputTokensMin     *int64
	OutputTokensMax     *int64
	CostMinNanoUSD      *int64
	CostMaxNanoUSD      *int64
	Limit               int
	Cursor              *Cursor
}

type AccessKeyRef struct {
	ID      uint
	Name    *string
	Deleted bool
}

type Record struct {
	RequestID               string
	CompletedAtMS           int64
	AccessKey               AccessKeyRef
	Protocol                protocol.Protocol
	Operation               execution.Operation
	UpstreamAPI             execution.UpstreamAPI
	ClientModel             string
	UpstreamModel           string
	UpstreamReportedModel   string
	ModelConsistency        telemetry.ModelConsistency
	Status                  telemetry.RequestStatus
	StatusCode              int
	Stream                  bool
	FirstResponseMs         *int64
	DurationMs              int64
	AttemptCount            int
	ErrorCode               string
	ErrorSummary            string
	AffinityHit             bool
	Reasoning               reasoning.Config
	Attempts                []Attempt
	GroupID                 uint
	ChannelID               channel.ID
	CredentialID            uint
	RouteMode               channel.RouteMode
	UsageState              usage.State
	CostState               pricing.CostState
	PricingCompleteness     pricing.Completeness
	UncachedInputTokens     int64
	CacheReadTokens         int64
	CacheWrite5MTokens      int64
	CacheWrite1HTokens      int64
	CacheWriteUnknownTokens int64
	OutputTokens            int64
	EstimatedCostNanoUSD    int64
}

type Page struct {
	Items      []Record
	NextCursor *Cursor
}

type UsageGranularity string

const (
	UsageGranularityHour UsageGranularity = "hour"
	UsageGranularityDay  UsageGranularity = "day"
)

type UsageDistributionDimension string

const (
	UsageDistributionDimensionGroup UsageDistributionDimension = "group"
	UsageDistributionDimensionModel UsageDistributionDimension = "model"
)

type UsageDistributionMetric string

const (
	UsageDistributionMetricRequests UsageDistributionMetric = "requests"
	UsageDistributionMetricCost     UsageDistributionMetric = "cost"
)

type UsageQuery struct {
	FromMS        int64
	ToMS          int64
	Granularity   UsageGranularity
	BucketWidthMS int64
	AccessKeyID   *uint
	GroupID       *uint
	ChannelID     channel.ID
	CredentialID  *uint
	UpstreamModel string
}

type UsageAggregate struct {
	RequestCount            int64
	SuccessCount            int64
	FailureCount            int64
	UncachedInputTokens     int64
	CacheReadTokens         int64
	CacheWrite5MTokens      int64
	CacheWrite1HTokens      int64
	CacheWriteUnknownTokens int64
	OutputTokens            int64
	EstimatedCostNanoUSD    int64
	UsageMissingCount       int64
	PartialCount            int64
	UnpricedRequestCount    int64
	PricingPartialCount     int64
}

type UsageSeriesPoint struct {
	BucketStartMS int64
	BucketEndMS   int64
	UsageAggregate
}

type UsageDistributionItem struct {
	GroupID uint
	Model   string
	UsageDistributionAggregate
}

type UsageDistributionAggregate struct {
	RequestCount         int64
	EstimatedCostNanoUSD int64
}

type UsageDistribution struct {
	Dimension UsageDistributionDimension
	Metric    UsageDistributionMetric
	Items     []UsageDistributionItem
	Other     *UsageDistributionAggregate
}

type UsageReport struct {
	Summary       UsageAggregate
	Series        []UsageSeriesPoint
	Distributions UsageDistributions
}

type UsageDistributions struct {
	Group map[UsageDistributionMetric]UsageDistribution
	Model map[UsageDistributionMetric]UsageDistribution
}

func (distributions UsageDistributions) Get(
	dimension UsageDistributionDimension,
	metric UsageDistributionMetric,
) (UsageDistribution, bool) {
	var values map[UsageDistributionMetric]UsageDistribution
	switch dimension {
	case UsageDistributionDimensionGroup:
		values = distributions.Group
	case UsageDistributionDimensionModel:
		values = distributions.Model
	default:
		return UsageDistribution{}, false
	}
	distribution, ok := values[metric]
	return distribution, ok
}

type Stats struct {
	EnqueuedTotal               uint64
	PersistedTotal              uint64
	DroppedNotRunningTotal      uint64
	DroppedQueueFullTotal       uint64
	DroppedStoppingTotal        uint64
	DroppedPersistFailedTotal   uint64
	DroppedShutdownTotal        uint64
	DroppedTotal                uint64
	WriteFailureTotal           uint64
	RetentionDeleteFailureTotal uint64
	QueueDepth                  int
	QueueCapacity               int
	LastWriteFailureAt          time.Time
	LastRetentionFailureAt      time.Time
}
