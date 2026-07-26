package pricing

import (
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

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
			rule.Prices.Output.Value != 99 {
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
		{name: "negative price", rule: Rule{Pattern: "gpt-4o", Prices: Prices{Output: Price{Value: -1, Set: true}}, Source: SourceUser}},
		{name: "negative unset price", rule: Rule{Pattern: "gpt-4o", Prices: Prices{UncachedInput: Price{Value: -1}, Output: Price{Value: 1, Set: true}}, Source: SourceUser}},
		{name: "nan price", rule: Rule{Pattern: "gpt-4o", Prices: Prices{Output: Price{Value: math.NaN(), Set: true}}, Source: SourceUser}},
		{name: "positive infinity price", rule: Rule{Pattern: "gpt-4o", Prices: Prices{Output: Price{Value: math.Inf(1), Set: true}}, Source: SourceUser}},
		{name: "negative infinity price", rule: Rule{Pattern: "gpt-4o", Prices: Prices{Output: Price{Value: math.Inf(-1), Set: true}}, Source: SourceUser}},
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

func TestCompileCopiesInputAndReturnsRuleValues(t *testing.T) {
	t.Parallel()

	rules := []Rule{{Pattern: "gpt-*", Prices: priced(1), Source: SourceUser}}
	table, err := Compile(rules)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	rules[0].Pattern = "other-*"
	rules[0].Prices.Output.Value = 99

	first, ok := table.Match("gpt-4o")
	if !ok || first.Pattern != "gpt-*" || first.Prices.Output.Value != 1 {
		t.Fatalf("first Match() = %+v, %t; want unmodified gpt-* rule", first, ok)
	}
	first.Pattern = "other-*"
	first.Prices.Output.Value = 99
	second, ok := table.Match("gpt-4o")
	if !ok || second.Pattern != "gpt-*" || second.Prices.Output.Value != 1 {
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
				if !ok || rule.Pattern != "gpt-*" || rule.Prices.Output.Value != 1 {
					errors <- "concurrent Match returned an unexpected rule"
					return
				}
				rule.Pattern = "mutated"
				rule.Prices.Output.Value = 99
			}
		}()
	}
	waitGroup.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}

	rule, ok := table.Match("gpt-4o")
	if !ok || rule.Pattern != "gpt-*" || rule.Prices.Output.Value != 1 {
		t.Fatalf("final Match() = %+v, %t; want unmodified gpt-* rule", rule, ok)
	}
}

func priced(value float64) Prices {
	return Prices{Output: Price{Value: value, Set: true}}
}

func replacePattern(rule Rule, pattern string) Rule {
	rule.Pattern = pattern
	return rule
}
