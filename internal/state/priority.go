package state

import "fmt"

const (
	DefaultPriority = 50
	MaxPriority     = 100
	MinPriority     = 1
)

func clonePriority(priority *int) *int {
	return cloneWeight(priority)
}

func validateManualPriority(subject string, priority *int) error {
	if priority != nil && (*priority < MinPriority || *priority > MaxPriority) {
		return fmt.Errorf("%s manual priority must be between %d and %d", subject, MinPriority, MaxPriority)
	}
	return nil
}

// ConfiguredPriority 未配置时使用默认优先级。
func ConfiguredPriority(priority *int) int {
	if priority == nil {
		return DefaultPriority
	}
	return *priority
}

func equalOptionalInt(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
