package pricing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
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
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("pricing: policy factor must be a JSON object")
	}

	canonicalKeys := map[string]struct{}{
		"rule_id":       {},
		"name_snapshot": {},
		"binding_scope": {},
		"revision":      {},
		"factor":        {},
		"multiplier":    {},
	}
	seenExact := make(map[string]struct{})
	seenLower := make(map[string]struct{})
	rawValues := make(map[string]json.RawMessage)

	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("pricing: object key must be a string")
		}
		lowerKey := strings.ToLower(key)
		if _, exists := seenLower[lowerKey]; exists {
			return fmt.Errorf("pricing: duplicate or alias policy factor field %q", key)
		}
		seenLower[lowerKey] = struct{}{}
		seenExact[key] = struct{}{}

		if _, ok := canonicalKeys[key]; !ok {
			return fmt.Errorf("pricing: unknown policy factor field %q", key)
		}

		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		rawValues[key] = raw
	}

	for req := range canonicalKeys {
		raw, ok := rawValues[req]
		if !ok || len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("pricing: policy factor missing or null required field %q", req)
		}
	}

	var ruleID string
	if err := json.Unmarshal(rawValues["rule_id"], &ruleID); err != nil || strings.TrimSpace(ruleID) == "" {
		return fmt.Errorf("pricing: policy factor rule_id must not be empty")
	}

	var nameSnapshot string
	if err := json.Unmarshal(rawValues["name_snapshot"], &nameSnapshot); err != nil || strings.TrimSpace(nameSnapshot) == "" {
		return fmt.Errorf("pricing: policy factor name_snapshot must not be empty")
	}

	var bindingScope string
	if err := json.Unmarshal(rawValues["binding_scope"], &bindingScope); err != nil || (bindingScope != "group" && bindingScope != "credential") {
		return fmt.Errorf("pricing: policy factor binding_scope must be group or credential")
	}

	var factorStr string
	if err := json.Unmarshal(rawValues["factor"], &factorStr); err != nil || factorStr == "" || strings.TrimSpace(factorStr) != factorStr {
		return fmt.Errorf("pricing: policy factor factor must be a non-empty string without whitespace")
	}

	var mult PriceMultiplier
	if err := json.Unmarshal(rawValues["multiplier"], &mult); err != nil || !mult.Valid() {
		return fmt.Errorf("pricing: policy factor multiplier invalid: %w", err)
	}

	parsedMult, err := ParsePriceMultiplier(factorStr)
	if err != nil {
		return fmt.Errorf("pricing: policy factor factor string invalid: %w", err)
	}
	if parsedMult != mult {
		return fmt.Errorf("pricing: policy factor factor %q does not match multiplier %v", factorStr, mult)
	}

	var revUint uint64
	if err := json.Unmarshal(rawValues["revision"], &revUint); err != nil {
		var revStr string
		if err2 := json.Unmarshal(rawValues["revision"], &revStr); err2 != nil {
			return fmt.Errorf("pricing: policy factor revision must be uint64 or string: %w", err)
		}
		if strings.TrimSpace(revStr) != revStr || revStr == "" {
			return fmt.Errorf("pricing: policy factor revision string invalid")
		}
		parsedRev, err3 := strconv.ParseUint(revStr, 10, 64)
		if err3 != nil {
			return fmt.Errorf("pricing: policy factor revision invalid: %w", err3)
		}
		revUint = parsedRev
	}
	if revUint == 0 {
		return fmt.Errorf("pricing: policy factor revision must be positive")
	}

	factor.RuleID = ruleID
	factor.NameSnapshot = nameSnapshot
	factor.BindingScope = bindingScope
	factor.Revision = revUint
	factor.Factor = factorStr
	factor.Multiplier = mult
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
