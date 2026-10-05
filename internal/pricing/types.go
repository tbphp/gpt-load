package pricing

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Mode identifies the price schedule selected for one request. Mode prices
// remain provider-neutral and contain no routing behavior.
type Mode string

const (
	ModeStandard  Mode = "standard"
	ModeFast      Mode = "fast"
	ModeUltrafast Mode = "ultrafast"
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

// PolicyFactor 记录单条命中的动态策略价格因子及其元数据快照。
type PolicyFactor struct {
	RuleID       string          `json:"rule_id"`
	NameSnapshot string          `json:"name_snapshot"`
	BindingScope string          `json:"binding_scope"` // "group" 或 "credential"
	Revision     uint64          `json:"revision"`
	Factor       string          `json:"factor"`
	Multiplier   PriceMultiplier `json:"multiplier"`
}

// UnmarshalJSON 严格反序列化 PolicyFactor，拒绝缺失或显式 null 的必要字段，并拒绝未知、别名和重复字段。
func (factor *PolicyFactor) UnmarshalJSON(data []byte) error {
	canonicalKeys := map[string]struct{}{
		"rule_id":       {},
		"name_snapshot": {},
		"binding_scope": {},
		"revision":      {},
		"factor":        {},
		"multiplier":    {},
	}
	rawValues, err := decodeStrictObjectFields(data, canonicalKeys, "policy factor")
	if err != nil {
		return err
	}

	for req := range canonicalKeys {
		if missingOrNull(rawValues, req) {
			return fmt.Errorf("pricing: policy factor missing or null required field %q", req)
		}
	}

	type wire PolicyFactor
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if strings.TrimSpace(decoded.RuleID) == "" || strings.TrimSpace(decoded.NameSnapshot) == "" {
		return fmt.Errorf("pricing: policy factor rule_id and name_snapshot must not be empty")
	}
	if decoded.BindingScope != "group" && decoded.BindingScope != "credential" {
		return fmt.Errorf("pricing: policy factor binding_scope must be group or credential")
	}
	if decoded.Factor == "" || strings.TrimSpace(decoded.Factor) != decoded.Factor {
		return fmt.Errorf("pricing: policy factor factor must be a non-empty string without whitespace")
	}
	parsed, err := ParsePriceMultiplier(decoded.Factor)
	if err != nil || parsed != decoded.Multiplier {
		return fmt.Errorf("pricing: invalid policy factor multiplier or factor mismatch")
	}
	if decoded.Revision == 0 {
		return fmt.Errorf("pricing: policy factor revision must be positive")
	}
	*factor = PolicyFactor(decoded)
	return nil
}

// Receipt is the immutable, versioned explanation of one request-time quote.
// It deliberately stores calculation inputs instead of a presentation string.
type Receipt struct {
	SchemaVersion          int               `json:"schema_version"`
	Method                 string            `json:"method"`
	MethodVersion          int               `json:"method_version"`
	Currency               string            `json:"currency"`
	PricingMode            Mode              `json:"pricing_mode,omitempty"`
	PriceMultipliers       *PriceMultipliers `json:"price_multipliers,omitempty"`
	BaseTotalNanoUSD       *int64            `json:"base_total_nano_usd,omitempty"`
	PolicyFactors          []PolicyFactor    `json:"policy_factors,omitempty"`
	Rule                   ReceiptRule       `json:"rule"`
	ContextThresholdTokens *int64            `json:"context_threshold_tokens,omitempty"`
	LineItems              []ReceiptLine     `json:"line_items"`
	TotalNanoUSD           int64             `json:"total_nano_usd"`
}

// Table is an immutable exact pricing snapshot.
type Table struct {
	rules map[Identity]Rule
}
