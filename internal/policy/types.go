package policy

import (
	"slices"
	"time"

	"gpt-load/internal/pricing"
)

// ValidationError 结构化校验错误：字段路径 + 可读原因
type ValidationError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return e.Path + ": " + e.Message
}

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
	ActionExcludeModels    ActionType = "exclude_models"
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

// StringFactFromModel 由已解析的模型名构造字符串事实：空模型名视为未知，非空视为已测量。
func StringFactFromModel(value string) StringFact {
	if value == "" {
		return StringFact{State: FactStateUnknown}
	}
	return StringFact{Value: value, State: FactStateMeasured}
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

// SameSample 判断两个额度窗口事实是否来自同一来源、同一身份代次的同一次采样。
// 仅用于冲突判定；状态归一化与当前时间判断仍由各调用点负责。
func (f QuotaWindowFact) SameSample(other QuotaWindowFact) bool {
	return f.Scope == other.Scope &&
		f.WindowSeconds == other.WindowSeconds &&
		f.SourceID != "" &&
		f.SourceID == other.SourceID &&
		f.IdentityGeneration == other.IdentityGeneration &&
		!f.ResetAt.IsZero() && !other.ResetAt.IsZero() &&
		!f.ObservedAt.IsZero() && !other.ObservedAt.IsZero() &&
		f.ResetAt.Equal(other.ResetAt) &&
		f.ObservedAt.Equal(other.ObservedAt)
}

// CloneQuotaWindows 返回 QuotaWindowFact 切片的独立深拷贝。
func CloneQuotaWindows(facts []QuotaWindowFact) []QuotaWindowFact {
	return slices.Clone(facts)
}

// Action 规则执行动作
type Action struct {
	Type       ActionType
	Models     []string                // exclude_models 动作的模型列表（预排序并去重）
	Factor     string                  // pricing 动作的原始因子字符串
	Multiplier pricing.PriceMultiplier // 解析后的六位小数倍率
}

// ContainsModel 判断模型是否在 Action.Models 列表中（二分查找，大小写敏感）
func (a Action) ContainsModel(model string) bool {
	if len(a.Models) == 0 || model == "" {
		return false
	}
	_, found := slices.BinarySearch(a.Models, model)
	return found
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
	Selector  *QuotaSelector

	// 时间段谓词形态
	Weekdays []int
	Ranges   []TimeRange
}

// Rule 一条完整规则
type Rule struct {
	ID      string
	Name    string
	Domain  Domain // 兼容可选保留或默认 scheduling
	Enabled bool
	When    *ConditionNode
	Actions []Action
}

// CompiledConfig 编译后不可变配置
type CompiledConfig struct {
	rules       []Rule
	groupPolicy GroupPolicyMode
	nodeCount   int
}

// GroupPolicyMode 决定凭据绑定是继承分组规则还是覆盖分组规则。
type GroupPolicyMode string

const (
	GroupPolicyInherit  GroupPolicyMode = "inherit"
	GroupPolicyOverride GroupPolicyMode = "override"
)

// GroupPolicyMode 返回配置声明的分组策略模式；未声明或 nil 接收者一律视为 inherit。
func (c *CompiledConfig) GroupPolicyMode() GroupPolicyMode {
	if c == nil || c.groupPolicy == "" {
		return GroupPolicyInherit
	}
	return c.groupPolicy
}

// NodeCount 返回已编译配置中包含的条件节点总数。
func (c *CompiledConfig) NodeCount() int {
	if c == nil {
		return 0
	}
	return c.nodeCount
}

// EvalContext 求值上下文
type EvalContext struct {
	Now           time.Time
	QuotaNow      time.Time
	RequestModel  StringFact
	UpstreamModel StringFact
	QuotaWindows  []QuotaWindowFact

	// 通用事实扩展表，无需修改 switch 或核心树
	CustomStringFacts map[string]StringFact
	CustomNumberFacts map[string]NumberFact
}

// EffectiveQuotaNow 返回额度窗口判断时生效的时间戳。
// 若未显式设置 QuotaNow（如生产热路径），回退为 Now；
// 若显式设置（如预览时模拟时间仅作用于时间段谓词），则优先返回真实的 QuotaNow，确保未来模拟不推演额度重置。
func (ctx *EvalContext) EffectiveQuotaNow() time.Time {
	if ctx != nil && !ctx.QuotaNow.IsZero() {
		return ctx.QuotaNow
	}
	if ctx != nil {
		return ctx.Now
	}
	return time.Time{}
}

// SchedulingMatch 调度规则命中快照
type SchedulingMatch struct {
	RuleID        string
	NameSnapshot  string
	Domain        Domain
	ActionType    ActionType
	ExcludedModel string
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
