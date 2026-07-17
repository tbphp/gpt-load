package health

import (
	"context"
	"errors"
)

type StatusClassifier interface {
	ClassifyStatus(status int, body []byte) ErrorClass
}

type Attempt struct {
	StatusCode     int
	Body           []byte
	Err            error
	RequestWritten bool
	Committed      bool
}

type Verdict struct {
	Retryable bool
}

type Rule func(StatusClassifier, Attempt) (Verdict, bool)

var judgeRules = []Rule{
	terminalBoundaryRule,
	transportRule,
	statusRule,
}

func Judge(classifier StatusClassifier, attempt Attempt) Verdict {
	for _, rule := range judgeRules {
		if verdict, matched := rule(classifier, attempt); matched {
			return verdict
		}
	}
	return Verdict{}
}

func terminalBoundaryRule(_ StatusClassifier, attempt Attempt) (Verdict, bool) {
	if attempt.Committed || errors.Is(attempt.Err, context.Canceled) {
		return Verdict{}, true
	}
	return Verdict{}, false
}

func transportRule(_ StatusClassifier, attempt Attempt) (Verdict, bool) {
	if attempt.Err == nil {
		return Verdict{}, false
	}
	return Verdict{Retryable: !attempt.RequestWritten}, true
}

func statusRule(classifier StatusClassifier, attempt Attempt) (Verdict, bool) {
	if classifier == nil || attempt.StatusCode == 0 {
		return Verdict{}, false
	}
	return Verdict{Retryable: classifier.ClassifyStatus(attempt.StatusCode, attempt.Body).IsRetryable()}, true
}
