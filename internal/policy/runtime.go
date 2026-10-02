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
	Config       []byte
}

// RuntimeView 是策略引擎在运行时的不可变只读视图
type RuntimeView struct {
	groups      map[uint]*CompiledConfig
	credentials map[uint]*CompiledConfig
}

// NewEmptyRuntimeView 返回不可变的空策略视图
func NewEmptyRuntimeView() *RuntimeView {
	return &RuntimeView{
		groups:      make(map[uint]*CompiledConfig),
		credentials: make(map[uint]*CompiledConfig),
	}
}

// NewRuntimeView 从已编译的配置创建深拷贝不可变视图
func NewRuntimeView(groups map[uint]*CompiledConfig, credentials map[uint]*CompiledConfig) *RuntimeView {
	g := make(map[uint]*CompiledConfig, len(groups))
	for k, v := range groups {
		if v != nil {
			g[k] = v.Clone()
		}
	}
	c := make(map[uint]*CompiledConfig, len(credentials))
	for k, v := range credentials {
		if v != nil {
			c[k] = v.Clone()
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
	groups := make(map[uint]*CompiledConfig)
	credentials := make(map[uint]*CompiledConfig)

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
		switch b.Scope {
		case "group":
			groups[b.GroupID] = compiled
		case "credential":
			credentials[b.CredentialID] = compiled
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
	return NewRuntimeView(v.groups, v.credentials)
}

// GroupPolicy 获取指定分组 ID 的已编译策略；未配置或未生效返回 nil
func (v *RuntimeView) GroupPolicy(groupID uint) *CompiledConfig {
	if v == nil || v.groups == nil {
		return nil
	}
	return v.groups[groupID]
}

// CredentialPolicy 获取指定凭据 ID 的已编译策略；未配置或未生效返回 nil
func (v *RuntimeView) CredentialPolicy(credentialID uint) *CompiledConfig {
	if v == nil || v.credentials == nil {
		return nil
	}
	return v.credentials[credentialID]
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
