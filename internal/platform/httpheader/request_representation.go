package httpheader

import (
	"net/http"
	"strings"
)

var requestRepresentationMetadataNames = [...]string{
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

func NormalizeUpstreamRequestRepresentation(request *http.Request, finalBodyLength int64) {
	if request == nil {
		return
	}
	if request.Header == nil {
		request.Header = make(http.Header)
	} else {
		request.Header = request.Header.Clone()
	}
	StripRequestRepresentationMetadata(request.Header)
	deleteField(request.Header, "Accept-Encoding")
	request.Header.Set("Accept-Encoding", "identity")
	request.ContentLength = finalBodyLength
}

// StripRequestRepresentationMetadata removes headers bound to the original
// HTTP request representation, regardless of their field-name casing.
func StripRequestRepresentationMetadata(headers http.Header) {
	if headers == nil {
		return
	}
	for _, name := range requestRepresentationMetadataNames {
		deleteField(headers, name)
	}
}

func deleteField(headers http.Header, target string) {
	for name := range headers {
		if strings.EqualFold(name, target) {
			delete(headers, name)
		}
	}
}
