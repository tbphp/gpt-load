package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestWriteReasonUsesStableDataPlaneEnvelope(t *testing.T) {
	tests := []reason{
		reasonInvalidAccessKey,
		reasonEndpointNotFound,
		reasonInvalidProtocolRequest,
		reasonModelRequiredByFilter,
		reasonNoCandidate,
		reasonUpstreamConnect,
		reasonUpstreamTimeout,
		reasonUpstreamProtocol,
		reasonRequestTooLarge,
		reasonUnsupportedContentEncoding,
		reasonInvalidContentEncoding,
		reasonNotAcceptable,
		reasonModelListTooLarge,
		reasonAccessKeyRateLimited,
	}
	gin.SetMode(gin.TestMode)
	for _, want := range tests {
		t.Run(want.Code, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			handler := &Handler{writeTimeout: downstreamWriteTimeout}
			if err := handler.writeReason(context, want); err != nil {
				t.Fatalf("writeReason() error = %v", err)
			}

			if recorder.Code != want.Status {
				t.Fatalf("status = %d, want %d", recorder.Code, want.Status)
			}
			var got struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Data    any    `json:"data"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if got.Code != want.Code || got.Message != want.Message || got.Data != nil {
				t.Fatalf("response = %#v, want code/message and no data", got)
			}
		})
	}
}

func TestContentCodingReasonsUseStableContract(t *testing.T) {
	tests := []struct {
		got  reason
		want reason
	}{
		{
			got: reasonUnsupportedContentEncoding,
			want: reason{
				Status:  http.StatusUnsupportedMediaType,
				Code:    "unsupported_content_encoding",
				Message: "Unsupported Content-Encoding.",
			},
		},
		{
			got: reasonInvalidContentEncoding,
			want: reason{
				Status:  http.StatusBadRequest,
				Code:    "invalid_content_encoding",
				Message: "Invalid encoded request body.",
			},
		},
		{
			got: reasonNotAcceptable,
			want: reason{
				Status:  http.StatusNotAcceptable,
				Code:    "not_acceptable",
				Message: "The gateway can only return an identity-encoded response.",
			},
		},
	}
	for _, test := range tests {
		if test.got != test.want {
			t.Errorf("reason %q = %#v, want %#v", test.want.Code, test.got, test.want)
		}
	}
}

func TestUpstreamProtocolReasonCoversNonStreamingResponses(t *testing.T) {
	const want = "Upstream returned an unsupported response."
	if reasonUpstreamProtocol.Message != want {
		t.Fatalf("reasonUpstreamProtocol.Message = %q, want %q", reasonUpstreamProtocol.Message, want)
	}
}
