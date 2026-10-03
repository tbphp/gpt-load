package policy

import "fmt"

// BindingConfig 封装用于构建 RuntimeView 的持久化策略绑定输入
type BindingConfig struct {
	Scope        string // "group" 或 "credential"
	GroupID      uint
	CredentialID uint
	Revision     uint64
	Config       []byte
}

// BoundPolicy 记录策略绑定的目标标识、版本与已编译配置
type BoundPolicy struct {
	Scope        string
	GroupID      uint
	CredentialID uint
	Revision     uint64
	Config       *CompiledConfig
}

// RuntimeView 是策略引擎在运行时的不可变只读视图
type RuntimeView struct {
	groups      map[uint]BoundPolicy
	credentials map[uint]BoundPolicy
}

// NewEmptyRuntimeView 返回不可变的空策略视图
func NewEmptyRuntimeView() *RuntimeView {
	return &RuntimeView{
		groups:      make(map[uint]BoundPolicy),
		credentials: make(map[uint]BoundPolicy),
	}
}

// CompileRuntimeView 编译一组持久化绑定配置并返回不可变 RuntimeView。
// 若任一绑定配置存在语法或约束错误，整体编译失败并返回错误，绝不生成损坏或部分生效的视图。
func CompileRuntimeView(bindings []BindingConfig) (*RuntimeView, error) {
	groups := make(map[uint]BoundPolicy)
	credentials := make(map[uint]BoundPolicy)
	seenGroups := make(map[uint]struct{})
	seenCredentials := make(map[uint]struct{})

	for _, b := range bindings {
		switch b.Scope {
		case "group":
			if b.GroupID == 0 || b.CredentialID != 0 {
				return nil, fmt.Errorf("invalid group policy binding target")
			}
			if _, duplicate := seenGroups[b.GroupID]; duplicate {
				return nil, fmt.Errorf("duplicate group policy binding for group %d", b.GroupID)
			}
			seenGroups[b.GroupID] = struct{}{}
		case "credential":
			if b.CredentialID == 0 || b.GroupID == 0 {
				return nil, fmt.Errorf("invalid credential policy binding target")
			}
			if _, duplicate := seenCredentials[b.CredentialID]; duplicate {
				return nil, fmt.Errorf("duplicate credential policy binding for credential %d", b.CredentialID)
			}
			seenCredentials[b.CredentialID] = struct{}{}
		default:
			return nil, fmt.Errorf("unsupported policy scope %q", b.Scope)
		}
		if len(b.Config) == 0 {
			continue
		}
		compiled, err := Compile(b.Config)
		if err != nil {
			return nil, fmt.Errorf("compile policy binding (scope=%s, group=%d, cred=%d): %w", b.Scope, b.GroupID, b.CredentialID, err)
		}
		if b.Scope == "group" && compiled.GroupPolicyMode() == GroupPolicyOverride {
			return nil, fmt.Errorf("group policy binding (group=%d) cannot use group_policy override", b.GroupID)
		}
		rev := b.Revision
		if rev == 0 {
			rev = 1
		}
		switch b.Scope {
		case "group":
			groups[b.GroupID] = BoundPolicy{
				Scope:    "group",
				GroupID:  b.GroupID,
				Revision: rev,
				Config:   compiled,
			}
		case "credential":
			credentials[b.CredentialID] = BoundPolicy{
				Scope:        "credential",
				GroupID:      b.GroupID,
				CredentialID: b.CredentialID,
				Revision:     rev,
				Config:       compiled,
			}
		}
	}

	return &RuntimeView{
		groups:      groups,
		credentials: credentials,
	}, nil
}

// GroupPolicy 获取指定分组 ID 的已编译策略；未配置或未生效返回 nil
func (v *RuntimeView) GroupPolicy(groupID uint) *CompiledConfig {
	if v == nil {
		return nil
	}
	return v.groups[groupID].Config
}

// GroupRevision 获取指定分组 ID 的策略绑定版本；未配置返回 0
func (v *RuntimeView) GroupRevision(groupID uint) uint64 {
	if v == nil {
		return 0
	}
	return v.groups[groupID].Revision
}

// CredentialPolicy 获取指定凭据 ID 的已编译策略；未配置或未生效返回 nil
func (v *RuntimeView) CredentialPolicy(credentialID uint) *CompiledConfig {
	if v == nil {
		return nil
	}
	return v.credentials[credentialID].Config
}

// CredentialRevision 获取指定凭据 ID 的策略绑定版本；未配置返回 0
func (v *RuntimeView) CredentialRevision(credentialID uint) uint64 {
	if v == nil {
		return 0
	}
	return v.credentials[credentialID].Revision
}

// HasEnabledPricingRules 返回配置中是否存在至少一条已启用的定价域规则
func (c *CompiledConfig) HasEnabledPricingRules() bool {
	if c == nil || len(c.rules) == 0 {
		return false
	}
	for _, r := range c.rules {
		if r.Enabled && r.Domain == DomainPricing {
			return true
		}
	}
	return false
}

// HasApplicablePricing 检查在当前分组与凭据的有效策略规则中，是否存在已启用的定价规则。
// 遵循 inherit (group + credential) 或 override (仅 credential) 模式，无需进行条件评估。
// 供网关/工作协程快速判断是否需要收集事实并执行定价评估。
func (v *RuntimeView) HasApplicablePricing(groupID, credentialID uint) bool {
	if v == nil {
		return false
	}
	gp, cp := v.effectivePolicies(groupID, credentialID)
	return gp.Config.HasEnabledPricingRules() || cp.Config.HasEnabledPricingRules()
}

// effectivePolicies 返回按应用顺序排列的分组与账号策略。
// inherit 允许账号追加规则，override 忽略分组规则；账号绑定必须归属当前分组。
func (v *RuntimeView) effectivePolicies(groupID, credentialID uint) (BoundPolicy, BoundPolicy) {
	if v == nil {
		return BoundPolicy{}, BoundPolicy{}
	}
	cp := v.credentials[credentialID]
	if cp.GroupID != groupID {
		cp = BoundPolicy{}
	}
	if cp.Config.GroupPolicyMode() == GroupPolicyOverride {
		return BoundPolicy{}, cp
	}
	return v.groups[groupID], cp
}

// EvalCandidate 针对候选凭据及目标执行调度域准入评估。
// 继承时任一分组或账号规则拒绝即排除，覆盖时仅评估账号规则。
func (v *RuntimeView) EvalCandidate(groupID, credentialID uint, ctx *EvalContext) (bool, *SchedulingMatch) {
	if v == nil || ctx == nil {
		return false, nil
	}
	gp, cp := v.effectivePolicies(groupID, credentialID)
	for _, bp := range [2]BoundPolicy{gp, cp} {
		if res := bp.Config.EvalScheduling(ctx); res.Excluded {
			return true, res.Reason
		}
	}
	return false, nil
}

// EvalPricing 针对候选凭据及目标执行定价域倍率评估。
// 继承时分组因子在前、账号因子在后；覆盖时仅保留账号因子。
func (v *RuntimeView) EvalPricing(groupID, credentialID uint, ctx *EvalContext) []PricingMatch {
	if v == nil || ctx == nil {
		return nil
	}
	gp, cp := v.effectivePolicies(groupID, credentialID)
	var matches []PricingMatch
	for _, bp := range [2]BoundPolicy{gp, cp} {
		res := bp.Config.EvalPricing(ctx)
		for i := range res.Matches {
			res.Matches[i].BindingScope = bp.Scope
			res.Matches[i].Revision = bp.Revision
		}
		if matches == nil {
			matches = res.Matches
		} else {
			matches = append(matches, res.Matches...)
		}
	}
	return matches
}
