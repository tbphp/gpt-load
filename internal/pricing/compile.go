package pricing

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const (
	maxProviderIDBytes = 246
	maxChannelIDBytes  = 64
	maxModelIDBytes    = 255
	maxModeBytes       = 64
)

// Valid reports whether mode is a canonical Models.dev mode key.
func (mode Mode) Valid() bool {
	if len(mode) == 0 || len(mode) > maxModeBytes {
		return false
	}
	for index, character := range []byte(mode) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			continue
		}
		if index > 0 && index < len(mode)-1 && (character == '-' || character == '_') {
			continue
		}
		return false
	}
	return true
}

// ProviderScopeKey builds the canonical scope for a Models.dev provider ID.
func ProviderScopeKey(providerID string) (string, error) {
	if len(providerID) == 0 || len(providerID) > maxProviderIDBytes {
		return "", fmt.Errorf("provider ID must be 1 through %d ASCII bytes", maxProviderIDBytes)
	}
	separator := true
	for _, character := range []byte(providerID) {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			separator = false
		case character == '.' || character == '-':
			if separator {
				return "", fmt.Errorf("provider ID must contain non-empty lowercase slug segments")
			}
			separator = true
		default:
			return "", fmt.Errorf("provider ID must contain only lowercase ASCII slug segments")
		}
	}
	if separator {
		return "", fmt.Errorf("provider ID must contain non-empty lowercase slug segments")
	}
	return "provider:" + providerID, nil
}

// GroupScopeKey builds the canonical scope for a positive group ID.
func GroupScopeKey(groupID uint) (string, error) {
	if groupID == 0 {
		return "", fmt.Errorf("group ID must be positive")
	}
	return "group:" + strconv.FormatUint(uint64(groupID), 10), nil
}

// NewTable validates and deep-clones exact pricing rules.
func NewTable(rules []Rule) (*Table, error) {
	table := &Table{rules: make(map[Identity]Rule, len(rules))}
	for _, rule := range rules {
		if err := validateRule(rule); err != nil {
			return nil, err
		}
		if _, exists := table.rules[rule.Identity]; exists {
			return nil, fmt.Errorf(
				"duplicate pricing identity channel_id=%q model_id=%q",
				rule.Identity.ChannelID,
				rule.Identity.ModelID,
			)
		}
		table.rules[rule.Identity] = cloneRule(rule)
	}
	return table, nil
}

// Lookup returns an immutable copy of the exact identity rule.
func (table *Table) Lookup(identity Identity) (Rule, bool) {
	if table == nil {
		return Rule{}, false
	}
	rule, ok := table.rules[identity]
	if !ok {
		return Rule{}, false
	}
	return cloneRule(rule), true
}

func validateRule(rule Rule) error {
	if err := validateIdentity(rule.Identity); err != nil {
		return err
	}
	if err := validatePrices(rule.Prices); err != nil {
		return err
	}
	previousThreshold := int64(-1)
	for _, tier := range rule.ContextTiers {
		if tier.InputThresholdTokens < 0 {
			return fmt.Errorf("context tier threshold must be non-negative")
		}
		if tier.InputThresholdTokens <= previousThreshold {
			return fmt.Errorf("context tier thresholds must be strictly increasing")
		}
		if err := validatePrices(tier.Prices); err != nil {
			return err
		}
		if !hasSetPrice(tier.Prices) {
			return fmt.Errorf("context tier must set at least one price")
		}
		previousThreshold = tier.InputThresholdTokens
	}
	for mode, prices := range rule.ModePrices {
		if !mode.Valid() || mode == ModeStandard {
			return fmt.Errorf("invalid non-standard pricing mode %q", mode)
		}
		if err := validatePrices(prices); err != nil {
			return err
		}
		if !hasSetPrice(prices) {
			return fmt.Errorf("pricing mode %q must set at least one price", mode)
		}
	}
	return nil
}

func validateIdentity(identity Identity) error {
	if err := validateChannelID(identity.ChannelID); err != nil {
		return err
	}
	return validateModelID(identity.ModelID)
}

func validateChannelID(channelID string) error {
	if len(channelID) == 0 || len(channelID) > maxChannelIDBytes {
		return fmt.Errorf("channel ID must be 1 through %d bytes", maxChannelIDBytes)
	}
	if strings.TrimSpace(channelID) != channelID {
		return fmt.Errorf("channel ID must not have surrounding whitespace")
	}
	for _, character := range channelID {
		if unicode.IsControl(character) {
			return fmt.Errorf("channel ID must not contain control characters")
		}
	}
	return nil
}

func validateModelID(modelID string) error {
	if len(modelID) == 0 || len(modelID) > maxModelIDBytes {
		return fmt.Errorf("model ID must be 1 through %d bytes", maxModelIDBytes)
	}
	if strings.TrimSpace(modelID) != modelID {
		return fmt.Errorf("model ID must not have surrounding whitespace")
	}
	for _, character := range modelID {
		if unicode.IsControl(character) {
			return fmt.Errorf("model ID must not contain control characters")
		}
	}
	return nil
}

func validateReceiptRule(rule ReceiptRule, schemaVersion int) error {
	switch schemaVersion {
	case 1:
		if err := validateModelID(rule.ModelID); err != nil {
			return err
		}
		if rule.ChannelID != "" {
			return fmt.Errorf("legacy v1 receipt must not contain a channel ID")
		}
		if rule.ScopeKey == "" {
			return fmt.Errorf("legacy receipt scope key is required")
		}
		return validateScopeKey(rule.ScopeKey)
	case 2:
		if err := validateModelID(rule.ModelID); err != nil {
			return err
		}
		if rule.ScopeKey != "" {
			return fmt.Errorf("global receipt must not contain a scope key")
		}
		if rule.ChannelID != "" {
			return fmt.Errorf("global receipt must not contain a channel ID")
		}
		return nil
	case 3, 4:
		if rule.ScopeKey != "" {
			return fmt.Errorf("channel receipt must not contain a scope key")
		}
		return validateIdentity(Identity{ChannelID: rule.ChannelID, ModelID: rule.ModelID})
	default:
		return fmt.Errorf("unsupported receipt rule schema version")
	}
}

func validateScopeKey(scopeKey string) error {
	if providerID, ok := strings.CutPrefix(scopeKey, "provider:"); ok {
		canonical, err := ProviderScopeKey(providerID)
		if err != nil || canonical != scopeKey {
			return fmt.Errorf("invalid provider scope key %q", scopeKey)
		}
		return nil
	}
	if groupText, ok := strings.CutPrefix(scopeKey, "group:"); ok {
		parsed, err := strconv.ParseUint(groupText, 10, strconv.IntSize)
		if err != nil {
			return fmt.Errorf("invalid group scope key %q", scopeKey)
		}
		canonical, err := GroupScopeKey(uint(parsed))
		if err != nil || canonical != scopeKey {
			return fmt.Errorf("invalid group scope key %q", scopeKey)
		}
		return nil
	}
	return fmt.Errorf("invalid pricing scope key %q", scopeKey)
}

func validatePrices(prices Prices) error {
	for _, price := range [...]Price{prices.Input, prices.Output, prices.CacheRead, prices.CacheWrite} {
		if price.NanoUSDPerMillion < 0 {
			return fmt.Errorf("pricing price must be non-negative")
		}
	}
	return nil
}

func hasSetPrice(prices Prices) bool {
	return prices.Input.Set || prices.Output.Set || prices.CacheRead.Set || prices.CacheWrite.Set
}

func cloneRule(rule Rule) Rule {
	if rule.ContextTiers != nil {
		rule.ContextTiers = append([]ContextTier(nil), rule.ContextTiers...)
	}
	if rule.ModePrices != nil {
		modePrices := make(map[Mode]Prices, len(rule.ModePrices))
		for mode, prices := range rule.ModePrices {
			modePrices[mode] = prices
		}
		rule.ModePrices = modePrices
	}
	return rule
}
