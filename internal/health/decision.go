package health

import (
	"fmt"
	"strings"
	"time"

	"gpt-load/internal/execution"
)

type Action uint8

const (
	ActionTerminate Action = iota
	ActionRetry
	ActionCooldownCredential
	ActionFailCredential
	ActionSkipGroup
)

// RetryDirective is the retry intent selected by JudgeExecution. It does not
// imply that the gateway actually started another attempt.
type RetryDirective string

const (
	RetryNone              RetryDirective = "none"
	RetryRefreshCredential RetryDirective = "refresh_credential"
	RetryNextCandidate     RetryDirective = "next_candidate"
)

// Effect is the single runtime side effect selected for one attempt.
type Effect string

const (
	EffectNone                    Effect = "none"
	EffectCooldownCredential      Effect = "cooldown_credential"
	EffectRecordCredentialFailure Effect = "record_credential_failure"
	EffectSkipGroup               Effect = "skip_group"
)

// RuleID is the stable identifier of the rule that produced a decision.
type RuleID string

func (directive RetryDirective) Valid() bool {
	return directive == RetryNone ||
		directive == RetryRefreshCredential ||
		directive == RetryNextCandidate
}

func (effect Effect) Valid() bool {
	return effect == EffectNone ||
		effect == EffectCooldownCredential ||
		effect == EffectRecordCredentialFailure ||
		effect == EffectSkipGroup
}

// DecisionContext contains the existing request and credential policy inputs
// that are not part of provider error evidence.
type DecisionContext struct {
	DefaultRateLimitCooldown time.Duration
	CredentialRefreshable    bool
	Method                   string
	Operation                execution.Operation
}

// Decision is the complete business decision for one execution attempt.
type Decision struct {
	Category      FailureCategory
	Origin        execution.ErrorOrigin
	Scope         execution.ErrorScope
	Retry         RetryDirective
	Effect        Effect
	CooldownUntil time.Time
	RuleID        RuleID
}

func (decision Decision) ShouldRetry() bool {
	return decision.Retry == RetryRefreshCredential ||
		decision.Retry == RetryNextCandidate
}

// LegacyAction projects the new independent retry/effect decision onto the
// retained request-log action vocabulary.
func (decision Decision) LegacyAction() Action {
	switch decision.Effect {
	case EffectCooldownCredential:
		return ActionCooldownCredential
	case EffectRecordCredentialFailure:
		return ActionFailCredential
	case EffectSkipGroup:
		return ActionSkipGroup
	}
	if decision.Retry == RetryRefreshCredential || decision.Retry == RetryNextCandidate {
		return ActionRetry
	}
	return ActionTerminate
}

// Validate checks the compact decision contract shared by Judge and Gateway.
func (decision Decision) Validate() error {
	if !decision.Category.Valid() {
		return fmt.Errorf("failure category is invalid")
	}
	if !decision.Origin.Valid() {
		return fmt.Errorf("failure origin is invalid")
	}
	if !decision.Scope.Valid() {
		return fmt.Errorf("failure scope is invalid")
	}
	if !decision.Retry.Valid() {
		return fmt.Errorf("retry directive is invalid")
	}
	if !decision.Effect.Valid() {
		return fmt.Errorf("effect is invalid")
	}
	if strings.TrimSpace(string(decision.RuleID)) == "" {
		return fmt.Errorf("rule ID is required")
	}
	if len(decision.RuleID) > 128 {
		return fmt.Errorf("rule ID exceeds maximum length")
	}
	for _, character := range decision.RuleID {
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '.' || character == '_' || character == '-' {
			continue
		}
		return fmt.Errorf("rule ID contains an invalid character")
	}
	switch decision.Effect {
	case EffectCooldownCredential:
		if decision.CooldownUntil.IsZero() {
			return fmt.Errorf("credential cooldown requires a deadline")
		}
	}
	if decision.Effect != EffectCooldownCredential && !decision.CooldownUntil.IsZero() {
		return fmt.Errorf("cooldown deadline requires credential cooldown effect")
	}
	return nil
}
