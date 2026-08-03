package gateway

import (
	"net/http"
	"testing"
)

func TestContentCodingReasonsRemainStable(t *testing.T) {
	tests := []struct {
		name   string
		reason reason
		status int
		code   string
	}{
		{name: "invalid encoding", reason: reasonInvalidContentEncoding, status: http.StatusBadRequest, code: "invalid_content_encoding"},
		{name: "unsupported encoding", reason: reasonUnsupportedContentEncoding, status: http.StatusUnsupportedMediaType, code: "unsupported_content_encoding"},
		{name: "identity rejected", reason: reasonNotAcceptable, status: http.StatusNotAcceptable, code: "not_acceptable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.reason.Status != test.status || test.reason.Code != test.code {
				t.Fatalf("reason = %#v, want status/code %d/%q", test.reason, test.status, test.code)
			}
		})
	}
}
