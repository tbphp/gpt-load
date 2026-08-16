package pricing

// Mode identifies the price schedule selected for one request. Mode prices
// remain provider-neutral and contain no routing behavior.
type Mode string

const (
	ModeStandard Mode = "standard"
	ModeFast     Mode = "fast"
)

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

// Identity is the exact channel and upstream-model pricing identity.
type Identity struct {
	ChannelID string `json:"channel_id"`
	ModelID   string `json:"model_id"`
}

// ReceiptRule is the frozen model identity written into a request-time cost
// receipt. ScopeKey is retained only to read historical v1 receipts; ChannelID
// is populated only by v3 and later receipts.
type ReceiptRule struct {
	ScopeKey  string `json:"scope_key,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
	ModelID   string `json:"model_id"`
}

// ContextTier replaces all base prices once its inclusive threshold is met.
type ContextTier struct {
	InputThresholdTokens int64
	Prices               Prices
}

// Schedule is one complete price schedule. Fast uses only Prices; other
// canonical modes may define their own context tiers when their source does.
type Schedule struct {
	Prices       Prices
	ContextTiers []ContextTier
}

// Rule is one exact channel and upstream-model price definition.
type Rule struct {
	Identity      Identity
	Prices        Prices
	ContextTiers  []ContextTier
	ModeSchedules map[Mode]Schedule
	IsManual      bool
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

// ReceiptMethodUnitRateSum identifies the stable line-item calculation used by
// the first persisted pricing receipt schema.
const ReceiptMethodUnitRateSum = "unit_rate_sum"

// ReceiptLineState distinguishes a priced component from a positive component
// whose exact rate was unavailable at request time.
type ReceiptLineState string

const (
	ReceiptLinePriced   ReceiptLineState = "priced"
	ReceiptLineUnpriced ReceiptLineState = "unpriced"
)

// ReceiptLine is one frozen term in the request-time cost calculation.
type ReceiptLine struct {
	Code                  string           `json:"code"`
	Quantity              int64            `json:"quantity"`
	RateNanoUSDPerMillion *int64           `json:"rate_nano_usd_per_million,omitempty"`
	Multiplier            Multiplier       `json:"multiplier"`
	State                 ReceiptLineState `json:"state"`
	AmountNanoUSD         *int64           `json:"amount_nano_usd,omitempty"`
}

// Receipt is the immutable, versioned explanation of one request-time quote.
// It deliberately stores calculation inputs instead of a presentation string.
type Receipt struct {
	SchemaVersion          int           `json:"schema_version"`
	Method                 string        `json:"method"`
	MethodVersion          int           `json:"method_version"`
	Currency               string        `json:"currency"`
	PricingMode            Mode          `json:"pricing_mode,omitempty"`
	Rule                   ReceiptRule   `json:"rule"`
	ContextThresholdTokens *int64        `json:"context_threshold_tokens,omitempty"`
	LineItems              []ReceiptLine `json:"line_items"`
	TotalNanoUSD           int64         `json:"total_nano_usd"`
}

// Table is an immutable exact pricing snapshot.
type Table struct {
	rules map[Identity]Rule
}
