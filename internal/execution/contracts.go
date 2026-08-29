// Package execution defines provider-neutral contracts for one selected upstream attempt.
package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/protocol"
	"gpt-load/internal/reasoning"
	"gpt-load/internal/usage"
)

// ValidationError identifies one invalid contract field without embedding secret values.
type ValidationError struct {
	Field  string
	Reason string
}

// Error implements error.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

// Operation identifies one logical upstream operation.
type Operation string

const (
	OperationChatCompletion       Operation = "chat_completion"
	OperationResponsesCreate      Operation = "responses_create"
	OperationResponsesRetrieve    Operation = "responses_retrieve"
	OperationResponsesDelete      Operation = "responses_delete"
	OperationResponsesCancel      Operation = "responses_cancel"
	OperationResponsesInputItems  Operation = "responses_input_items"
	OperationResponsesCompact     Operation = "responses_compact"
	OperationResponsesInputTokens Operation = "responses_input_tokens"
	OperationCountTokens          Operation = "count_tokens"
	OperationResponsesPassthrough Operation = "responses_passthrough"
	OperationImagesGenerate       Operation = "images_generate"
	OperationImagesEdit           Operation = "images_edit"
	OperationEmbeddingsCreate     Operation = "embeddings_create"
	OperationListModels           Operation = "list_models"
	OperationProbe                Operation = "probe"
)

// Valid reports whether the operation is supported by the execution contract.
func (o Operation) Valid() bool {
	switch o {
	case OperationChatCompletion,
		OperationResponsesCreate,
		OperationResponsesRetrieve,
		OperationResponsesDelete,
		OperationResponsesCancel,
		OperationResponsesInputItems,
		OperationResponsesCompact,
		OperationResponsesInputTokens,
		OperationCountTokens,
		OperationResponsesPassthrough,
		OperationImagesGenerate,
		OperationImagesEdit,
		OperationEmbeddingsCreate,
		OperationListModels,
		OperationProbe:
		return true
	default:
		return false
	}
}

// ReplayPolicy describes the proof required before GPT-Load may dispatch the
// same logical request to another upstream candidate after a request may have
// reached an upstream.
type ReplayPolicy uint8

const (
	// ReplayPolicyLegacy preserves the established behavior of existing
	// operations. Individual health rules still decide whether retry is safe.
	ReplayPolicyLegacy ReplayPolicy = iota
	// ReplayPolicyRequireRejectedBeforeProcessing permits another dispatch only
	// when the provider explicitly proves that processing never started.
	ReplayPolicyRequireRejectedBeforeProcessing
)

// ReplayPolicy returns the operation-level replay contract.
func (o Operation) ReplayPolicy() ReplayPolicy {
	switch o {
	case OperationImagesGenerate, OperationImagesEdit, OperationEmbeddingsCreate:
		return ReplayPolicyRequireRejectedBeforeProcessing
	default:
		return ReplayPolicyLegacy
	}
}

// RouteMode records the already-selected wire strategy for one attempt.
// Executors must execute this decision or reject it; they must not reroute it.
type RouteMode string

const (
	RouteNative    RouteMode = "native"
	RouteConverted RouteMode = "converted"
)

// Valid reports whether the route mode is recognized.
func (m RouteMode) Valid() bool {
	return m == RouteNative || m == RouteConverted
}

// RouteRequirement describes whether a request may use a converted route.
// It protects operation and provider-resource semantics, not optional model
// capabilities such as tools, reasoning, or multimodal input.
type RouteRequirement string

const (
	RouteRequirementAny    RouteRequirement = "any"
	RouteRequirementNative RouteRequirement = "native"
)

// Normalize maps the internal zero value to the default best-effort policy.
func (r RouteRequirement) Normalize() RouteRequirement {
	if r == "" {
		return RouteRequirementAny
	}
	return r
}

// Valid reports whether the normalized requirement is recognized.
func (r RouteRequirement) Valid() bool {
	switch r.Normalize() {
	case RouteRequirementAny, RouteRequirementNative:
		return true
	default:
		return false
	}
}

// Allows reports whether the selected wire route mode satisfies the native
// requirement. Target operation coverage is validated separately.
func (r RouteRequirement) Allows(mode RouteMode) bool {
	if !mode.Valid() || !r.Valid() {
		return false
	}
	return r.Normalize() == RouteRequirementAny || mode == RouteNative
}

// CredentialSnapshot is the exact logical credential selected for an attempt.
// Secret data is private and is never included in JSON.
type CredentialSnapshot struct {
	ID                 uint   `json:"id"`
	Version            uint64 `json:"version"`
	IdentityGeneration uint64 `json:"identity_generation"`

	data []byte
}

// NewCredentialSnapshot copies the credential data into a snapshot.
func NewCredentialSnapshot(id uint, version, identityGeneration uint64, data []byte) CredentialSnapshot {
	return CredentialSnapshot{
		ID:                 id,
		Version:            version,
		IdentityGeneration: identityGeneration,
		data:               cloneBytes(data),
	}
}

// Data returns an independent copy of the secret credential data.
func (c CredentialSnapshot) Data() []byte {
	return cloneBytes(c.data)
}

// Clone returns an independent credential snapshot.
func (c CredentialSnapshot) Clone() CredentialSnapshot {
	clone := c
	clone.data = cloneBytes(c.data)
	return clone
}

// MarshalJSON exposes only non-secret credential identity metadata.
func (c CredentialSnapshot) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID                 uint   `json:"id"`
		Version            uint64 `json:"version"`
		IdentityGeneration uint64 `json:"identity_generation"`
	}{
		ID:                 c.ID,
		Version:            c.Version,
		IdentityGeneration: c.IdentityGeneration,
	})
}

// AttemptTimeouts contains per-attempt timeout overrides. Zero inherits the caller default.
type AttemptTimeouts struct {
	FirstByte  time.Duration `json:"first_byte,omitempty"`
	Request    time.Duration `json:"request,omitempty"`
	StreamIdle time.Duration `json:"stream_idle,omitempty"`
}

// AttemptSpec is a fully selected, provider-neutral upstream attempt.
// NewAttemptSpec or Clone must be used at ownership boundaries because Query,
// Header, Body, TargetConfig, and Credential contain reference-backed values.
type AttemptSpec struct {
	RequestID        string            `json:"request_id"`
	AttemptID        string            `json:"attempt_id"`
	Sequence         uint32            `json:"sequence"`
	ChannelID        string            `json:"channel_id"`
	RouteMode        RouteMode         `json:"route_mode"`
	ClientProtocol   protocol.Protocol `json:"client_protocol"`
	Operation        Operation         `json:"operation"`
	RouteRequirement RouteRequirement  `json:"route_requirement"`
	ClientModel      string            `json:"client_model,omitempty"`
	UpstreamModel    string            `json:"upstream_model,omitempty"`
	Method           string            `json:"method"`
	Path             string            `json:"path"`
	Query            url.Values        `json:"query,omitempty"`
	// RawQuery preserves the original query bytes when exact forwarding matters.
	// It is mutually exclusive with Query and intentionally is not URL-decoded.
	RawQuery string      `json:"raw_query,omitempty"`
	Header   http.Header `json:"header,omitempty"`
	Body     []byte      `json:"body,omitempty"`
	// IncludeUsage asks the executor to request provider usage details when the
	// selected operation supports an explicit wire option.
	IncludeUsage bool `json:"include_usage,omitempty"`
	// ForceCredentialRefresh is set only by GPT-Load after a provider explicitly
	// rejects this selected subscription credential before processing.
	ForceCredentialRefresh bool `json:"force_credential_refresh,omitempty"`
	// ContinuityKey is an opaque tenant-scoped value for provider-private
	// thinking/tool continuity. It is never persisted, logged, or exposed.
	ContinuityKey string `json:"-"`
	// TargetConfig is non-secret configuration resolved by the channel registry.
	TargetConfig     json.RawMessage         `json:"target_config,omitempty"`
	Timeouts         AttemptTimeouts         `json:"timeouts"`
	Credential       CredentialSnapshot      `json:"credential"`
	Proxy            outboundproxy.Effective `json:"-"`
	ProxyFingerprint string                  `json:"-"`
}

// NewAttemptSpec takes ownership of an independent clone of spec.
func NewAttemptSpec(spec AttemptSpec) AttemptSpec {
	spec.RouteRequirement = spec.RouteRequirement.Normalize()
	return spec.Clone()
}

// Clone returns an independent attempt specification.
func (s AttemptSpec) Clone() AttemptSpec {
	clone := s
	clone.Query = cloneValues(s.Query)
	clone.Header = cloneHeader(s.Header)
	clone.Body = cloneBytes(s.Body)
	clone.TargetConfig = cloneRawMessage(s.TargetConfig)
	clone.Credential = s.Credential.Clone()
	return clone
}

// DispatchState describes whether an attempt may have reached an upstream or
// was completed entirely within GPT-Load.
type DispatchState string

const (
	DispatchNotSent   DispatchState = "not_sent"
	DispatchMaybeSent DispatchState = "maybe_sent"
	DispatchLocal     DispatchState = "local"
)

// Valid reports whether the dispatch state is recognized.
func (s DispatchState) Valid() bool {
	return s == DispatchNotSent || s == DispatchMaybeSent || s == DispatchLocal
}

// MaxErrorSummaryLength bounds sanitized error messages retained by the contract.
const MaxErrorSummaryLength = 4096

// ErrorOrigin identifies the responsibility domain where a failure originated.
type ErrorOrigin string

const (
	ErrorOriginClient     ErrorOrigin = "client"
	ErrorOriginUpstream   ErrorOrigin = "upstream"
	ErrorOriginDownstream ErrorOrigin = "downstream"
	ErrorOriginInternal   ErrorOrigin = "internal"
)

// Valid reports whether the optional origin is recognized.
func (o ErrorOrigin) Valid() bool {
	switch o {
	case "", ErrorOriginClient, ErrorOriginUpstream, ErrorOriginDownstream, ErrorOriginInternal:
		return true
	default:
		return false
	}
}

// ErrorScope identifies the resource boundary affected by a failure.
type ErrorScope string

const (
	ErrorScopeRequest    ErrorScope = "request"
	ErrorScopeModel      ErrorScope = "model"
	ErrorScopeCredential ErrorScope = "credential"
	ErrorScopeGroup      ErrorScope = "group"
)

// Valid reports whether the optional scope is recognized.
func (s ErrorScope) Valid() bool {
	switch s {
	case "", ErrorScopeRequest, ErrorScopeModel, ErrorScopeCredential, ErrorScopeGroup:
		return true
	default:
		return false
	}
}

// ErrorKind is the stable category used for retry and health decisions.
type ErrorKind string

const (
	ErrorKindTransport             ErrorKind = "transport"
	ErrorKindTimeout               ErrorKind = "timeout"
	ErrorKindCanceled              ErrorKind = "canceled"
	ErrorKindHTTP                  ErrorKind = "http"
	ErrorKindProvider              ErrorKind = "provider"
	ErrorKindInvalidRequest        ErrorKind = "invalid_request"
	ErrorKindConversionUnsupported ErrorKind = "conversion_unsupported"
	ErrorKindInternal              ErrorKind = "internal"
)

const (
	ErrorCodeCriticalSemanticLoss         = "critical_semantic_loss"
	ErrorCodeTargetConversionNotSupported = "target_conversion_not_supported"
	ErrorCodeTargetSerializationFailed    = "target_serialization_failed"
)

// Valid reports whether the error kind is recognized.
func (k ErrorKind) Valid() bool {
	switch k {
	case ErrorKindTransport,
		ErrorKindTimeout,
		ErrorKindCanceled,
		ErrorKindHTTP,
		ErrorKindProvider,
		ErrorKindInvalidRequest,
		ErrorKindConversionUnsupported,
		ErrorKindInternal:
		return true
	default:
		return false
	}
}

// FailureHint is a provider-neutral classification hint. GPT-Load remains the
// owner of health and retry decisions.
type FailureHint string

const (
	FailureHintInvalidCredential       FailureHint = "invalid_credential"
	FailureHintRefreshRequired         FailureHint = "refresh_required"
	FailureHintReauthorizationRequired FailureHint = "reauthorization_required"
	FailureHintRateLimited             FailureHint = "rate_limited"
	FailureHintRequestRejected         FailureHint = "request_rejected"
	FailureHintCandidateUnavailable    FailureHint = "candidate_unavailable"
	FailureHintModelUnavailable        FailureHint = "model_unavailable"
	FailureHintHostError               FailureHint = "host_error"
)

// Valid reports whether the optional hint is recognized.
func (h FailureHint) Valid() bool {
	switch h {
	case "", FailureHintInvalidCredential, FailureHintRefreshRequired,
		FailureHintReauthorizationRequired, FailureHintRateLimited,
		FailureHintRequestRejected, FailureHintCandidateUnavailable,
		FailureHintModelUnavailable, FailureHintHostError:
		return true
	default:
		return false
	}
}

// ReplaySafety records whether a provider has explicitly confirmed that the
// failed model request was rejected before processing.
type ReplaySafety string

const (
	ReplaySafetyUnknown                  ReplaySafety = "unknown"
	ReplaySafetyRejectedBeforeProcessing ReplaySafety = "rejected_before_processing"
)

func (s ReplaySafety) Valid() bool {
	return s == "" || s == ReplaySafetyUnknown || s == ReplaySafetyRejectedBeforeProcessing
}

// ErrorEvidence contains provider-neutral evidence for an unsuccessful attempt.
// Summary may retain a bounded, sanitized upstream error message; raw error
// bodies are never retained.
type ErrorEvidence struct {
	Kind         ErrorKind     `json:"kind"`
	Hint         FailureHint   `json:"hint,omitempty"`
	OriginHint   ErrorOrigin   `json:"origin_hint,omitempty"`
	ScopeHint    ErrorScope    `json:"scope_hint,omitempty"`
	StatusCode   int           `json:"status_code,omitempty"`
	Type         string        `json:"type,omitempty"`
	Code         string        `json:"code,omitempty"`
	Summary      string        `json:"summary,omitempty"`
	RequestID    string        `json:"request_id,omitempty"`
	RetryAfter   time.Duration `json:"retry_after,omitempty"`
	ReplaySafety ReplaySafety  `json:"replay_safety,omitempty"`
	Header       http.Header   `json:"-"`
}

// Clone returns an independent error evidence value.
func (e ErrorEvidence) Clone() ErrorEvidence {
	clone := e
	clone.Header = cloneHeader(e.Header)
	return clone
}

// UsageEvidence contains normalized usage and the raw provider evidence used to derive it.
type UsageEvidence struct {
	Normalized usage.Result `json:"normalized"`
	Raw        []byte       `json:"raw,omitempty"`
}

// Clone returns an independent usage evidence value.
func (e UsageEvidence) Clone() UsageEvidence {
	clone := e
	clone.Raw = cloneBytes(e.Raw)
	return clone
}

// AttemptResult is the terminal result of a non-streaming attempt.
// The result owns Header, Body, Usage, and Error after return from Executor.
type AttemptResult struct {
	DispatchState     DispatchState     `json:"dispatch_state"`
	ResponseStarted   bool              `json:"response_started"`
	UpstreamProtocol  protocol.Protocol `json:"upstream_protocol,omitempty"`
	AppliedReasoning  *reasoning.Config `json:"applied_reasoning,omitempty"`
	StatusCode        int               `json:"status_code,omitempty"`
	Header            http.Header       `json:"header,omitempty"`
	Body              []byte            `json:"body,omitempty"`
	Model             string            `json:"model,omitempty"`
	UpstreamRequestID string            `json:"upstream_request_id,omitempty"`
	Usage             *UsageEvidence    `json:"usage,omitempty"`
	Error             *ErrorEvidence    `json:"error,omitempty"`
}

// Clone returns an independent attempt result.
func (r AttemptResult) Clone() AttemptResult {
	clone := r
	clone.Header = cloneHeader(r.Header)
	clone.Body = cloneBytes(r.Body)
	if r.Usage != nil {
		usage := r.Usage.Clone()
		clone.Usage = &usage
	}
	if r.Error != nil {
		evidence := r.Error.Clone()
		clone.Error = &evidence
	}
	if r.AppliedReasoning != nil {
		reasoningConfig := r.AppliedReasoning.Clone()
		clone.AppliedReasoning = &reasoningConfig
	}
	return clone
}

// StreamEventKind identifies one streaming event category.
type StreamEventKind string

const (
	StreamEventReady StreamEventKind = "ready"
	StreamEventData  StreamEventKind = "data"
	StreamEventUsage StreamEventKind = "usage"
)

// Valid reports whether the streaming event kind is recognized.
func (k StreamEventKind) Valid() bool {
	return k == StreamEventReady || k == StreamEventData || k == StreamEventUsage
}

// StreamEvent is one ordered item emitted by a streaming attempt.
// Ownership of Header, Data, and Usage transfers to the stream sink. Executors
// must not mutate or reuse reference-backed event values after the sink call.
type StreamEvent struct {
	Sequence          uint64          `json:"sequence"`
	Kind              StreamEventKind `json:"kind"`
	StatusCode        int             `json:"status_code,omitempty"`
	Header            http.Header     `json:"header,omitempty"`
	UpstreamRequestID string          `json:"upstream_request_id,omitempty"`
	Data              []byte          `json:"data,omitempty"`
	Usage             *UsageEvidence  `json:"usage,omitempty"`
}

// Clone returns an independent stream event.
func (e StreamEvent) Clone() StreamEvent {
	clone := e
	clone.Header = cloneHeader(e.Header)
	clone.Data = cloneBytes(e.Data)
	if e.Usage != nil {
		usage := e.Usage.Clone()
		clone.Usage = &usage
	}
	return clone
}

// StreamResult is the terminal metadata returned after streaming ends.
// The result owns Header, Usage, and Error after return from Executor.
type StreamResult struct {
	DispatchState     DispatchState     `json:"dispatch_state"`
	ResponseStarted   bool              `json:"response_started"`
	UpstreamProtocol  protocol.Protocol `json:"upstream_protocol,omitempty"`
	AppliedReasoning  *reasoning.Config `json:"applied_reasoning,omitempty"`
	StatusCode        int               `json:"status_code,omitempty"`
	Header            http.Header       `json:"header,omitempty"`
	Model             string            `json:"model,omitempty"`
	UpstreamRequestID string            `json:"upstream_request_id,omitempty"`
	Usage             *UsageEvidence    `json:"usage,omitempty"`
	Error             *ErrorEvidence    `json:"error,omitempty"`
}

// Clone returns an independent streaming result.
func (r StreamResult) Clone() StreamResult {
	clone := r
	clone.Header = cloneHeader(r.Header)
	if r.Usage != nil {
		usage := r.Usage.Clone()
		clone.Usage = &usage
	}
	if r.Error != nil {
		evidence := r.Error.Clone()
		clone.Error = &evidence
	}
	if r.AppliedReasoning != nil {
		reasoningConfig := r.AppliedReasoning.Clone()
		clone.AppliedReasoning = &reasoningConfig
	}
	return clone
}

// StreamSink consumes events synchronously. Returning an error stops the stream.
type StreamSink func(StreamEvent) error

// Executor executes exactly the selected attempt described by AttemptSpec.
// Implementations must not mutate spec or retain reference-backed values from it.
type Executor interface {
	Execute(context.Context, AttemptSpec) AttemptResult
	ExecuteStream(context.Context, AttemptSpec, StreamSink) StreamResult
}

var _ json.Marshaler = CredentialSnapshot{}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), value...)
}

func cloneRawMessage(value json.RawMessage) json.RawMessage {
	if value == nil {
		return nil
	}
	return json.RawMessage(cloneBytes(value))
}

func cloneHeader(value http.Header) http.Header {
	if value == nil {
		return nil
	}
	return value.Clone()
}

func cloneValues(value url.Values) url.Values {
	if value == nil {
		return nil
	}
	clone := make(url.Values, len(value))
	for key, values := range value {
		clone[key] = append([]string(nil), values...)
	}
	return clone
}
