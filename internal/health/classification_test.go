package health

import "testing"

func TestErrorClassIsRetryable(t *testing.T) {
	tests := []struct {
		name  string
		class ErrorClass
		want  bool
	}{
		{name: "zero value is non-retryable", class: ErrorClassNonRetryable, want: false},
		{name: "retryable", class: ErrorClassRetryable, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.class.IsRetryable(); got != tt.want {
				t.Fatalf("ErrorClass.IsRetryable() = %t, want %t", got, tt.want)
			}
		})
	}
}
