package policy

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
)

// Registry 保存参数、谓词与动作的元数据描述
type Registry struct {
	mu     sync.RWMutex
	params map[string]ParamDescriptor
}

// NewRegistry 创建支持扩展普通参数的注册表；谓词和动作固定为引擎内置实现。
func NewRegistry() *Registry {
	return &Registry{params: make(map[string]ParamDescriptor)}
}

var builtinActions = []ActionDescriptor{
	{Type: ActionExcludeCandidate, Domain: DomainScheduling,
		Label: "action.exclude_candidate.label", Description: "action.exclude_candidate.desc", Fields: []string{"type"}},
	{Type: ActionMultiplyPrice, Domain: DomainPricing,
		Label: "action.multiply_price.label", Description: "action.multiply_price.desc", Fields: []string{"type", "factor"}},
}

var builtinPredicates = []PredicateDescriptor{
	{Name: "time_window", Label: "predicate.time_window.label", Description: "predicate.time_window.desc",
		Domains: []Domain{DomainScheduling, DomainPricing}},
}

func cloneParamDescriptor(d ParamDescriptor) ParamDescriptor {
	res := d
	res.Operators = slices.Clone(d.Operators)
	res.Domains = slices.Clone(d.Domains)
	res.BindingScopes = slices.Clone(d.BindingScopes)
	res.Reducers = slices.Clone(d.Reducers)
	if d.SelectorConstraint != nil {
		res.SelectorConstraint = &SelectorConstraint{
			Scope:                 d.SelectorConstraint.Scope,
			WindowSecondsRequired: d.SelectorConstraint.WindowSecondsRequired,
		}
	}
	return res
}

func clonePredicateDescriptor(d PredicateDescriptor) PredicateDescriptor {
	res := d
	res.Domains = slices.Clone(d.Domains)
	return res
}

func cloneActionDescriptor(d ActionDescriptor) ActionDescriptor {
	res := d
	res.Fields = slices.Clone(d.Fields)
	return res
}

// RegisterParam 注册参数描述，验证操作符类型兼容性、作用域并深拷贝存储
func (r *Registry) RegisterParam(desc ParamDescriptor) error {
	if desc.Key == "" {
		return errors.New("parameter key cannot be empty")
	}
	if !desc.Type.Valid() {
		return fmt.Errorf("invalid parameter type %q", desc.Type)
	}
	if len(desc.Operators) == 0 {
		return fmt.Errorf("parameter %q must declare at least one operator", desc.Key)
	}

	// 校验操作符与参数类型的严格兼容性
	for _, op := range desc.Operators {
		switch desc.Type {
		case ParamTypeString:
			if op != "eq" && op != "in" {
				return fmt.Errorf("string parameter %q cannot use operator %q (allowed: eq, in)", desc.Key, op)
			}
		case ParamTypeNumber:
			if op != "eq" && op != "lt" && op != "lte" && op != "gt" && op != "gte" {
				return fmt.Errorf("number parameter %q cannot use operator %q (allowed: eq, lt, lte, gt, gte)", desc.Key, op)
			}
		}
	}

	if len(desc.Domains) == 0 {
		return fmt.Errorf("parameter %q must declare at least one domain", desc.Key)
	}
	for _, d := range desc.Domains {
		if !d.Valid() {
			return fmt.Errorf("parameter %q declares invalid domain %q", desc.Key, d)
		}
	}

	if len(desc.BindingScopes) == 0 {
		return fmt.Errorf("parameter %q must declare at least one binding scope", desc.Key)
	}
	for _, s := range desc.BindingScopes {
		if s != "group" && s != "credential" {
			return fmt.Errorf("parameter %q declares invalid binding scope %q (allowed: group, credential)", desc.Key, s)
		}
	}

	// 本阶段 selectorConstraint 只支持显式额度合同 (credential.quota.remaining_ratio)
	if desc.SelectorConstraint != nil {
		if desc.Key != "credential.quota.remaining_ratio" {
			return fmt.Errorf("parameter %q cannot declare selector constraint: only explicit quota contract supported", desc.Key)
		}
		if desc.SelectorConstraint.Scope != "account" {
			return fmt.Errorf("quota parameter must specify scope 'account'")
		}
		if !desc.SelectorConstraint.WindowSecondsRequired {
			return fmt.Errorf("quota parameter must require window_seconds")
		}
		if len(desc.Reducers) != 1 || desc.Reducers[0] != "min" {
			return fmt.Errorf("quota parameter must specify reducers ['min']")
		}
	} else if len(desc.Reducers) > 0 {
		return fmt.Errorf("parameter %q cannot declare reducers without selector constraint", desc.Key)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.params[desc.Key]; exists {
		return fmt.Errorf("duplicate parameter key %q", desc.Key)
	}
	r.params[desc.Key] = cloneParamDescriptor(desc)
	return nil
}

// FindParam 查询参数描述（返回深拷贝副本）
func (r *Registry) FindParam(key string) (ParamDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	desc, ok := r.params[key]
	if !ok {
		return ParamDescriptor{}, false
	}
	return cloneParamDescriptor(desc), true
}

// ListParams 列出所有参数描述（按 Key 字典序稳定排序并深拷贝）
func (r *Registry) ListParams() []ParamDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]ParamDescriptor, 0, len(r.params))
	for _, desc := range r.params {
		list = append(list, cloneParamDescriptor(desc))
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Key < list[j].Key
	})
	return list
}

// FindPredicate 查询内置谓词的独立描述副本。
func (r *Registry) FindPredicate(name string) (PredicateDescriptor, bool) {
	for _, desc := range builtinPredicates {
		if desc.Name == name {
			return clonePredicateDescriptor(desc), true
		}
	}
	return PredicateDescriptor{}, false
}

// ListPredicates 按名字序返回内置谓词的独立描述副本。
func (r *Registry) ListPredicates() []PredicateDescriptor {
	list := make([]PredicateDescriptor, len(builtinPredicates))
	for i, desc := range builtinPredicates {
		list[i] = clonePredicateDescriptor(desc)
	}
	return list
}

// FindAction 查询内置动作的独立描述副本。
func (r *Registry) FindAction(actionType ActionType) (ActionDescriptor, bool) {
	for _, desc := range builtinActions {
		if desc.Type == actionType {
			return cloneActionDescriptor(desc), true
		}
	}
	return ActionDescriptor{}, false
}

// ListActions 按类型序返回内置动作的独立描述副本。
func (r *Registry) ListActions() []ActionDescriptor {
	list := make([]ActionDescriptor, len(builtinActions))
	for i, desc := range builtinActions {
		list[i] = cloneActionDescriptor(desc)
	}
	return list
}

// DefaultRegistry 默认全局注册表
var DefaultRegistry = NewRegistry()

func init() {
	// 注册 request.model
	_ = DefaultRegistry.RegisterParam(ParamDescriptor{
		Key:           "request.model",
		Type:          ParamTypeString,
		Label:         "fact.request.model.label",
		Description:   "fact.request.model.desc",
		Operators:     []string{"eq", "in"},
		Domains:       []Domain{DomainScheduling, DomainPricing},
		BindingScopes: []string{"group", "credential"},
	})

	// 注册 upstream.model
	_ = DefaultRegistry.RegisterParam(ParamDescriptor{
		Key:           "upstream.model",
		Type:          ParamTypeString,
		Label:         "fact.upstream.model.label",
		Description:   "fact.upstream.model.desc",
		Operators:     []string{"eq", "in"},
		Domains:       []Domain{DomainScheduling, DomainPricing},
		BindingScopes: []string{"group", "credential"},
	})

	// 注册 credential.quota.remaining_ratio
	// 按合同分组逐账号规则也能读当前候选额度，BindingScopes 必须为 group 和 credential
	_ = DefaultRegistry.RegisterParam(ParamDescriptor{
		Key:           "credential.quota.remaining_ratio",
		Type:          ParamTypeNumber,
		Unit:          "ratio",
		Label:         "fact.credential.quota.remaining_ratio.label",
		Description:   "fact.credential.quota.remaining_ratio.desc",
		Operators:     []string{"eq", "lt", "lte", "gt", "gte"},
		Domains:       []Domain{DomainScheduling, DomainPricing},
		BindingScopes: []string{"group", "credential"},
		SelectorConstraint: &SelectorConstraint{
			Scope:                 "account",
			WindowSecondsRequired: true,
		},
		Reducers: []string{"min"},
	})

}
