package policy

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func TestStructuredValidationErrors(t *testing.T) {
	// 1. 验证 rules[0].when.any[1].value 路径及 code
	jsonWithDeepError := `{
		"schema_version": 1,
		"rules": [{
			"id": "r1", "name": "R1", "domain": "scheduling", "enabled": true,
			"when": {
				"any": [
					{"fact": "request.model", "op": "eq", "value": "Astra"},
					{"fact": "request.model", "op": "eq", "value": " InvalidWhitespace "}
				]
			},
			"then": {"type": "exclude_candidate"}
		}]
	}`
	_, err := Compile([]byte(jsonWithDeepError))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var valErr *ValidationError
	if !errors.As(err, &valErr) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if valErr.Path != "rules[0].when.any[1].value" {
		t.Fatalf("expected path rules[0].when.any[1].value, got %q", valErr.Path)
	}
	if valErr.Code != ErrCodeInvalidValue {
		t.Fatalf("expected code ERR_INVALID_VALUE, got %q", valErr.Code)
	}

	// 2. 验证 rules[0].then.factor 路径及 code
	jsonWithFactorError := `{
		"schema_version": 1,
		"rules": [{
			"id": "r1", "name": "R1", "domain": "pricing", "enabled": true,
			"when": {"fact": "request.model", "op": "eq", "value": "Astra"},
			"then": {"type": "multiply_price", "factor": "1005"}
		}]
	}`
	_, err = Compile([]byte(jsonWithFactorError))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.As(err, &valErr) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if valErr.Path != "rules[0].then.factor" {
		t.Fatalf("expected path rules[0].then.factor, got %q", valErr.Path)
	}

	// 3. 验证 duplicate key 错误代码与路径
	jsonWithDupKey := `{
		"schema_version": 1,
		"rules": [{
			"id": "r1", "name": "R1", "domain": "scheduling", "enabled": true,
			"when": {
				"fact": "request.model",
				"fact": "upstream.model",
				"op": "eq",
				"value": "Astra"
			},
			"then": {"type": "exclude_candidate"}
		}]
	}`
	_, err = Compile([]byte(jsonWithDupKey))
	if err == nil {
		t.Fatal("expected error for duplicate key, got nil")
	}
	if !errors.As(err, &valErr) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if valErr.Code != ErrCodeDuplicateKey {
		t.Fatalf("expected code ERR_DUPLICATE_KEY, got %q", valErr.Code)
	}
	if !strings.HasSuffix(valErr.Path, "fact") {
		t.Fatalf("expected path ending in fact, got %q", valErr.Path)
	}
}

func TestRejectInvalidUTF8(t *testing.T) {
	// 非法 UTF-8 字节：包含 0xff
	badBytes := []byte("{\"schema_version\": 1, \"rules\": [{\"id\": \"r1\", \"name\": \"\xff\xfe\", \"domain\": \"scheduling\", \"enabled\": true, \"when\": {\"fact\": \"request.model\", \"op\": \"eq\", \"value\": \"Astra\"}, \"then\": {\"type\": \"exclude_candidate\"}}]}")
	_, err := Compile(badBytes)
	if err == nil {
		t.Fatal("expected invalid UTF-8 error, got nil")
	}
	var valErr *ValidationError
	if !errors.As(err, &valErr) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if valErr.Code != ErrCodeInvalidUTF8 {
		t.Fatalf("expected ERR_INVALID_UTF8, got %q", valErr.Code)
	}
}

func TestNumberFactNaNAndInfStaysUnknown(t *testing.T) {
	reg := NewRegistry()
	err := reg.RegisterParam(ParamDescriptor{
		Key:           "custom.latency",
		Type:          ParamTypeNumber,
		Label:         "Latency",
		Description:   "Latency in ms",
		Operators:     []string{"eq", "lt", "lte", "gt", "gte"},
		Domains:       []Domain{DomainScheduling},
		BindingScopes: []string{"credential"},
	})
	if err != nil {
		t.Fatal(err)
	}

	jsonStr := `{
		"schema_version": 1,
		"rules": [
			{
				"id": "r-gt", "name": "High latency", "domain": "scheduling", "enabled": true,
				"when": {"fact": "custom.latency", "op": "gt", "value": 100},
				"then": {"type": "exclude_candidate"}
			},
			{
				"id": "r-not", "name": "Not High latency", "domain": "scheduling", "enabled": true,
				"when": {"not": {"fact": "custom.latency", "op": "gt", "value": 100}},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`

	cfg, err := CompileWithRegistry([]byte(jsonStr), reg)
	if err != nil {
		t.Fatal(err)
	}

	for _, invalidNum := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		ctx := &EvalContext{
			Now: time.Now(),
			CustomNumberFacts: map[string]NumberFact{
				"custom.latency": {Value: invalidNum, State: FactStateMeasured},
			},
		}

		// 快速热路径不能排除
		sRes := cfg.EvalScheduling(ctx)
		if sRes.Excluded {
			t.Fatalf("invalid number %v should stay unknown and NOT exclude candidate", invalidNum)
		}

		// 诊断模式应为 skipped_unknown
		diag := cfg.Inspect(ctx)
		if diag.Rules[0].Status != RuleStatusSkippedUnknown {
			t.Fatalf("rule 0 for %v status = %s, want skipped_unknown", invalidNum, diag.Rules[0].Status)
		}
		if diag.Rules[1].Status != RuleStatusSkippedUnknown {
			t.Fatalf("rule 1 (NOT) for %v status = %s, want skipped_unknown (NOT cannot turn invalid number true)", invalidNum, diag.Rules[1].Status)
		}
	}
}

func TestRegistryDescriptorImmutabilityAndOrder(t *testing.T) {
	reg := NewRegistry()

	origScopes := []string{"group", "credential"}
	err := reg.RegisterParam(ParamDescriptor{
		Key:           "test.param",
		Type:          ParamTypeString,
		Label:         "L",
		Description:   "D",
		Operators:     []string{"eq"},
		Domains:       []Domain{DomainScheduling},
		BindingScopes: origScopes,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 外部修改传入的 slice，注册表内部不受影响
	origScopes[0] = "MUTATED"
	desc, ok := reg.FindParam("test.param")
	if !ok || desc.BindingScopes[0] == "MUTATED" {
		t.Fatal("registry leaked internal binding scopes on RegisterParam")
	}

	// 修改 FindParam 返回的 slice，注册表内部不受影响
	desc.BindingScopes[0] = "MUTATED_2"
	descAgain, _ := reg.FindParam("test.param")
	if descAgain.BindingScopes[0] == "MUTATED_2" {
		t.Fatal("registry leaked internal binding scopes on FindParam")
	}

	// 注册第二个 param 验证 ListParams 排序稳定性
	err = reg.RegisterParam(ParamDescriptor{
		Key:           "another.param",
		Type:          ParamTypeString,
		Label:         "A",
		Description:   "A",
		Operators:     []string{"eq"},
		Domains:       []Domain{DomainScheduling},
		BindingScopes: []string{"group"},
	})
	if err != nil {
		t.Fatal(err)
	}

	list := reg.ListParams()
	if len(list) != 2 || list[0].Key != "another.param" || list[1].Key != "test.param" {
		t.Fatalf("ListParams not in stable ascending order: %+v", list)
	}
}

func TestExactDecimalThresholdSemantics(t *testing.T) {
	// 1. 普通 0.1 正常编译，eq 比较与常规 float64 事实精确匹配
	cfgPointOne, err := Compile([]byte(`{
		"schema_version": 1,
		"rules": [{
			"id": "r-01", "name": "0.1", "domain": "scheduling", "enabled": true,
			"when": {
				"fact": "credential.quota.remaining_ratio",
				"select": {"scope": "account", "window_seconds": 18000},
				"reduce": "min",
				"op": "eq",
				"value": 0.1
			},
			"then": {"type": "exclude_candidate"}
		}]
	}`))
	if err != nil {
		t.Fatalf("compile 0.1 failed: %v", err)
	}

	ctx01 := &EvalContext{
		QuotaWindows: []QuotaWindowFact{
			{Scope: "account", WindowSeconds: 18000, Ratio: 0.1, State: FactStateMeasured},
		},
	}
	if !cfgPointOne.EvalScheduling(ctx01).Excluded {
		t.Fatal("expected value=0.1 to match ratio=0.1 on eq")
	}

	// 2. 0.99999999999999999 (NumShift=-1, 严格 < 1.0)
	cfgNearOne, err := Compile([]byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "r-eq", "name": "eq", "domain": "scheduling", "enabled": true,
				"when": {
					"fact": "credential.quota.remaining_ratio",
					"select": {"scope": "account", "window_seconds": 18000},
					"reduce": "min",
					"op": "eq",
					"value": 0.99999999999999999
				},
				"then": {"type": "exclude_candidate"}
			},
			{
				"id": "r-lt", "name": "lt", "domain": "scheduling", "enabled": true,
				"when": {
					"fact": "credential.quota.remaining_ratio",
					"select": {"scope": "account", "window_seconds": 18000},
					"reduce": "min",
					"op": "lt",
					"value": 0.99999999999999999
				},
				"then": {"type": "exclude_candidate"}
			},
			{
				"id": "r-gte", "name": "gte", "domain": "scheduling", "enabled": true,
				"when": {
					"fact": "credential.quota.remaining_ratio",
					"select": {"scope": "account", "window_seconds": 18000},
					"reduce": "min",
					"op": "gte",
					"value": 0.99999999999999999
				},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`))
	if err != nil {
		t.Fatalf("compile near-one failed: %v", err)
	}

	// 验证 Clone 深拷贝 NumShift
	clonedNearOne := cfgNearOne.Clone()
	if clonedNearOne.Rules()[0].When.NumShift != -1 {
		t.Fatalf("cloned NumShift expected -1, got %d", clonedNearOne.Rules()[0].When.NumShift)
	}

	// 事实 ratio = 1.0 时：
	// eq 应为 false（不等于不可表示的 0.99999999999999999）
	// lt 应为 false（1.0 不小于 0.99999999999999999）
	// gte 应为 true（1.0 >= 0.99999999999999999）
	ctxOne := &EvalContext{
		QuotaWindows: []QuotaWindowFact{
			{Scope: "account", WindowSeconds: 18000, Ratio: 1.0, State: FactStateMeasured},
		},
	}
	diagOne := clonedNearOne.Inspect(ctxOne)
	if diagOne.Rules[0].Status != RuleStatusMiss {
		t.Fatalf("rule eq expected miss on ratio=1.0, got %s", diagOne.Rules[0].Status)
	}
	if diagOne.Rules[1].Status != RuleStatusMiss {
		t.Fatalf("rule lt expected miss on ratio=1.0, got %s", diagOne.Rules[1].Status)
	}
	if diagOne.Rules[2].Status != RuleStatusHit {
		t.Fatalf("rule gte expected hit on ratio=1.0, got %s", diagOne.Rules[2].Status)
	}

	// 事实 ratio = 0.95 时：
	// lt 应为 true (0.95 < 0.99999999999999999)
	// gte 应为 false (0.95 不大于等于 0.99999999999999999)
	ctxLow := &EvalContext{
		QuotaWindows: []QuotaWindowFact{
			{Scope: "account", WindowSeconds: 18000, Ratio: 0.95, State: FactStateMeasured},
		},
	}
	diagLow := clonedNearOne.Inspect(ctxLow)
	if diagLow.Rules[1].Status != RuleStatusHit {
		t.Fatalf("rule lt expected hit on ratio=0.95, got %s", diagLow.Rules[1].Status)
	}
	if diagLow.Rules[2].Status != RuleStatusMiss {
		t.Fatalf("rule gte expected miss on ratio=0.95, got %s", diagLow.Rules[2].Status)
	}

	// 3. 规范化等价十进制语法（0.10, 0.100）与 0.1 完全等价匹配
	for _, rawValue := range []string{"0.10", "0.100"} {
		cfgEqSyntax, err := Compile([]byte(fmt.Sprintf(`{
			"schema_version": 1,
			"rules": [{
				"id": "r-eq-syntax", "name": "eq-syntax", "domain": "scheduling", "enabled": true,
				"when": {
					"fact": "credential.quota.remaining_ratio",
					"select": {"scope": "account", "window_seconds": 18000},
					"reduce": "min",
					"op": "eq",
					"value": %s
				},
				"then": {"type": "exclude_candidate"}
			}]
		}`, rawValue)))
		if err != nil {
			t.Fatalf("compile %s failed: %v", rawValue, err)
		}
		if cfgEqSyntax.Rules()[0].When.NumShift != 0 {
			t.Fatalf("expected NumShift=0 for %s, got %d", rawValue, cfgEqSyntax.Rules()[0].When.NumShift)
		}
		if !cfgEqSyntax.EvalScheduling(ctx01).Excluded {
			t.Fatalf("expected value=%s to match ratio=0.1 on eq", rawValue)
		}
	}

}
