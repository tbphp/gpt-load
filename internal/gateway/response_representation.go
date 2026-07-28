package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/platform/utils"
)

type preparedSuccessRepresentation struct {
	headers http.Header
	wire    []byte
	plain   []byte
	changed bool
}

func (forwarder *Forwarder) prepareSuccessRepresentation(
	input ForwardInput,
	headers http.Header,
	wire []byte,
	secrets []string,
) (preparedSuccessRepresentation, error) {
	if int64(len(wire)) > maxNonStreamingResponseBodyBytes {
		return preparedSuccessRepresentation{}, successRepresentationProtocolError("wire body exceeds limit")
	}

	encoding, ok := inspectableSuccessBodyEncoding(headers, wire)
	if !ok {
		return preparedSuccessRepresentation{}, successRepresentationProtocolError("unsupported or malformed Content-Encoding")
	}
	originalPlain, err := utils.DecompressResponseLimited(
		encoding,
		wire,
		maxNonStreamingResponseBodyBytes,
	)
	if err != nil {
		return preparedSuccessRepresentation{}, successRepresentationProtocolError("decompress response body")
	}

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

	changed := !bytes.Equal(originalPlain, downstreamPlain)
	preparedHeaders := headers.Clone()
	if preparedHeaders == nil {
		preparedHeaders = make(http.Header)
	}
	preparedWire := bytes.Clone(wire)
	if changed {
		preparedWire, err = utils.CompressResponse(encoding, downstreamPlain)
		if err != nil {
			return preparedSuccessRepresentation{}, successRepresentationProtocolError("recompress response body")
		}
		if int64(len(preparedWire)) > maxNonStreamingResponseBodyBytes {
			return preparedSuccessRepresentation{}, successRepresentationProtocolError("recompressed response body exceeds limit")
		}
		updateRewrittenBodyHeaders(preparedHeaders, len(preparedWire))
	}
	if representationMetadataContainsSecrets(preparedHeaders, secrets) {
		return preparedSuccessRepresentation{}, successRepresentationProtocolError("credential collision in rebuilt representation metadata")
	}
	preparedHeaders = sanitizeForwardResponseHeaders(preparedHeaders, input, secrets...)

	return preparedSuccessRepresentation{
		headers: preparedHeaders,
		wire:    preparedWire,
		plain:   inspectablePlain,
		changed: changed,
	}, nil
}

func inspectableSuccessBodyEncoding(headers http.Header, wire []byte) (string, bool) {
	values := headers.Values("Content-Encoding")
	if len(values) > 1 {
		return "", false
	}
	encoding := ""
	if len(values) == 1 {
		encoding = strings.ToLower(strings.TrimSpace(values[0]))
	}
	switch encoding {
	case "", "identity":
		return encoding, true
	case "gzip", "br", "deflate", "zstd":
		return encoding, len(wire) > 0
	default:
		return "", false
	}
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
