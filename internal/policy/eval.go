package policy

import (
	"fmt"
	"math"
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
			ratio, ok, _ := scanQuotaMin(ctx.QuotaWindows, node.Selector, ctx.Now)
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
	case ConditionKindAll:
		hasUnknown := false
		unknownReason := ""
		childrenDiag := make([]NodeInspectResult, 0, len(node.Children))
		hasFalse := false

		for _, child := range node.Children {
			t, r, diag := evalConditionInspect(child, ctx)
			childrenDiag = append(childrenDiag, diag)
			if t == TruthFalse {
				hasFalse = true
			} else if t == TruthUnknown {
				hasUnknown = true
				if unknownReason == "" {
					unknownReason = r
				}
			}
		}

		if hasFalse {
			return TruthFalse, "", NodeInspectResult{
				Kind:     ConditionKindAll,
				Truth:    TruthFalse,
				Children: childrenDiag,
			}
		}
		if hasUnknown {
			return TruthUnknown, unknownReason, NodeInspectResult{
				Kind:          ConditionKindAll,
				Truth:         TruthUnknown,
				UnknownReason: unknownReason,
				Children:      childrenDiag,
			}
		}
		return TruthTrue, "", NodeInspectResult{
			Kind:     ConditionKindAll,
			Truth:    TruthTrue,
			Children: childrenDiag,
		}

	case ConditionKindAny:
		hasTrue := false
		hasUnknown := false
		unknownReason := ""
		childrenDiag := make([]NodeInspectResult, 0, len(node.Children))

		for _, child := range node.Children {
			t, r, diag := evalConditionInspect(child, ctx)
			childrenDiag = append(childrenDiag, diag)
			if t == TruthTrue {
				hasTrue = true
			} else if t == TruthUnknown {
				hasUnknown = true
				if unknownReason == "" {
					unknownReason = r
				}
			}
		}

		if hasTrue {
			return TruthTrue, "", NodeInspectResult{
				Kind:     ConditionKindAny,
				Truth:    TruthTrue,
				Children: childrenDiag,
			}
		}
		if hasUnknown {
			return TruthUnknown, unknownReason, NodeInspectResult{
				Kind:          ConditionKindAny,
				Truth:         TruthUnknown,
				UnknownReason: unknownReason,
				Children:      childrenDiag,
			}
		}
		return TruthFalse, "", NodeInspectResult{
			Kind:     ConditionKindAny,
			Truth:    TruthFalse,
			Children: childrenDiag,
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
			matchingRatio, ok, reason := scanQuotaMin(ctx.QuotaWindows, node.Selector, ctx.Now)
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

// scanQuotaMin 单次标量扫描求 min，零切片分配。
// 遵循合同：无匹配、成员无效、未知、重置时间过期或来源歧义整叶返回未知，不可被有效成员掩盖。
func scanQuotaMin(windows []QuotaWindowFact, sel *QuotaSelector, now time.Time) (float64, bool, string) {
	if sel == nil {
		return 0, false, "nil quota selector"
	}

	minVal := math.MaxFloat64
	matchCount := 0

	for _, w := range windows {
		if w.Scope != sel.Scope || w.WindowSeconds != sel.WindowSeconds {
			continue
		}
		matchCount++

		// 1. 若匹配集合中含有未知状态成员，整叶立即为 unknown（未知不可被有效成员掩盖）
		if w.State == FactStateUnknown {
			return 0, false, fmt.Sprintf("quota window (%s, %ds) member state is unknown", sel.Scope, sel.WindowSeconds)
		}

		// 2. 若匹配成员状态非有效（measured / retained_in_period / inferred_reset），整叶立即为 unknown
		if !w.State.IsEffective() {
			return 0, false, fmt.Sprintf("quota window (%s, %ds) member state is %s", sel.Scope, sel.WindowSeconds, w.State)
		}

		// 3. 校验时间有效性与重置上界：
		//    - 已到达/超过重置时间：unknown（无固定周期不补充证据，reset 到点不能继续当 measured）
		//    - 观测时间处于未来 (ObservedAt > now)：unknown（拒绝 future ObservedAt）
		//    - 说明：真实归一化事实的 missing reset / missing observedAt 由 NormalizeQuotaWindowFact
		//      统一标记为 FactStateUnknown；纯求值单测或合成事实未配置 ResetAt/ObservedAt 时不以此拦截。
		if !now.IsZero() && !w.ResetAt.IsZero() && !now.Before(w.ResetAt) {
			return 0, false, fmt.Sprintf("quota window (%s, %ds) reset reached at %v (now %v)", sel.Scope, sel.WindowSeconds, w.ResetAt, now)
		}
		if !now.IsZero() && !w.ObservedAt.IsZero() && now.Before(w.ObservedAt) {
			return 0, false, fmt.Sprintf("quota window (%s, %ds) future observed at %v (now %v)", sel.Scope, sel.WindowSeconds, w.ObservedAt, now)
		}

		// 4. 若数值非法（NaN、Inf 或超出 [0.0, 1.0]），整叶立即为 unknown（坏成员不忽略）
		if math.IsNaN(w.Ratio) || math.IsInf(w.Ratio, 0) || w.Ratio < 0.0 || w.Ratio > 1.0 {
			return 0, false, fmt.Sprintf("invalid quota member ratio %v (must be finite in [0.0, 1.0])", w.Ratio)
		}

		if w.Ratio < minVal {
			minVal = w.Ratio
		}
	}

	// 无匹配窗口
	if matchCount == 0 {
		return 0, false, fmt.Sprintf("no quota window found for scope %q window %ds", sel.Scope, sel.WindowSeconds)
	}

	return minVal, true, ""
}

func compareString(actual string, op string, expected string, inValues []string) TruthValue {
	switch op {
	case "eq":
		if actual == expected {
			return TruthTrue
		}
		return TruthFalse
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

func compareNumber(actual float64, op string, expected float64) TruthValue {
	return compareNumberWithShift(actual, op, expected, 0)
}

func compareNumberWithShift(actual float64, op string, expected float64, shift int8) TruthValue {
	if shift == 0 {
		switch op {
		case "eq":
			if actual == expected {
				return TruthTrue
			}
			return TruthFalse
		case "lt":
			if actual < expected {
				return TruthTrue
			}
			return TruthFalse
		case "lte":
			if actual <= expected {
				return TruthTrue
			}
			return TruthFalse
		case "gt":
			if actual > expected {
				return TruthTrue
			}
			return TruthFalse
		case "gte":
			if actual >= expected {
				return TruthTrue
			}
			return TruthFalse
		default:
			return TruthUnknown
		}
	}

	if shift < 0 {
		// 原始十进制严格小于 expected (例如 0.99999999999999999 < 1.0)
		switch op {
		case "eq":
			return TruthFalse
		case "lt", "lte":
			if actual < expected {
				return TruthTrue
			}
			return TruthFalse
		case "gt", "gte":
			if actual >= expected {
				return TruthTrue
			}
			return TruthFalse
		default:
			return TruthUnknown
		}
	}

	// shift > 0: 原始十进制严格大于 expected
	switch op {
	case "eq":
		return TruthFalse
	case "lt", "lte":
		if actual <= expected {
			return TruthTrue
		}
		return TruthFalse
	case "gt", "gte":
		if actual > expected {
			return TruthTrue
		}
		return TruthFalse
	default:
		return TruthUnknown
	}
}
