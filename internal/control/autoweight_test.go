package control

import (
	"testing"

	"gpt-load/internal/health"
)

func TestCalculateAutoWeight(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		stats health.CredentialStats
		want  int
	}{
		{name: "empty window", stats: health.CredentialStats{}, want: 50},
		{name: "insufficient samples", stats: health.CredentialStats{Success: 9}, want: 50},
		{name: "all successes", stats: health.CredentialStats{Success: 10}, want: 92},
		{name: "mixed results", stats: health.CredentialStats{Success: 8, Failure: 2}, want: 75},
		{name: "failure streak penalty", stats: health.CredentialStats{Success: 8, Failure: 2, ConsecutiveFailure: 1}, want: 38},
		{name: "minimum weight", stats: health.CredentialStats{Failure: 10, ConsecutiveFailure: 10}, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := calculateAutoWeight(tt.stats); got != tt.want {
				t.Fatalf("calculateAutoWeight(%#v) = %d, want %d", tt.stats, got, tt.want)
			}
		})
	}
}
