package health

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

type classifierFunc func(int, []byte) FailureCategory

func (function classifierFunc) ClassifyStatus(status int, body []byte) FailureCategory {
	return function(status, body)
}

func fixedCategory(category FailureCategory) StatusClassifier {
	return classifierFunc(func(int, []byte) FailureCategory { return category })
}

func TestJudgeAppliesRulesInSafetyOrder(t *testing.T) {
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		classifier StatusClassifier
		attempt    Attempt
		want       Result
	}{
		{
			name: "committed overrides rate limit",
			classifier: classifierFunc(func(int, []byte) FailureCategory {
				return FailureCategoryRateLimited
			}),
			attempt: Attempt{Committed: true, StatusCode: http.StatusTooManyRequests, Now: now},
			want:    Result{Action: ActionTerminate},
		},
		{
			name:    "downstream cancellation terminates",
			attempt: Attempt{Err: context.Canceled, RetryableBeforeCommit: true},
			want:    Result{Action: ActionTerminate},
		},
		{
			name:    "request-written ambiguous error terminates",
			attempt: Attempt{Err: errors.New("connection reset"), RequestWritten: true},
			want:    Result{Action: ActionTerminate},
		},
		{
			name:    "explicit safe pre-commit signal retries",
			attempt: Attempt{Err: errors.New("first event timeout"), RequestWritten: true, RetryableBeforeCommit: true},
			want:    Result{Action: ActionRetry},
		},
		{
			name:    "pre-write transport skips group",
			attempt: Attempt{Err: errors.New("dial tcp failed")},
			want:    Result{Action: ActionSkipGroup},
		},
		{
			name:       "rate limit uses parsed reset",
			classifier: fixedCategory(FailureCategoryRateLimited),
			attempt: Attempt{StatusCode: http.StatusTooManyRequests,
				Header: http.Header{"Retry-After": {"30"}}, Now: now},
			want: Result{Action: ActionCooldownKey, CooldownUntil: now.Add(30 * time.Second)},
		},
		{
			name:       "rate limit without valid reset requests fixed fallback",
			classifier: fixedCategory(FailureCategoryRateLimited),
			attempt:    Attempt{StatusCode: http.StatusTooManyRequests, Now: now},
			want:       Result{Action: ActionCooldownKey, UseFixed: true},
		},
		{
			name:       "model unavailable cools for one hour",
			classifier: fixedCategory(FailureCategoryModelUnavailable),
			attempt:    Attempt{StatusCode: http.StatusNotFound, Now: now},
			want:       Result{Action: ActionCooldownKey, CooldownUntil: now.Add(time.Hour)},
		},
		{
			name:       "invalid key fails key",
			classifier: fixedCategory(FailureCategoryInvalidKey),
			attempt:    Attempt{StatusCode: http.StatusUnauthorized, Now: now},
			want:       Result{Action: ActionFailKey},
		},
		{
			name:       "host error skips group",
			classifier: fixedCategory(FailureCategoryUpstreamHostError),
			attempt:    Attempt{StatusCode: http.StatusServiceUnavailable, Now: now},
			want:       Result{Action: ActionSkipGroup},
		},
		{
			name:       "client error terminates",
			classifier: fixedCategory(FailureCategoryClientError),
			attempt:    Attempt{StatusCode: http.StatusBadRequest, Now: now},
			want:       Result{Action: ActionTerminate},
		},
		{
			name:    "nil classifier terminates",
			attempt: Attempt{StatusCode: http.StatusInternalServerError},
			want:    Result{Action: ActionTerminate},
		},
		{
			name:       "empty attempt terminates",
			classifier: fixedCategory(FailureCategoryRateLimited),
			want:       Result{Action: ActionTerminate},
		},
		{
			name:       "OK terminates",
			classifier: fixedCategory(FailureCategoryOK),
			attempt:    Attempt{StatusCode: http.StatusOK},
			want:       Result{Action: ActionTerminate},
		},
		{
			name:       "ambiguous terminates",
			classifier: fixedCategory(FailureCategoryAmbiguous),
			attempt:    Attempt{StatusCode: http.StatusTemporaryRedirect},
			want:       Result{Action: ActionTerminate},
		},
		{
			name:       "downstream category terminates",
			classifier: fixedCategory(FailureCategoryDownstreamCancel),
			attempt:    Attempt{StatusCode: http.StatusBadRequest},
			want:       Result{Action: ActionTerminate},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Judge(test.classifier, test.attempt); got != test.want {
				t.Fatalf("Judge() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestResultShouldRetryIsDerivedFromAction(t *testing.T) {
	var zero Result
	if zero.Action != ActionTerminate {
		t.Fatalf("zero Result Action = %d, want ActionTerminate", zero.Action)
	}
	if zero.ShouldRetry() {
		t.Fatal("zero Result ShouldRetry() = true, want false")
	}

	tests := []struct {
		action Action
		want   bool
	}{
		{action: ActionTerminate, want: false},
		{action: ActionRetry, want: true},
		{action: ActionCooldownKey, want: true},
		{action: ActionFailKey, want: true},
		{action: ActionSkipGroup, want: true},
		{action: Action(255), want: false},
	}
	for _, test := range tests {
		if got := (Result{Action: test.action}).ShouldRetry(); got != test.want {
			t.Fatalf("Action %d ShouldRetry() = %t, want %t", test.action, got, test.want)
		}
	}
}
