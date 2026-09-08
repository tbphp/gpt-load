package bifrost

import (
	"net/http"
	"testing"

	"gpt-load/internal/execution"
)

func TestCandidateCapabilityRejectionsAreNarrow(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   execution.FailureHint
	}{
		{name: "operation", status: 400, body: `{"error":{"type":"invalid_request_error","code":"unsupported_operation"}}`, want: execution.FailureHintModelUnavailable},
		{name: "operation alias", status: 400, body: `{"error":{"code":"operation_not_supported"}}`, want: execution.FailureHintModelUnavailable},
		{name: "unsupported model legacy spelling", status: 400, body: `{"error":{"type":"invalid_request_error","code":"unsupported-model"}}`, want: execution.FailureHintModelUnavailable},
		{name: "known function calling rejection", status: 400, body: `{"error":{"type":"invalid_request_error","message":"function calling is not supported with this model"}}`, want: execution.FailureHintModelUnavailable},
		{name: "context limit", status: 400, body: `{"error":{"type":"invalid_request_error","code":"context_length_exceeded"}}`, want: execution.FailureHintRequestRejected},
		{name: "generic invalid request", status: 400, body: `{"error":{"type":"invalid_request_error","message":"invalid request"}}`, want: execution.FailureHintRequestRejected},
		{name: "quoted message is not rejection evidence", status: 400, body: `{"error":{"type":"invalid_request_error","message":"invalid input: function calling is not supported with this model"}}`, want: execution.FailureHintRequestRejected},
		{name: "explicit invalid parameter wins", status: 400, body: `{"error":{"type":"invalid_request_error","code":"invalid_parameter","message":"function calling is not supported with this model"}}`, want: execution.FailureHintRequestRejected},
		{name: "generic forbidden", status: http.StatusForbidden, body: `{"error":{"code":"permission_denied"}}`},
		{name: "resource missing", status: http.StatusNotFound, body: `{"error":{"code":"response_not_found"}}`},
		{name: "unknown server outcome", status: http.StatusBadGateway, body: `{"error":{"code":"unsupported_operation"}}`, want: execution.FailureHintHostError},
		{name: "rate limit remains rate limit", status: http.StatusTooManyRequests, body: `{"error":{"code":"unsupported_operation"}}`, want: execution.FailureHintRateLimited},
	} {
		t.Run(test.name, func(t *testing.T) {
			evidence := passthroughHTTPError(test.status, nil, []byte(test.body), nil)
			if evidence.Hint != test.want {
				t.Fatalf("hint = %q, want %q", evidence.Hint, test.want)
			}
		})
	}
}
