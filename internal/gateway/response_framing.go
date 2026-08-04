package gateway

import (
	"net/http"
	"strconv"
	"strings"
)

type responseBodyPolicy struct {
	readBody              bool
	writeBody             bool
	preserveContentLength bool
}

func classifyResponseBody(method string, status int) responseBodyPolicy {
	if status >= http.StatusContinue && status < http.StatusOK {
		return responseBodyPolicy{}
	}
	switch status {
	case http.StatusNoContent, http.StatusResetContent:
		return responseBodyPolicy{}
	case http.StatusNotModified:
		return responseBodyPolicy{preserveContentLength: true}
	}
	if strings.EqualFold(method, http.MethodHead) {
		return responseBodyPolicy{preserveContentLength: true}
	}
	return responseBodyPolicy{readBody: true, writeBody: true}
}

func normalizeBufferedResponse(
	method string,
	status int,
	headers http.Header,
	body []byte,
) (http.Header, bool) {
	normalized := cloneEndToEndHeaders(headers)
	policy := classifyResponseBody(method, status)
	representationHeadersChanged := len(headerFieldValues(normalized, "Content-Encoding")) > 0
	deleteHeaderField(normalized, "Content-Encoding")

	switch {
	case policy.writeBody:
		representationHeadersChanged = representationHeadersChanged ||
			!hasSingleHeaderFieldValue(normalized, "Content-Length", strconv.Itoa(len(body)))
		deleteHeaderField(normalized, "Content-Length")
		normalized.Set("Content-Length", strconv.Itoa(len(body)))
	case status == http.StatusResetContent:
		if contentLength, ok := singleNonNegativeContentLength(normalized); ok && contentLength == "0" {
			deleteHeaderField(normalized, "Content-Length")
			normalized.Set("Content-Length", "0")
		} else {
			representationHeadersChanged = representationHeadersChanged ||
				len(headerFieldValues(normalized, "Content-Length")) > 0
			deleteHeaderField(normalized, "Content-Length")
		}
	case policy.preserveContentLength:
		if strings.EqualFold(method, http.MethodHead) && status != http.StatusNotModified && body != nil {
			representationHeadersChanged = representationHeadersChanged ||
				!hasSingleHeaderFieldValue(normalized, "Content-Length", strconv.Itoa(len(body)))
			deleteHeaderField(normalized, "Content-Length")
			normalized.Set("Content-Length", strconv.Itoa(len(body)))
			break
		}
		if contentLength, ok := singleNonNegativeContentLength(normalized); ok {
			deleteHeaderField(normalized, "Content-Length")
			normalized.Set("Content-Length", contentLength)
		} else {
			representationHeadersChanged = representationHeadersChanged ||
				len(headerFieldValues(normalized, "Content-Length")) > 0
			deleteHeaderField(normalized, "Content-Length")
		}
	default:
		representationHeadersChanged = representationHeadersChanged ||
			len(headerFieldValues(normalized, "Content-Length")) > 0
		deleteHeaderField(normalized, "Content-Length")
	}
	normalizeBufferedContentRange(status, normalized)
	if representationHeadersChanged {
		deleteHeaderField(normalized, "Signature")
		deleteHeaderField(normalized, "Signature-Input")
	}
	stripDownstreamResponseSignatures(normalized)

	return normalized, policy.writeBody
}

func normalizeBufferedContentRange(status int, headers http.Header) {
	switch status {
	case http.StatusPartialContent:
		return
	case http.StatusRequestedRangeNotSatisfiable:
		if hasSafeUnsatisfiedContentRange(headers) {
			return
		}
	}
	deleteHeaderField(headers, "Content-Range")
}

func hasSafeUnsatisfiedContentRange(headers http.Header) bool {
	values := headerFieldValues(headers, "Content-Range")
	if len(values) != 1 {
		return false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bytes") {
		return false
	}
	rangeParts := strings.Split(parts[1], "/")
	if len(rangeParts) != 2 || rangeParts[0] != "*" || rangeParts[1] == "" {
		return false
	}
	for _, character := range rangeParts[1] {
		if character < '0' || character > '9' {
			return false
		}
	}
	_, err := strconv.ParseUint(rangeParts[1], 10, 63)
	return err == nil
}

func singleNonNegativeContentLength(headers http.Header) (string, bool) {
	values := headerFieldValues(headers, "Content-Length")
	if len(values) != 1 || values[0] == "" {
		return "", false
	}
	for _, character := range values[0] {
		if character < '0' || character > '9' {
			return "", false
		}
	}
	value, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || value < 0 {
		return "", false
	}
	return values[0], true
}
