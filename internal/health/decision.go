package health

import "time"

type Action uint8

const (
	ActionTerminate Action = iota
	ActionRetry
	ActionCooldownCredential
	ActionFailCredential
	ActionSkipGroup
)

type Result struct {
	Category      FailureCategory
	Action        Action
	CooldownUntil time.Time
	UseFixed      bool
}

func (result Result) ShouldRetry() bool {
	switch result.Action {
	case ActionRetry, ActionCooldownCredential, ActionFailCredential, ActionSkipGroup:
		return true
	default:
		return false
	}
}
