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

type Attempt struct {
	StatusCode            int
	Body                  []byte
	Header                http.Header
	Now                   time.Time
	Err                   error
	RequestWritten        bool
	Committed             bool
	RetryableBeforeCommit bool
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
	statusRule,
}

func Judge(classifier StatusClassifier, attempt Attempt) Result {
	for _, rule := range judgeRules {
		if result, matched := rule(classifier, attempt); matched {
			return result
		}
	}
	return Result{Action: ActionTerminate}
}

func terminalBoundaryRule(_ StatusClassifier, attempt Attempt) (Result, bool) {
	if attempt.Committed || errors.Is(attempt.Err, context.Canceled) ||
		(attempt.Err != nil && attempt.RequestWritten && !attempt.RetryableBeforeCommit) {
		return Result{Action: ActionTerminate}, true
	}
	return Result{}, false
}

func explicitPreCommitRetryRule(_ StatusClassifier, attempt Attempt) (Result, bool) {
	if attempt.Err == nil || !attempt.RetryableBeforeCommit {
		return Result{}, false
	}
	return Result{Action: ActionRetry}, true
}

func transportRule(_ StatusClassifier, attempt Attempt) (Result, bool) {
	if attempt.Err == nil {
		return Result{}, false
	}
	if !attempt.RequestWritten {
		return Result{Action: ActionSkipGroup}, true
	}
	return Result{Action: ActionTerminate}, true
}

func statusRule(classifier StatusClassifier, attempt Attempt) (Result, bool) {
	if classifier == nil || attempt.StatusCode == 0 {
		return Result{Action: ActionTerminate}, true
	}
	switch classifier.ClassifyStatus(attempt.StatusCode, attempt.Body) {
	case FailureCategoryRateLimited:
		if until, ok := ParseRateLimitReset(attempt.Header, attempt.Now); ok {
			return Result{Action: ActionCooldownKey, CooldownUntil: until}, true
		}
		return Result{Action: ActionCooldownKey, UseFixed: true}, true
	case FailureCategoryModelUnavailable:
		return Result{Action: ActionCooldownKey, CooldownUntil: attempt.Now.Add(time.Hour)}, true
	case FailureCategoryInvalidKey:
		return Result{Action: ActionFailKey}, true
	case FailureCategoryUpstreamHostError:
		return Result{Action: ActionSkipGroup}, true
	default:
		return Result{Action: ActionTerminate}, true
	}
}
