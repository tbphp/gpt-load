package control

import (
	"math"

	"gpt-load/internal/health"
	"gpt-load/internal/state"
)

func calculateAutoWeight(stats health.KeyStats) int {
	total := stats.Success + stats.Failure
	if total < 10 {
		return state.DefaultWeight
	}
	successScore := float64(stats.Success+1) / float64(total+2)
	weight := int(math.Round(100 * successScore * math.Pow(0.5, float64(stats.ConsecutiveFailure))))
	if weight < 1 {
		return 1
	}
	if weight > state.MaxWeight {
		return state.MaxWeight
	}
	return weight
}
