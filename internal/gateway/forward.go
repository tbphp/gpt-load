package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/textproto"
	"strings"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/contentcoding"
	platformheader "gpt-load/internal/platform/httpheader"
	"gpt-load/internal/protocol"
	"gpt-load/internal/reasoning"
	"gpt-load/internal/state"
	"gpt-load/internal/usage"
)

// ForwardInput is the frozen logical-attempt input shared by the gateway
// orchestrator and the provider-neutral execution adapter.
type ForwardInput struct {
	Dialect           dialect.Dialect
	ObserveUsage      bool
	Group             state.GroupView
	APIKey            string
	CredentialSecrets []string
	Request           *dialect.ParsedRequest
	ExternalModel     string
	UpstreamModelID   string
	OnStreamReady     func()
	OnFirstResponse   func()

	RequestID        string
	AttemptID        string
	AttemptSequence  uint32
	ClientProtocol   protocol.Protocol
	Operation        execution.Operation
	RouteRequirement execution.RouteRequirement
	ChannelID        string
	RouteMode        execution.RouteMode
	TargetConfig     json.RawMessage
	Credential       execution.CredentialSnapshot
	// ForceCredentialRefresh preserves one explicit provider-auth retry as a
	// globally counted Attempt on the same selected credential.
	ForceCredentialRefresh bool
}

// UpstreamResult is the gateway's stable view of one logical execution
// attempt. SDK-internal transport recovery remains inside this result.
type UpstreamResult struct {
	StatusCode                int
	Header                    http.Header
	Body                      []byte
	ClassificationBody        []byte
	UpstreamReportedModel     string
	ResponseModelObserved     bool
	ResponseModelMismatch     bool
	ErrorSummary              string
	Err                       error
	RequestWritten            bool
	Committed                 bool
	ProviderErrorBeforeCommit bool
	Stream                    StreamObservation
	Usage                     usage.Result
	DispatchState             execution.DispatchState
	ResponseStarted           bool
	UpstreamProtocol          protocol.Protocol
	AppliedReasoning          reasoning.Config
	UpstreamRequestID         string
	ExecutionError            *execution.ErrorEvidence
}

func (result UpstreamResult) HasResponse() bool {
	return result.StatusCode != 0 && result.Err == nil
}

const (
	maxNonStreamingResponseBodyBytes = int64(32 << 20)
	maxErrorResponseBodyBytes        = int64(64 << 10)
	maxDecompressedErrorBodyBytes    = int64(1 << 20)
	maxStreamingErrorBodyBytes       = int(maxErrorResponseBodyBytes)
)

var ErrUpstreamProtocol = errors.New("upstream protocol error")

func needsModelRewrite(input ForwardInput) bool {
	return input.ExternalModel != "" &&
		input.UpstreamModelID != "" &&
		input.ExternalModel != input.UpstreamModelID
}

func isResolvedCredentialHeaderName(name string) bool {
	return platformheader.IsCredentialName(name)
}

func rewriteBoundedLiteral(body []byte, literal, replacement string, limit int64) ([]byte, bool) {
	if literal == "" {
		return body, int64(len(body)) <= limit
	}
	if !json.Valid(body) {
		return replaceAllBounded(body, []byte(literal), []byte(replacement), limit)
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	budget := int64(0)
	rewritten, changed, ok := rewriteJSONLiteralStrings(value, literal, replacement, limit, &budget)
	if !ok {
		return nil, false
	}
	if !changed {
		return body, true
	}
	encoded, err := json.Marshal(rewritten)
	if err != nil || int64(len(encoded)) > limit {
		return nil, false
	}
	return encoded, true
}

func rewriteJSONLiteralStrings(
	value any,
	literal string,
	replacement string,
	limit int64,
	budget *int64,
) (any, bool, bool) {
	switch typed := value.(type) {
	case string:
		return replaceStringBounded(typed, literal, replacement, limit, budget)
	case []any:
		changed := false
		for index, item := range typed {
			rewritten, itemChanged, ok := rewriteJSONLiteralStrings(
				item, literal, replacement, limit, budget,
			)
			if !ok {
				return nil, false, false
			}
			typed[index] = rewritten
			changed = changed || itemChanged
		}
		return typed, changed, true
	case map[string]any:
		rewrittenObject := make(map[string]any, len(typed))
		changed := false
		for key, item := range typed {
			rewrittenKey, keyChanged, ok := replaceStringBounded(
				key, literal, replacement, limit, budget,
			)
			if !ok {
				return nil, false, false
			}
			if _, exists := rewrittenObject[rewrittenKey]; exists {
				return nil, false, false
			}
			rewrittenItem, itemChanged, ok := rewriteJSONLiteralStrings(
				item, literal, replacement, limit, budget,
			)
			if !ok {
				return nil, false, false
			}
			rewrittenObject[rewrittenKey] = rewrittenItem
			changed = changed || keyChanged || itemChanged
		}
		return rewrittenObject, changed, true
	default:
		return value, false, true
	}
}

func replaceStringBounded(
	source string,
	old string,
	replacement string,
	limit int64,
	budget *int64,
) (string, bool, bool) {
	if old == "" {
		return source, false, true
	}
	count := strings.Count(source, old)
	if count == 0 {
		return source, false, true
	}
	resultLength, ok := replacementResultLength(
		int64(len(source)), int64(len(old)), int64(len(replacement)), int64(count), limit,
	)
	if !ok || budget == nil || *budget > limit-resultLength {
		return "", false, false
	}
	*budget += resultLength
	return strings.ReplaceAll(source, old, replacement), true, true
}

func replaceAllBounded(source, old, replacement []byte, limit int64) ([]byte, bool) {
	if len(old) == 0 {
		return source, int64(len(source)) <= limit
	}
	count := bytes.Count(source, old)
	if count == 0 {
		return source, int64(len(source)) <= limit
	}
	if _, ok := replacementResultLength(
		int64(len(source)), int64(len(old)), int64(len(replacement)), int64(count), limit,
	); !ok {
		return nil, false
	}
	return bytes.ReplaceAll(source, old, replacement), true
}

func replacementResultLength(sourceLength, oldLength, replacementLength, count, limit int64) (int64, bool) {
	if sourceLength < 0 || oldLength <= 0 || replacementLength < 0 || count < 0 || limit < 0 || sourceLength > limit {
		return 0, false
	}
	if replacementLength <= oldLength {
		return sourceLength - count*(oldLength-replacementLength), true
	}
	delta := replacementLength - oldLength
	if count > (limit-sourceLength)/delta {
		return 0, false
	}
	return sourceLength + count*delta, true
}

var representationMetadataHeaderNames = [...]string{
	"Content-Encoding",
	"Content-Length",
	"ETag",
	"Digest",
	"Content-MD5",
	"Content-Range",
	"Content-Digest",
	"Repr-Digest",
	"Signature",
	"Signature-Input",
}

func stripDownstreamResponseSignatures(headers http.Header) {
	deleteHeaderField(headers, "Signature")
	deleteHeaderField(headers, "Signature-Input")
}

func invalidateRewrittenBodyHeaders(headers http.Header) {
	for _, name := range representationMetadataHeaderNames {
		if strings.EqualFold(name, "Content-Encoding") {
			continue
		}
		deleteHeaderField(headers, name)
	}
}

func validateStreamContentEncoding(headers http.Header) error {
	encoding, err := contentcoding.ParseContentEncoding(
		headerFieldValues(headers, "Content-Encoding"),
	)
	if err != nil || encoding != contentcoding.Identity {
		return fmt.Errorf("%w: non-identity stream Content-Encoding", ErrUpstreamProtocol)
	}
	return nil
}

var hopByHopHeaders = map[string]struct{}{
	"Connection": {}, "Proxy-Connection": {}, "Keep-Alive": {},
	"Proxy-Authenticate": {}, "Proxy-Authorization": {}, "Te": {},
	"Trailer": {}, "Transfer-Encoding": {}, "Upgrade": {},
}

func cloneEndToEndHeaders(source http.Header) http.Header {
	cloned := source.Clone()
	if cloned == nil {
		cloned = make(http.Header)
	}
	for name, values := range source {
		if !strings.EqualFold(name, "Connection") {
			continue
		}
		for _, value := range values {
			for _, token := range strings.Split(value, ",") {
				if tokenName := strings.TrimSpace(token); tokenName != "" {
					deleteHeaderField(cloned, tokenName)
				}
			}
		}
	}
	for name := range cloned {
		if isHopByHopHeader(name) || isDebugHeader(name) {
			delete(cloned, name)
		}
	}
	return cloned
}

func isHopByHopHeader(name string) bool {
	for hopName := range hopByHopHeaders {
		if strings.EqualFold(name, hopName) {
			return true
		}
	}
	return false
}

func sanitizeUpstreamRequestHeaders(headers http.Header) {
	if headers == nil {
		return
	}
	for name, values := range headers {
		if !strings.EqualFold(name, "Connection") {
			continue
		}
		for _, value := range values {
			for _, token := range strings.Split(value, ",") {
				if tokenName := strings.TrimSpace(token); tokenName != "" {
					deleteHeaderField(headers, tokenName)
				}
			}
		}
	}
	for name := range headers {
		if isHopByHopHeader(name) ||
			platformheader.IsForbiddenRequestRuleName(name) ||
			isDebugHeader(name) {
			delete(headers, name)
		}
	}
}

func headerValuesContainLiteral(values []string, literal string) bool {
	if literal == "" {
		return false
	}
	for _, value := range values {
		if strings.Contains(value, literal) {
			return true
		}
	}
	return false
}

func sanitizeForwardResponseHeaders(
	source http.Header,
	input ForwardInput,
	additionalSecrets ...string,
) http.Header {
	headers := cloneEndToEndHeaders(source)
	stripDownstreamResponseSignatures(headers)
	deleted := endToEndHeaderCleanupDeletedField(source, headers)
	namesToDelete := make([]string, 0)
	for actualName, values := range headers {
		deleteField := isResolvedCredentialHeaderName(actualName) ||
			strings.HasPrefix(strings.ToLower(actualName), "x-upstream-") ||
			strings.EqualFold(actualName, "Set-Cookie") ||
			strings.EqualFold(actualName, "Set-Cookie2") ||
			headerValuesContainLiteral(values, input.APIKey)
		for _, secret := range append(append([]string(nil), input.CredentialSecrets...), additionalSecrets...) {
			if deleteField || secret == "" || secret == input.APIKey {
				continue
			}
			if headerValuesContainLiteral(values, secret) {
				deleteField = true
				break
			}
		}
		if deleteField {
			namesToDelete = append(namesToDelete, actualName)
		}
	}
	deleted = deleted || len(namesToDelete) > 0
	for _, name := range namesToDelete {
		deleteHeaderField(headers, name)
	}
	if !needsModelRewrite(input) {
		if deleted {
			stripDownstreamResponseSignatures(headers)
		}
		return headers
	}
	for name, values := range headers {
		nameContainsModel := headerNameContainsLiteral(name, input.UpstreamModelID)
		valuesContainModel := headerValuesContainLiteral(values, input.UpstreamModelID)
		if !nameContainsModel && !valuesContainModel {
			continue
		}
		if isRequiredRepresentationFramingHeader(name) {
			continue
		}
		if strings.EqualFold(name, "Content-Type") {
			if valuesContainModel && contentTypeContainsDisallowedModel(values, input.UpstreamModelID) {
				deleteHeaderField(headers, name)
				deleted = true
			}
			continue
		}
		deleteHeaderField(headers, name)
		deleted = true
	}
	if deleted {
		stripDownstreamResponseSignatures(headers)
	}
	return headers
}

func endToEndHeaderCleanupDeletedField(source, headers http.Header) bool {
	for sourceName := range source {
		found := false
		for headerName := range headers {
			if strings.EqualFold(sourceName, headerName) {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}
	return false
}

func headerNameContainsLiteral(name, literal string) bool {
	return literal != "" && strings.Contains(strings.ToLower(name), strings.ToLower(literal))
}

func isRepresentationMetadataHeader(name string) bool {
	for _, metadataName := range representationMetadataHeaderNames {
		if strings.EqualFold(name, metadataName) {
			return true
		}
	}
	return false
}

func isRequiredRepresentationFramingHeader(name string) bool {
	return strings.EqualFold(name, "Content-Encoding") ||
		strings.EqualFold(name, "Content-Length")
}

func contentTypeContainsDisallowedModel(values []string, upstreamModel string) bool {
	allowedMediaTypes := map[string]struct{}{
		"application/json":         {},
		"application/problem+json": {},
		"application/x-ndjson":     {},
		"text/event-stream":        {},
		"text/plain":               {},
	}
	for _, value := range values {
		if !strings.Contains(value, upstreamModel) {
			continue
		}
		mediaType, _, err := mime.ParseMediaType(value)
		if err != nil {
			return true
		}
		if _, allowed := allowedMediaTypes[strings.ToLower(mediaType)]; !allowed {
			return true
		}

		mediaEnd := strings.IndexByte(value, ';')
		if mediaEnd < 0 {
			mediaEnd = len(value)
		}
		rawMediaType := value[:mediaEnd]
		trimmedMediaType := strings.TrimSpace(rawMediaType)
		mediaStart := strings.Index(rawMediaType, trimmedMediaType)
		mediaEnd = mediaStart + len(trimmedMediaType)
		for searchFrom := 0; searchFrom <= len(value)-len(upstreamModel); {
			index := strings.Index(value[searchFrom:], upstreamModel)
			if index < 0 {
				break
			}
			index += searchFrom
			if index < mediaStart || index+len(upstreamModel) > mediaEnd {
				return true
			}
			searchFrom = index + len(upstreamModel)
		}
	}
	return false
}

func deleteHeaderField(headers http.Header, name string) {
	for actualName := range headers {
		if strings.EqualFold(actualName, name) {
			delete(headers, actualName)
		}
	}
}

func canonicalHeaderIdentity(name string) string {
	if name == "" {
		return ""
	}
	return strings.ToLower(textproto.CanonicalMIMEHeaderKey(name))
}

func isDebugHeader(name string) bool {
	for _, reserved := range debugHeaderNames {
		if strings.EqualFold(name, reserved) {
			return true
		}
	}
	return false
}

func removeDownstreamCredentials(headers http.Header) {
	for name := range headers {
		if platformheader.IsCredentialName(name) {
			delete(headers, name)
		}
	}
}

func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}
