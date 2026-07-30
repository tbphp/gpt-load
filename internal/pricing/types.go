package pricing

import "time"

// Price is a USD price per one million tokens. Set distinguishes zero from an
// unavailable price.
type Price struct {
	NanoUSDPerMillion NanoUSD
	Set               bool
}

// Prices is the provider-neutral price breakdown.
type Prices struct {
	UncachedInput Price
	CacheRead     Price
	CacheWrite5M  Price
	CacheWrite1H  Price
	Output        Price
}

// LongContextPolicy adjusts component prices after an input-token threshold.
type LongContextPolicy struct {
	InputThresholdTokens int64
	InputMultiplier      Multiplier
	OutputMultiplier     Multiplier
}

// Source identifies where a pricing rule came from.
type Source string

const (
	SourceBuiltin Source = "builtin"
	SourceUser    Source = "user"
)

// Rule matches an upstream model pattern to a price breakdown.
type Rule struct {
	Pattern           string
	Prices            Prices
	Source            Source
	SourceURL         string
	UpdatedAt         time.Time
	LongContextPolicy *LongContextPolicy
}

// CostState describes whether a usage result can be priced.
type CostState string

const (
	CostStatePriced        CostState = "priced"
	CostStateUnpriced      CostState = "unpriced"
	CostStateNotApplicable CostState = "not_applicable"
)

// Quote is a calculated request cost in nano USD.
type Quote struct {
	State                CostState
	EstimatedCostNanoUSD NanoUSD
}

// Table is an immutable compiled set of pricing rules.
type Table struct {
	userExact       map[string]Rule
	userPrefixes    []Rule
	builtinExact    map[string]Rule
	builtinPrefixes []Rule
}
