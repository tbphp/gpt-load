package policy

import (
	"slices"
	"testing"
)

// TestBuiltinParamsMetadata 断言内置参数元数据自洽：类型与操作符兼容、域与绑定作用域合法、额度合同形状固定。
func TestBuiltinParamsMetadata(t *testing.T) {
	want := []struct {
		key       string
		paramType ParamType
		operators []string
	}{
		{"credential.quota.remaining_ratio", ParamTypeNumber, []string{"eq", "lt", "lte", "gt", "gte"}},
		{"request.model", ParamTypeString, []string{"eq", "in"}},
		{"upstream.model", ParamTypeString, []string{"eq", "in"}},
	}
	if len(BuiltinParams) != len(want) {
		t.Fatalf("builtin param count = %d, want %d", len(BuiltinParams), len(want))
	}

	for i, tc := range want {
		desc := BuiltinParams[i]
		if desc.Key != tc.key || desc.Type != tc.paramType {
			t.Fatalf("BuiltinParams[%d] = %q/%q, want %q/%q", i, desc.Key, desc.Type, tc.key, tc.paramType)
		}
		if !slices.Equal(desc.Operators, tc.operators) {
			t.Errorf("%s operators = %v, want %v", desc.Key, desc.Operators, tc.operators)
		}
		if desc.Label == "" || desc.Description == "" {
			t.Errorf("%s missing label or description", desc.Key)
		}
		for _, op := range desc.Operators {
			allowed := []string{"eq", "in"}
			if desc.Type == ParamTypeNumber {
				allowed = []string{"eq", "lt", "lte", "gt", "gte"}
			}
			if !slices.Contains(allowed, op) {
				t.Errorf("%s (%s) declares incompatible operator %q", desc.Key, desc.Type, op)
			}
		}
		if len(desc.Domains) == 0 {
			t.Errorf("%s declares no domain", desc.Key)
		}
		for _, d := range desc.Domains {
			if !d.Valid() {
				t.Errorf("%s declares invalid domain %q", desc.Key, d)
			}
		}
		if len(desc.BindingScopes) == 0 {
			t.Errorf("%s declares no binding scope", desc.Key)
		}
		for _, s := range desc.BindingScopes {
			if s != "group" && s != "credential" {
				t.Errorf("%s declares invalid binding scope %q", desc.Key, s)
			}
		}

		// selectorConstraint 只支持显式额度合同 credential.quota.remaining_ratio
		if desc.SelectorConstraint == nil {
			if len(desc.Reducers) > 0 {
				t.Errorf("%s declares reducers without selector constraint", desc.Key)
			}
			continue
		}
		if desc.Key != "credential.quota.remaining_ratio" {
			t.Errorf("%s cannot declare a selector constraint", desc.Key)
		}
		if desc.SelectorConstraint.Scope != "account" || !desc.SelectorConstraint.WindowSecondsRequired {
			t.Errorf("%s selector constraint = %+v, want account scope requiring window_seconds", desc.Key, desc.SelectorConstraint)
		}
		if !slices.Equal(desc.Reducers, []string{"min"}) {
			t.Errorf("%s reducers = %v, want [min]", desc.Key, desc.Reducers)
		}
	}

	if got := ListParams(); !slices.EqualFunc(got, BuiltinParams, func(a, b ParamDescriptor) bool { return a.Key == b.Key }) {
		t.Errorf("ListParams() = %v, want the static BuiltinParams slice", got)
	}
	if desc, ok := FindParam("request.model"); !ok || desc.Type != ParamTypeString {
		t.Errorf("FindParam(request.model) = %+v/%v", desc, ok)
	}
	if _, ok := FindParam("missing.param"); ok {
		t.Error("FindParam returned an unknown parameter")
	}
}

func TestBuiltinPredicateAndActionMetadata(t *testing.T) {
	if len(BuiltinPredicates) != 1 || BuiltinPredicates[0].Name != "time_window" {
		t.Fatalf("BuiltinPredicates = %+v", BuiltinPredicates)
	}
	if _, ok := FindPredicate("time_window"); !ok {
		t.Error("time_window predicate not found")
	}
	if _, ok := FindPredicate("unknown_predicate"); ok {
		t.Error("FindPredicate returned an unknown predicate")
	}

	actions := ListActions()
	wantTypes := []ActionType{ActionExcludeCandidate, ActionExcludeModels, ActionMultiplyPrice}
	if len(actions) != len(wantTypes) {
		t.Fatalf("ListActions() = %+v", actions)
	}
	for i, wantType := range wantTypes {
		if actions[i].Type != wantType {
			t.Errorf("ListActions()[%d].Type = %q, want %q", i, actions[i].Type, wantType)
		}
		if actions[i].Label == "" || actions[i].Description == "" || !actions[i].Domain.Valid() {
			t.Errorf("action %s metadata incomplete: %+v", wantType, actions[i])
		}
		if _, ok := FindAction(wantType); !ok {
			t.Errorf("FindAction(%s) not found", wantType)
		}
	}
	if _, ok := FindAction("unknown_action"); ok {
		t.Error("FindAction returned an unknown action")
	}
}
