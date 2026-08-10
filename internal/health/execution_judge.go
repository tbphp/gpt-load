package health

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"gpt-load/internal/execution"
)

// ExecutionAttempt is the provider-neutral evidence used for one health and
// retry decision after an executor call.
type ExecutionAttempt struct {
	DispatchState       execution.DispatchState
	StatusCode          int
	Header              http.Header
	Evidence            *execution.ErrorEvidence
	DownstreamCommitted bool
	DownstreamErr       error
	Now                 time.Time
}

// JudgeExecution decides the GPT-Load action for one executor result.
func JudgeExecution(attempt ExecutionAttempt) Result {
	if errors.Is(attempt.DownstreamErr, context.Canceled) ||
		errors.Is(attempt.DownstreamErr, context.DeadlineExceeded) {
		return Result{Category: FailureCategoryDownstreamCancel, Action: ActionTerminate}
	}
	if attempt.DownstreamCommitted {
		if attempt.DownstreamErr == nil && attempt.Evidence == nil && isSuccessStatus(attempt.StatusCode) {
			return Result{Category: FailureCategoryOK, Action: ActionTerminate}
		}
		return Result{Category: FailureCategoryAmbiguous, Action: ActionTerminate}
	}
	if attempt.DownstreamErr != nil {
		return Result{Category: FailureCategoryAmbiguous, Action: ActionTerminate}
	}

	if attempt.Evidence == nil {
		if attempt.DispatchState.Valid() && isSuccessStatus(attempt.StatusCode) {
			return Result{Category: FailureCategoryOK, Action: ActionTerminate}
		}
		return Result{Category: FailureCategoryAmbiguous, Action: ActionTerminate}
	}

	if attempt.Evidence.Kind == execution.ErrorKindCanceled {
		return Result{Category: FailureCategoryDownstreamCancel, Action: ActionTerminate}
	}
	if attempt.DispatchState == execution.DispatchNotSent {
		switch attempt.Evidence.Kind {
		case execution.ErrorKindTransport, execution.ErrorKindTimeout:
			return Result{Category: FailureCategoryUpstreamHostError, Action: ActionSkipGroup}
		case execution.ErrorKindInvalidRequest:
			return Result{Category: FailureCategoryClientError, Action: ActionTerminate}
		default:
			return Result{Category: FailureCategoryAmbiguous, Action: ActionTerminate}
		}
	}
	if attempt.DispatchState != execution.DispatchMaybeSent {
		return Result{Category: FailureCategoryAmbiguous, Action: ActionTerminate}
	}
	if !attempt.ResponseStarted() &&
		(attempt.Evidence.Kind == execution.ErrorKindTransport || attempt.Evidence.Kind == execution.ErrorKindTimeout) {
		return Result{Category: FailureCategoryAmbiguous, Action: ActionTerminate}
	}

	category := classifyExecutionEvidence(attempt)
	return resultForExecutionCategory(category, attempt)
}

func (attempt ExecutionAttempt) ResponseStarted() bool {
	return attempt.StatusCode != 0 || attempt.Evidence != nil && attempt.Evidence.StatusCode != 0
}

func classifyExecutionEvidence(attempt ExecutionAttempt) FailureCategory {
	statusCode := attempt.StatusCode
	if statusCode == 0 && attempt.Evidence != nil {
		statusCode = attempt.Evidence.StatusCode
	}
	markers := ""
	if attempt.Evidence != nil {
		markers = strings.ToLower(strings.Join([]string{
			attempt.Evidence.Type,
			attempt.Evidence.Code,
			attempt.Evidence.Summary,
		}, " "))
	}

	switch {
	case statusCode == http.StatusTooManyRequests || containsAny(markers,
		"rate_limit", "rate limit", "too_many_requests", "quota_exceeded",
		"resource_exhausted", "throttl"):
		return FailureCategoryRateLimited
	case statusCode == http.StatusUnauthorized ||
		statusCode == http.StatusPaymentRequired ||
		statusCode == http.StatusForbidden:
		return FailureCategoryInvalidKey
	case containsAny(markers,
		"model_not_found", "model not found", "model_not_available",
		"model unavailable", "deployment_not_found", "unsupported_model"):
		return FailureCategoryModelUnavailable
	case statusCode >= http.StatusInternalServerError && statusCode <= 599:
		return FailureCategoryUpstreamHostError
	case statusCode >= http.StatusBadRequest && statusCode <= 499:
		return FailureCategoryClientError
	case attempt.Evidence != nil && attempt.Evidence.Kind == execution.ErrorKindInvalidRequest:
		return FailureCategoryClientError
	default:
		return FailureCategoryAmbiguous
	}
}

func resultForExecutionCategory(category FailureCategory, attempt ExecutionAttempt) Result {
	switch category {
	case FailureCategoryRateLimited:
		if attempt.Evidence != nil && attempt.Evidence.RetryAfter > 0 {
			return Result{
				Category:      category,
				Action:        ActionCooldownCredential,
				CooldownUntil: attempt.Now.Add(attempt.Evidence.RetryAfter),
			}
		}
		header := attempt.Header
		if len(header) == 0 && attempt.Evidence != nil {
			header = attempt.Evidence.Header
		}
		if until, ok := ParseRateLimitReset(header, attempt.Now); ok {
			return Result{Category: category, Action: ActionCooldownCredential, CooldownUntil: until}
		}
		return Result{Category: category, Action: ActionCooldownCredential, UseFixed: true}
	case FailureCategoryModelUnavailable:
		return Result{
			Category:      category,
			Action:        ActionCooldownCredential,
			CooldownUntil: attempt.Now.Add(time.Hour),
		}
	case FailureCategoryInvalidKey:
		return Result{Category: category, Action: ActionFailCredential}
	case FailureCategoryUpstreamHostError:
		return Result{Category: category, Action: ActionSkipGroup}
	default:
		return Result{Category: category, Action: ActionTerminate}
	}
}

func isSuccessStatus(statusCode int) bool {
	return statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices
}

func containsAny(value string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
