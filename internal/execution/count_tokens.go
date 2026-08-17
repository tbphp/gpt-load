package execution

import (
	"net/http"
	"strings"
)

// UpstreamCountTokensUnsupported reports whether a CountTokens response
// explicitly says that the upstream endpoint or operation is unavailable.
// Callers use this to return the provider error without rotating credentials
// or degrading credential health.
func UpstreamCountTokensUnsupported(
	operation Operation,
	status int,
	typeValue string,
	codeValue string,
) bool {
	if operation != OperationCountTokens && operation != OperationResponsesInputTokens {
		return false
	}
	switch status {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return true
	}
	for _, value := range []string{typeValue, codeValue} {
		normalized := strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToLower(strings.TrimSpace(value)))
		switch normalized {
		case "unsupported_operation", "operation_not_supported", "not_implemented":
			return true
		}
	}
	return false
}
