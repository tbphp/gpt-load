package pricing

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const (
	maxProviderIDBytes = 246
	maxModelIDBytes    = 255
)

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
			return nil, fmt.Errorf("duplicate pricing model ID %q", rule.Identity.ModelID)
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
	return nil
}

func validateIdentity(identity Identity) error {
	if len(identity.ModelID) == 0 || len(identity.ModelID) > maxModelIDBytes {
		return fmt.Errorf("model ID must be 1 through %d bytes", maxModelIDBytes)
	}
	if strings.TrimSpace(identity.ModelID) != identity.ModelID {
		return fmt.Errorf("model ID must not have surrounding whitespace")
	}
	for _, character := range identity.ModelID {
		if unicode.IsControl(character) {
			return fmt.Errorf("model ID must not contain control characters")
		}
	}
	return nil
}

func validateReceiptRule(rule ReceiptRule, requireScope bool) error {
	if err := validateIdentity(Identity{ModelID: rule.ModelID}); err != nil {
		return err
	}
	if rule.ScopeKey == "" {
		if requireScope {
			return fmt.Errorf("legacy receipt scope key is required")
		}
		return nil
	}
	if !requireScope {
		return fmt.Errorf("global receipt must not contain a scope key")
	}
	return validateScopeKey(rule.ScopeKey)
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
	return rule
}
