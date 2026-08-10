package health

import "testing"

func TestResultShouldRetry(t *testing.T) {
	tests := []struct {
		action Action
		want   bool
	}{
		{action: ActionTerminate, want: false},
		{action: ActionRetry, want: true},
		{action: ActionCooldownCredential, want: true},
		{action: ActionFailCredential, want: true},
		{action: ActionSkipGroup, want: true},
		{action: Action(255), want: false},
	}
	for _, test := range tests {
		if got := (Result{Action: test.action}).ShouldRetry(); got != test.want {
			t.Fatalf("Result{Action:%d}.ShouldRetry() = %t, want %t", test.action, got, test.want)
		}
	}
}
