package pricing

// Price is a USD price per one million tokens. Set distinguishes zero from an
// unavailable price.
type Price struct {
	NanoUSDPerMillion NanoUSD
	Set               bool
}

// Prices is the provider-neutral price breakdown.
type Prices struct {
	Input      Price
	Output     Price
	CacheRead  Price
	CacheWrite Price
}

// Identity is the exact pricing identity of a real upstream model in one
// canonical provider or group scope.
type Identity struct {
	ScopeKey string
	ModelID  string
}

// ContextTier replaces all base prices once its inclusive threshold is met.
type ContextTier struct {
	InputThresholdTokens int64
	Prices               Prices
}

// Rule is one exact scope and upstream-model price definition.
type Rule struct {
	Identity     Identity
	Prices       Prices
	ContextTiers []ContextTier
	IsManual     bool
}

// CostState describes whether a usage result can be priced.
type CostState string

const (
	CostStatePriced        CostState = "priced"
	CostStateUnpriced      CostState = "unpriced"
	CostStateNotApplicable CostState = "not_applicable"
)

// Completeness describes whether every billable usage dimension was priced.
type Completeness string

const (
	CompletenessComplete      Completeness = "complete"
	CompletenessPartial       Completeness = "partial"
	CompletenessUnavailable   Completeness = "unavailable"
	CompletenessNotApplicable Completeness = "not_applicable"
)

// Quote is a calculated request cost in nano USD.
type Quote struct {
	State                CostState
	Completeness         Completeness
	EstimatedCostNanoUSD NanoUSD
}

// Table is an immutable exact pricing snapshot.
type Table struct {
	rules map[Identity]Rule
}
