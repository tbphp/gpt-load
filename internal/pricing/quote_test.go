package pricing

import (
	"math"
	"testing"

	"gpt-load/internal/usage"
)

func TestQuoteUsesFiveTokenPricesWithoutRounding(t *testing.T) {
	t.Parallel()

	table := mustCompile(t, Rule{
		Pattern: "model",
		Prices: Prices{
			UncachedInput: Price{Value: 1.11, Set: true},
			CacheRead:     Price{Value: 2.22, Set: true},
			CacheWrite5M:  Price{Value: 3.33, Set: true},
			CacheWrite1H:  Price{Value: 4.44, Set: true},
			Output:        Price{Value: 5.55, Set: true},
		},
		Source: SourceUser,
	})
	quote := table.Quote("model", usage.Result{
		Tokens: usage.Tokens{
			UncachedInput: 1_000_000,
			CacheRead:     1_000_000,
			CacheWrite5M:  1_000_000,
			CacheWrite1H:  1_000_000,
			Output:        1_000_000,
		},
		State: usage.StateComplete,
	})
	if quote.State != CostStatePriced {
		t.Fatalf("Quote() state = %q, want %q", quote.State, CostStatePriced)
	}
	assertCostClose(t, quote.Cost, 16.65)
}

func TestQuoteDistinguishesUnsetFromExplicitZero(t *testing.T) {
	t.Parallel()

	explicitZero := mustCompile(t, Rule{
		Pattern: "zero",
		Prices:  Prices{Output: Price{Value: 0, Set: true}},
		Source:  SourceUser,
	})
	zeroOutput := explicitZero.Quote("zero", usage.Result{
		Tokens: usage.Tokens{Output: 1},
		State:  usage.StateComplete,
	})
	if zeroOutput.State != CostStatePriced {
		t.Fatalf("explicit zero output state = %q, want %q", zeroOutput.State, CostStatePriced)
	}
	assertCostClose(t, zeroOutput.Cost, 0)

	unsetRead := explicitZero.Quote("zero", usage.Result{
		Tokens: usage.Tokens{CacheRead: 1},
		State:  usage.StateComplete,
	})
	if unsetRead.State != CostStateUnpriced || unsetRead.Cost != 0 {
		t.Fatalf("unset cache read quote = %+v, want unpriced zero", unsetRead)
	}

	zeroTokens := explicitZero.Quote("zero", usage.Result{State: usage.StateComplete})
	if zeroTokens.State != CostStatePriced {
		t.Fatalf("zero-token quote state = %q, want %q", zeroTokens.State, CostStatePriced)
	}
	assertCostClose(t, zeroTokens.Cost, 0)
}

func TestQuoteMapsUsageAndDiagnosticsToCostState(t *testing.T) {
	t.Parallel()

	table := mustCompile(t, Rule{
		Pattern: "model",
		Prices:  Prices{Output: Price{Value: 1, Set: true}},
		Source:  SourceUser,
	})
	if quote := table.Quote("missing", usage.Result{State: usage.StateComplete}); quote.State != CostStateUnpriced || quote.Cost != 0 {
		t.Fatalf("unmatched complete quote = %+v, want unpriced zero", quote)
	}
	if quote := table.Quote("model", usage.Result{State: usage.StateNotApplicable}); quote.State != CostStateNotApplicable || quote.Cost != 0 {
		t.Fatalf("not-applicable quote = %+v, want not_applicable zero", quote)
	}
	if quote := table.Quote("model", usage.Result{State: usage.StateMissing}); quote.State != CostStateUnpriced || quote.Cost != 0 {
		t.Fatalf("missing quote = %+v, want unpriced zero", quote)
	}
	for _, state := range []usage.State{usage.StateComplete, usage.StatePartial} {
		quote := table.Quote("model", usage.Result{Tokens: usage.Tokens{Output: 1}, State: state})
		if quote.State != CostStatePriced {
			t.Errorf("%s quote state = %q, want %q", state, quote.State, CostStatePriced)
		}
	}
	for _, diagnostic := range []usage.DiagnosticCode{
		usage.DiagnosticUnsupportedBillableDetail,
		usage.DiagnosticNegativeValue,
		usage.DiagnosticInvalidNumber,
		usage.DiagnosticMissingRequiredField,
		usage.DiagnosticInconsistentTotal,
		usage.DiagnosticInvalidEventSequence,
	} {
		var diagnostics usage.Diagnostics
		diagnostics.Add(diagnostic)
		quote := table.Quote("model", usage.Result{
			Tokens:      usage.Tokens{Output: 1},
			State:       usage.StateComplete,
			Diagnostics: diagnostics,
		})
		if quote.State != CostStateUnpriced || quote.Cost != 0 {
			t.Errorf("diagnostic %q quote = %+v, want unpriced zero", diagnostic, quote)
		}
	}
	for _, diagnostic := range []usage.DiagnosticCode{
		usage.DiagnosticCacheWriteDefaulted5M,
		usage.DiagnosticInvalidPayload,
	} {
		var diagnostics usage.Diagnostics
		diagnostics.Add(diagnostic)
		quote := table.Quote("model", usage.Result{
			Tokens:      usage.Tokens{Output: 1},
			State:       usage.StateComplete,
			Diagnostics: diagnostics,
		})
		if quote.State != CostStatePriced {
			t.Errorf("diagnostic %q quote state = %q, want %q", diagnostic, quote.State, CostStatePriced)
		}
	}
	if quote := table.Quote("model", usage.Result{State: usage.State("unknown")}); quote.State != CostStateUnpriced || quote.Cost != 0 {
		t.Fatalf("unknown usage state quote = %+v, want unpriced zero", quote)
	}
}

func TestQuoteRejectsNonFiniteMultiplicationAndAccumulation(t *testing.T) {
	t.Parallel()

	multiplication := mustCompile(t, Rule{
		Pattern: "multiply",
		Prices:  Prices{Output: Price{Value: math.MaxFloat64, Set: true}},
		Source:  SourceUser,
	})
	if quote := multiplication.Quote("multiply", usage.Result{
		Tokens: usage.Tokens{Output: 2_000_000},
		State:  usage.StateComplete,
	}); quote.State != CostStateUnpriced || quote.Cost != 0 {
		t.Fatalf("non-finite multiplication quote = %+v, want unpriced zero", quote)
	}

	accumulation := mustCompile(t, Rule{
		Pattern: "accumulate",
		Prices: Prices{
			UncachedInput: Price{Value: math.MaxFloat64 / 2, Set: true},
			CacheRead:     Price{Value: math.MaxFloat64 / 2, Set: true},
			Output:        Price{Value: math.MaxFloat64 / 2, Set: true},
		},
		Source: SourceUser,
	})
	if quote := accumulation.Quote("accumulate", usage.Result{
		Tokens: usage.Tokens{UncachedInput: 1_000_000, CacheRead: 1_000_000, Output: 1_000_000},
		State:  usage.StateComplete,
	}); quote.State != CostStateUnpriced || quote.Cost != 0 {
		t.Fatalf("non-finite accumulation quote = %+v, want unpriced zero", quote)
	}
}

func TestQuoteRejectsNegativeTokensAndTokenTotalOverflow(t *testing.T) {
	t.Parallel()

	table := mustCompile(t, Rule{
		Pattern: "model",
		Prices: Prices{
			UncachedInput: Price{Value: 0, Set: true},
			CacheRead:     Price{Value: 0, Set: true},
			CacheWrite5M:  Price{Value: 0, Set: true},
			CacheWrite1H:  Price{Value: 0, Set: true},
			Output:        Price{Value: 0, Set: true},
		},
		Source: SourceUser,
	})
	var perFieldOverflow int64 = math.MaxInt64/5 + 1
	tests := []struct {
		name   string
		tokens usage.Tokens
	}{
		{
			name:   "negative token",
			tokens: usage.Tokens{UncachedInput: -1},
		},
		{
			name: "five-field total overflow",
			tokens: usage.Tokens{
				UncachedInput: perFieldOverflow,
				CacheRead:     perFieldOverflow,
				CacheWrite5M:  perFieldOverflow,
				CacheWrite1H:  perFieldOverflow,
				Output:        perFieldOverflow,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			quote := table.Quote("model", usage.Result{
				Tokens: test.tokens,
				State:  usage.StateComplete,
			})
			if quote.State != CostStateUnpriced || quote.Cost != 0 {
				t.Fatalf("Quote() = %+v, want unpriced zero", quote)
			}
		})
	}
}

func mustCompile(t *testing.T, rules ...Rule) *Table {
	t.Helper()
	table, err := Compile(rules)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	return table
}

func assertCostClose(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("cost = %.17g, want %.17g", got, want)
	}
}
