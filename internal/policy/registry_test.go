package policy

import (
	"testing"
)

func TestRegistryBasicOperations(t *testing.T) {
	reg := NewRegistry()

	// 成功注册
	err := reg.RegisterParam(ParamDescriptor{
		Key:           "test.param",
		Type:          ParamTypeString,
		Label:         "label",
		Description:   "desc",
		Operators:     []string{"eq"},
		Domains:       []Domain{DomainScheduling},
		BindingScopes: []string{"group"},
	})
	if err != nil {
		t.Fatalf("expected successful registration, got %v", err)
	}

	// 查出
	desc, ok := reg.FindParam("test.param")
	if !ok || desc.Key != "test.param" {
		t.Fatalf("FindParam failed")
	}

	// 重复注册报错
	err = reg.RegisterParam(ParamDescriptor{
		Key:           "test.param",
		Type:          ParamTypeString,
		Label:         "label",
		Description:   "desc",
		Operators:     []string{"eq"},
		Domains:       []Domain{DomainScheduling},
		BindingScopes: []string{"group"},
	})
	if err == nil {
		t.Fatal("expected duplicate key error, got nil")
	}

	// 空 key 报错
	err = reg.RegisterParam(ParamDescriptor{
		Key:       "",
		Type:      ParamTypeString,
		Operators: []string{"eq"},
		Domains:   []Domain{DomainScheduling},
	})
	if err == nil {
		t.Fatal("expected empty key error, got nil")
	}

	// 非法类型报错
	err = reg.RegisterParam(ParamDescriptor{
		Key:       "invalid.type",
		Type:      "unsupported_type",
		Operators: []string{"eq"},
		Domains:   []Domain{DomainScheduling},
	})
	if err == nil {
		t.Fatal("expected invalid type error, got nil")
	}

	// 缺少 operator
	err = reg.RegisterParam(ParamDescriptor{
		Key:     "no.op",
		Type:    ParamTypeString,
		Domains: []Domain{DomainScheduling},
	})
	if err == nil {
		t.Fatal("expected no operator error, got nil")
	}

	// 缺少 domain
	err = reg.RegisterParam(ParamDescriptor{
		Key:       "no.domain",
		Type:      ParamTypeString,
		Operators: []string{"eq"},
	})
	if err == nil {
		t.Fatal("expected no domain error, got nil")
	}

	// 非法 domain
	err = reg.RegisterParam(ParamDescriptor{
		Key:       "invalid.domain",
		Type:      ParamTypeString,
		Operators: []string{"eq"},
		Domains:   []Domain{"bad_domain"},
	})
	if err == nil {
		t.Fatal("expected invalid domain error, got nil")
	}

	// 列表
	list := reg.ListParams()
	if len(list) != 1 {
		t.Fatalf("expected 1 param, got %d", len(list))
	}
}

func TestRegistryPredicateAndAction(t *testing.T) {
	reg := NewRegistry()
	predicates := reg.ListPredicates()
	if len(predicates) != 1 || predicates[0].Name != "time_window" {
		t.Fatalf("unexpected predicates: %+v", predicates)
	}
	predicates[0].Domains[0] = "mutated"
	predicate, ok := reg.FindPredicate("time_window")
	if !ok || predicate.Domains[0] != DomainScheduling {
		t.Fatal("predicate discovery leaked mutable state")
	}
	actions := reg.ListActions()
	if len(actions) != 2 || actions[0].Type != ActionExcludeCandidate || actions[1].Type != ActionMultiplyPrice {
		t.Fatalf("unexpected actions: %+v", actions)
	}
	actions[0].Fields[0] = "mutated"
	action, ok := reg.FindAction(ActionExcludeCandidate)
	if !ok || action.Fields[0] != "type" {
		t.Fatal("action discovery leaked mutable state")
	}
}

func TestRegistryRejectsQuotaSelectorWithoutRequiredWindow(t *testing.T) {
	reg := NewRegistry()
	err := reg.RegisterParam(ParamDescriptor{
		Key:           "credential.quota.remaining_ratio",
		Type:          ParamTypeNumber,
		Operators:     []string{"eq"},
		Domains:       []Domain{DomainScheduling},
		BindingScopes: []string{"credential"},
		SelectorConstraint: &SelectorConstraint{
			Scope:                 "account",
			WindowSecondsRequired: false,
		},
		Reducers: []string{"min"},
	})
	if err == nil {
		t.Fatal("expected quota selector without required window to be rejected")
	}
}

func TestDefaultRegistryContents(t *testing.T) {
	// 验证默认注册表首批内置定义
	reqModel, ok := DefaultRegistry.FindParam("request.model")
	if !ok || reqModel.Type != ParamTypeString {
		t.Fatal("request.model missing or invalid in DefaultRegistry")
	}
	upModel, ok := DefaultRegistry.FindParam("upstream.model")
	if !ok || upModel.Type != ParamTypeString {
		t.Fatal("upstream.model missing or invalid in DefaultRegistry")
	}
	quotaRatio, ok := DefaultRegistry.FindParam("credential.quota.remaining_ratio")
	if !ok || quotaRatio.Type != ParamTypeNumber {
		t.Fatal("credential.quota.remaining_ratio missing or invalid in DefaultRegistry")
	}
	if _, ok := DefaultRegistry.FindPredicate("time_window"); !ok {
		t.Fatal("time_window predicate missing in DefaultRegistry")
	}
	if _, ok := DefaultRegistry.FindAction(ActionExcludeCandidate); !ok {
		t.Fatal("exclude_candidate action missing in DefaultRegistry")
	}
	if _, ok := DefaultRegistry.FindAction(ActionMultiplyPrice); !ok {
		t.Fatal("multiply_price action missing in DefaultRegistry")
	}
}
