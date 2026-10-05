package policy

import (
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
		if !rule.Enabled {
			continue
		}
		truth := evalConditionFast(rule.When, ctx)
		if truth == TruthTrue {
			for _, act := range rule.Actions {
				match := &SchedulingMatch{
					RuleID:       rule.ID,
					NameSnapshot: rule.Name,
					Domain:       DomainScheduling,
					ActionType:   act.Type,
				}
				switch act.Type {
				case ActionExcludeCandidate:
					return SchedulingResult{Excluded: true, Reason: match}
				case ActionExcludeModels:
					if !ctx.RequestModel.State.IsEffective() {
						continue
					}
					if act.ContainsModel(ctx.RequestModel.Value) {
						match.ExcludedModel = ctx.RequestModel.Value
						return SchedulingResult{Excluded: true, Reason: match}
					}
				}
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
		if !rule.Enabled {
			continue
		}
		truth := evalConditionFast(rule.When, ctx)
		if truth == TruthTrue {
			// 单规则内多个 multiply_price 保持 latest apply 覆盖
			var latestPrice *Action
			for i := range rule.Actions {
				if rule.Actions[i].Type == ActionMultiplyPrice {
					latestPrice = &rule.Actions[i]
				}
			}
			if latestPrice != nil {
				matches = append(matches, PricingMatch{
					RuleID:       rule.ID,
					NameSnapshot: rule.Name,
					Domain:       DomainPricing,
					Factor:       latestPrice.Factor,
					Multiplier:   latestPrice.Multiplier,
				})
			}
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
	case ConditionKindAll, ConditionKindAny:
		// all 的单位元为 true、决定性结果为 false；any 相反。决定性命中即刻短路，其余按三值折叠。
		identity, decisive, combine := TruthTrue, TruthFalse, TruthValue.And
		if node.Kind == ConditionKindAny {
			identity, decisive, combine = TruthFalse, TruthTrue, TruthValue.Or
		}
		result := identity
		for _, child := range node.Children {
			truth := evalConditionFast(child, ctx)
			if truth == decisive {
				return decisive
			}
			result = combine(result, truth)
		}
		return result

	case ConditionKindNot:
		return evalConditionFast(node.Child, ctx).Not()

	case ConditionKindTimeWindow:
		return evaluateTimeWindow(node.Weekdays, node.Ranges, ctx.Now)

	case ConditionKindParam:
		return evalParamLeaf(node, ctx)

	default:
		return TruthUnknown
	}
}

// evalParamLeaf 求值参数叶子节点，返回三值结果。热路径零分配。
func evalParamLeaf(node *ConditionNode, ctx *EvalContext) TruthValue {
	if node.IsQuota {
		ratio, ok := scanQuotaMin(ctx.QuotaWindows, node.Selector, ctx.EffectiveQuotaNow())
		if !ok {
			return TruthUnknown
		}
		return compareNumber(ratio, node.Op, node.NumValue)
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
		return compareNumber(fact.Value, node.Op, node.NumValue)

	default:
		return TruthUnknown
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
func scanQuotaMin(windows []QuotaWindowFact, sel *QuotaSelector, now time.Time) (float64, bool) {
	if sel == nil {
		return 0, false
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
			return 0, false
		}

		// 2. 若匹配成员状态非有效（measured / retained_in_period / inferred_reset），整叶立即为 unknown
		if !w.State.IsEffective() {
			return 0, false
		}

		// 3. 校验时间有效性与重置上界：
		//    - 已到达/超过重置时间：unknown（无固定周期不补充证据，reset 到点不能继续当 measured）
		//    - 观测时间处于未来 (ObservedAt > now)：unknown（拒绝 future ObservedAt）
		//    - 说明：真实归一化事实的 missing reset / missing observedAt 由 NormalizeQuotaWindowFact
		//      统一标记为 FactStateUnknown；纯求值单测或合成事实未配置 ResetAt/ObservedAt 时不以此拦截。
		if !now.IsZero() && !w.ResetAt.IsZero() && !now.Before(w.ResetAt) {
			return 0, false
		}
		if !now.IsZero() && !w.ObservedAt.IsZero() && now.Before(w.ObservedAt) {
			return 0, false
		}

		// 4. 若数值非法（NaN、Inf 或超出 [0.0, 1.0]），整叶立即为 unknown（坏成员不忽略）
		if math.IsNaN(w.Ratio) || math.IsInf(w.Ratio, 0) || w.Ratio < 0.0 || w.Ratio > 1.0 {
			return 0, false
		}

		// 5. 校验同来源同真实周期/sample/identity 的冲突额度：
		//    仅当具有明确来源 (SourceID != "") 且具备真实 sample 元数据 (非零 ResetAt 与 ObservedAt)
		//    并属于同一身份代次时，才判定为同样本。若同样本内出现不相等的归一化比率 (Ratio != Ratio)，
		//    来源歧义，整叶返回 unknown。
		//    不同明确来源（如不同 provider 或独立探针）正常保留并参与 min 求值。
		for j := 0; j < i; j++ {
			prev := windows[j]
			if prev.SameSample(w) && prev.Ratio != w.Ratio {
				return 0, false
			}
		}

		if w.Ratio < minVal {
			minVal = w.Ratio
		}
	}

	// 无匹配窗口
	if matchCount == 0 {
		return 0, false
	}

	return minVal, true
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
	switch op {
	case "eq":
		return truthOf(actual == expected)
	case "lt":
		return truthOf(actual < expected)
	case "lte":
		return truthOf(actual <= expected)
	case "gt":
		return truthOf(actual > expected)
	case "gte":
		return truthOf(actual >= expected)
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
