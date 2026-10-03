package policy

import (
	"fmt"
	"time"

	"gpt-load/internal/pricing"
)

// ValidationError 结构化校验错误
type ValidationError struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("%s (%s)", e.Message, e.Code)
	}
	return fmt.Sprintf("%s: %s (%s)", e.Path, e.Message, e.Code)
}

// 稳定错误 Code 常量
const (
	ErrCodeInvalidUTF8         = "ERR_INVALID_UTF8"
	ErrCodeDuplicateKey        = "ERR_DUPLICATE_KEY"
	ErrCodeUnknownField        = "ERR_UNKNOWN_FIELD"
	ErrCodeInvalidType         = "ERR_INVALID_TYPE"
	ErrCodeMissingField        = "ERR_MISSING_FIELD"
	ErrCodeInvalidValue        = "ERR_INVALID_VALUE"
	ErrCodeBudgetExceeded      = "ERR_BUDGET_EXCEEDED"
	ErrCodeUnsupportedOperator = "ERR_UNSUPPORTED_OPERATOR"
	ErrCodeDomainMismatch      = "ERR_DOMAIN_MISMATCH"
	ErrCodeInvalidCondition    = "ERR_INVALID_CONDITION"
)

// Domain 规则执行域
type Domain string

const (
	DomainScheduling Domain = "scheduling"
	DomainPricing    Domain = "pricing"
)

// Valid 检查执行域是否合法
func (d Domain) Valid() bool {
	return d == DomainScheduling || d == DomainPricing
}

// ActionType 动作类型
type ActionType string

const (
	ActionExcludeCandidate ActionType = "exclude_candidate"
	ActionMultiplyPrice    ActionType = "multiply_price"
)

// TruthValue 三值逻辑真值
type TruthValue string

const (
	TruthFalse   TruthValue = "false"
	TruthTrue    TruthValue = "true"
	TruthUnknown TruthValue = "unknown"
)

// Not 逻辑非
func (t TruthValue) Not() TruthValue {
	switch t {
	case TruthTrue:
		return TruthFalse
	case TruthFalse:
		return TruthTrue
	default:
		return TruthUnknown
	}
}

// And 逻辑与
func (t TruthValue) And(other TruthValue) TruthValue {
	if t == TruthFalse || other == TruthFalse {
		return TruthFalse
	}
	if t == TruthTrue && other == TruthTrue {
		return TruthTrue
	}
	return TruthUnknown
}

// Or 逻辑或
func (t TruthValue) Or(other TruthValue) TruthValue {
	if t == TruthTrue || other == TruthTrue {
		return TruthTrue
	}
	if t == TruthFalse && other == TruthFalse {
		return TruthFalse
	}
	return TruthUnknown
}

// ParamType 参数值类型
type ParamType string

const (
	ParamTypeString ParamType = "string"
	ParamTypeNumber ParamType = "number"
)

// Valid 检查参数类型是否合法
func (p ParamType) Valid() bool {
	return p == ParamTypeString || p == ParamTypeNumber
}

// SelectorConstraint 选择器约束
type SelectorConstraint struct {
	Scope                 string `json:"scope,omitempty"`
	WindowSecondsRequired bool   `json:"window_seconds_required,omitempty"`
}

// ParamDescriptor 参数发现描述
type ParamDescriptor struct {
	Key                string              `json:"key"`
	Type               ParamType           `json:"type"`
	Unit               string              `json:"unit,omitempty"`
	Label              string              `json:"label"`
	Description        string              `json:"description"`
	Operators          []string            `json:"operators"`
	Domains            []Domain            `json:"domains"`
	BindingScopes      []string            `json:"binding_scopes"`
	SelectorConstraint *SelectorConstraint `json:"selector_constraint,omitempty"`
	Reducers           []string            `json:"reducers,omitempty"`
}

// PredicateDescriptor 谓词描述
type PredicateDescriptor struct {
	Name        string   `json:"name"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Domains     []Domain `json:"domains"`
}

// ActionDescriptor 动作描述
type ActionDescriptor struct {
	Type        ActionType `json:"type"`
	Domain      Domain     `json:"domain"`
	Label       string     `json:"label"`
	Description string     `json:"description"`
	Fields      []string   `json:"fields"`
}

// FactState 事实有效性状态
type FactState string

const (
	FactStateMeasured         FactState = "measured"
	FactStateRetainedInPeriod FactState = "retained_in_period"
	FactStateInferredReset    FactState = "inferred_reset"
	FactStateUnknown          FactState = "unknown"
)

// IsEffective 判断事实是否有效可用
func (s FactState) IsEffective() bool {
	return s == FactStateMeasured || s == FactStateRetainedInPeriod || s == FactStateInferredReset
}

// StringFact 字符串类型事实
type StringFact struct {
	Value string
	State FactState
}

// NumberFact 数值类型事实
type NumberFact struct {
	Value float64
	State FactState
}

// QuotaSelector 额度选择器
type QuotaSelector struct {
	Scope         string `json:"scope"`
	WindowSeconds int    `json:"window_seconds"`
}

// QuotaWindowFact 额度窗口事实
type QuotaWindowFact struct {
	Scope              string
	WindowSeconds      int
	Ratio              float64
	State              FactState
	ResetAt            time.Time
	ObservedAt         time.Time
	SourceID           string
	IdentityGeneration uint64
}

// CloneQuotaWindows 返回 QuotaWindowFact 切片的独立深拷贝。
func CloneQuotaWindows(facts []QuotaWindowFact) []QuotaWindowFact {
	if facts == nil {
		return nil
	}
	cloned := make([]QuotaWindowFact, len(facts))
	copy(cloned, facts)
	return cloned
}

// Action 规则执行动作
type Action struct {
	Type       ActionType
	Factor     string                  // pricing 动作的原始因子字符串
	Multiplier pricing.PriceMultiplier // 解析后的六位小数倍率
}

// ConditionKind 条件节点形态
type ConditionKind string

const (
	ConditionKindAll        ConditionKind = "all"
	ConditionKindAny        ConditionKind = "any"
	ConditionKindNot        ConditionKind = "not"
	ConditionKindParam      ConditionKind = "param"
	ConditionKindTimeWindow ConditionKind = "time_window"
)

// TimeRange [StartMin, EndMin] 分钟数表示
type TimeRange struct {
	StartMin int
	EndMin   int
}

// ConditionNode 条件抽象语法树节点
type ConditionNode struct {
	Kind ConditionKind

	// 复合节点形态
	Children []*ConditionNode // for all, any
	Child    *ConditionNode   // for not

	// 参数比较形态及编译期内化元数据
	Fact      string
	ParamType ParamType // 编译期记录：ParamTypeString, ParamTypeNumber
	IsQuota   bool      // 编译期记录：是否为 credential.quota.remaining_ratio
	Op        string
	StrValue  string
	InValues  []string
	NumValue  float64
	NumShift  int8 // 记录原始十进制相对 NumValue 的偏移: 0=精确, -1=小于, 1=大于
	Selector  *QuotaSelector
	Reduce    string

	// 时间段谓词形态
	Predicate string
	Weekdays  []int
	Ranges    []TimeRange
}

// Rule 一条完整规则
type Rule struct {
	ID      string
	Name    string
	Domain  Domain
	Enabled bool
	When    *ConditionNode
	Then    Action
}

// CompiledConfig 编译后不可变配置
type CompiledConfig struct {
	rules []Rule
}

// EvalContext 求值上下文
type EvalContext struct {
	Now           time.Time
	RequestModel  StringFact
	UpstreamModel StringFact
	QuotaWindows  []QuotaWindowFact

	// 通用事实扩展表，无需修改 switch 或核心树
	CustomStringFacts map[string]StringFact
	CustomNumberFacts map[string]NumberFact
}

// SchedulingMatch 调度规则命中快照
type SchedulingMatch struct {
	RuleID       string
	NameSnapshot string
	Domain       Domain
}

// SchedulingResult 调度求值结果
type SchedulingResult struct {
	Excluded bool
	Reason   *SchedulingMatch
}

// PricingMatch 定价倍率命中记录
type PricingMatch struct {
	RuleID       string
	NameSnapshot string
	Domain       Domain
	Factor       string
	Multiplier   pricing.PriceMultiplier
	BindingScope string
	Revision     uint64
}

// PricingResult 定价求值结果
type PricingResult struct {
	Matches []PricingMatch
}

// RuleStatus 规则评估状态
type RuleStatus string

const (
	RuleStatusHit            RuleStatus = "hit"
	RuleStatusMiss           RuleStatus = "miss"
	RuleStatusSkippedUnknown RuleStatus = "skipped_unknown"
	RuleStatusDisabled       RuleStatus = "disabled"
)

// NodeInspectResult 条件节点求值诊断
type NodeInspectResult struct {
	Kind          ConditionKind       `json:"kind"`
	Fact          string              `json:"fact,omitempty"`
	Truth         TruthValue          `json:"truth"`
	UnknownReason string              `json:"unknown_reason,omitempty"`
	Children      []NodeInspectResult `json:"children,omitempty"`
}

// RuleInspectResult 规则求值诊断
type RuleInspectResult struct {
	RuleID       string            `json:"rule_id"`
	NameSnapshot string            `json:"name_snapshot"`
	Domain       Domain            `json:"domain"`
	Enabled      bool              `json:"enabled"`
	Status       RuleStatus        `json:"status"`
	Condition    NodeInspectResult `json:"condition"`
	Action       Action            `json:"action"`
}

// InspectResult 配置求值诊断
type InspectResult struct {
	Rules []RuleInspectResult `json:"rules"`
}
