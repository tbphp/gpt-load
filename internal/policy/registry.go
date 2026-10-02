package policy

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Registry 保存参数、谓词与动作的元数据描述
type Registry struct {
	mu         sync.RWMutex
	params     map[string]ParamDescriptor
	predicates map[string]PredicateDescriptor
	actions    map[ActionType]ActionDescriptor
}

// NewEmptyRegistry 创建不包含任何内置元数据的空注册表
func NewEmptyRegistry() *Registry {
	return &Registry{
		params:     make(map[string]ParamDescriptor),
		predicates: make(map[string]PredicateDescriptor),
		actions:    make(map[ActionType]ActionDescriptor),
	}
}

func registerBuiltins(r *Registry) {
	_ = r.RegisterAction(ActionDescriptor{
		Type:        ActionExcludeCandidate,
		Domain:      DomainScheduling,
		Label:       "action.exclude_candidate.label",
		Description: "action.exclude_candidate.desc",
		Fields:      []string{"type"},
	})
	_ = r.RegisterAction(ActionDescriptor{
		Type:        ActionMultiplyPrice,
		Domain:      DomainPricing,
		Label:       "action.multiply_price.label",
		Description: "action.multiply_price.desc",
		Fields:      []string{"type", "factor"},
	})
	_ = r.RegisterPredicate(PredicateDescriptor{
		Name:        "time_window",
		Label:       "predicate.time_window.label",
		Description: "predicate.time_window.desc",
		Domains:     []Domain{DomainScheduling, DomainPricing},
	})
}

// NewRegistry 创建包含引擎内置动作与内置谓词的基础注册表，便于在此基础上注册扩展参数
func NewRegistry() *Registry {
	r := NewEmptyRegistry()
	registerBuiltins(r)
	return r
}

func cloneParamDescriptor(d ParamDescriptor) ParamDescriptor {
	res := d
	if d.Operators != nil {
		res.Operators = make([]string, len(d.Operators))
		copy(res.Operators, d.Operators)
	}
	if d.Domains != nil {
		res.Domains = make([]Domain, len(d.Domains))
		copy(res.Domains, d.Domains)
	}
	if d.BindingScopes != nil {
		res.BindingScopes = make([]string, len(d.BindingScopes))
		copy(res.BindingScopes, d.BindingScopes)
	}
	if d.Reducers != nil {
		res.Reducers = make([]string, len(d.Reducers))
		copy(res.Reducers, d.Reducers)
	}
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
	if d.Domains != nil {
		res.Domains = make([]Domain, len(d.Domains))
		copy(res.Domains, d.Domains)
	}
	return res
}

func cloneActionDescriptor(d ActionDescriptor) ActionDescriptor {
	res := d
	if d.Fields != nil {
		res.Fields = make([]string, len(d.Fields))
		copy(res.Fields, d.Fields)
	}
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

// RegisterPredicate 注册谓词描述
func (r *Registry) RegisterPredicate(desc PredicateDescriptor) error {
	if desc.Name == "" {
		return errors.New("predicate name cannot be empty")
	}
	if len(desc.Domains) == 0 {
		return fmt.Errorf("predicate %q must declare at least one domain", desc.Name)
	}
	for _, d := range desc.Domains {
		if !d.Valid() {
			return fmt.Errorf("predicate %q declares invalid domain %q", desc.Name, d)
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.predicates[desc.Name]; exists {
		return fmt.Errorf("duplicate predicate %q", desc.Name)
	}
	r.predicates[desc.Name] = clonePredicateDescriptor(desc)
	return nil
}

// FindPredicate 查询谓词描述（返回深拷贝副本）
func (r *Registry) FindPredicate(name string) (PredicateDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	desc, ok := r.predicates[name]
	if !ok {
		return PredicateDescriptor{}, false
	}
	return clonePredicateDescriptor(desc), true
}

// ListPredicates 列出所有谓词描述（按 Name 字典序稳定排序并深拷贝）
func (r *Registry) ListPredicates() []PredicateDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]PredicateDescriptor, 0, len(r.predicates))
	for _, desc := range r.predicates {
		list = append(list, clonePredicateDescriptor(desc))
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
	return list
}

// RegisterAction 注册动作描述
func (r *Registry) RegisterAction(desc ActionDescriptor) error {
	if desc.Type == "" {
		return errors.New("action type cannot be empty")
	}
	if !desc.Domain.Valid() {
		return fmt.Errorf("invalid action domain %q", desc.Domain)
	}
	if len(desc.Fields) == 0 {
		return fmt.Errorf("action %q must declare at least one field", desc.Type)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.actions[desc.Type]; exists {
		return fmt.Errorf("duplicate action %q", desc.Type)
	}
	r.actions[desc.Type] = cloneActionDescriptor(desc)
	return nil
}

// FindAction 查询动作描述（返回深拷贝副本）
func (r *Registry) FindAction(actionType ActionType) (ActionDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	desc, ok := r.actions[actionType]
	if !ok {
		return ActionDescriptor{}, false
	}
	return cloneActionDescriptor(desc), true
}

// ListActions 列出所有动作描述（按 Type 字典序稳定排序并深拷贝）
func (r *Registry) ListActions() []ActionDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]ActionDescriptor, 0, len(r.actions))
	for _, desc := range r.actions {
		list = append(list, cloneActionDescriptor(desc))
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Type < list[j].Type
	})
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
