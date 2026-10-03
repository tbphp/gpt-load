package policy

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// EvalScheduling 评估 scheduling 域的规则
// 若任一已启用规则命中排除动作，则返回 Excluded: true 及排除原因快照
func (c *CompiledConfig) EvalScheduling(ctx *EvalContext) SchedulingResult {
	if c == nil || len(c.rules) == 0 || ctx == nil {
		return SchedulingResult{Excluded: false}
	}

	for _, rule := range c.rules {
		if !rule.Enabled || rule.Domain != DomainScheduling {
			continue
		}
		truth := evalConditionFast(rule.When, ctx)
		if truth == TruthTrue {
			return SchedulingResult{
				Excluded: true,
				Reason: &SchedulingMatch{
					RuleID:       rule.ID,
					NameSnapshot: rule.Name,
					Domain:       rule.Domain,
				},
			}
		}
	}

	return SchedulingResult{Excluded: false}
}

// EvalPricing 评估 pricing 域的规则
// 返回全部命中的有序倍率记录；整条未决 (unknown) 的规则跳过并不阻断后续命中
func (c *CompiledConfig) EvalPricing(ctx *EvalContext) PricingResult {
	if c == nil || len(c.rules) == 0 || ctx == nil {
		return PricingResult{Matches: nil}
	}

	matches := make([]PricingMatch, 0)
	for _, rule := range c.rules {
		if !rule.Enabled || rule.Domain != DomainPricing {
			continue
		}
		truth := evalConditionFast(rule.When, ctx)
		if truth == TruthTrue {
			matches = append(matches, PricingMatch{
				RuleID:       rule.ID,
				NameSnapshot: rule.Name,
				Domain:       rule.Domain,
				Factor:       rule.Then.Factor,
				Multiplier:   rule.Then.Multiplier,
			})
		}
	}

	return PricingResult{Matches: matches}
}

// evalConditionFast 热路径纯快速求值：三值逻辑短路，零树分配，零元数据注册表查找
func evalConditionFast(node *ConditionNode, ctx *EvalContext) TruthValue {
	if node == nil {
		return TruthUnknown
	}

	switch node.Kind {
	case ConditionKindAll:
		hasUnknown := false
		for _, child := range node.Children {
			t := evalConditionFast(child, ctx)
			if t == TruthFalse {
				return TruthFalse // false AND unknown = false，可直接短路
			}
			if t == TruthUnknown {
				hasUnknown = true
			}
		}
		if hasUnknown {
			return TruthUnknown
		}
		return TruthTrue

	case ConditionKindAny:
		hasUnknown := false
		for _, child := range node.Children {
			t := evalConditionFast(child, ctx)
			if t == TruthTrue {
				return TruthTrue // true OR unknown = true，可直接短路
			}
			if t == TruthUnknown {
				hasUnknown = true
			}
		}
		if hasUnknown {
			return TruthUnknown
		}
		return TruthFalse

	case ConditionKindNot:
		return evalConditionFast(node.Child, ctx).Not()

	case ConditionKindTimeWindow:
		return evaluateTimeWindow(node.Weekdays, node.Ranges, ctx.Now)

	case ConditionKindParam:
		if node.IsQuota {
			ratio, ok, _ := scanQuotaMin(ctx.QuotaWindows, node.Selector, ctx.EffectiveQuotaNow(), false)
			if !ok {
				return TruthUnknown
			}
			return compareNumberWithShift(ratio, node.Op, node.NumValue, node.NumShift)
		}

		switch node.ParamType {
		case ParamTypeString:
			fact, ok := getStringFact(node.Fact, ctx)
			if !ok || !fact.State.IsEffective() {
				return TruthUnknown
			}
			return compareString(fact.Value, node.Op, node.StrValue, node.InValues)

		case ParamTypeNumber:
			fact, ok := getNumberFact(node.Fact, ctx)
			if !ok || !fact.State.IsEffective() {
				return TruthUnknown
			}
			if math.IsNaN(fact.Value) || math.IsInf(fact.Value, 0) {
				return TruthUnknown
			}
			return compareNumberWithShift(fact.Value, node.Op, node.NumValue, node.NumShift)

		default:
			return TruthUnknown
		}

	default:
		return TruthUnknown
	}
}

// Inspect 纯计算诊断模式，返回每条规则及其条件树各节点的求值状态与原因，不修改输入
func (c *CompiledConfig) Inspect(ctx *EvalContext) InspectResult {
	if c == nil || len(c.rules) == 0 || ctx == nil {
		return InspectResult{Rules: nil}
	}

	results := make([]RuleInspectResult, 0, len(c.rules))
	for _, rule := range c.rules {
		truth, _, nodeDiag := evalConditionInspect(rule.When, ctx)
		var status RuleStatus
		if !rule.Enabled {
			status = RuleStatusDisabled
		} else {
			switch truth {
			case TruthTrue:
				status = RuleStatusHit
			case TruthFalse:
				status = RuleStatusMiss
			default:
				status = RuleStatusSkippedUnknown
			}
		}

		results = append(results, RuleInspectResult{
			RuleID:       rule.ID,
			NameSnapshot: rule.Name,
			Domain:       rule.Domain,
			Enabled:      rule.Enabled,
			Status:       status,
			Condition:    nodeDiag,
			Action:       rule.Then,
		})
	}

	return InspectResult{Rules: results}
}

// evalConditionInspect 诊断模式递归求值，记录完整条件树节点诊断与 unknown 原因
func evalConditionInspect(node *ConditionNode, ctx *EvalContext) (TruthValue, string, NodeInspectResult) {
	if node == nil {
		return TruthUnknown, "nil condition node", NodeInspectResult{Truth: TruthUnknown, UnknownReason: "nil condition node"}
	}

	switch node.Kind {
	case ConditionKindAll, ConditionKindAny:
		combine := TruthValue.And
		truth := TruthTrue
		if node.Kind == ConditionKindAny {
			combine = TruthValue.Or
			truth = TruthFalse
		}
		unknownReason := ""
		childrenDiag := make([]NodeInspectResult, 0, len(node.Children))
		for _, child := range node.Children {
			t, r, diag := evalConditionInspect(child, ctx)
			childrenDiag = append(childrenDiag, diag)
			if t == TruthUnknown && unknownReason == "" {
				unknownReason = r
			}
			truth = combine(truth, t)
		}
		if truth != TruthUnknown {
			unknownReason = ""
		}
		return truth, unknownReason, NodeInspectResult{
			Kind:          node.Kind,
			Truth:         truth,
			UnknownReason: unknownReason,
			Children:      childrenDiag,
		}

	case ConditionKindNot:
		t, r, diag := evalConditionInspect(node.Child, ctx)
		resTruth := t.Not()
		return resTruth, r, NodeInspectResult{
			Kind:          ConditionKindNot,
			Truth:         resTruth,
			UnknownReason: r,
			Children:      []NodeInspectResult{diag},
		}

	case ConditionKindTimeWindow:
		truth := evaluateTimeWindow(node.Weekdays, node.Ranges, ctx.Now)
		return truth, "", NodeInspectResult{
			Kind:  ConditionKindTimeWindow,
			Truth: truth,
		}

	case ConditionKindParam:
		if node.IsQuota {
			matchingRatio, ok, reason := scanQuotaMin(ctx.QuotaWindows, node.Selector, ctx.EffectiveQuotaNow(), true)
			if !ok {
				return TruthUnknown, reason, NodeInspectResult{
					Kind:          ConditionKindParam,
					Fact:          node.Fact,
					Truth:         TruthUnknown,
					UnknownReason: reason,
				}
			}
			truth := compareNumberWithShift(matchingRatio, node.Op, node.NumValue, node.NumShift)
			return truth, "", NodeInspectResult{
				Kind:  ConditionKindParam,
				Fact:  node.Fact,
				Truth: truth,
			}
		}

		switch node.ParamType {
		case ParamTypeString:
			fact, ok := getStringFact(node.Fact, ctx)
			if !ok || !fact.State.IsEffective() {
				reason := fmt.Sprintf("fact %q unavailable or state is %s", node.Fact, fact.State)
				return TruthUnknown, reason, NodeInspectResult{
					Kind:          ConditionKindParam,
					Fact:          node.Fact,
					Truth:         TruthUnknown,
					UnknownReason: reason,
				}
			}
			truth := compareString(fact.Value, node.Op, node.StrValue, node.InValues)
			return truth, "", NodeInspectResult{
				Kind:  ConditionKindParam,
				Fact:  node.Fact,
				Truth: truth,
			}

		case ParamTypeNumber:
			fact, ok := getNumberFact(node.Fact, ctx)
			if !ok || !fact.State.IsEffective() {
				reason := fmt.Sprintf("fact %q unavailable or state is %s", node.Fact, fact.State)
				return TruthUnknown, reason, NodeInspectResult{
					Kind:          ConditionKindParam,
					Fact:          node.Fact,
					Truth:         TruthUnknown,
					UnknownReason: reason,
				}
			}
			if math.IsNaN(fact.Value) || math.IsInf(fact.Value, 0) {
				reason := fmt.Sprintf("number fact %q value is NaN or Inf", node.Fact)
				return TruthUnknown, reason, NodeInspectResult{
					Kind:          ConditionKindParam,
					Fact:          node.Fact,
					Truth:         TruthUnknown,
					UnknownReason: reason,
				}
			}
			truth := compareNumberWithShift(fact.Value, node.Op, node.NumValue, node.NumShift)
			return truth, "", NodeInspectResult{
				Kind:  ConditionKindParam,
				Fact:  node.Fact,
				Truth: truth,
			}

		default:
			reason := fmt.Sprintf("unsupported param type %q", node.ParamType)
			return TruthUnknown, reason, NodeInspectResult{
				Kind:          ConditionKindParam,
				Fact:          node.Fact,
				Truth:         TruthUnknown,
				UnknownReason: reason,
			}
		}

	default:
		return TruthUnknown, "unknown condition kind", NodeInspectResult{
			Kind:          node.Kind,
			Truth:         TruthUnknown,
			UnknownReason: "unknown condition kind",
		}
	}
}

func getStringFact(factKey string, ctx *EvalContext) (StringFact, bool) {
	if factKey == "request.model" {
		return ctx.RequestModel, true
	}
	if factKey == "upstream.model" {
		return ctx.UpstreamModel, true
	}
	if ctx.CustomStringFacts != nil {
		f, ok := ctx.CustomStringFacts[factKey]
		return f, ok
	}
	return StringFact{}, false
}

func getNumberFact(factKey string, ctx *EvalContext) (NumberFact, bool) {
	if ctx.CustomNumberFacts != nil {
		f, ok := ctx.CustomNumberFacts[factKey]
		return f, ok
	}
	return NumberFact{}, false
}

// scanQuotaMin 评估匹配选择器的额度窗口最小值。
// 遵循合同：无匹配、成员无效、未知、重置时间过期或来源歧义整叶返回未知，不可被有效成员掩盖。
//
// 来源冲突校验规则：
// 仅当具有明确来源 (SourceID != "") 且具备真实 sample 元数据 (非零 ResetAt 与 ObservedAt)
// 并属于同一身份代次时，才判定为同样本。若同样本内出现不相等的归一化比率 (Ratio != Ratio)，
// 判定为来源歧义，整叶返回 unknown（不引入任意容差，避免如 0.099 与 0.101 跨越阈值仍被当成有效）。
// 不同明确来源（如不同 provider 或独立探针）正常保留并参与 min 求值。
func scanQuotaMin(windows []QuotaWindowFact, sel *QuotaSelector, now time.Time, diagnostics bool) (float64, bool, string) {
	if sel == nil {
		if !diagnostics {
			return 0, false, ""
		}
		return 0, false, "nil quota selector"
	}

	minVal := math.MaxFloat64
	matchCount := 0

	for i, w := range windows {
		if w.Scope != sel.Scope || w.WindowSeconds != sel.WindowSeconds {
			continue
		}
		matchCount++

		// 1. 若匹配集合中含有未知状态成员，整叶立即为 unknown（未知不可被有效成员掩盖）
		if w.State == FactStateUnknown {
			if !diagnostics {
				return 0, false, ""
			}
			return 0, false, fmt.Sprintf("quota window (%s, %ds) member state is unknown", sel.Scope, sel.WindowSeconds)
		}

		// 2. 若匹配成员状态非有效（measured / retained_in_period / inferred_reset），整叶立即为 unknown
		if !w.State.IsEffective() {
			if !diagnostics {
				return 0, false, ""
			}
			return 0, false, fmt.Sprintf("quota window (%s, %ds) member state is %s", sel.Scope, sel.WindowSeconds, w.State)
		}

		// 3. 校验时间有效性与重置上界：
		//    - 已到达/超过重置时间：unknown（无固定周期不补充证据，reset 到点不能继续当 measured）
		//    - 观测时间处于未来 (ObservedAt > now)：unknown（拒绝 future ObservedAt）
		//    - 说明：真实归一化事实的 missing reset / missing observedAt 由 NormalizeQuotaWindowFact
		//      统一标记为 FactStateUnknown；纯求值单测或合成事实未配置 ResetAt/ObservedAt 时不以此拦截。
		if !now.IsZero() && !w.ResetAt.IsZero() && !now.Before(w.ResetAt) {
			if !diagnostics {
				return 0, false, ""
			}
			return 0, false, fmt.Sprintf("quota window (%s, %ds) reset reached at %v (now %v)", sel.Scope, sel.WindowSeconds, w.ResetAt, now)
		}
		if !now.IsZero() && !w.ObservedAt.IsZero() && now.Before(w.ObservedAt) {
			if !diagnostics {
				return 0, false, ""
			}
			return 0, false, fmt.Sprintf("quota window (%s, %ds) future observed at %v (now %v)", sel.Scope, sel.WindowSeconds, w.ObservedAt, now)
		}

		// 4. 若数值非法（NaN、Inf 或超出 [0.0, 1.0]），整叶立即为 unknown（坏成员不忽略）
		if math.IsNaN(w.Ratio) || math.IsInf(w.Ratio, 0) || w.Ratio < 0.0 || w.Ratio > 1.0 {
			if !diagnostics {
				return 0, false, ""
			}
			return 0, false, fmt.Sprintf("invalid quota member ratio %v (must be finite in [0.0, 1.0])", w.Ratio)
		}

		// 5. 校验同来源同真实周期/sample/identity 的冲突额度：
		//    仅当具有明确来源 (SourceID != "") 且具备真实 sample 元数据 (非零 ResetAt 与 ObservedAt)
		//    并属于同一身份代次时，才判定为同样本。若同样本内出现不相等的归一化比率 (Ratio != Ratio)，
		//    来源歧义，整叶返回 unknown。
		//    不同明确来源（如不同 provider 或独立探针）正常保留并参与 min 求值。
		for j := 0; j < i; j++ {
			prev := windows[j]
			if prev.Scope != sel.Scope || prev.WindowSeconds != sel.WindowSeconds {
				continue
			}
			if prev.SourceID != "" &&
				prev.SourceID == w.SourceID &&
				prev.IdentityGeneration == w.IdentityGeneration &&
				!prev.ResetAt.IsZero() && !prev.ObservedAt.IsZero() &&
				!w.ResetAt.IsZero() && !w.ObservedAt.IsZero() &&
				prev.ResetAt.Equal(w.ResetAt) &&
				prev.ObservedAt.Equal(w.ObservedAt) {
				if prev.Ratio != w.Ratio {
					if !diagnostics {
						return 0, false, ""
					}
					return 0, false, fmt.Sprintf("conflicting quota ratios (%.4f vs %.4f) for same source %q sample", prev.Ratio, w.Ratio, w.SourceID)
				}
			}
		}

		if w.Ratio < minVal {
			minVal = w.Ratio
		}
	}

	// 无匹配窗口
	if matchCount == 0 {
		if !diagnostics {
			return 0, false, ""
		}
		return 0, false, fmt.Sprintf("no quota window found for scope %q window %ds", sel.Scope, sel.WindowSeconds)
	}

	return minVal, true, ""
}

func compareString(actual string, op string, expected string, inValues []string) TruthValue {
	switch op {
	case "eq":
		return truthOf(matchStringPattern(actual, expected))
	case "in":
		for _, item := range inValues {
			if actual == item {
				return TruthTrue
			}
		}
		return TruthFalse
	default:
		return TruthUnknown
	}
}

// 只有 * 是通配符；其它字符保持字面含义，模型路径中的 / 也可被匹配。
func matchStringPattern(actual, pattern string) bool {
	prefix, rest, wildcard := strings.Cut(pattern, "*")
	if !wildcard {
		return actual == pattern
	}
	if !strings.HasPrefix(actual, prefix) {
		return false
	}
	actual = actual[len(prefix):]
	for {
		part, next, more := strings.Cut(rest, "*")
		if !more {
			return strings.HasSuffix(actual, part)
		}
		at := strings.Index(actual, part)
		if at < 0 {
			return false
		}
		actual = actual[at+len(part):]
		rest = next
	}
}

func compareNumber(actual float64, op string, expected float64) TruthValue {
	return compareNumberWithShift(actual, op, expected, 0)
}

func compareNumberWithShift(actual float64, op string, expected float64, shift int8) TruthValue {
	switch op {
	case "eq":
		return truthOf(shift == 0 && actual == expected)
	case "lt":
		return truthOf(actual < expected || (actual == expected && shift > 0))
	case "lte":
		return truthOf(actual < expected || (actual == expected && shift >= 0))
	case "gt":
		return truthOf(actual > expected || (actual == expected && shift < 0))
	case "gte":
		return truthOf(actual > expected || (actual == expected && shift <= 0))
	default:
		return TruthUnknown
	}
}

func truthOf(condition bool) TruthValue {
	if condition {
		return TruthTrue
	}
	return TruthFalse
}
