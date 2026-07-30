package requestlog

import (
	"errors"
	"time"

	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
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
	Sequence        int                       `json:"sequence"`
	GroupID         uint                      `json:"group_id"`
	GroupName       string                    `json:"group_name"`
	KeyID           uint                      `json:"key_id"`
	UpstreamModel   string                    `json:"upstream_model"`
	StatusCode      int                       `json:"status_code"`
	DurationMs      int64                     `json:"duration_ms"`
	FailureCategory telemetry.FailureCategory `json:"failure_category"`
	Action          telemetry.Action          `json:"action"`
	WillRetry       bool                      `json:"will_retry"`
	ErrorCode       string                    `json:"error_code"`
	ErrorSummary    string                    `json:"error_summary"`
	Committed       bool                      `json:"committed"`
}

type Cursor struct {
	CompletedAtMS int64
	RequestID     string
}

type ListQuery struct {
	FromMS      *int64
	ToMS        *int64
	GroupID     *uint
	ClientModel string
	AccessKeyID *uint
	Status      telemetry.RequestStatus
	RequestID   string
	Limit       int
	Cursor      *Cursor
}

type AccessKeyRef struct {
	ID      uint
	Name    *string
	Deleted bool
}

type Record struct {
	RequestID            string
	CompletedAtMS        int64
	AccessKey            AccessKeyRef
	Protocol             protocol.Protocol
	ClientModel          string
	UpstreamModel        string
	Status               telemetry.RequestStatus
	StatusCode           int
	DurationMs           int64
	ErrorCode            string
	ErrorSummary         string
	AffinityHit          bool
	Attempts             []Attempt
	GroupID              uint
	UsageState           usage.State
	CostState            pricing.CostState
	UncachedInputTokens  int64
	CacheReadTokens      int64
	CacheWrite5MTokens   int64
	CacheWrite1HTokens   int64
	OutputTokens         int64
	EstimatedCostNanoUSD int64
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

type UsageBreakdownOrder string

const (
	UsageBreakdownOrderRequests UsageBreakdownOrder = "requests"
	UsageBreakdownOrderCost     UsageBreakdownOrder = "cost"
)

type UsageQuery struct {
	FromMS         int64
	ToMS           int64
	Granularity    UsageGranularity
	GroupID        *uint
	Model          string
	Limit          int
	BreakdownOrder UsageBreakdownOrder
}

type UsageAggregate struct {
	RequestCount         int64
	SuccessCount         int64
	FailureCount         int64
	UncachedInputTokens  int64
	CacheReadTokens      int64
	CacheWrite5MTokens   int64
	CacheWrite1HTokens   int64
	OutputTokens         int64
	EstimatedCostNanoUSD int64
	UsageMissingCount    int64
	PartialCount         int64
	UnpricedRequestCount int64
}

type UsageSeriesPoint struct {
	BucketStartMS int64
	BucketEndMS   int64
	UsageAggregate
}

type UsageBreakdown struct {
	GroupID uint
	Model   string
	UsageAggregate
}

type UsageReport struct {
	Summary             UsageAggregate
	Series              []UsageSeriesPoint
	Breakdown           []UsageBreakdown
	BreakdownTruncated  bool
	BreakdownOrder      UsageBreakdownOrder
	BreakdownGroupCount int64
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
