package pricing

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestValidatePattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
		wantErr bool
	}{
		{name: "exact", pattern: "gpt-5.6"},
		{name: "prefix", pattern: "vendor-*"},
		{name: "global", pattern: "*"},
		{name: "empty", pattern: "", wantErr: true},
		{name: "too long", pattern: strings.Repeat("a", 256), wantErr: true},
		{name: "leading whitespace", pattern: " gpt-5.6", wantErr: true},
		{name: "trailing whitespace", pattern: "gpt-5.6 ", wantErr: true},
		{name: "control character", pattern: "gpt-\a5.6", wantErr: true},
		{name: "question mark", pattern: "vendor-?", wantErr: true},
		{name: "embedded star", pattern: "vendor-*model", wantErr: true},
		{name: "multiple stars", pattern: "vendor-**", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidatePattern(test.pattern)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidatePattern(%q) error = %v, wantErr %t", test.pattern, err, test.wantErr)
			}
		})
	}
}

func TestCompileMatchUsesSourceKindAndLongestPrefixPriority(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	table, err := Compile([]Rule{
		{Pattern: "gpt-4o", Prices: priced(1), Source: SourceBuiltin, SourceURL: "https://builtin.example/pricing", UpdatedAt: updatedAt},
		{Pattern: "gpt-*", Prices: priced(2), Source: SourceBuiltin, SourceURL: "https://builtin.example/pricing", UpdatedAt: updatedAt},
		{Pattern: "gpt-*", Prices: priced(3), Source: SourceUser},
		{Pattern: "gpt-4.1", Prices: priced(4), Source: SourceUser},
		{Pattern: "gpt-4o-mini*", Prices: priced(5), Source: SourceUser},
		{Pattern: "claude-*", Prices: priced(6), Source: SourceBuiltin, SourceURL: "https://builtin.example/pricing", UpdatedAt: updatedAt},
		{Pattern: "*", Prices: priced(7), Source: SourceBuiltin, SourceURL: "https://builtin.example/pricing", UpdatedAt: updatedAt},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	tests := []struct {
		name    string
		model   string
		pattern string
		source  Source
		matched bool
	}{
		{name: "user prefix outranks builtin exact", model: "gpt-4o", pattern: "gpt-*", source: SourceUser, matched: true},
		{name: "user exact outranks user prefix", model: "gpt-4.1", pattern: "gpt-4.1", source: SourceUser, matched: true},
		{name: "longest user prefix wins", model: "gpt-4o-mini-2026", pattern: "gpt-4o-mini*", source: SourceUser, matched: true},
		{name: "builtin is used after user miss", model: "claude-sonnet", pattern: "claude-*", source: SourceBuiltin, matched: true},
		{name: "star matches nonempty model", model: "other-model", pattern: "*", source: SourceBuiltin, matched: true},
		{name: "empty model does not match", model: "", matched: false},
		{name: "model matching is case sensitive", model: "GPT-4O", pattern: "*", source: SourceBuiltin, matched: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rule, ok := table.Match(test.model)
			if ok != test.matched {
				t.Fatalf("Match(%q) matched = %t, want %t", test.model, ok, test.matched)
			}
			if !test.matched {
				return
			}
			if rule.Pattern != test.pattern || rule.Source != test.source {
				t.Fatalf("Match(%q) = pattern:%q source:%q, want pattern:%q source:%q", test.model, rule.Pattern, rule.Source, test.pattern, test.source)
			}
		})
	}
}

func TestCompileMatchBareUserCatchAllShadowsBuiltins(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	table, err := Compile([]Rule{
		{
			Pattern:   "gpt-4o",
			Prices:    priced(1),
			Source:    SourceBuiltin,
			SourceURL: "https://builtin.example/pricing",
			UpdatedAt: updatedAt,
		},
		{Pattern: "*", Prices: priced(99), Source: SourceUser},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	for _, model := range []string{"gpt-4o", "claude-opus", "unknown-model"} {
		rule, ok := table.Match(model)
		if !ok || rule.Pattern != "*" || rule.Source != SourceUser ||
			rule.Prices.Output.NanoUSDPerMillion != 99 {
			t.Errorf("Match(%q) = %+v, %t; want global user override", model, rule, ok)
		}
	}
}

func TestCompileRejectsInvalidRules(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	validBuiltin := Rule{Pattern: "gpt-4o", Prices: priced(1), Source: SourceBuiltin, SourceURL: "https://builtin.example/pricing", UpdatedAt: updatedAt}
	tests := []struct {
		name string
		rule Rule
	}{
		{name: "empty pattern", rule: replacePattern(validBuiltin, "")},
		{name: "pattern exceeds 255 bytes", rule: replacePattern(validBuiltin, strings.Repeat("a", 256))},
		{name: "leading whitespace", rule: replacePattern(validBuiltin, " gpt-4o")},
		{name: "trailing whitespace", rule: replacePattern(validBuiltin, "gpt-4o ")},
		{name: "unicode control", rule: replacePattern(validBuiltin, "gpt-\a4o")},
		{name: "question mark", rule: replacePattern(validBuiltin, "gpt-?")},
		{name: "star in middle", rule: replacePattern(validBuiltin, "gpt-*o")},
		{name: "multiple stars", rule: replacePattern(validBuiltin, "gpt-**")},
		{name: "invalid source", rule: Rule{Pattern: "gpt-4o", Prices: priced(1), Source: Source("remote")}},
		{name: "all prices unset", rule: Rule{Pattern: "gpt-4o", Source: SourceUser}},
		{name: "negative price", rule: Rule{Pattern: "gpt-4o", Prices: Prices{Output: fixedPrice(-1)}, Source: SourceUser}},
		{name: "negative unset price", rule: Rule{Pattern: "gpt-4o", Prices: Prices{UncachedInput: Price{NanoUSDPerMillion: -1}, Output: fixedPrice(1)}, Source: SourceUser}},
		{name: "builtin missing URL", rule: Rule{Pattern: "gpt-4o", Prices: priced(1), Source: SourceBuiltin, UpdatedAt: updatedAt}},
		{name: "builtin missing update time", rule: Rule{Pattern: "gpt-4o", Prices: priced(1), Source: SourceBuiltin, SourceURL: "https://builtin.example/pricing"}},
		{name: "user source URL", rule: Rule{Pattern: "gpt-4o", Prices: priced(1), Source: SourceUser, SourceURL: "https://user.example/pricing"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Compile([]Rule{test.rule}); err == nil {
				t.Fatal("Compile() error = nil, want invalid rule error")
			}
		})
	}

	if _, err := Compile([]Rule{validBuiltin, validBuiltin}); err == nil {
		t.Fatal("Compile() duplicate same-source pattern error = nil, want error")
	}
	userSamePattern := Rule{Pattern: "gpt-4o", Prices: priced(2), Source: SourceUser}
	if _, err := Compile([]Rule{validBuiltin, userSamePattern}); err != nil {
		t.Fatalf("Compile() same pattern in different sources error = %v", err)
	}
}

func TestCompileRejectsUserAndPrefixLongContextPolicies(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	validPolicy := func() *LongContextPolicy {
		return &LongContextPolicy{
			InputThresholdTokens: 272_000,
			InputMultiplier:      Multiplier{Numerator: 2, Denominator: 1},
			OutputMultiplier:     Multiplier{Numerator: 3, Denominator: 2},
		}
	}
	validBuiltin := func(pattern string) Rule {
		return Rule{
			Pattern:           pattern,
			Prices:            priced(1),
			Source:            SourceBuiltin,
			SourceURL:         "https://builtin.example/pricing",
			UpdatedAt:         updatedAt,
			LongContextPolicy: validPolicy(),
		}
	}
	tests := []struct {
		name string
		rule Rule
	}{
		{
			name: "user exact policy",
			rule: Rule{
				Pattern: "gpt-5.6", Prices: priced(1), Source: SourceUser,
				LongContextPolicy: validPolicy(),
			},
		},
		{
			name: "user prefix policy",
			rule: Rule{
				Pattern: "gpt-*", Prices: priced(1), Source: SourceUser,
				LongContextPolicy: validPolicy(),
			},
		},
		{
			name: "user global policy",
			rule: Rule{
				Pattern: "*", Prices: priced(1), Source: SourceUser,
				LongContextPolicy: validPolicy(),
			},
		},
		{name: "builtin prefix policy", rule: validBuiltin("gpt-*")},
		{
			name: "zero threshold",
			rule: func() Rule {
				rule := validBuiltin("gpt-5.6")
				rule.LongContextPolicy.InputThresholdTokens = 0
				return rule
			}(),
		},
		{
			name: "negative threshold",
			rule: func() Rule {
				rule := validBuiltin("gpt-5.6")
				rule.LongContextPolicy.InputThresholdTokens = -1
				return rule
			}(),
		},
		{
			name: "zero input multiplier",
			rule: func() Rule {
				rule := validBuiltin("gpt-5.6")
				rule.LongContextPolicy.InputMultiplier = Multiplier{}
				return rule
			}(),
		},
		{
			name: "negative output multiplier numerator",
			rule: func() Rule {
				rule := validBuiltin("gpt-5.6")
				rule.LongContextPolicy.OutputMultiplier = Multiplier{Numerator: -1, Denominator: 1}
				return rule
			}(),
		},
		{
			name: "negative input multiplier denominator",
			rule: func() Rule {
				rule := validBuiltin("gpt-5.6")
				rule.LongContextPolicy.InputMultiplier = Multiplier{Numerator: 1, Denominator: -1}
				return rule
			}(),
		},
		{
			name: "zero output multiplier denominator",
			rule: func() Rule {
				rule := validBuiltin("gpt-5.6")
				rule.LongContextPolicy.OutputMultiplier = Multiplier{Numerator: 1}
				return rule
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Compile([]Rule{test.rule}); err == nil {
				t.Fatal("Compile() error = nil, want invalid long-context policy error")
			}
		})
	}
}

func TestCompileCopiesLongContextPolicy(t *testing.T) {
	t.Parallel()

	policy := &LongContextPolicy{
		InputThresholdTokens: 272_000,
		InputMultiplier:      Multiplier{Numerator: 2, Denominator: 1},
		OutputMultiplier:     Multiplier{Numerator: 3, Denominator: 2},
	}
	rules := []Rule{{
		Pattern:           "gpt-5.6",
		Prices:            priced(1),
		Source:            SourceBuiltin,
		SourceURL:         "https://builtin.example/pricing",
		UpdatedAt:         time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC),
		LongContextPolicy: policy,
	}}
	table, err := Compile(rules)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	policy.InputThresholdTokens = 1
	policy.InputMultiplier = Multiplier{Numerator: 99, Denominator: 1}
	first, ok := table.Match("gpt-5.6")
	if !ok || first.LongContextPolicy == nil {
		t.Fatalf("first Match() = %+v, %t; want policy", first, ok)
	}
	if first.LongContextPolicy.InputThresholdTokens != 272_000 ||
		first.LongContextPolicy.InputMultiplier != (Multiplier{Numerator: 2, Denominator: 1}) ||
		first.LongContextPolicy.OutputMultiplier != (Multiplier{Numerator: 3, Denominator: 2}) {
		t.Fatalf("first Match() policy = %+v, want original values", first.LongContextPolicy)
	}
	if first.LongContextPolicy == policy {
		t.Fatal("Compile() retained the caller's policy pointer")
	}

	first.LongContextPolicy.OutputMultiplier = Multiplier{Numerator: 99, Denominator: 1}
	second, ok := table.Match("gpt-5.6")
	if !ok || second.LongContextPolicy == nil {
		t.Fatalf("second Match() = %+v, %t; want policy", second, ok)
	}
	if second.LongContextPolicy.OutputMultiplier != (Multiplier{Numerator: 3, Denominator: 2}) {
		t.Fatalf("second Match() policy = %+v, want immutable copy", second.LongContextPolicy)
	}
	if second.LongContextPolicy == first.LongContextPolicy {
		t.Fatal("Match() returned its internal policy pointer")
	}
}

func TestCompileCopiesInputAndReturnsRuleValues(t *testing.T) {
	t.Parallel()

	rules := []Rule{{Pattern: "gpt-*", Prices: priced(1), Source: SourceUser}}
	table, err := Compile(rules)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	rules[0].Pattern = "other-*"
	rules[0].Prices.Output.NanoUSDPerMillion = 99

	first, ok := table.Match("gpt-4o")
	if !ok || first.Pattern != "gpt-*" || first.Prices.Output.NanoUSDPerMillion != 1 {
		t.Fatalf("first Match() = %+v, %t; want unmodified gpt-* rule", first, ok)
	}
	first.Pattern = "other-*"
	first.Prices.Output.NanoUSDPerMillion = 99
	second, ok := table.Match("gpt-4o")
	if !ok || second.Pattern != "gpt-*" || second.Prices.Output.NanoUSDPerMillion != 1 {
		t.Fatalf("second Match() = %+v, %t; want unmodified gpt-* rule", second, ok)
	}
}

func TestTableMatchIsConcurrentAndImmutable(t *testing.T) {
	t.Parallel()

	table, err := Compile([]Rule{
		{Pattern: "gpt-*", Prices: priced(1), Source: SourceUser},
		{Pattern: "*", Prices: priced(2), Source: SourceBuiltin, SourceURL: "https://builtin.example/pricing", UpdatedAt: time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	const goroutines = 32
	const matchesPerGoroutine = 200
	errors := make(chan string, goroutines)
	var waitGroup sync.WaitGroup
	for range goroutines {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for range matchesPerGoroutine {
				rule, ok := table.Match("gpt-4o")
				if !ok || rule.Pattern != "gpt-*" || rule.Prices.Output.NanoUSDPerMillion != 1 {
					errors <- "concurrent Match returned an unexpected rule"
					return
				}
				rule.Pattern = "mutated"
				rule.Prices.Output.NanoUSDPerMillion = 99
			}
		}()
	}
	waitGroup.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}

	rule, ok := table.Match("gpt-4o")
	if !ok || rule.Pattern != "gpt-*" || rule.Prices.Output.NanoUSDPerMillion != 1 {
		t.Fatalf("final Match() = %+v, %t; want unmodified gpt-* rule", rule, ok)
	}
}

func priced(value NanoUSD) Prices {
	return Prices{Output: fixedPrice(value)}
}

func replacePattern(rule Rule, pattern string) Rule {
	rule.Pattern = pattern
	return rule
}
