package execution

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Validate validates the credential identity and secret data.
func (c CredentialSnapshot) Validate() error {
	if c.ID == 0 {
		return validationError("credential.id", "must be greater than zero")
	}
	if c.Version == 0 {
		return validationError("credential.version", "must be greater than zero")
	}
	if c.IdentityGeneration == 0 {
		return validationError("credential.identity_generation", "must be greater than zero")
	}
	if len(c.data) == 0 {
		return validationError("credential.data", "must not be empty")
	}
	return nil
}

// Validate validates the timeout values.
func (t AttemptTimeouts) Validate() error {
	if t.FirstByte < 0 {
		return validationError("timeouts.first_byte", "must not be negative")
	}
	if t.Request < 0 {
		return validationError("timeouts.request", "must not be negative")
	}
	if t.StreamIdle < 0 {
		return validationError("timeouts.stream_idle", "must not be negative")
	}
	return nil
}

// Validate validates the complete attempt specification.
func (s AttemptSpec) Validate() error {
	if strings.TrimSpace(s.RequestID) == "" {
		return validationError("request_id", "must not be empty")
	}
	if strings.TrimSpace(s.AttemptID) == "" {
		return validationError("attempt_id", "must not be empty")
	}
	if s.Sequence == 0 {
		return validationError("sequence", "must be greater than zero")
	}
	if strings.TrimSpace(s.ChannelID) == "" {
		return validationError("channel_id", "must not be empty")
	}
	if strings.TrimSpace(s.TargetKind) == "" || len(s.TargetKind) > 64 || containsControl(s.TargetKind) {
		return validationError("target_kind", "must be a safe non-empty identifier")
	}
	if !s.RouteMode.Valid() {
		return validationError("route_mode", "unsupported value")
	}
	if !s.ClientProtocol.Valid() {
		return validationError("client_protocol", "unsupported value")
	}
	if !s.Operation.Valid() {
		return validationError("operation", "unsupported value")
	}
	if !s.RouteRequirement.Valid() {
		return validationError("route_requirement", "unsupported value")
	}
	if operationRequiresModel(s.Operation) {
		if strings.TrimSpace(s.ClientModel) == "" {
			return validationError("client_model", "must not be empty for this operation")
		}
		if strings.TrimSpace(s.UpstreamModel) == "" {
			return validationError("upstream_model", "must not be empty for this operation")
		}
	}
	if s.Operation == OperationProbe {
		if s.Method != "" || s.Path != "" || len(s.Query) != 0 || s.RawQuery != "" || len(s.Body) != 0 {
			return validationError("probe", "must not contain provider wire fields")
		}
	} else {
		if !validHTTPToken(s.Method) {
			return validationError("method", "must be a valid HTTP method")
		}
		if err := validateRequestPath(s.Path); err != nil {
			return validationError("path", "must be an absolute request path without query or fragment")
		}
		if s.RawQuery != "" {
			if len(s.Query) != 0 {
				return validationError("raw_query", "must not be combined with query")
			}
			if strings.Contains(s.RawQuery, "#") || containsControl(s.RawQuery) {
				return validationError("raw_query", "contains a fragment or control characters")
			}
		}
		if err := validateValues(s.Query); err != nil {
			return validationError("query", "contains invalid control characters")
		}
	}
	if err := validateHeader(s.Header); err != nil {
		return validationError("header", "contains an invalid name or value")
	}
	if len(s.TargetConfig) > 0 && !json.Valid(s.TargetConfig) {
		return validationError("target_config", "must contain valid JSON")
	}
	if s.ClientParameters != nil {
		if err := s.ClientParameters.Validate(); err != nil {
			return validationError("client_parameters", "must contain a valid safe projection")
		}
	}
	if err := s.Timeouts.Validate(); err != nil {
		return err
	}
	return s.Credential.Validate()
}

// Validate validates error evidence.
func (e ErrorEvidence) Validate() error {
	if !e.Kind.Valid() {
		return validationError("error.kind", "unsupported value")
	}
	if !e.Hint.Valid() {
		return validationError("error.hint", "unsupported value")
	}
	if !validOptionalHTTPStatus(e.StatusCode) {
		return validationError("error.status_code", "must be zero or a valid HTTP status")
	}
	if strings.TrimSpace(e.Summary) == "" {
		return validationError("error.summary", "must not be empty")
	}
	if utf8.RuneCountInString(e.Summary) > MaxErrorSummaryLength {
		return validationError("error.summary", "exceeds maximum length")
	}
	if containsControl(e.Summary) {
		return validationError("error.summary", "must be a single safe line")
	}
	if containsControl(e.Type) {
		return validationError("error.type", "contains control characters")
	}
	if containsControl(e.Code) {
		return validationError("error.code", "contains control characters")
	}
	if containsControl(e.RequestID) {
		return validationError("error.request_id", "contains control characters")
	}
	if e.RetryAfter < 0 {
		return validationError("error.retry_after", "must not be negative")
	}
	if err := validateSafeEvidenceHeader(e.Header); err != nil {
		return validationError("error.header", "contains a non-safe header")
	}
	return nil
}

// Validate validates a terminal attempt result.
func (r AttemptResult) Validate() error {
	if r.UpstreamAPI != "" && !r.UpstreamAPI.Valid() {
		return validationError("upstream_api", "unsupported value")
	}
	if err := validateResultMetadata(
		r.DispatchState,
		r.ResponseStarted,
		r.StatusCode,
		r.Header,
		r.UpstreamRequestID,
		r.Error,
	); err != nil {
		return err
	}
	if r.DispatchState == DispatchNotSent && len(r.Body) > 0 {
		return validationError("body", "must be empty when dispatch_state is not_sent")
	}
	if !r.ResponseStarted && len(r.Body) > 0 {
		return validationError("body", "requires response_started")
	}
	if r.ConversionTrace != nil {
		if err := r.ConversionTrace.Validate(); err != nil {
			return validationError("conversion_trace", "must contain a valid safe projection")
		}
	}
	return nil
}

// Validate validates a stream event.
func (e StreamEvent) Validate() error {
	if !e.Kind.Valid() {
		return validationError("stream_event.kind", "unsupported value")
	}
	if e.Sequence == 0 {
		return validationError("stream_event.sequence", "must be greater than zero")
	}
	switch e.Kind {
	case StreamEventReady:
		if e.Sequence != 1 {
			return validationError("stream_event.sequence", "ready event must be first")
		}
		if !validHTTPStatus(e.StatusCode) {
			return validationError("stream_event.status_code", "ready event requires a valid HTTP status")
		}
		if e.Header == nil {
			return validationError("stream_event.header", "ready event requires initial headers")
		}
		if err := validateHeader(e.Header); err != nil {
			return validationError("stream_event.header", "contains an invalid name or value")
		}
		if len(e.Data) > 0 || e.Usage != nil {
			return validationError("stream_event.ready", "must not contain data or usage")
		}
	case StreamEventData:
		if e.Sequence == 1 {
			return validationError("stream_event.sequence", "data must follow a ready event")
		}
		if len(e.Data) == 0 {
			return validationError("stream_event.data", "must not be empty")
		}
		if e.Usage != nil || hasReadyMetadata(e) {
			return validationError("stream_event.data", "must not contain ready metadata or usage")
		}
	case StreamEventUsage:
		if e.Sequence == 1 {
			return validationError("stream_event.sequence", "usage must follow a ready event")
		}
		if e.Usage == nil {
			return validationError("stream_event.usage", "must not be empty")
		}
		if len(e.Data) > 0 || hasReadyMetadata(e) {
			return validationError("stream_event.usage", "must not contain ready metadata or data")
		}
	}
	return nil
}

// Validate validates a terminal streaming result.
func (r StreamResult) Validate() error {
	if r.UpstreamAPI != "" && !r.UpstreamAPI.Valid() {
		return validationError("upstream_api", "unsupported value")
	}
	if err := validateResultMetadata(
		r.DispatchState,
		r.ResponseStarted,
		r.StatusCode,
		r.Header,
		r.UpstreamRequestID,
		r.Error,
	); err != nil {
		return err
	}
	if r.ConversionTrace != nil {
		if err := r.ConversionTrace.Validate(); err != nil {
			return validationError("conversion_trace", "must contain a valid safe projection")
		}
	}
	return nil
}

func validateResultMetadata(
	dispatchState DispatchState,
	responseStarted bool,
	statusCode int,
	header http.Header,
	upstreamRequestID string,
	errorEvidence *ErrorEvidence,
) error {
	if !dispatchState.Valid() {
		return validationError("dispatch_state", "unsupported value")
	}
	if statusCode > 0 && !responseStarted {
		return validationError("response_started", "must be true when status_code is present")
	}
	if !responseStarted && (len(header) > 0 || upstreamRequestID != "") {
		return validationError("response_started", "must be true when upstream response metadata is present")
	}
	if responseStarted && !validHTTPStatus(statusCode) {
		return validationError("status_code", "response_started requires a valid HTTP status")
	}
	if dispatchState == DispatchNotSent {
		if responseStarted || statusCode != 0 || len(header) > 0 || upstreamRequestID != "" {
			return validationError("dispatch_state", "not_sent cannot contain upstream response metadata")
		}
	}
	if responseStarted && dispatchState != DispatchMaybeSent {
		return validationError("dispatch_state", "response_started requires maybe_sent")
	}
	if err := validateHeader(header); err != nil {
		return validationError("header", "contains an invalid name or value")
	}
	if containsControl(upstreamRequestID) {
		return validationError("upstream_request_id", "contains control characters")
	}
	if errorEvidence == nil {
		if !responseStarted || statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
			return validationError("error", "is required unless a started response has a 2xx status")
		}
		return nil
	}
	if err := errorEvidence.Validate(); err != nil {
		return err
	}
	if errorEvidence.StatusCode > 0 && !responseStarted {
		return validationError("error.status_code", "requires response_started")
	}
	if responseStarted && errorEvidence.StatusCode != 0 && errorEvidence.StatusCode != statusCode {
		return validationError("error.status_code", "must match result status_code")
	}
	return nil
}

func operationRequiresModel(operation Operation) bool {
	return operation == OperationChatCompletion ||
		operation == OperationResponsesCreate ||
		operation == OperationResponsesCompact ||
		operation == OperationResponsesInputTokens ||
		operation == OperationProbe
}

func validateRequestPath(path string) error {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return validationError("path", "invalid prefix")
	}
	parsed, err := url.ParseRequestURI(path)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return validationError("path", "invalid request path")
	}
	return nil
}

func validateValues(values url.Values) error {
	for key, items := range values {
		if key == "" || containsControl(key) {
			return validationError("query", "invalid key")
		}
		for _, item := range items {
			if containsControl(item) {
				return validationError("query", "invalid value")
			}
		}
	}
	return nil
}

func validateHeader(header http.Header) error {
	for name, values := range header {
		if !validHTTPToken(name) {
			return validationError("header", "invalid name")
		}
		for _, value := range values {
			if strings.ContainsAny(value, "\r\n\x00") {
				return validationError("header", "invalid value")
			}
		}
	}
	return nil
}

func validateSafeEvidenceHeader(header http.Header) error {
	if err := validateHeader(header); err != nil {
		return err
	}
	for name := range header {
		canonical := http.CanonicalHeaderKey(name)
		if canonical == "Retry-After" ||
			canonical == "Request-Id" ||
			canonical == "X-Request-Id" ||
			canonical == "Traceparent" ||
			strings.HasPrefix(canonical, "Ratelimit-") ||
			strings.HasPrefix(canonical, "X-Ratelimit-") {
			continue
		}
		return validationError("header", "not allowlisted")
	}
	return nil
}

func validOptionalHTTPStatus(statusCode int) bool {
	return statusCode == 0 || validHTTPStatus(statusCode)
}

func validHTTPStatus(statusCode int) bool {
	return statusCode >= 100 && statusCode <= 599
}

func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r <= 31 || r >= 127 || strings.ContainsRune("()<>@,;:\\\"/[]?={} \t", r) {
			return false
		}
	}
	return true
}

func containsControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func hasReadyMetadata(event StreamEvent) bool {
	return event.StatusCode != 0 || event.Header != nil || event.UpstreamRequestID != ""
}

func validationError(field, reason string) *ValidationError {
	return &ValidationError{Field: field, Reason: reason}
}
