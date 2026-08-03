package gateway

import (
	"fmt"
	"net/http"
	"strconv"

	"gpt-load/internal/platform/contentcoding"
)

func readDecodedRequestBody(
	request *http.Request,
	encodedLimit int64,
	decodedLimit int64,
) ([]byte, http.Header, error) {
	if request == nil || request.Body == nil {
		return nil, nil, fmt.Errorf("%w: request body is required", contentcoding.ErrInvalidEncoding)
	}
	if request.ContentLength > encodedLimit && encodedLimit >= 0 {
		return nil, nil, contentcoding.ErrEncodedTooLarge
	}
	body, err := contentcoding.ReadDecodedBody(
		request.Body,
		request.Header.Values("Content-Encoding"),
		encodedLimit,
		decodedLimit,
	)
	if err != nil {
		return nil, nil, err
	}
	headers := cloneEndToEndHeaders(request.Header)
	stripRepresentationMetadata(headers)
	headers.Set("Accept-Encoding", "identity")
	return body, headers, nil
}

func stripRepresentationMetadata(headers http.Header) {
	if headers == nil {
		return
	}
	for _, name := range representationMetadataHeaderNames {
		deleteHeaderField(headers, name)
	}
}

func rebuildPlainBufferedResponseHeaders(headers http.Header, bodyLength int) {
	stripRepresentationMetadata(headers)
	headers.Set("Content-Length", strconv.Itoa(bodyLength))
}

func normalizePlainStreamingResponseHeaders(headers http.Header) {
	stripRepresentationMetadata(headers)
}
