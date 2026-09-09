package httpheader

import (
	"net/http"
	"strings"
)

var representationMetadataNames = [...]string{
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
	StripRepresentationMetadata(request.Header)
	deleteField(request.Header, "Accept-Encoding")
	request.Header.Set("Accept-Encoding", "identity")
	request.ContentLength = finalBodyLength
}

// StripRepresentationMetadata 清理绑定原始 HTTP 正文的元数据，适用于请求与响应。
func StripRepresentationMetadata(headers http.Header) {
	if headers == nil {
		return
	}
	for _, name := range representationMetadataNames {
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
