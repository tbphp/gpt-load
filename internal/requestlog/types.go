package requestlog

import (
	"errors"
	"time"

	"gpt-load/internal/protocol"
	"gpt-load/internal/telemetry"
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
	KeyMask         string                    `json:"key_mask"`
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
	CompletedAt time.Time
	RequestID   string
}

type ListQuery struct {
	From        *time.Time
	To          *time.Time
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
	RequestID     string
	CompletedAt   time.Time
	AccessKey     AccessKeyRef
	Protocol      protocol.Protocol
	ClientModel   string
	UpstreamModel string
	Status        telemetry.RequestStatus
	StatusCode    int
	DurationMs    int64
	ErrorCode     string
	ErrorSummary  string
	AffinityHit   bool
	Attempts      []Attempt
}

type Page struct {
	Items      []Record
	NextCursor *Cursor
}

type Stats struct {
	EnqueuedTotal                uint64
	PersistedTotal               uint64
	DroppedNotRunningTotal       uint64
	DroppedQueueFullTotal        uint64
	DroppedStoppingTotal         uint64
	DroppedPersistFailedTotal    uint64
	DroppedShutdownTotal         uint64
	DroppedTotal                 uint64
	WriteFailureTotal            uint64
	RetentionInvalidSettingTotal uint64
	RetentionDeleteFailureTotal  uint64
	QueueDepth                   int
	QueueCapacity                int
	LastWriteFailureAt           time.Time
	LastRetentionFailureAt       time.Time
}
