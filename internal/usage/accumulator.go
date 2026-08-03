package usage

import "errors"

var (
	// ErrFinalized indicates an accumulator has already published its result.
	ErrFinalized = errors.New("usage accumulator finalized")
	// ErrNegativePatch indicates a patch contains a negative token count.
	ErrNegativePatch = errors.New("usage patch contains negative token count")
	// ErrTokenOverflow indicates the combined token count exceeds int64.
	ErrTokenOverflow = errors.New("usage token total overflows int64")
)

const (
	presenceUncachedInput uint8 = 1 << iota
	presenceCacheRead
	presenceCacheWrite5M
	presenceCacheWrite1H
	presenceCacheWriteUnknown
	presenceOutput
)

// Accumulator collects one usage result across one or more provider events.
type Accumulator struct {
	tokens      Tokens
	presence    uint8
	diagnostics Diagnostics
	final       bool
	finalized   bool
}

// ReplaceSnapshot replaces every token and diagnostic value with patch.
func (a *Accumulator) ReplaceSnapshot(patch Patch) error {
	if a.finalized {
		return ErrFinalized
	}
	if !validPatch(patch) {
		return ErrNegativePatch
	}

	candidate := Accumulator{
		diagnostics: patch.Diagnostics,
		final:       patch.Final,
	}
	candidate.apply(patch)
	if _, ok := CheckedTotal(candidate.tokens); !ok {
		return ErrTokenOverflow
	}
	a.tokens = candidate.tokens
	a.presence = candidate.presence
	a.diagnostics = candidate.diagnostics
	a.final = candidate.final
	return nil
}

// MergePatch overwrites patch's present values and retains all other values.
func (a *Accumulator) MergePatch(patch Patch) error {
	if a.finalized {
		return ErrFinalized
	}
	if !validPatch(patch) {
		return ErrNegativePatch
	}

	candidate := *a
	candidate.apply(patch)
	candidate.diagnostics.Merge(patch.Diagnostics)
	candidate.final = candidate.final || patch.Final
	if _, ok := CheckedTotal(candidate.tokens); !ok {
		return ErrTokenOverflow
	}
	a.tokens = candidate.tokens
	a.presence = candidate.presence
	a.diagnostics = candidate.diagnostics
	a.final = candidate.final
	return nil
}

// Finalize publishes a result once.
func (a *Accumulator) Finalize(applicable bool) (Result, bool) {
	if a.finalized {
		return Result{}, false
	}
	a.finalized = true
	if !applicable {
		return Result{State: StateNotApplicable}, true
	}

	state := StatePartial
	if a.presence == 0 {
		state = StateMissing
	} else if a.final {
		state = StateComplete
	}
	return Result{Tokens: a.tokens, State: state, Diagnostics: a.diagnostics}, true
}

func validPatch(patch Patch) bool {
	for _, value := range [...]*int64{
		patch.UncachedInput,
		patch.CacheRead,
		patch.CacheWrite5M,
		patch.CacheWrite1H,
		patch.CacheWriteUnknown,
		patch.Output,
	} {
		if value != nil && *value < 0 {
			return false
		}
	}
	return true
}

func (a *Accumulator) apply(patch Patch) {
	if patch.UncachedInput != nil {
		a.tokens.UncachedInput = *patch.UncachedInput
		a.presence |= presenceUncachedInput
	}
	if patch.CacheRead != nil {
		a.tokens.CacheRead = *patch.CacheRead
		a.presence |= presenceCacheRead
	}
	if patch.CacheWrite5M != nil {
		a.tokens.CacheWrite5M = *patch.CacheWrite5M
		a.presence |= presenceCacheWrite5M
	}
	if patch.CacheWrite1H != nil {
		a.tokens.CacheWrite1H = *patch.CacheWrite1H
		a.presence |= presenceCacheWrite1H
	}
	if patch.CacheWriteUnknown != nil {
		a.tokens.CacheWriteUnknown = *patch.CacheWriteUnknown
		a.presence |= presenceCacheWriteUnknown
	}
	if patch.Output != nil {
		a.tokens.Output = *patch.Output
		a.presence |= presenceOutput
	}
}
