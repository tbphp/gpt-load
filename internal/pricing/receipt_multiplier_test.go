package pricing

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gpt-load/internal/usage"
)

func TestValidateReceiptPriceMultipliersPreserveHistoricalVersionBoundary(t *testing.T) {
	for version := 1; version <= 4; version++ {
		rule := ReceiptRule{ModelID: "model"}
		if version == 1 {
			rule.ScopeKey = "provider:openai"
		} else if version >= 3 {
			rule.ChannelID = "openai"
		}
		receipt := validReceipt(version, rule)
		if err := ValidateReceipt(receipt); err != nil {
			t.Fatalf("historical v%d receipt rejected: %v", version, err)
		}
		receipt.PriceMultipliers = &PriceMultipliers{Group: DefaultPriceMultiplier, AccessKey: DefaultPriceMultiplier}
		if err := ValidateReceipt(receipt); err == nil {
			t.Fatalf("historical v%d receipt accepted price multipliers", version)
		}
	}
}

func TestValidateReceiptPriceMultipliersRejectsMissingAndTamperedInputs(t *testing.T) {
	identity := Identity{ChannelID: "openai", ModelID: "model"}
	table := mustTable(t, Rule{Identity: identity, Prices: Prices{Input: fixedPrice(100)}})
	for _, test := range []struct {
		name   string
		mutate func(*Receipt)
	}{
		{"missing multipliers", func(r *Receipt) { r.PriceMultipliers = nil }},
		{"negative group", func(r *Receipt) { r.PriceMultipliers.Group = -1 }},
		{"access key over limit", func(r *Receipt) { r.PriceMultipliers.AccessKey = 1_000_000_001 }},
		{"changed group", func(r *Receipt) { r.PriceMultipliers.Group = DefaultPriceMultiplier }},
		{"changed access key", func(r *Receipt) { r.PriceMultipliers.AccessKey = DefaultPriceMultiplier }},
		{"changed protocol multiplier", func(r *Receipt) { r.LineItems[0].Multiplier.Numerator = 2 }},
		{"changed base rate", func(r *Receipt) { *r.LineItems[0].RateNanoUSDPerMillion = 200 }},
		{"changed line amount", func(r *Receipt) { *r.LineItems[0].AmountNanoUSD = 101 }},
		{"missing base total", func(r *Receipt) { r.BaseTotalNanoUSD = nil }},
		{"negative base total", func(r *Receipt) { *r.BaseTotalNanoUSD = -1 }},
		{"changed base total", func(r *Receipt) { *r.BaseTotalNanoUSD = 101 }},
		{"changed total", func(r *Receipt) { r.TotalNanoUSD = 100 }},
		{"downgraded schema", func(r *Receipt) { r.SchemaVersion = 4 }},
		{"downgraded to component multiplier schema", func(r *Receipt) { r.SchemaVersion = 5 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, receipt := table.QuoteForModeWithMultipliers(identity, usage.Result{
				State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1_000_000},
			}, ModeStandard, PriceMultipliers{Group: 800_000, AccessKey: 1_500_000})
			if receipt == nil {
				t.Fatal("expected receipt")
			}
			test.mutate(receipt)
			if err := ValidateReceipt(*receipt); err == nil {
				t.Fatalf("ValidateReceipt accepted %s", test.name)
			}
		})
	}
}

func TestReceiptJSONRejectsNullMultipliersAndHistoricalInjectedFields(t *testing.T) {
	for _, test := range []struct {
		name string
		json string
	}{
		{"v5 null factors", `{"schema_version":5,"price_multipliers":null}`},
		{"v4 null factors", `{"schema_version":4,"price_multipliers":null}`},
		{"v4 explicit factors", `{"schema_version":4,"price_multipliers":{"group":"1","access_key":"1"}}`},
		{"v5 missing group", `{"schema_version":5,"price_multipliers":{"access_key":"1"}}`},
		{"v5 unknown factor", `{"schema_version":5,"price_multipliers":{"group":"1","access_key":"1","other":"1"}}`},
		{"v6 null base total", `{"schema_version":6,"base_total_nano_usd":null}`},
		{"v5 injected base total", `{"schema_version":5,"base_total_nano_usd":0}`},
		{"v5 null base total", `{"schema_version":5,"base_total_nano_usd":null}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var receipt Receipt
			if err := json.Unmarshal([]byte(test.json), &receipt); err == nil {
				t.Fatalf("Unmarshal accepted %s", test.json)
			}
		})
	}

	// 自定义解码仍须保留请求日志现有的未知字段拒绝行为。
	var receipt Receipt
	decoder := json.NewDecoder(strings.NewReader(`{"schema_version":4,"unknown":1}`))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err == nil {
		t.Fatal("receipt decoder accepted an unknown field")
	}
}

func TestValidateHistoricalV5ReceiptKeepsOriginalComponentRounding(t *testing.T) {
	const encoded = `{
		"schema_version":5,"method":"unit_rate_sum","method_version":1,"currency":"USD","pricing_mode":"standard",
		"rule":{"channel_id":"openai","model_id":"model"},"price_multipliers":{"group":"2","access_key":"1"},
		"line_items":[
			{"code":"input","quantity":1,"rate_nano_usd_per_million":600000,"multiplier":{"numerator":1,"denominator":1},"state":"priced","amount_nano_usd":1},
			{"code":"output","quantity":1,"rate_nano_usd_per_million":600000,"multiplier":{"numerator":1,"denominator":1},"state":"priced","amount_nano_usd":1}
		],"total_nano_usd":2
	}`
	var receipt Receipt
	if err := json.Unmarshal([]byte(encoded), &receipt); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReceipt(receipt); err != nil {
		t.Fatalf("historical v5 receipt changed: %v", err)
	}
	if receipt.BaseTotalNanoUSD != nil || receipt.TotalNanoUSD != 2 {
		t.Fatalf("historical receipt was reinterpreted: %#v", receipt)
	}
	// 相同已存分项，v5 总额不能套用 v6 的总额倍率算法改成 4。
	receipt.TotalNanoUSD = 4
	if err := ValidateReceipt(receipt); err == nil {
		t.Fatal("historical v5 accepted total-adjustment calculation")
	}
}

func TestValidateV7ReceiptWithPolicyFactorsAndRejectsTampering(t *testing.T) {
	identity := Identity{ChannelID: "openai", ModelID: "model"}
	table := mustTable(t, Rule{Identity: identity, Prices: Prices{Input: fixedPrice(100)}})
	f2, _ := ParsePriceMultiplier("2")
	factors := []PolicyFactor{
		{RuleID: "p1", NameSnapshot: "Rule 1", BindingScope: "group", Revision: 1, Factor: "2", Multiplier: f2},
	}
	_, receipt := table.QuoteForModeWithMultipliers(identity, usage.Result{
		State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1_000_000},
	}, ModeStandard, PriceMultipliers{Group: DefaultPriceMultiplier, AccessKey: DefaultPriceMultiplier}, factors...)
	if receipt == nil {
		t.Fatal("expected receipt")
	}
	if err := ValidateReceipt(*receipt); err != nil {
		t.Fatalf("v7 receipt rejected: %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*Receipt)
	}{
		{"empty factors", func(r *Receipt) { r.PolicyFactors = nil }},
		{"empty rule id", func(r *Receipt) { r.PolicyFactors[0].RuleID = "" }},
		{"empty name snapshot", func(r *Receipt) { r.PolicyFactors[0].NameSnapshot = "" }},
		{"invalid scope", func(r *Receipt) { r.PolicyFactors[0].BindingScope = "invalid" }},
		{"zero revision", func(r *Receipt) { r.PolicyFactors[0].Revision = 0 }},
		{"tampered factor string", func(r *Receipt) { r.PolicyFactors[0].Factor = "3" }},
		{"tampered factor multiplier", func(r *Receipt) { r.PolicyFactors[0].Multiplier = 3_000_000 }},
		{"tampered total", func(r *Receipt) { r.TotalNanoUSD = 100 }},
		{"downgraded to v6 with policy factors", func(r *Receipt) { r.SchemaVersion = 6 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			clone := *receipt
			clone.PolicyFactors = append([]PolicyFactor(nil), receipt.PolicyFactors...)
			test.mutate(&clone)
			if err := ValidateReceipt(clone); err == nil {
				t.Fatalf("ValidateReceipt accepted %s", test.name)
			}
		})
	}
}

func TestReceiptStrictJSONDecoding(t *testing.T) {
	identity := Identity{ChannelID: "openai", ModelID: "model"}
	table := mustTable(t, Rule{Identity: identity, Prices: Prices{Input: fixedPrice(100)}})
	f0, _ := ParsePriceMultiplier("0")
	zeroFactor := PolicyFactor{RuleID: "p", NameSnapshot: "free", BindingScope: "group", Revision: 1, Factor: "0", Multiplier: f0}
	_, receipt := table.QuoteForModeWithMultipliers(identity, usage.Result{
		State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1_000_000},
	}, ModeStandard, PriceMultipliers{Group: DefaultPriceMultiplier, AccessKey: DefaultPriceMultiplier}, zeroFactor)
	if receipt == nil {
		t.Fatal("expected receipt")
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Strict factor JSON tests: unknown, duplicate, duplicate_top
	for _, tc := range []struct{ name, from, to string }{
		{"unknown field in factor", `"rule_id":"p"`, `"surprise":true,"rule_id":"p"`},
		{"duplicate field in factor", `"multiplier":"0"`, `"multiplier":"2","multiplier":"0"`},
		{"duplicate top-level schema_version", `"schema_version":7`, `"schema_version":6,"schema_version":7`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			altered := strings.Replace(string(raw), tc.from, tc.to, 1)
			if altered == string(raw) {
				t.Fatal("replacement did not match")
			}
			var decoded Receipt
			err := json.Unmarshal([]byte(altered), &decoded)
			if err == nil {
				err = ValidateReceipt(decoded)
			}
			if err == nil {
				t.Fatalf("accepted %s: %s", tc.name, altered)
			}
		})
	}

	// 2. All required fields non-null
	for _, field := range []string{"rule_id", "name_snapshot", "binding_scope", "revision", "factor", "multiplier"} {
		for _, isNull := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-null=%v", field, isNull), func(t *testing.T) {
				var obj map[string]any
				if err := json.Unmarshal(raw, &obj); err != nil {
					t.Fatal(err)
				}
				factor := obj["policy_factors"].([]any)[0].(map[string]any)
				if isNull {
					factor[field] = nil
				} else {
					delete(factor, field)
				}
				bad, _ := json.Marshal(obj)
				var decoded Receipt
				if err := json.Unmarshal(bad, &decoded); err == nil {
					t.Fatalf("accepted missing/null %s: %s", field, bad)
				}
			})
		}
	}
}

func TestReceiptValidationConstraints(t *testing.T) {
	identity := Identity{ChannelID: "openai", ModelID: "model"}
	table := mustTable(t, Rule{Identity: identity, Prices: Prices{Input: fixedPrice(100)}})
	f1, _ := ParsePriceMultiplier("1")
	factor := PolicyFactor{RuleID: "p", NameSnapshot: "one", BindingScope: "group", Revision: 1, Factor: "1", Multiplier: f1}
	_, receipt := table.QuoteForModeWithMultipliers(identity, usage.Result{
		State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1_000_000},
	}, ModeStandard, PriceMultipliers{Group: DefaultPriceMultiplier, AccessKey: DefaultPriceMultiplier}, factor)
	if receipt == nil {
		t.Fatal("expected receipt")
	}

	// 1. One revision per binding: inconsistent revisions for same scope
	t.Run("one revision per binding scope", func(t *testing.T) {
		r := *receipt
		r.PolicyFactors = append([]PolicyFactor(nil), receipt.PolicyFactors...)
		r.PolicyFactors = append(r.PolicyFactors, PolicyFactor{
			RuleID: "p2", NameSnapshot: "diff rev", BindingScope: "group", Revision: 2, Factor: "1", Multiplier: f1,
		})
		if err := ValidateReceipt(r); err == nil {
			t.Fatal("accepted factors with different revisions for the same group binding")
		}
	})

	// 2. Reject 101 factors in one scope
	t.Run("reject 101 in group scope", func(t *testing.T) {
		r := *receipt
		r.PolicyFactors = make([]PolicyFactor, 101)
		for i := range r.PolicyFactors {
			r.PolicyFactors[i] = PolicyFactor{
				RuleID: fmt.Sprintf("p%d", i), NameSnapshot: "one", BindingScope: "group", Revision: 1, Factor: "1", Multiplier: f1,
			}
		}
		if err := ValidateReceipt(r); err == nil {
			t.Fatal("accepted 101 factors in group scope")
		}
	})

	// 3. Reject over 200 total
	t.Run("reject 201 factors total", func(t *testing.T) {
		r := *receipt
		r.PolicyFactors = make([]PolicyFactor, 201)
		for i := range r.PolicyFactors {
			r.PolicyFactors[i] = PolicyFactor{
				RuleID: fmt.Sprintf("p%d", i), NameSnapshot: "one", BindingScope: "group", Revision: 1, Factor: "1", Multiplier: f1,
			}
		}
		if err := ValidateReceipt(r); err == nil {
			t.Fatal("accepted 201 factors total")
		}
	})

	// 4. Accept 200 bounded (100 group, 100 credential) and allow cross-binding duplicate IDs
	t.Run("accept 200 bounded and cross-binding duplicate IDs", func(t *testing.T) {
		r := *receipt
		r.PolicyFactors = nil
		for _, scope := range []string{"group", "credential"} {
			for i := 0; i < 100; i++ {
				r.PolicyFactors = append(r.PolicyFactors, PolicyFactor{
					RuleID: fmt.Sprintf("p%d", i), NameSnapshot: "rule", BindingScope: scope, Revision: 1, Factor: "1", Multiplier: f1,
				})
			}
		}
		if err := ValidateReceipt(r); err != nil {
			t.Fatalf("failed valid 200 factor receipt: %v", err)
		}
		// Interleave: credential before group must fail
		r.PolicyFactors[0], r.PolicyFactors[100] = r.PolicyFactors[100], r.PolicyFactors[0]
		if err := ValidateReceipt(r); err == nil {
			t.Fatal("accepted out-of-order scopes (credential before group)")
		}
	})
}

func TestQuotePrecisionChains(t *testing.T) {
	identity := Identity{ChannelID: "openai", ModelID: "model"}
	base := PriceMultipliers{Group: DefaultPriceMultiplier, AccessKey: DefaultPriceMultiplier}
	for _, tc := range []struct {
		name        string
		group, cred string
		amount      int64
		want        int64
	}{
		{"just_below_half", "1.000001", "0.999999", 5000000001, 5000000000},
		{"huge_intermediate_cancel", "1000", "0.001", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			table := mustTable(t, Rule{Identity: identity, Prices: Prices{Input: fixedPrice(NanoUSD(tc.amount))}})
			var factors []PolicyFactor
			for _, side := range []struct{ scope, factor string }{{"group", tc.group}, {"credential", tc.cred}} {
				m, err := ParsePriceMultiplier(side.factor)
				if err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 100; i++ {
					factors = append(factors, PolicyFactor{
						RuleID: fmt.Sprintf("p%d", i), NameSnapshot: "frozen", BindingScope: side.scope, Revision: 1, Factor: side.factor, Multiplier: m,
					})
				}
			}
			q, r := table.QuoteForModeWithMultipliers(identity, usage.Result{
				State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1_000_000},
			}, ModeStandard, base, factors...)
			if q.State != CostStatePriced || int64(q.EstimatedCostNanoUSD) != tc.want || r == nil || len(r.PolicyFactors) != 200 || ValidateReceipt(*r) != nil {
				t.Fatalf("precision chain %s failed: q=%+v receipt=%+v", tc.name, q, r)
			}
		})
	}
}

func TestReceiptStrictTopFieldsAndAliases(t *testing.T) {
	identity := Identity{ChannelID: "openai", ModelID: "model"}
	table := mustTable(t, Rule{Identity: identity, Prices: Prices{Input: fixedPrice(100)}})
	zeroFactor := PolicyFactor{RuleID: "p", NameSnapshot: "free", BindingScope: "group", Revision: 1, Factor: "0", Multiplier: 0}
	_, receipt := table.QuoteForModeWithMultipliers(identity, usage.Result{
		State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1_000_000},
	}, ModeStandard, PriceMultipliers{Group: DefaultPriceMultiplier, AccessKey: DefaultPriceMultiplier}, zeroFactor)
	if receipt == nil {
		t.Fatal("no receipt")
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, from, to string }{
		{"duplicate_total", `"total_nano_usd":0`, `"total_nano_usd":12345,"total_nano_usd":0`},
		{"duplicate_factors", `"policy_factors":`, `"policy_factors":[],"policy_factors":`},
		{"null_total", `"total_nano_usd":0`, `"total_nano_usd":null`},
		{"missing_total", `,"total_nano_usd":0`, ``},
		{"alias_factor_duplicate", `"multiplier":"0"`, `"Multiplier":"2","multiplier":"0"`},
		{"alias_schema", `"schema_version":7`, `"SCHEMA_VERSION":7`},
		{"unknown_top", `"schema_version":7`, `"unexpected":true,"schema_version":7`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			altered := strings.Replace(string(raw), tc.from, tc.to, 1)
			if altered == string(raw) {
				t.Fatal("no replacement")
			}
			var decoded Receipt
			err := json.Unmarshal([]byte(altered), &decoded)
			if err == nil {
				err = ValidateReceipt(decoded)
			}
			if err == nil {
				t.Fatalf("accepted %s: %s", tc.name, altered)
			}
		})
	}

	// Schema downgrade attempts
	rDowngrade := *receipt
	rDowngrade.PolicyFactors[0].Factor = "1"
	rDowngrade.PolicyFactors[0].Multiplier = DefaultPriceMultiplier
	rDowngrade.TotalNanoUSD = *rDowngrade.BaseTotalNanoUSD
	rDowngrade.PolicyFactors = nil
	rawDowngrade, _ := json.Marshal(rDowngrade)
	for _, ver := range []string{
		`"schema_version":7,"schema_version":6`,
		`"schema_version":6,"schema_version":7`,
		`"schema_version":7,"SCHEMA_VERSION":6`,
		`"SCHEMA_VERSION":7,"schema_version":6`,
	} {
		t.Run("downgrade_"+ver, func(t *testing.T) {
			bad := strings.Replace(string(rawDowngrade), `"schema_version":7`, ver, 1)
			var decoded Receipt
			err := json.Unmarshal([]byte(bad), &decoded)
			if err == nil {
				err = ValidateReceipt(decoded)
			}
			if err == nil {
				t.Fatalf("v7 schema downgrade accepted: %s", bad)
			}
		})
	}
}

func TestQuoteEnforcesCentralFactorInvariants(t *testing.T) {
	id := Identity{ChannelID: "openai", ModelID: "model"}
	table := mustTable(t, Rule{Identity: id, Prices: Prices{Input: fixedPrice(100)}})
	for _, bad := range []PolicyFactor{
		{RuleID: "p", NameSnapshot: "p", BindingScope: "access_key", Revision: 1, Factor: "0", Multiplier: 0},
		{RuleID: "p", NameSnapshot: "p", BindingScope: "group", Revision: 1, Factor: "2", Multiplier: 0},
	} {
		q, r := table.QuoteForModeWithMultipliers(id, usage.Result{
			State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1_000_000},
		}, ModeStandard, PriceMultipliers{Group: DefaultPriceMultiplier, AccessKey: DefaultPriceMultiplier}, bad)
		if r != nil || q.State != CostStateUnpriced {
			t.Fatalf("quote produced priced result and invalid receipt rather than enforcing central validation: quote=%+v receipt=%+v", q, r)
		}
	}
}

func TestReceiptV7LineItemsRequiredNonNullAndHistoricalNullable(t *testing.T) {
	id := Identity{ChannelID: "openai", ModelID: "model"}
	table := mustTable(t, Rule{Identity: id, Prices: Prices{Input: fixedPrice(100)}})
	_, r := table.QuoteForModeWithMultipliers(id, usage.Result{State: usage.StateComplete}, ModeStandard,
		PriceMultipliers{Group: DefaultPriceMultiplier, AccessKey: DefaultPriceMultiplier},
		PolicyFactor{RuleID: "p", NameSnapshot: "p", BindingScope: "group", Revision: 1, Factor: "1", Multiplier: DefaultPriceMultiplier},
	)
	if r == nil || ValidateReceipt(*r) != nil {
		t.Fatalf("invalid legitimate zero-token receipt: %+v", r)
	}
	raw, _ := json.Marshal(r)
	for _, isNull := range []bool{false, true} {
		t.Run(fmt.Sprintf("v7-line_items-null=%v", isNull), func(t *testing.T) {
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(raw, &obj); err != nil {
				t.Fatal(err)
			}
			if isNull {
				obj["line_items"] = json.RawMessage("null")
			} else {
				delete(obj, "line_items")
			}
			bad, _ := json.Marshal(obj)
			var decoded Receipt
			err := json.Unmarshal(bad, &decoded)
			if err == nil {
				err = ValidateReceipt(decoded)
			}
			if err == nil {
				t.Fatalf("v7 accepted missing/null line_items: %s", bad)
			}
		})
	}

	// Historical v1-v6 receipts preserve null/missing line_items in UnmarshalJSON
	for v := 1; v <= 6; v++ {
		t.Run(fmt.Sprintf("v%d-nullable-line_items", v), func(t *testing.T) {
			histReceipt := Receipt{
				SchemaVersion: v,
				Method:        ReceiptMethodUnitRateSum,
				MethodVersion: 1,
				Currency:      "USD",
				Rule: ReceiptRule{
					ChannelID: "openai",
					ModelID:   "model",
				},
				LineItems:    nil,
				TotalNanoUSD: 0,
			}
			if v >= 4 {
				histReceipt.PricingMode = ModeStandard
			}
			if v >= 5 {
				histReceipt.PriceMultipliers = &PriceMultipliers{Group: DefaultPriceMultiplier, AccessKey: DefaultPriceMultiplier}
			}
			if v >= 6 {
				zero := int64(0)
				histReceipt.BaseTotalNanoUSD = &zero
			}
			data, err := json.Marshal(histReceipt)
			if err != nil {
				t.Fatal(err)
			}
			var dec Receipt
			if err := json.Unmarshal(data, &dec); err != nil {
				t.Fatalf("historical v%d rejected nullable line_items: %v", v, err)
			}
		})
	}
}
