package policy

import (
	"fmt"
	"sync/atomic"
)

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

// NewRuntimeView 从已编译的配置创建深拷贝不可变视图
func NewRuntimeView(groups map[uint]*CompiledConfig, credentials map[uint]*CompiledConfig) *RuntimeView {
	g := make(map[uint]BoundPolicy, len(groups))
	for k, v := range groups {
		if v != nil {
			g[k] = BoundPolicy{
				Scope:    "group",
				GroupID:  k,
				Revision: 1,
				Config:   v.Clone(),
			}
		}
	}
	c := make(map[uint]BoundPolicy, len(credentials))
	for k, v := range credentials {
		if v != nil {
			c[k] = BoundPolicy{
				Scope:        "credential",
				CredentialID: k,
				Revision:     1,
				Config:       v.Clone(),
			}
		}
	}
	return &RuntimeView{
		groups:      g,
		credentials: c,
	}
}

// CompileRuntimeView 编译一组持久化绑定配置并返回不可变 RuntimeView。
// 若任一绑定配置存在语法或约束错误，整体编译失败并返回错误，绝不生成损坏或部分生效的视图。
func CompileRuntimeView(bindings []BindingConfig) (*RuntimeView, error) {
	groups := make(map[uint]BoundPolicy)
	credentials := make(map[uint]BoundPolicy)

	for _, b := range bindings {
		switch b.Scope {
		case "group":
			if b.GroupID == 0 || b.CredentialID != 0 {
				return nil, fmt.Errorf("invalid group policy binding target")
			}
			if _, duplicate := groups[b.GroupID]; duplicate {
				return nil, fmt.Errorf("duplicate group policy binding for group %d", b.GroupID)
			}
		case "credential":
			if b.CredentialID == 0 || b.GroupID == 0 {
				return nil, fmt.Errorf("invalid credential policy binding target")
			}
			if _, duplicate := credentials[b.CredentialID]; duplicate {
				return nil, fmt.Errorf("duplicate credential policy binding for credential %d", b.CredentialID)
			}
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

// Clone 返回 RuntimeView 的完整深拷贝
func (v *RuntimeView) Clone() *RuntimeView {
	if v == nil {
		return NewEmptyRuntimeView()
	}
	g := make(map[uint]BoundPolicy, len(v.groups))
	for k, bp := range v.groups {
		bpClone := bp
		if bp.Config != nil {
			bpClone.Config = bp.Config.Clone()
		}
		g[k] = bpClone
	}
	c := make(map[uint]BoundPolicy, len(v.credentials))
	for k, bp := range v.credentials {
		bpClone := bp
		if bp.Config != nil {
			bpClone.Config = bp.Config.Clone()
		}
		c[k] = bpClone
	}
	return &RuntimeView{
		groups:      g,
		credentials: c,
	}
}

// GroupPolicy 获取指定分组 ID 的已编译策略；未配置或未生效返回 nil
func (v *RuntimeView) GroupPolicy(groupID uint) *CompiledConfig {
	if v == nil || v.groups == nil {
		return nil
	}
	bp, ok := v.groups[groupID]
	if !ok {
		return nil
	}
	return bp.Config
}

// CredentialPolicy 获取指定凭据 ID 的已编译策略；未配置或未生效返回 nil
func (v *RuntimeView) CredentialPolicy(credentialID uint) *CompiledConfig {
	if v == nil || v.credentials == nil {
		return nil
	}
	bp, ok := v.credentials[credentialID]
	if !ok {
		return nil
	}
	return bp.Config
}

// EvalCandidate 针对候选凭据及目标执行调度域准入评估。
// 遵循逐账号语义 (account-wise semantics)：
// 1. 若当前分组存在已启用调度规则且命中 exclude_candidate，返回 excluded=true
// 2. 若当前凭据存在已启用调度规则且命中 exclude_candidate，返回 excluded=true
// 3. 否则返回 excluded=false
func (v *RuntimeView) EvalCandidate(groupID, credentialID uint, ctx *EvalContext) (bool, *SchedulingMatch) {
	if v == nil || ctx == nil {
		return false, nil
	}
	if gp := v.GroupPolicy(groupID); gp != nil {
		if res := gp.EvalScheduling(ctx); res.Excluded {
			return true, res.Reason
		}
	}
	if cp := v.CredentialPolicy(credentialID); cp != nil {
		if res := cp.EvalScheduling(ctx); res.Excluded {
			return true, res.Reason
		}
	}
	return false, nil
}

// EvalPricing 针对候选凭据及目标执行定价域倍率评估。
// 顺序保证：首先评估当前分组规则（按保存列表顺序），然后评估当前凭据规则（按保存列表顺序）。
// 所有确定命中 (TruthTrue) 的规则加入有序因子链（包括 x0、x1、总乘积为 1 等）；
// 未决 (TruthUnknown) 或未命中 (TruthFalse) 的规则跳过并不阻断后续规则。
func (v *RuntimeView) EvalPricing(groupID, credentialID uint, ctx *EvalContext) []PricingMatch {
	if v == nil || ctx == nil {
		return nil
	}
	var matches []PricingMatch
	if gp, ok := v.groups[groupID]; ok && gp.Config != nil {
		res := gp.Config.EvalPricing(ctx)
		for _, m := range res.Matches {
			m.BindingScope = "group"
			m.Revision = gp.Revision
			matches = append(matches, m)
		}
	}
	if cp, ok := v.credentials[credentialID]; ok && cp.Config != nil {
		res := cp.Config.EvalPricing(ctx)
		for _, m := range res.Matches {
			m.BindingScope = "credential"
			m.Revision = cp.Revision
			matches = append(matches, m)
		}
	}
	return matches
}

// Runtime 管理不可变 RuntimeView 的发布与原子指针切换。
// 编译失败或无效发布保留上一版有效视图 (stale/failed publication retaining the last valid runtime view)。
type Runtime struct {
	current atomic.Pointer[RuntimeView]
}

// NewRuntime 创建初始化为空视图的策略发布运行时
func NewRuntime() *Runtime {
	r := &Runtime{}
	r.current.Store(NewEmptyRuntimeView())
	return r
}

// Load 读取当前原子存储的不可变视图
func (r *Runtime) Load() *RuntimeView {
	if r == nil {
		return nil
	}
	view := r.current.Load()
	if view == nil {
		return NewEmptyRuntimeView()
	}
	return view
}

// Publish 原子发布新的已编译视图
func (r *Runtime) Publish(view *RuntimeView) {
	if r == nil || view == nil {
		return
	}
	r.current.Store(view)
}

// PublishBindings 编译并原子发布绑定；若编译失败则返回错误并保持当前视图不变
func (r *Runtime) PublishBindings(bindings []BindingConfig) error {
	if r == nil {
		return nil
	}
	view, err := CompileRuntimeView(bindings)
	if err != nil {
		return err
	}
	r.Publish(view)
	return nil
}
