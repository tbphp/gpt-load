package control

import (
	"testing"

	"gpt-load/internal/health"
)

func TestCalculateAutoWeight(t *testing.T) {
	tests := []struct {
		name  string
		stats health.KeyStats
		want  int
	}{
		{name: "empty window", stats: health.KeyStats{}, want: 50},
		{name: "insufficient samples", stats: health.KeyStats{Success: 9}, want: 50},
		{name: "all successes", stats: health.KeyStats{Success: 10}, want: 92},
		{name: "mixed results", stats: health.KeyStats{Success: 8, Failure: 2}, want: 75},
		{name: "failure streak penalty", stats: health.KeyStats{Success: 8, Failure: 2, ConsecutiveFailure: 1}, want: 38},
		{name: "minimum weight", stats: health.KeyStats{Failure: 10, ConsecutiveFailure: 10}, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := calculateAutoWeight(tt.stats); got != tt.want {
				t.Fatalf("calculateAutoWeight(%#v) = %d, want %d", tt.stats, got, tt.want)
			}
		})
	}
}
