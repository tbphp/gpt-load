package errors

import (
	"regexp"
	"strconv"
)

// FailureContext carries structured failure information for key health updates.
type FailureContext struct {
	StatusCode   int
	ErrorMessage string
}

// FailureCategory represents the policy bucket for a failed request.
type FailureCategory string

const (
	FailureNone       FailureCategory = "none"
	FailureAuth       FailureCategory = "auth"
	FailurePermission FailureCategory = "permission"
	FailureRateLimit  FailureCategory = "rate_limit"
	FailureGeneral    FailureCategory = "general"
)

var validatorStatusPattern = regexp.MustCompile(`\[status (\d{3})\]`)

// ClassifyFailure resolves the failure category with status-code priority.
func ClassifyFailure(ctx FailureContext) FailureCategory {
	statusCode := ctx.StatusCode
	if statusCode == 0 {
		statusCode = ExtractStatusCode(ctx.ErrorMessage)
	}

	switch statusCode {
	case 401:
		return FailureAuth
	case 403:
		return FailurePermission
	case 429:
		return FailureRateLimit
	}

	if IsUnCounted(ctx.ErrorMessage) {
		return FailureNone
	}

	return FailureGeneral
}

// ExtractStatusCode parses validator errors in the format "[status XXX] message".
func ExtractStatusCode(errMsg string) int {
	matches := validatorStatusPattern.FindStringSubmatch(errMsg)
	if len(matches) != 2 {
		return 0
	}

	statusCode, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0
	}

	return statusCode
}
