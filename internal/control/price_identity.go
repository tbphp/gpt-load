package control

import (
	"fmt"
	"strconv"
	"strings"

	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

const (
	priceScopeKindProvider = "provider"
	priceScopeKindGroup    = "group"
)

type parsedPriceScope struct {
	kind    string
	id      string
	groupID uint
}

// PriceIdentityForGroup returns the exact persisted upstream-model pricing
// identity for a Group. Upstream URLs and aliases are deliberately excluded.
func PriceIdentityForGroup(group models.Group, modelID string) (pricing.Identity, error) {
	scopeKey, err := PriceScopeKeyForGroup(group)
	if err != nil {
		return pricing.Identity{}, err
	}
	identity := pricing.Identity{ScopeKey: scopeKey, ModelID: modelID}
	if _, err := pricing.NewTable([]pricing.Rule{{Identity: identity}}); err != nil {
		return pricing.Identity{}, fmt.Errorf("validate price identity: %w", err)
	}
	return identity, nil
}

// PriceScopeKeyForGroup returns the canonical persisted pricing scope without
// considering aliases or upstream URLs.
func PriceScopeKeyForGroup(group models.Group) (string, error) {
	if group.ProviderID != nil {
		return pricing.ProviderScopeKey(*group.ProviderID)
	}
	return pricing.GroupScopeKey(group.ID)
}

func normalizeProviderID(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	if _, err := pricing.ProviderScopeKey(*value); err != nil {
		return nil, err
	}
	cloned := *value
	return &cloned, nil
}

func parsePriceScopeKey(scopeKey string) (parsedPriceScope, error) {
	if providerID, ok := strings.CutPrefix(scopeKey, "provider:"); ok {
		canonical, err := pricing.ProviderScopeKey(providerID)
		if err != nil || canonical != scopeKey {
			return parsedPriceScope{}, fmt.Errorf("invalid provider price scope")
		}
		return parsedPriceScope{kind: priceScopeKindProvider, id: providerID}, nil
	}
	if groupText, ok := strings.CutPrefix(scopeKey, "group:"); ok {
		parsed, err := strconv.ParseUint(groupText, 10, strconv.IntSize)
		if err != nil {
			return parsedPriceScope{}, fmt.Errorf("invalid Group price scope")
		}
		groupID := uint(parsed)
		canonical, err := pricing.GroupScopeKey(groupID)
		if err != nil || canonical != scopeKey {
			return parsedPriceScope{}, fmt.Errorf("invalid Group price scope")
		}
		return parsedPriceScope{kind: priceScopeKindGroup, id: groupText, groupID: groupID}, nil
	}
	return parsedPriceScope{}, fmt.Errorf("invalid model price scope")
}
