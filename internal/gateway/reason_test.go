package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestWriteReasonUsesStableDataPlaneEnvelope(t *testing.T) {
	tests := []reason{
		reasonInvalidAccessKey,
		reasonEndpointNotFound,
		reasonCannotExtractModel,
		reasonNoCandidate,
		reasonUpstreamConnect,
		reasonUpstreamTimeout,
		reasonUpstreamProtocol,
		reasonRequestTooLarge,
	}
	gin.SetMode(gin.TestMode)
	for _, want := range tests {
		t.Run(want.Code, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			writeReason(context, want)

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
