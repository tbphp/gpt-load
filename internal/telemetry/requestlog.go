package telemetry

import (
	"time"

	"gpt-load/internal/protocol"
)

type RequestStatus string

const (
	RequestStatusSuccess    RequestStatus = "success"
	RequestStatusError      RequestStatus = "error"
	RequestStatusIncomplete RequestStatus = "incomplete"
	RequestStatusCanceled   RequestStatus = "canceled"
)

type FailureCategory string

const (
	FailureCategoryOK               FailureCategory = "ok"
	FailureCategoryRateLimited      FailureCategory = "rate_limited"
	FailureCategoryModelUnavailable FailureCategory = "model_unavailable"
	FailureCategoryInvalidKey       FailureCategory = "invalid_key"
	FailureCategoryUpstreamHost     FailureCategory = "upstream_host_error"
	FailureCategoryClientError      FailureCategory = "client_error"
	FailureCategoryDownstreamCancel FailureCategory = "downstream_cancel"
	FailureCategoryAmbiguous        FailureCategory = "ambiguous"
)

type Action string

const (
	ActionTerminate   Action = "terminate"
	ActionRetry       Action = "retry"
	ActionCooldownKey Action = "cooldown_key"
	ActionFailKey     Action = "fail_key"
	ActionSkipGroup   Action = "skip_group"
)

type Attempt struct {
	Sequence        int
	GroupID         uint
	GroupName       string
	KeyID           uint
	KeyMask         string
	UpstreamModel   string
	StatusCode      int
	DurationMs      int64
	FailureCategory FailureCategory
	Action          Action
	WillRetry       bool
	ErrorCode       string
	ErrorSummary    string
	Committed       bool
}

type RequestEvent struct {
	RequestID     string
	CompletedAt   time.Time
	AccessKeyID   uint
	Protocol      protocol.Protocol
	ClientModel   string
	UpstreamModel string
	Status        RequestStatus
	StatusCode    int
	ErrorCode     string
	ErrorSummary  string
	DurationMs    int64
	AffinityHit   bool
	Attempts      []Attempt
}

type RequestLogSink interface {
	Emit(RequestEvent)
}

type NoopRequestLogSink struct{}

func (NoopRequestLogSink) Emit(RequestEvent) {}
