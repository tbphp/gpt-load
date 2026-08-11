package telemetry

import (
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/reasoning"
	"gpt-load/internal/usage"
)

type RequestStatus string

const (
	RequestStatusSuccess    RequestStatus = "success"
	RequestStatusError      RequestStatus = "error"
	RequestStatusIncomplete RequestStatus = "incomplete"
	RequestStatusCanceled   RequestStatus = "canceled"
)

type ModelConsistency string

const (
	ModelConsistencyNotApplicable ModelConsistency = "not_applicable"
	ModelConsistencyMatch         ModelConsistency = "match"
	ModelConsistencyUnknown       ModelConsistency = "unknown"
	ModelConsistencyMismatch      ModelConsistency = "mismatch"
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
	ActionTerminate          Action = "terminate"
	ActionRetry              Action = "retry"
	ActionCooldownCredential Action = "cooldown_credential"
	ActionFailCredential     Action = "fail_credential"
	ActionSkipGroup          Action = "skip_group"
)

type Attempt struct {
	Sequence          int
	GroupID           uint
	GroupName         string
	ChannelID         channel.ID
	CredentialID      uint
	Operation         execution.Operation
	RouteMode         channel.RouteMode
	UpstreamModel     string
	UpstreamRequestID string
	DispatchState     execution.DispatchState
	ResponseStarted   bool
	UpstreamAPI       execution.UpstreamAPI
	Reasoning         reasoning.Config
	StatusCode        int
	DurationMs        int64
	FailureCategory   FailureCategory
	Action            Action
	WillRetry         bool
	ErrorCode         string
	ErrorSummary      string
	Committed         bool
}

// PricingObservation is the frozen, dependency-neutral quote selected by the
// gateway for the attempt whose usage is attributed to the request outcome.
type PricingObservation struct {
	UpstreamModel        string
	CostState            string
	PricingCompleteness  string
	EstimatedCostNanoUSD int64
	ReceiptJSON          string
}

type UsageObservation struct {
	Result          usage.Result
	GroupID         uint
	ChannelID       channel.ID
	CredentialID    uint
	AttemptSequence int
	Pricing         PricingObservation
}

type RequestEvent struct {
	RequestID             string
	CompletedAt           time.Time
	AccessKeyID           uint
	Protocol              protocol.Protocol
	Operation             execution.Operation
	ClientModel           string
	UpstreamModel         string
	UpstreamReportedModel string
	ModelConsistency      ModelConsistency
	Status                RequestStatus
	StatusCode            int
	ErrorCode             string
	ErrorSummary          string
	Stream                bool
	FirstResponseMs       *int64
	DurationMs            int64
	AffinityHit           bool
	Reasoning             reasoning.Config
	Attempts              []Attempt
	Usage                 UsageObservation
}

type RequestLogSink interface {
	Emit(RequestEvent)
}

type NoopRequestLogSink struct{}

func (NoopRequestLogSink) Emit(RequestEvent) {}
