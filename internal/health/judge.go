package health

import (
	"context"
	"errors"
	"net/http"
	"time"
)

type StatusClassifier interface {
	ClassifyStatus(status int, body []byte) FailureCategory
}

type ProviderErrorClassifier interface {
	ClassifyProviderError(body []byte) FailureCategory
}

type Attempt struct {
	StatusCode                int
	Body                      []byte
	Header                    http.Header
	Now                       time.Time
	Err                       error
	RequestWritten            bool
	Committed                 bool
	RetryableBeforeCommit     bool
	ProviderErrorBeforeCommit bool
}

type Action uint8

const (
	ActionTerminate Action = iota
	ActionRetry
	ActionCooldownKey
	ActionFailKey
	ActionSkipGroup
)

type Result struct {
	Category      FailureCategory
	Action        Action
	CooldownUntil time.Time
	UseFixed      bool
}

func (result Result) ShouldRetry() bool {
	switch result.Action {
	case ActionRetry, ActionCooldownKey, ActionFailKey, ActionSkipGroup:
		return true
	default:
		return false
	}
}

type Rule func(StatusClassifier, Attempt) (Result, bool)

var judgeRules = []Rule{
	terminalBoundaryRule,
	explicitPreCommitRetryRule,
	transportRule,
	providerErrorRule,
	statusRule,
}

func Judge(classifier StatusClassifier, attempt Attempt) Result {
	for _, rule := range judgeRules {
		if result, matched := rule(classifier, attempt); matched {
			return result
		}
	}
	return Result{Category: FailureCategoryAmbiguous, Action: ActionTerminate}
}

func terminalBoundaryRule(_ StatusClassifier, attempt Attempt) (Result, bool) {
	switch {
	case errors.Is(attempt.Err, context.Canceled):
		return Result{Category: FailureCategoryDownstreamCancel, Action: ActionTerminate}, true
	case attempt.Committed && attempt.Err == nil:
		return Result{Category: FailureCategoryOK, Action: ActionTerminate}, true
	case attempt.Committed:
		return Result{Category: FailureCategoryAmbiguous, Action: ActionTerminate}, true
	case attempt.Err != nil && attempt.RequestWritten:
		return Result{Category: FailureCategoryAmbiguous, Action: ActionTerminate}, true
	default:
		return Result{}, false
	}
}

func explicitPreCommitRetryRule(_ StatusClassifier, attempt Attempt) (Result, bool) {
	if attempt.Err == nil || attempt.RequestWritten || !attempt.RetryableBeforeCommit {
		return Result{}, false
	}
	return Result{Category: FailureCategoryAmbiguous, Action: ActionRetry}, true
}

func transportRule(_ StatusClassifier, attempt Attempt) (Result, bool) {
	if attempt.Err == nil {
		return Result{}, false
	}
	if !attempt.RequestWritten {
		return Result{Category: FailureCategoryUpstreamHostError, Action: ActionSkipGroup}, true
	}
	return Result{Category: FailureCategoryAmbiguous, Action: ActionTerminate}, true
}

func providerErrorRule(classifier StatusClassifier, attempt Attempt) (Result, bool) {
	if !attempt.ProviderErrorBeforeCommit {
		return Result{}, false
	}
	providerClassifier, ok := classifier.(ProviderErrorClassifier)
	if !ok {
		return Result{Category: FailureCategoryAmbiguous, Action: ActionTerminate}, true
	}
	return resultForCategory(
		providerClassifier.ClassifyProviderError(attempt.Body),
		attempt,
	), true
}

func statusRule(classifier StatusClassifier, attempt Attempt) (Result, bool) {
	if classifier == nil || attempt.StatusCode == 0 {
		return Result{Category: FailureCategoryAmbiguous, Action: ActionTerminate}, true
	}
	category := classifier.ClassifyStatus(attempt.StatusCode, attempt.Body)
	return resultForCategory(category, attempt), true
}

func resultForCategory(category FailureCategory, attempt Attempt) Result {
	switch category {
	case FailureCategoryRateLimited:
		if until, ok := ParseRateLimitReset(attempt.Header, attempt.Now); ok {
			return Result{Category: category, Action: ActionCooldownKey, CooldownUntil: until}
		}
		return Result{Category: category, Action: ActionCooldownKey, UseFixed: true}
	case FailureCategoryModelUnavailable:
		return Result{Category: category, Action: ActionCooldownKey, CooldownUntil: attempt.Now.Add(time.Hour)}
	case FailureCategoryInvalidKey:
		return Result{Category: category, Action: ActionFailKey}
	case FailureCategoryUpstreamHostError:
		return Result{Category: category, Action: ActionSkipGroup}
	default:
		return Result{Category: category, Action: ActionTerminate}
	}
}
