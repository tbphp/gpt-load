package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/contentcoding"
	"gpt-load/internal/platform/redact"
)

type preparedSuccessRepresentation struct {
	headers          http.Header
	downstream       []byte
	inspectable      []byte
	modelObservation responseModelObservation
	changed          bool
}

type preparedErrorRepresentation struct {
	headers        http.Header
	downstream     []byte
	classification []byte
	changed        bool
}

type responseProcessor struct {
	redactor *redact.Redactor
}

func (forwarder *responseProcessor) prepareSuccessRepresentation(
	input ForwardInput,
	status int,
	headers http.Header,
	wire []byte,
	secrets []string,
) (preparedSuccessRepresentation, error) {
	if int64(len(wire)) > maxNonStreamingResponseBodyBytes {
		return preparedSuccessRepresentation{}, successRepresentationProtocolError("wire body exceeds limit")
	}

	encoding, err := contentcoding.ParseContentEncoding(
		headerFieldValues(headers, "Content-Encoding"),
	)
	if err != nil {
		return preparedSuccessRepresentation{}, successRepresentationProtocolError("unsupported or malformed Content-Encoding")
	}
	originalPlain, err := contentcoding.DecodeLimited(
		encoding,
		wire,
		maxNonStreamingResponseBodyBytes,
	)
	if err != nil {
		return preparedSuccessRepresentation{}, successRepresentationProtocolError("decompress response body")
	}
	modelTracker := newResponseModelTracker(input.Dialect, input.UpstreamModelID)
	modelTracker.observe(originalPlain)

	patternSafePlain := forwarder.redactor.Bytes(originalPlain)
	if int64(len(patternSafePlain)) > maxNonStreamingResponseBodyBytes {
		return preparedSuccessRepresentation{}, successRepresentationProtocolError("redacted response body exceeds limit")
	}
	safePlain, residualCredential, ok := redactCredentialLiterals(
		patternSafePlain,
		secrets,
		maxNonStreamingResponseBodyBytes,
	)
	if !ok {
		return preparedSuccessRepresentation{}, successRepresentationProtocolError("redact response body")
	}
	if residualCredential {
		return preparedSuccessRepresentation{}, successRepresentationProtocolError("credential remains in response body")
	}

	inspectablePlain := bytes.Clone(safePlain)
	downstreamPlain := safePlain
	if needsModelRewrite(input) {
		rewriter, supported := input.Dialect.(dialect.ModelRewriter)
		if !supported {
			return preparedSuccessRepresentation{}, successRepresentationProtocolError("dialect does not support model rewrite")
		}
		downstreamPlain, err = rewriter.RewriteResponseModel(safePlain, input.ExternalModel)
		if err != nil {
			return preparedSuccessRepresentation{}, successRepresentationProtocolError("rewrite response model")
		}
		if int64(len(downstreamPlain)) > maxNonStreamingResponseBodyBytes {
			return preparedSuccessRepresentation{}, successRepresentationProtocolError("rewritten response body exceeds limit")
		}
		if credentialLiteralsRemain(downstreamPlain, secrets) {
			return preparedSuccessRepresentation{}, successRepresentationProtocolError("credential remains after model rewrite")
		}
	}

	// The execution adapter may already have projected the selected upstream
	// model back to the client alias. Conservatively treat every aliased route
	// as a representation rewrite so upstream validators/digests are never
	// forwarded for bytes whose identity changed before this boundary.
	changed := encoding != contentcoding.Identity || needsModelRewrite(input) ||
		!bytes.Equal(originalPlain, downstreamPlain)
	if status == http.StatusPartialContent && changed {
		return preparedSuccessRepresentation{}, successRepresentationProtocolError("changed partial response")
	}

	preparedHeaders := headers.Clone()
	if preparedHeaders == nil {
		preparedHeaders = make(http.Header)
	}
	representationHeadersChanged := len(headerFieldValues(preparedHeaders, "Content-Encoding")) > 0 ||
		!hasSingleHeaderFieldValue(
			preparedHeaders,
			"Content-Length",
			strconv.Itoa(len(downstreamPlain)),
		)
	deleteHeaderField(preparedHeaders, "Content-Encoding")
	if changed {
		invalidateRewrittenBodyHeaders(preparedHeaders)
	}
	beforeSanitize := preparedHeaders.Clone()
	preparedHeaders = sanitizeForwardResponseHeaders(preparedHeaders, input, secrets...)
	representationHeadersChanged = representationHeadersChanged ||
		!reflect.DeepEqual(beforeSanitize, preparedHeaders)
	deleteHeaderField(preparedHeaders, "Content-Encoding")
	deleteHeaderField(preparedHeaders, "Content-Length")
	preparedHeaders.Set("Content-Length", strconv.Itoa(len(downstreamPlain)))
	if representationHeadersChanged {
		deleteHeaderField(preparedHeaders, "Signature")
		deleteHeaderField(preparedHeaders, "Signature-Input")
	}
	if representationMetadataContainsSecrets(preparedHeaders, secrets) {
		return preparedSuccessRepresentation{}, successRepresentationProtocolError("credential collision in rebuilt representation metadata")
	}
	if status == http.StatusPartialContent && !hasSafeContentRange(preparedHeaders, len(downstreamPlain)) {
		return preparedSuccessRepresentation{}, successRepresentationProtocolError("partial response lacks safe Content-Range")
	}

	return preparedSuccessRepresentation{
		headers:          preparedHeaders,
		downstream:       bytes.Clone(downstreamPlain),
		inspectable:      inspectablePlain,
		modelObservation: modelTracker.observation(),
		changed:          changed,
	}, nil
}

func (forwarder *responseProcessor) prepareErrorRepresentation(
	input ForwardInput,
	headers http.Header,
	wire []byte,
	secrets []string,
) preparedErrorRepresentation {
	if int64(len(wire)) > maxErrorResponseBodyBytes {
		return forwarder.failClosedErrorRepresentation(input, headers, secrets)
	}

	encoding, err := contentcoding.ParseContentEncoding(
		headerFieldValues(headers, "Content-Encoding"),
	)
	if err != nil {
		return forwarder.failClosedErrorRepresentation(input, headers, secrets)
	}
	originalPlain, err := contentcoding.DecodeLimited(
		encoding,
		wire,
		maxDecompressedErrorBodyBytes,
	)
	if err != nil {
		return forwarder.failClosedErrorRepresentation(input, headers, secrets)
	}

	patternSafePlain := forwarder.redactor.Bytes(originalPlain)
	if int64(len(patternSafePlain)) > maxDecompressedErrorBodyBytes {
		return forwarder.failClosedErrorRepresentation(input, headers, secrets)
	}
	safePlain, residualCredential, ok := redactCredentialLiterals(
		patternSafePlain,
		secrets,
		maxDecompressedErrorBodyBytes,
	)
	if !ok || residualCredential {
		return forwarder.failClosedErrorRepresentation(input, headers, secrets)
	}

	classificationPlain := bytes.Clone(safePlain)
	downstreamPlain := safePlain
	if needsModelRewrite(input) {
		downstreamPlain, ok = rewriteBoundedLiteral(
			safePlain,
			input.UpstreamModelID,
			input.ExternalModel,
			maxDecompressedErrorBodyBytes,
		)
		if !ok || credentialLiteralsRemain(downstreamPlain, secrets) {
			return forwarder.failClosedErrorRepresentation(input, headers, secrets)
		}
	}

	changed := encoding != contentcoding.Identity ||
		!bytes.Equal(originalPlain, downstreamPlain)
	preparedHeaders := headers.Clone()
	if preparedHeaders == nil {
		preparedHeaders = make(http.Header)
	}
	representationHeadersChanged := len(headerFieldValues(preparedHeaders, "Content-Encoding")) > 0 ||
		!hasSingleHeaderFieldValue(
			preparedHeaders,
			"Content-Length",
			strconv.Itoa(len(downstreamPlain)),
		)
	deleteHeaderField(preparedHeaders, "Content-Encoding")
	if changed {
		invalidateRewrittenBodyHeaders(preparedHeaders)
	}
	beforeSanitize := preparedHeaders.Clone()
	preparedHeaders = sanitizeForwardResponseHeaders(preparedHeaders, input, secrets...)
	representationHeadersChanged = representationHeadersChanged ||
		!reflect.DeepEqual(beforeSanitize, preparedHeaders)
	deleteHeaderField(preparedHeaders, "Content-Encoding")
	deleteHeaderField(preparedHeaders, "Content-Length")
	preparedHeaders.Set("Content-Length", strconv.Itoa(len(downstreamPlain)))
	if representationHeadersChanged {
		deleteHeaderField(preparedHeaders, "Signature")
		deleteHeaderField(preparedHeaders, "Signature-Input")
	}
	if representationMetadataContainsSecrets(preparedHeaders, secrets) {
		return forwarder.failClosedErrorRepresentation(input, headers, secrets)
	}

	return preparedErrorRepresentation{
		headers:        preparedHeaders,
		downstream:     bytes.Clone(downstreamPlain),
		classification: classificationPlain,
		changed:        changed,
	}
}

func (forwarder *responseProcessor) failClosedErrorRepresentation(
	input ForwardInput,
	headers http.Header,
	secrets []string,
) preparedErrorRepresentation {
	preparedHeaders := headers.Clone()
	if preparedHeaders == nil {
		preparedHeaders = make(http.Header)
	}
	deleteHeaderField(preparedHeaders, "Content-Encoding")
	invalidateRewrittenBodyHeaders(preparedHeaders)
	preparedHeaders = sanitizeForwardResponseHeaders(preparedHeaders, input, secrets...)
	deleteHeaderField(preparedHeaders, "Content-Encoding")
	invalidateRewrittenBodyHeaders(preparedHeaders)
	body := []byte(redact.Placeholder)
	preparedHeaders.Set("Content-Type", "text/plain; charset=utf-8")
	preparedHeaders.Set("Content-Length", strconv.Itoa(len(body)))
	return preparedErrorRepresentation{
		headers:        preparedHeaders,
		downstream:     bytes.Clone(body),
		classification: bytes.Clone(body),
		changed:        true,
	}
}

func hasSingleHeaderFieldValue(headers http.Header, name, want string) bool {
	values := headerFieldValues(headers, name)
	return len(values) == 1 && values[0] == want
}

func hasSafeContentRange(headers http.Header, bodyLength int) bool {
	if bodyLength <= 0 {
		return false
	}
	rangeValue, ok := parseSatisfiedContentRange(headers)
	if !ok {
		return false
	}
	return rangeValue.end-rangeValue.start == int64(bodyLength)-1
}

func hasSafeHeadContentRange(headers http.Header) bool {
	rangeValue, ok := parseSatisfiedContentRange(headers)
	if !ok {
		return false
	}
	values := headerFieldValues(headers, "Content-Length")
	if len(values) == 0 {
		return true
	}
	contentLength, ok := singleNonNegativeContentLength(headers)
	if !ok {
		return false
	}
	length, err := strconv.ParseInt(contentLength, 10, 64)
	if err != nil || length <= 0 {
		return false
	}
	return rangeValue.end-rangeValue.start == length-1
}

type satisfiedContentRange struct {
	start int64
	end   int64
}

func parseSatisfiedContentRange(headers http.Header) (satisfiedContentRange, bool) {
	values := headerFieldValues(headers, "Content-Range")
	if len(values) != 1 {
		return satisfiedContentRange{}, false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bytes") {
		return satisfiedContentRange{}, false
	}
	rangeParts := strings.Split(parts[1], "/")
	if len(rangeParts) != 2 {
		return satisfiedContentRange{}, false
	}
	bounds := strings.Split(rangeParts[0], "-")
	if len(bounds) != 2 {
		return satisfiedContentRange{}, false
	}
	if !isDecimalContentRangeNumber(bounds[0]) || !isDecimalContentRangeNumber(bounds[1]) {
		return satisfiedContentRange{}, false
	}
	start, startErr := strconv.ParseInt(bounds[0], 10, 64)
	end, endErr := strconv.ParseInt(bounds[1], 10, 64)
	if startErr != nil || endErr != nil || start < 0 || end < start {
		return satisfiedContentRange{}, false
	}
	if rangeParts[1] == "*" {
		return satisfiedContentRange{start: start, end: end}, true
	}
	if !isDecimalContentRangeNumber(rangeParts[1]) {
		return satisfiedContentRange{}, false
	}
	total, totalErr := strconv.ParseInt(rangeParts[1], 10, 64)
	if totalErr != nil || total <= end {
		return satisfiedContentRange{}, false
	}
	return satisfiedContentRange{start: start, end: end}, true
}

func isDecimalContentRangeNumber(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func representationMetadataContainsSecrets(headers http.Header, secrets []string) bool {
	for name, values := range headers {
		if !isRepresentationMetadataHeader(name) {
			continue
		}
		for _, secret := range secrets {
			if headerValuesContainLiteral(values, secret) {
				return true
			}
		}
	}
	return false
}

func successRepresentationProtocolError(stage string) error {
	return fmt.Errorf("%w: invalid success response representation at %s", ErrUpstreamProtocol, stage)
}

type credentialLiteralReplacers struct {
	redact   *strings.Replacer
	residual *strings.Replacer
}

func newCredentialLiteralReplacers(secrets []string) (credentialLiteralReplacers, bool) {
	sorted := append([]string(nil), secrets...)
	sort.SliceStable(sorted, func(left, right int) bool {
		return len(sorted[left]) > len(sorted[right])
	})

	redactionPairs := make([]string, 0, len(sorted)*2)
	residualPairs := make([]string, 0, len(sorted)*2)
	seen := make(map[string]struct{}, len(sorted))
	for _, secret := range sorted {
		if secret == "" {
			continue
		}
		if _, exists := seen[secret]; exists {
			continue
		}
		seen[secret] = struct{}{}
		redactionPairs = append(redactionPairs, secret, redact.Placeholder)
		residualPairs = append(residualPairs, secret, "")
	}
	if len(redactionPairs) == 0 {
		return credentialLiteralReplacers{}, false
	}
	return credentialLiteralReplacers{
		redact:   strings.NewReplacer(redactionPairs...),
		residual: strings.NewReplacer(residualPairs...),
	}, true
}

func redactCredentialLiterals(
	body []byte,
	secrets []string,
	limit int64,
) ([]byte, bool, bool) {
	replacers, exists := newCredentialLiteralReplacers(secrets)
	if !exists {
		return bytes.Clone(body), false, int64(len(body)) <= limit
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return redactNonJSONCredentialLiterals(body, replacers, limit)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return redactNonJSONCredentialLiterals(body, replacers, limit)
	}

	rewritten, changed, residual, ok := rewriteJSONCredentialLiterals(value, replacers, limit)
	if !ok || residual {
		return nil, residual, ok
	}
	if !changed {
		return bytes.Clone(body), false, true
	}
	encoded, err := json.Marshal(rewritten)
	if err != nil || int64(len(encoded)) > limit {
		return nil, false, false
	}
	return encoded, false, true
}

func redactNonJSONCredentialLiterals(
	body []byte,
	replacers credentialLiteralReplacers,
	limit int64,
) ([]byte, bool, bool) {
	rewritten, ok := replaceCredentialStringBounded(string(body), replacers.redact, limit)
	if !ok {
		return nil, false, false
	}
	if credentialLiteralRemains(rewritten, replacers.residual) {
		return nil, true, true
	}
	return []byte(rewritten), false, true
}

func rewriteJSONCredentialLiterals(
	value any,
	replacers credentialLiteralReplacers,
	limit int64,
) (any, bool, bool, bool) {
	switch typed := value.(type) {
	case string:
		rewritten, ok := replaceCredentialStringBounded(typed, replacers.redact, limit)
		if !ok {
			return nil, false, false, false
		}
		if credentialLiteralRemains(rewritten, replacers.residual) {
			return nil, false, true, true
		}
		return rewritten, rewritten != typed, false, true
	case json.Number:
		if credentialLiteralRemains(typed.String(), replacers.residual) {
			return nil, false, true, true
		}
		return value, false, false, true
	case bool:
		if credentialLiteralRemains(fmt.Sprintf("%t", typed), replacers.residual) {
			return nil, false, true, true
		}
		return value, false, false, true
	case nil:
		if credentialLiteralRemains("null", replacers.residual) {
			return nil, false, true, true
		}
		return nil, false, false, true
	case []any:
		changed := false
		for index, item := range typed {
			rewritten, itemChanged, residual, ok := rewriteJSONCredentialLiterals(
				item,
				replacers,
				limit,
			)
			if !ok || residual {
				return nil, false, residual, ok
			}
			typed[index] = rewritten
			changed = changed || itemChanged
		}
		return typed, changed, false, true
	case map[string]any:
		rewrittenObject := make(map[string]any, len(typed))
		changed := false
		for key, item := range typed {
			rewrittenKey, ok := replaceCredentialStringBounded(key, replacers.redact, limit)
			if !ok {
				return nil, false, false, false
			}
			if credentialLiteralRemains(rewrittenKey, replacers.residual) {
				return nil, false, true, true
			}
			if _, exists := rewrittenObject[rewrittenKey]; exists {
				return nil, false, false, false
			}
			rewrittenItem, itemChanged, residual, ok := rewriteJSONCredentialLiterals(
				item,
				replacers,
				limit,
			)
			if !ok || residual {
				return nil, false, residual, ok
			}
			rewrittenObject[rewrittenKey] = rewrittenItem
			changed = changed || rewrittenKey != key || itemChanged
		}
		return rewrittenObject, changed, false, true
	default:
		return nil, false, false, false
	}
}

func replaceCredentialStringBounded(
	source string,
	replacer *strings.Replacer,
	limit int64,
) (string, bool) {
	writer := &boundedCredentialBuffer{limit: limit}
	if _, err := replacer.WriteString(writer, source); err != nil {
		return "", false
	}
	return writer.buffer.String(), true
}

func credentialLiteralRemains(source string, residual *strings.Replacer) bool {
	return residual.Replace(source) != source
}

func credentialLiteralsRemain(body []byte, secrets []string) bool {
	replacers, exists := newCredentialLiteralReplacers(secrets)
	if !exists {
		return false
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return credentialLiteralRemains(string(body), replacers.residual)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return credentialLiteralRemains(string(body), replacers.residual)
	}
	return jsonCredentialLiteralsRemain(value, replacers.residual)
}

func jsonCredentialLiteralsRemain(value any, residual *strings.Replacer) bool {
	switch typed := value.(type) {
	case string:
		return credentialLiteralRemains(typed, residual)
	case json.Number:
		return credentialLiteralRemains(typed.String(), residual)
	case bool:
		return credentialLiteralRemains(fmt.Sprintf("%t", typed), residual)
	case nil:
		return credentialLiteralRemains("null", residual)
	case []any:
		for _, item := range typed {
			if jsonCredentialLiteralsRemain(item, residual) {
				return true
			}
		}
		return false
	case map[string]any:
		for key, item := range typed {
			if credentialLiteralRemains(key, residual) ||
				jsonCredentialLiteralsRemain(item, residual) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

type boundedCredentialBuffer struct {
	buffer bytes.Buffer
	limit  int64
}

func (writer *boundedCredentialBuffer) Write(value []byte) (int, error) {
	if writer == nil || writer.limit < 0 ||
		int64(writer.buffer.Len()) > writer.limit-int64(len(value)) {
		return 0, fmt.Errorf("credential replacement exceeds response limit")
	}
	return writer.buffer.Write(value)
}
