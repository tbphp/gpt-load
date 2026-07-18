package dialect

import (
	"net/http"
	"strings"

	"gpt-load/internal/health"
)

func classifyStatusWithMarkers(status int, body []byte, markers []string) health.ErrorClass {
	switch status {
	case http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusTooManyRequests:
		return health.ErrorClassRetryable
	}
	if status >= http.StatusInternalServerError {
		return health.ErrorClassRetryable
	}
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		return health.ErrorClassNonRetryable
	}

	lowered := strings.ToLower(string(body))
	for _, marker := range markers {
		if strings.Contains(lowered, marker) {
			return health.ErrorClassRetryable
		}
	}
	return health.ErrorClassNonRetryable
}
