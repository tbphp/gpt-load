package policy

// BuiltinParams 内置参数元数据：静态只读，按 Key 字典序排列。调用方只可读取，不得修改切片内容。
var BuiltinParams = []ParamDescriptor{
	{
		Key:           "credential.quota.remaining_ratio",
		Type:          ParamTypeNumber,
		Unit:          "ratio",
		Label:         "fact.credential.quota.remaining_ratio.label",
		Description:   "fact.credential.quota.remaining_ratio.desc",
		Operators:     []string{"eq", "lt", "lte", "gt", "gte"},
		Domains:       []Domain{DomainScheduling, DomainPricing},
		BindingScopes: []string{"group", "credential"},
		// 按合同分组逐账号规则也能读当前候选额度，因此 BindingScopes 必须包含 group 与 credential
		SelectorConstraint: &SelectorConstraint{
			Scope:                 "account",
			WindowSecondsRequired: true,
		},
		Reducers: []string{"min"},
	},
	{
		Key:           "request.model",
		Type:          ParamTypeString,
		Label:         "fact.request.model.label",
		Description:   "fact.request.model.desc",
		Operators:     []string{"eq", "in"},
		Domains:       []Domain{DomainScheduling, DomainPricing},
		BindingScopes: []string{"group", "credential"},
	},
	{
		Key:           "upstream.model",
		Type:          ParamTypeString,
		Label:         "fact.upstream.model.label",
		Description:   "fact.upstream.model.desc",
		Operators:     []string{"eq", "in"},
		Domains:       []Domain{DomainScheduling, DomainPricing},
		BindingScopes: []string{"group", "credential"},
	},
}

// BuiltinPredicates 内置谓词元数据：静态只读，按 Name 字典序排列。
var BuiltinPredicates = []PredicateDescriptor{
	{Name: "time_window", Label: "predicate.time_window.label", Description: "predicate.time_window.desc",
		Domains: []Domain{DomainScheduling, DomainPricing}},
}

// BuiltinActions 内置动作元数据：静态只读，按 Type 字典序排列。
var BuiltinActions = []ActionDescriptor{
	{Type: ActionExcludeCandidate, Domain: DomainScheduling,
		Label: "action.exclude_candidate.label", Description: "action.exclude_candidate.desc"},
	{Type: ActionExcludeModels, Domain: DomainScheduling,
		Label: "action.exclude_models.label", Description: "action.exclude_models.desc"},
	{Type: ActionMultiplyPrice, Domain: DomainPricing,
		Label: "action.multiply_price.label", Description: "action.multiply_price.desc"},
}

// FindParam 查询内置参数描述
func FindParam(key string) (ParamDescriptor, bool) {
	for _, desc := range BuiltinParams {
		if desc.Key == key {
			return desc, true
		}
	}
	return ParamDescriptor{}, false
}

// ListParams 返回内置参数描述（静态切片，按 Key 字典序）
func ListParams() []ParamDescriptor {
	return BuiltinParams
}

// FindPredicate 查询内置谓词描述
func FindPredicate(name string) (PredicateDescriptor, bool) {
	for _, desc := range BuiltinPredicates {
		if desc.Name == name {
			return desc, true
		}
	}
	return PredicateDescriptor{}, false
}

// ListPredicates 返回内置谓词描述（静态切片，按 Name 字典序）
func ListPredicates() []PredicateDescriptor {
	return BuiltinPredicates
}

// FindAction 查询内置动作描述
func FindAction(actionType ActionType) (ActionDescriptor, bool) {
	for _, desc := range BuiltinActions {
		if desc.Type == actionType {
			return desc, true
		}
	}
	return ActionDescriptor{}, false
}

// ListActions 返回内置动作描述（静态切片，按 Type 字典序）
func ListActions() []ActionDescriptor {
	return BuiltinActions
}
