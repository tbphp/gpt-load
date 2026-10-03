package state

import (
	"math"
	"strings"
	"time"

	"gpt-load/internal/policy"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

// NormalizeQuotaWindows maps raw provider observation quota windows into immutable,
// normalized policy QuotaWindowFact records with sample metadata and identity guard.
//
// Rules:
//  1. Only account-scoped windows without model restrictions may match as account scope.
//     Provider model-specific windows (or windows with non-empty ModelIDs) must not impersonate account windows.
//  2. If window seconds, resetAt, or observedAt are missing on an account window, it enters FactStateUnknown.
//     Missing reset is never permanently measured; missing observedAt cannot be blindly trusted from row-level fallback.
//  3. If observedAt > resetAt, the sample timing is invalid and enters FactStateUnknown.
//  4. Ratio is derived from Utilization and/or Remaining/Limit.
//  5. Illegal (NaN, Inf, negative limit/remaining/utilization), missing, or ambiguous (discrepant values)
//     members enter FactStateUnknown so they cannot be masked by valid members.
//  6. Contradictory readings from the same source, sample timing (non-zero reset/observed), and identity generation
//     enter FactStateUnknown (distinct observations with unequal ratios represent source ambiguity; no arbitrary tolerance).
func NormalizeQuotaWindows(windows []providerobservation.QuotaWindow, identityGeneration uint64) []policy.QuotaWindowFact {
	if len(windows) == 0 {
		return nil
	}
	facts := make([]policy.QuotaWindowFact, 0, len(windows))
	for _, window := range windows {
		facts = append(facts, NormalizeQuotaWindowFact(window, identityGeneration))
	}

	// 校验同来源同真实周期/sample/identity 的冲突额度：
	// 仅当具有明确非空来源且具备真实 sample 元数据（非零 reset 与 observed 时间戳）并属于同一身份代次时，
	// 方判定为同样本。若同样本内出现不相等的归一化比率 (Ratio != Ratio)，属于来源歧义，
	// 冲突成员全部置为 FactStateUnknown，避免借单窗口度量归一化容差造成规则求值失真。
	for i := 0; i < len(facts); i++ {
		for j := i + 1; j < len(facts); j++ {
			if facts[i].Scope != facts[j].Scope || facts[i].WindowSeconds != facts[j].WindowSeconds {
				continue
			}
			if facts[i].SourceID != "" &&
				facts[i].SourceID == facts[j].SourceID &&
				facts[i].IdentityGeneration == facts[j].IdentityGeneration &&
				!facts[i].ResetAt.IsZero() && !facts[i].ObservedAt.IsZero() &&
				!facts[j].ResetAt.IsZero() && !facts[j].ObservedAt.IsZero() &&
				facts[i].ResetAt.Equal(facts[j].ResetAt) &&
				facts[i].ObservedAt.Equal(facts[j].ObservedAt) {
				if facts[i].Ratio != facts[j].Ratio {
					facts[i].State = policy.FactStateUnknown
					facts[j].State = policy.FactStateUnknown
				}
			}
		}
	}

	return facts
}

// NormalizeQuotaWindowFact normalizes a single provider QuotaWindow into a policy QuotaWindowFact.
func NormalizeQuotaWindowFact(window providerobservation.QuotaWindow, identityGeneration uint64) policy.QuotaWindowFact {
	scope := strings.ToLower(strings.TrimSpace(window.Scope))
	if scope == "account" && len(window.ModelIDs) > 0 {
		scope = "model"
	}

	winSec := 0
	hasWinSec := false
	if window.WindowSeconds != nil && *window.WindowSeconds > 0 && *window.WindowSeconds <= math.MaxInt {
		winSec = int(*window.WindowSeconds)
		hasWinSec = true
	}

	var resetAt time.Time
	if window.ResetAtMS != nil && *window.ResetAtMS > 0 {
		resetAt = time.UnixMilli(*window.ResetAtMS).UTC()
	}

	var observedAt time.Time
	if window.ObservedAtMS != nil && *window.ObservedAtMS > 0 {
		observedAt = time.UnixMilli(*window.ObservedAtMS).UTC()
	}

	fact := policy.QuotaWindowFact{
		Scope:              scope,
		WindowSeconds:      winSec,
		Ratio:              0,
		State:              policy.FactStateUnknown,
		ResetAt:            resetAt,
		ObservedAt:         observedAt,
		SourceID:           window.SourceID,
		IdentityGeneration: identityGeneration,
	}

	if !hasWinSec && scope == "account" {
		return fact
	}

	// 缺失可靠生命周期元数据（无 reset、无 observedAt、或 observedAt 在 reset 之后）转 unknown
	if scope == "account" && (resetAt.IsZero() || observedAt.IsZero() || (!resetAt.IsZero() && !observedAt.IsZero() && observedAt.After(resetAt))) {
		return fact
	}

	if window.State != "" && window.State != "available" && window.State != "exhausted" {
		return fact
	}

	hasUtil := window.Utilization != nil
	var utilRemaining float64
	var utilValid bool
	if hasUtil {
		u := *window.Utilization
		if !math.IsNaN(u) && !math.IsInf(u, 0) && u >= 0 {
			utilRemaining = math.Max(0, math.Min(1, 1.0-u))
			utilValid = true
		}
	}

	hasRemPart := window.Remaining != nil || window.Limit != nil
	hasRemComplete := window.Remaining != nil && window.Limit != nil
	var remRemaining float64
	var remValid bool
	if hasRemComplete {
		rem := *window.Remaining
		lim := *window.Limit
		if !math.IsNaN(rem) && !math.IsInf(rem, 0) && !math.IsNaN(lim) && !math.IsInf(lim, 0) && lim > 0 && rem >= 0 {
			remRemaining = math.Max(0, math.Min(1, rem/lim))
			remValid = true
		}
	}

	// 字段出现但数值非法
	if hasUtil && !utilValid {
		return fact
	}
	if hasRemPart && !remValid {
		return fact
	}

	// State == "exhausted"
	if window.State == "exhausted" {
		// 若状态标记为 exhausted 但数值明确声明仍有剩余额度，视为歧义
		if (hasUtil && utilRemaining > 0.01) || (hasRemComplete && remRemaining > 0.01) {
			return fact
		}
		fact.Ratio = 0.0
		fact.State = policy.FactStateMeasured
		return fact
	}

	// State != "exhausted"
	switch {
	case utilValid && remValid:
		// 两者皆有，检查数值一致性（差值超过 1% 视为歧义）
		if math.Abs(utilRemaining-remRemaining) > 0.01 {
			return fact
		}
		fact.Ratio = utilRemaining
		fact.State = policy.FactStateMeasured
	case utilValid:
		fact.Ratio = utilRemaining
		fact.State = policy.FactStateMeasured
	case remValid:
		fact.Ratio = remRemaining
		fact.State = policy.FactStateMeasured
	default:
		// 缺失可用额度数值
		fact.State = policy.FactStateUnknown
	}

	return fact
}
