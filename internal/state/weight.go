package state

import "fmt"

const (
	DefaultWeight = 50
	MaxWeight     = 100
)

func cloneWeight(weight *int) *int {
	if weight == nil {
		return nil
	}
	cloned := *weight
	return &cloned
}

func validateManualWeight(subject string, weight *int) error {
	if weight != nil && (*weight < 0 || *weight > MaxWeight) {
		return fmt.Errorf("%s manual weight must be between 0 and %d", subject, MaxWeight)
	}
	return nil
}

// ConfiguredWeight 保留显式值（包括历史 0），未配置时使用默认权重。
func ConfiguredWeight(weight *int) int {
	if weight == nil {
		return DefaultWeight
	}
	return *weight
}
