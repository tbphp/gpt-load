package pricing

import "time"

// Price is a USD price per one million tokens. Set distinguishes zero from an
// unavailable price.
type Price struct {
	Value float64
	Set   bool
}

// Prices is the provider-neutral price breakdown.
type Prices struct {
	UncachedInput Price
	CacheRead     Price
	CacheWrite5M  Price
	CacheWrite1H  Price
	Output        Price
}

// Source identifies where a pricing rule came from.
type Source string

const (
	SourceBuiltin Source = "builtin"
	SourceUser    Source = "user"
)

// Rule matches an upstream model pattern to a price breakdown.
type Rule struct {
	Pattern   string
	Prices    Prices
	Source    Source
	SourceURL string
	UpdatedAt time.Time
}

// CostState describes whether a usage result can be priced.
type CostState string

const (
	CostStatePriced        CostState = "priced"
	CostStateUnpriced      CostState = "unpriced"
	CostStateNotApplicable CostState = "not_applicable"
)

// Quote is a calculated request cost in USD.
type Quote struct {
	State CostState
	Cost  float64
}

// Table is an immutable compiled set of pricing rules.
type Table struct {
	userExact       map[string]Rule
	userPrefixes    []Rule
	builtinExact    map[string]Rule
	builtinPrefixes []Rule
}
