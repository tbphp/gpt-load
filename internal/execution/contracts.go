// Package execution defines provider-neutral contracts for one selected upstream attempt.
package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"

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
	OperationResponsesPassthrough Operation = "responses_passthrough"
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
		OperationResponsesPassthrough,
		OperationListModels,
		OperationProbe:
		return true
	default:
		return false
	}
}

// RouteMode records the already-selected wire strategy for one attempt.
// Executors must execute this decision or reject it; they must not reroute it.
type RouteMode string

const (
	RouteNative    RouteMode = "native"
	RouteConverted RouteMode = "converted"
)

// UpstreamAPI identifies the actual upstream wire API selected for an attempt.
// Values are stable product vocabulary and intentionally exclude SDK names.
type UpstreamAPI string

const (
	UpstreamAPIOpenAIChatCompletions UpstreamAPI = "openai-chat-completions"
	UpstreamAPIOpenAIResponses       UpstreamAPI = "openai-responses"
	UpstreamAPIAnthropicMessages     UpstreamAPI = "anthropic-messages"
	UpstreamAPIGeminiGenerateContent UpstreamAPI = "gemini-generate-content"
	UpstreamAPIOpenAIModels          UpstreamAPI = "openai-models"
	UpstreamAPIAnthropicModels       UpstreamAPI = "anthropic-models"
	UpstreamAPIGeminiModels          UpstreamAPI = "gemini-models"
	UpstreamAPIAzureOpenAI           UpstreamAPI = "azure-openai"
	UpstreamAPIAWSBedrock            UpstreamAPI = "aws-bedrock"
	UpstreamAPIGoogleVertex          UpstreamAPI = "google-vertex"
)

// Valid reports whether the upstream API value is recognized.
func (api UpstreamAPI) Valid() bool {
	switch api {
	case UpstreamAPIOpenAIChatCompletions,
		UpstreamAPIOpenAIResponses,
		UpstreamAPIAnthropicMessages,
		UpstreamAPIGeminiGenerateContent,
		UpstreamAPIOpenAIModels,
		UpstreamAPIAnthropicModels,
		UpstreamAPIGeminiModels,
		UpstreamAPIAzureOpenAI,
		UpstreamAPIAWSBedrock,
		UpstreamAPIGoogleVertex:
		return true
	default:
		return false
	}
}

// Valid reports whether the route mode is recognized.
func (m RouteMode) Valid() bool {
	return m == RouteNative || m == RouteConverted
}

// Feature identifies an optional execution capability.
type Feature string

const (
	FeatureStreaming               Feature = "streaming"
	FeatureTools                   Feature = "tools"
	FeatureReasoning               Feature = "reasoning"
	FeatureMultimodal              Feature = "multimodal"
	FeatureStructuredOutput        Feature = "structured_output"
	FeatureNativeResourceSemantics Feature = "native_resource_semantics"
)

// Valid reports whether the feature is recognized.
func (f Feature) Valid() bool {
	switch f {
	case FeatureStreaming,
		FeatureTools,
		FeatureReasoning,
		FeatureMultimodal,
		FeatureStructuredOutput,
		FeatureNativeResourceSemantics:
		return true
	default:
		return false
	}
}

// FeatureSet is an immutable set of optional execution capabilities.
type FeatureSet struct {
	features map[Feature]struct{}
}

// NewFeatureSet constructs a feature set.
func NewFeatureSet(features ...Feature) (FeatureSet, error) {
	set := FeatureSet{features: make(map[Feature]struct{}, len(features))}
	for _, feature := range features {
		if !feature.Valid() {
			return FeatureSet{}, &ValidationError{Field: "feature", Reason: "unsupported value"}
		}
		set.features[feature] = struct{}{}
	}
	return set, nil
}

// Has reports whether feature is present.
func (s FeatureSet) Has(feature Feature) bool {
	_, ok := s.features[feature]
	return ok
}

// Features returns a stable snapshot of the set.
func (s FeatureSet) Features() []Feature {
	features := make([]Feature, 0, len(s.features))
	for feature := range s.features {
		features = append(features, feature)
	}
	sort.Slice(features, func(i, j int) bool {
		return features[i] < features[j]
	})
	return features
}

// Clone returns an independent feature set.
func (s FeatureSet) Clone() FeatureSet {
	clone := FeatureSet{features: make(map[Feature]struct{}, len(s.features))}
	for feature := range s.features {
		clone.features[feature] = struct{}{}
	}
	return clone
}

// Validate validates all features in the set.
func (s FeatureSet) Validate() error {
	for feature := range s.features {
		if !feature.Valid() {
			return &ValidationError{Field: "feature", Reason: "unsupported value"}
		}
	}
	return nil
}

// Capability binds an operation to the optional features supported for it.
type Capability struct {
	Operation Operation
	Features  FeatureSet
}

// CapabilitySet is an immutable set of supported operations and features.
type CapabilitySet struct {
	capabilities map[Operation]FeatureSet
}

// NewCapabilitySet constructs a capability set.
func NewCapabilitySet(capabilities ...Capability) (CapabilitySet, error) {
	set := CapabilitySet{capabilities: make(map[Operation]FeatureSet, len(capabilities))}
	for _, capability := range capabilities {
		if !capability.Operation.Valid() {
			return CapabilitySet{}, &ValidationError{Field: "operation", Reason: "unsupported value"}
		}
		if err := capability.Features.Validate(); err != nil {
			return CapabilitySet{}, err
		}
		features, ok := set.capabilities[capability.Operation]
		if !ok {
			features = FeatureSet{features: make(map[Feature]struct{})}
		}
		for feature := range capability.Features.features {
			features.features[feature] = struct{}{}
		}
		set.capabilities[capability.Operation] = features
	}
	return set, nil
}

// Has reports whether operation is supported.
func (s CapabilitySet) Has(operation Operation) bool {
	_, ok := s.capabilities[operation]
	return ok
}

// Supports reports whether operation and all required features are supported.
func (s CapabilitySet) Supports(operation Operation, required FeatureSet) bool {
	if !operation.Valid() || required.Validate() != nil {
		return false
	}
	available, ok := s.capabilities[operation]
	if !ok {
		return false
	}
	for feature := range required.features {
		if !available.Has(feature) {
			return false
		}
	}
	return true
}

// Features returns an independent set of features supported for operation.
func (s CapabilitySet) Features(operation Operation) FeatureSet {
	features, ok := s.capabilities[operation]
	if !ok {
		return FeatureSet{features: make(map[Feature]struct{})}
	}
	return features.Clone()
}

// Operations returns a stable snapshot of supported operations.
func (s CapabilitySet) Operations() []Operation {
	operations := make([]Operation, 0, len(s.capabilities))
	for operation := range s.capabilities {
		operations = append(operations, operation)
	}
	sort.Slice(operations, func(i, j int) bool {
		return operations[i] < operations[j]
	})
	return operations
}

// Clone returns an independent capability set.
func (s CapabilitySet) Clone() CapabilitySet {
	clone := CapabilitySet{capabilities: make(map[Operation]FeatureSet, len(s.capabilities))}
	for operation, features := range s.capabilities {
		clone.capabilities[operation] = features.Clone()
	}
	return clone
}

// Validate validates all operations and features in the set.
func (s CapabilitySet) Validate() error {
	for operation, features := range s.capabilities {
		if !operation.Valid() {
			return &ValidationError{Field: "operation", Reason: "unsupported value"}
		}
		if err := features.Validate(); err != nil {
			return err
		}
	}
	return nil
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
	TargetKind       string            `json:"target_kind"`
	RouteMode        RouteMode         `json:"route_mode"`
	ClientProtocol   protocol.Protocol `json:"client_protocol"`
	Operation        Operation         `json:"operation"`
	RequiredFeatures FeatureSet        `json:"-"`
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
	// TargetConfig is non-secret configuration resolved by the channel registry.
	TargetConfig json.RawMessage    `json:"target_config,omitempty"`
	Timeouts     AttemptTimeouts    `json:"timeouts"`
	Credential   CredentialSnapshot `json:"credential"`
}

// NewAttemptSpec takes ownership of an independent clone of spec.
func NewAttemptSpec(spec AttemptSpec) AttemptSpec {
	return spec.Clone()
}

// Clone returns an independent attempt specification.
func (s AttemptSpec) Clone() AttemptSpec {
	clone := s
	clone.RequiredFeatures = s.RequiredFeatures.Clone()
	clone.Query = cloneValues(s.Query)
	clone.Header = cloneHeader(s.Header)
	clone.Body = cloneBytes(s.Body)
	clone.TargetConfig = cloneRawMessage(s.TargetConfig)
	clone.Credential = s.Credential.Clone()
	return clone
}

// DispatchState describes whether an attempt may have reached the upstream.
type DispatchState string

const (
	DispatchNotSent   DispatchState = "not_sent"
	DispatchMaybeSent DispatchState = "maybe_sent"
)

// Valid reports whether the dispatch state is recognized.
func (s DispatchState) Valid() bool {
	return s == DispatchNotSent || s == DispatchMaybeSent
}

// MaxErrorSummaryLength bounds sanitized error messages retained by the contract.
const MaxErrorSummaryLength = 4096

// ErrorKind is the stable category used for retry and health decisions.
type ErrorKind string

const (
	ErrorKindTransport      ErrorKind = "transport"
	ErrorKindTimeout        ErrorKind = "timeout"
	ErrorKindCanceled       ErrorKind = "canceled"
	ErrorKindHTTP           ErrorKind = "http"
	ErrorKindProvider       ErrorKind = "provider"
	ErrorKindInvalidRequest ErrorKind = "invalid_request"
	ErrorKindInternal       ErrorKind = "internal"
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
	FailureHintInvalidCredential FailureHint = "invalid_credential"
	FailureHintRateLimited       FailureHint = "rate_limited"
	FailureHintModelUnavailable  FailureHint = "model_unavailable"
	FailureHintHostError         FailureHint = "host_error"
)

// Valid reports whether the optional hint is recognized.
func (h FailureHint) Valid() bool {
	switch h {
	case "", FailureHintInvalidCredential, FailureHintRateLimited,
		FailureHintModelUnavailable, FailureHintHostError:
		return true
	default:
		return false
	}
}

// ErrorEvidence contains provider-neutral evidence for an unsuccessful attempt.
// Summary may retain a bounded, sanitized upstream error message; raw error
// bodies are never retained.
type ErrorEvidence struct {
	Kind       ErrorKind     `json:"kind"`
	Hint       FailureHint   `json:"hint,omitempty"`
	StatusCode int           `json:"status_code,omitempty"`
	Type       string        `json:"type,omitempty"`
	Code       string        `json:"code,omitempty"`
	Summary    string        `json:"summary,omitempty"`
	RequestID  string        `json:"request_id,omitempty"`
	RetryAfter time.Duration `json:"retry_after,omitempty"`
	Header     http.Header   `json:"-"`
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
	UpstreamAPI       UpstreamAPI       `json:"upstream_api,omitempty"`
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
	UpstreamAPI       UpstreamAPI       `json:"upstream_api,omitempty"`
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
// Implementations must return an independent capability snapshot and must not
// mutate spec or retain reference-backed values from it.
type Executor interface {
	Capabilities() CapabilitySet
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
