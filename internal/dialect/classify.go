package dialect

import (
	"net/http"
	"strings"

	"gpt-load/internal/health"
)

type failureMarkers struct {
	rateLimited      []string
	modelUnavailable []string
	invalidKey       []string
	upstreamHost     []string
}

func classifyStatusWithMarkers(
	status int,
	body []byte,
	markers failureMarkers,
) health.FailureCategory {
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		return health.FailureCategoryOK
	}
	lowered := strings.ToLower(string(body))
	switch {
	case status == http.StatusTooManyRequests || containsFailureMarker(lowered, markers.rateLimited):
		return health.FailureCategoryRateLimited
	case status == http.StatusNotFound || containsFailureMarker(lowered, markers.modelUnavailable):
		return health.FailureCategoryModelUnavailable
	case status == http.StatusUnauthorized || status == http.StatusForbidden ||
		containsFailureMarker(lowered, markers.invalidKey):
		return health.FailureCategoryInvalidKey
	case status >= http.StatusInternalServerError || containsFailureMarker(lowered, markers.upstreamHost):
		return health.FailureCategoryUpstreamHostError
	case status >= http.StatusBadRequest && status < http.StatusInternalServerError:
		return health.FailureCategoryClientError
	default:
		return health.FailureCategoryAmbiguous
	}
}

func containsFailureMarker(body string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}
