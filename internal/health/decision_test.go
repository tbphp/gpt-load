package health

import "testing"

func TestDecisionShouldRetry(t *testing.T) {
	tests := []struct {
		retry RetryDirective
		want  bool
	}{
		{retry: RetryNone, want: false},
		{retry: RetryRefreshCredential, want: true},
		{retry: RetryNextCandidate, want: true},
		{retry: RetryDirective("unknown"), want: false},
		{retry: "", want: false},
	}
	for _, test := range tests {
		if got := (Decision{Retry: test.retry}).ShouldRetry(); got != test.want {
			t.Fatalf("Decision{Retry:%q}.ShouldRetry() = %t, want %t", test.retry, got, test.want)
		}
	}
}
