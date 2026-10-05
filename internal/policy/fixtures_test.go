package policy

import (
	"encoding/json"
	"testing"
	"time"
)

// 本文件提供 package policy 测试共用的紧凑夹具。
// 需要精确数值字面量或严格 JSON 拒绝路径的用例仍保留原始 JSON 字符串。

// mustJSON 序列化夹具，失败即终止测试。
func mustJSON(tb testing.TB, v any) []byte {
	tb.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		tb.Fatalf("marshal fixture: %v", err)
	}
	return data
}

// bindingConfig 构造绑定配置，mode 非空时写入 group_policy。
func bindingConfig(tb testing.TB, mode string, rules ...map[string]any) []byte {
	tb.Helper()
	if rules == nil {
		rules = []map[string]any{}
	}
	doc := map[string]any{"schema_version": 1, "rules": rules}
	if mode != "" {
		doc["group_policy"] = mode
	}
	return mustJSON(tb, doc)
}

// configJSON 构造无 group_policy 的 schema_version 1 文档。
func configJSON(tb testing.TB, rules ...map[string]any) []byte {
	tb.Helper()
	return bindingConfig(tb, "", rules...)
}

// compileRules 编译由给定规则对象构成的配置，失败即终止测试。
func compileRules(tb testing.TB, rules ...map[string]any) *CompiledConfig {
	tb.Helper()
	cfg, err := Compile(configJSON(tb, rules...))
	if err != nil {
		tb.Fatalf("compile fixture: %v", err)
	}
	return cfg
}

func modelEq(value string) map[string]any {
	return map[string]any{"fact": "request.model", "op": "eq", "value": value}
}

func upstreamEq(value string) map[string]any {
	return map[string]any{"fact": "upstream.model", "op": "eq", "value": value}
}

// quotaLt 构造 account 作用域 remaining_ratio < value 的额度条件。
func quotaLt(windowSeconds int, value float64) map[string]any {
	return map[string]any{
		"fact":   "credential.quota.remaining_ratio",
		"select": map[string]any{"scope": "account", "window_seconds": windowSeconds},
		"reduce": "min",
		"op":     "lt",
		"value":  value,
	}
}

func schedRule(id string, condition any) map[string]any {
	return map[string]any{
		"id": id, "name": id, "domain": string(DomainScheduling), "enabled": true,
		"when": condition, "actions": []any{map[string]any{"type": string(ActionExcludeCandidate)}},
	}
}

func priceRule(id, factor string, condition any) map[string]any {
	return map[string]any{
		"id": id, "name": id, "domain": string(DomainPricing), "enabled": true,
		"when": condition, "actions": []any{map[string]any{"type": string(ActionMultiplyPrice), "factor": factor}},
	}
}

func measuredString(value string) StringFact {
	return StringFact{Value: value, State: FactStateMeasured}
}

// modelCtx 返回 request.model 已测量为 value 的求值上下文。
func modelCtx(value string) *EvalContext {
	return &EvalContext{Now: time.Now(), RequestModel: measuredString(value)}
}


func modelFact(value string) StringFact {
	return StringFact{Value: value, State: FactStateMeasured}
}

func measuredCtx(value string) *EvalContext {
	return &EvalContext{Now: time.Now(), RequestModel: modelFact(value)}
}
