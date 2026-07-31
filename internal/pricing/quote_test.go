package pricing

import (
	"math"
	"testing"
	"time"

	"gpt-load/internal/usage"
)

func TestQuoteRoundsFiveTokenComponentsBeforeSumming(t *testing.T) {
	t.Parallel()

	table := mustCompile(t, Rule{
		Pattern: "model",
		Prices: Prices{
			UncachedInput: fixedPrice(1),
			CacheRead:     fixedPrice(1),
			CacheWrite5M:  fixedPrice(1),
			CacheWrite1H:  fixedPrice(1),
			Output:        fixedPrice(1),
		},
		Source: SourceUser,
	})
	quote := table.Quote("model", usage.Result{
		Tokens: usage.Tokens{
			UncachedInput: 500_000,
			CacheRead:     500_000,
			CacheWrite5M:  500_000,
			CacheWrite1H:  500_000,
			Output:        500_000,
		},
		State: usage.StateComplete,
	})
	if quote.State != CostStatePriced {
		t.Fatalf("Quote() state = %q, want %q", quote.State, CostStatePriced)
	}
	if quote.EstimatedCostNanoUSD != 5 {
		t.Fatalf("Quote() cost = %d, want 5 nano USD", quote.EstimatedCostNanoUSD)
	}
}

func TestQuoteAppliesBuiltinLongContextPolicyAtStrictThreshold(t *testing.T) {
	t.Parallel()

	table := mustCompile(t, Rule{
		Pattern: "gpt-5.6",
		Prices: Prices{
			UncachedInput: fixedPrice(1_000_000_000),
			CacheRead:     fixedPrice(2_000_000_000),
			CacheWrite5M:  fixedPrice(3_000_000_000),
			CacheWrite1H:  fixedPrice(4_000_000_000),
			Output:        fixedPrice(5_000_000_000),
		},
		Source:            SourceBuiltin,
		SourceURL:         "https://builtin.example/pricing",
		UpdatedAt:         time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC),
		LongContextPolicy: testLongContextPolicy(),
	})
	tests := []struct {
		name   string
		tokens usage.Tokens
		want   NanoUSD
	}{
		{
			name: "equal threshold does not multiply",
			tokens: usage.Tokens{
				UncachedInput: 68_000,
				CacheRead:     68_000,
				CacheWrite5M:  68_000,
				CacheWrite1H:  68_000,
				Output:        1_000_000,
			},
			want: 5_680_000_000,
		},
		{
			name: "one token over multiplies every input price and output price",
			tokens: usage.Tokens{
				UncachedInput: 68_000,
				CacheRead:     68_000,
				CacheWrite5M:  68_000,
				CacheWrite1H:  68_001,
				Output:        1_000_000,
			},
			want: 8_860_008_000,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := usage.Result{
				Tokens: test.tokens,
				State:  usage.StateComplete,
			}
			before := result.Tokens

			quote := table.Quote("gpt-5.6", result)

			if quote.State != CostStatePriced {
				t.Fatalf("Quote() state = %q, want %q", quote.State, CostStatePriced)
			}
			if quote.EstimatedCostNanoUSD != test.want {
				t.Fatalf("Quote() cost = %d, want %d nano USD", quote.EstimatedCostNanoUSD, test.want)
			}
			if result.Tokens != before {
				t.Fatalf("Quote() mutated tokens: got %+v, want %+v", result.Tokens, before)
			}
		})
	}
}

func TestQuoteFailsClosedOnMissingPriceAndOverflow(t *testing.T) {
	t.Parallel()

	baseRule := Rule{
		Pattern: "model",
		Prices: Prices{
			UncachedInput: fixedPrice(1),
			CacheRead:     fixedPrice(1),
			CacheWrite5M:  fixedPrice(1),
			CacheWrite1H:  fixedPrice(1),
			Output:        fixedPrice(1),
		},
		Source:            SourceBuiltin,
		SourceURL:         "https://builtin.example/pricing",
		UpdatedAt:         time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC),
		LongContextPolicy: testLongContextPolicy(),
	}
	missingPrice := baseRule
	missingPrice.Prices.CacheWrite1H = Price{}
	componentOverflow := baseRule
	componentOverflow.Prices.Output = fixedPrice(math.MaxInt64)
	sumOverflow := baseRule
	sumOverflow.Prices.UncachedInput = fixedPrice(math.MaxInt64)
	sumOverflow.Prices.Output = fixedPrice(1)

	tests := []struct {
		name   string
		rule   Rule
		tokens usage.Tokens
	}{
		{
			name: "four-input token sum overflow",
			rule: baseRule,
			tokens: usage.Tokens{
				UncachedInput: math.MaxInt64,
				CacheRead:     1,
			},
		},
		{
			name: "missing used component price",
			rule: missingPrice,
			tokens: usage.Tokens{
				UncachedInput: 272_000,
				CacheWrite1H:  1,
			},
		},
		{
			name:   "component result overflow",
			rule:   componentOverflow,
			tokens: usage.Tokens{Output: 1_000_001},
		},
		{
			name: "final sum overflow",
			rule: sumOverflow,
			tokens: usage.Tokens{
				UncachedInput: 1_000_000,
				Output:        1_000_000,
			},
		},
		{
			name:   "multiplied result overflow",
			rule:   componentOverflow,
			tokens: usage.Tokens{UncachedInput: 272_001, Output: 1_000_000},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			quote := mustCompile(t, test.rule).Quote("model", usage.Result{
				Tokens: test.tokens,
				State:  usage.StateComplete,
			})
			if quote.State != CostStateUnpriced || quote.EstimatedCostNanoUSD != 0 {
				t.Fatalf("Quote() = %+v, want unpriced zero", quote)
			}
		})
	}
}

func TestQuoteUserOverridesDoNotInheritBuiltinLongContextPolicy(t *testing.T) {
	t.Parallel()

	for _, pattern := range []string{"gpt-5.6", "gpt-5.*", "*"} {
		t.Run(pattern, func(t *testing.T) {
			rules := BuiltinRules()
			rules = append(rules, Rule{
				Pattern: pattern,
				Prices: Prices{
					UncachedInput: fixedPrice(1_000_000_000),
					Output:        fixedPrice(1_000_000_000),
				},
				Source: SourceUser,
			})
			table, err := Compile(rules)
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}

			quote := table.Quote("gpt-5.6", usage.Result{
				Tokens: usage.Tokens{
					UncachedInput: 272_001,
					Output:        1_000_000,
				},
				State: usage.StateComplete,
			})

			if quote.State != CostStatePriced {
				t.Fatalf("Quote() state = %q, want %q", quote.State, CostStatePriced)
			}
			if quote.EstimatedCostNanoUSD != 1_272_001_000 {
				t.Fatalf("Quote() cost = %d, want 1272001000 nano USD", quote.EstimatedCostNanoUSD)
			}
		})
	}
}

func TestQuoteDistinguishesUnsetFromExplicitZero(t *testing.T) {
	t.Parallel()

	explicitZero := mustCompile(t, Rule{
		Pattern: "zero",
		Prices:  Prices{Output: fixedPrice(0)},
		Source:  SourceUser,
	})
	zeroOutput := explicitZero.Quote("zero", usage.Result{
		Tokens: usage.Tokens{Output: 1},
		State:  usage.StateComplete,
	})
	if zeroOutput.State != CostStatePriced || zeroOutput.EstimatedCostNanoUSD != 0 {
		t.Fatalf("explicit zero quote = %+v, want priced zero", zeroOutput)
	}

	unsetRead := explicitZero.Quote("zero", usage.Result{
		Tokens: usage.Tokens{CacheRead: 1},
		State:  usage.StateComplete,
	})
	if unsetRead.State != CostStateUnpriced || unsetRead.EstimatedCostNanoUSD != 0 {
		t.Fatalf("unset cache read quote = %+v, want unpriced zero", unsetRead)
	}

	zeroTokens := explicitZero.Quote("zero", usage.Result{State: usage.StateComplete})
	if zeroTokens.State != CostStatePriced || zeroTokens.EstimatedCostNanoUSD != 0 {
		t.Fatalf("zero-token quote = %+v, want priced zero", zeroTokens)
	}
}

func TestQuoteMapsUsageAndDiagnosticsToCostState(t *testing.T) {
	t.Parallel()

	table := mustCompile(t, Rule{
		Pattern: "model",
		Prices:  Prices{Output: fixedPrice(1_000_000_000)},
		Source:  SourceUser,
	})
	assertUnpriced := func(t *testing.T, quote Quote) {
		t.Helper()
		if quote.State != CostStateUnpriced || quote.EstimatedCostNanoUSD != 0 {
			t.Fatalf("Quote() = %+v, want unpriced zero", quote)
		}
	}
	assertUnpriced(t, table.Quote("missing", usage.Result{State: usage.StateComplete}))
	if quote := table.Quote("model", usage.Result{State: usage.StateNotApplicable}); quote.State != CostStateNotApplicable || quote.EstimatedCostNanoUSD != 0 {
		t.Fatalf("not-applicable quote = %+v, want not_applicable zero", quote)
	}
	assertUnpriced(t, table.Quote("model", usage.Result{State: usage.StateMissing}))
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
		if quote.State != CostStateUnpriced || quote.EstimatedCostNanoUSD != 0 {
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
	assertUnpriced(t, table.Quote("model", usage.Result{State: usage.State("unknown")}))
}

func TestQuoteRejectsNegativeTokensAndTokenTotalOverflow(t *testing.T) {
	t.Parallel()

	table := mustCompile(t, Rule{
		Pattern: "model",
		Prices: Prices{
			UncachedInput: fixedPrice(0),
			CacheRead:     fixedPrice(0),
			CacheWrite5M:  fixedPrice(0),
			CacheWrite1H:  fixedPrice(0),
			Output:        fixedPrice(0),
		},
		Source: SourceUser,
	})
	perFieldOverflow := int64(math.MaxInt64/5 + 1)
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
			if quote.State != CostStateUnpriced || quote.EstimatedCostNanoUSD != 0 {
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

func fixedPrice(value NanoUSD) Price {
	return Price{NanoUSDPerMillion: value, Set: true}
}

func testLongContextPolicy() *LongContextPolicy {
	return &LongContextPolicy{
		InputThresholdTokens: 272_000,
		InputMultiplier:      Multiplier{Numerator: 2, Denominator: 1},
		OutputMultiplier:     Multiplier{Numerator: 3, Denominator: 2},
	}
}
