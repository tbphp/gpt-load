package health

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

type classifierFunc func(int, []byte) ErrorClass

func (function classifierFunc) ClassifyStatus(status int, body []byte) ErrorClass {
	return function(status, body)
}

func TestJudgeAppliesRulesInSafetyOrder(t *testing.T) {
	retryableStatus := classifierFunc(func(int, []byte) ErrorClass { return ErrorClassRetryable })
	nonRetryableStatus := classifierFunc(func(int, []byte) ErrorClass { return ErrorClassNonRetryable })
	transportFailure := errors.New("connection failed")

	tests := []struct {
		name       string
		classifier StatusClassifier
		attempt    Attempt
		wantRetry  bool
	}{
		{
			name:       "committed overrides retryable status",
			classifier: retryableStatus,
			attempt:    Attempt{Committed: true, StatusCode: http.StatusUnauthorized},
		},
		{
			name:       "downstream cancellation terminates",
			classifier: retryableStatus,
			attempt:    Attempt{Err: context.Canceled},
		},
		{
			name:       "pre-write connection failure retries",
			classifier: nonRetryableStatus,
			attempt:    Attempt{Err: transportFailure, RequestWritten: false},
			wantRetry:  true,
		},
		{
			name:       "pre-write timeout retries",
			classifier: nonRetryableStatus,
			attempt:    Attempt{Err: context.DeadlineExceeded, RequestWritten: false},
			wantRetry:  true,
		},
		{
			name:       "post-write timeout terminates",
			classifier: retryableStatus,
			attempt:    Attempt{Err: context.DeadlineExceeded, RequestWritten: true},
		},
		{
			name:       "post-write disconnect terminates",
			classifier: retryableStatus,
			attempt:    Attempt{Err: transportFailure, RequestWritten: true},
		},
		{
			name:       "dialect retryable status",
			classifier: retryableStatus,
			attempt:    Attempt{StatusCode: http.StatusTooManyRequests, Body: []byte(`{"error":"rate_limit"}`)},
			wantRetry:  true,
		},
		{
			name:       "dialect non-retryable status",
			classifier: nonRetryableStatus,
			attempt:    Attempt{StatusCode: http.StatusBadRequest, Body: []byte(`{"error":"invalid input"}`)},
		},
		{
			name:    "nil classifier terminates",
			attempt: Attempt{StatusCode: http.StatusInternalServerError},
		},
		{
			name:       "empty attempt terminates",
			classifier: retryableStatus,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Judge(tt.classifier, tt.attempt)
			if got.Retryable != tt.wantRetry {
				t.Fatalf("Judge() = %#v, want Retryable=%t", got, tt.wantRetry)
			}
		})
	}
}
