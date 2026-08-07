package control

import (
	"fmt"

	"gpt-load/internal/pricing"
)

// PriceIdentityForModel returns the persisted global pricing identity.
func PriceIdentityForModel(modelID string) (pricing.Identity, error) {
	identity := pricing.Identity{ModelID: modelID}
	if _, err := pricing.NewTable([]pricing.Rule{{Identity: identity}}); err != nil {
		return pricing.Identity{}, fmt.Errorf("validate price identity: %w", err)
	}
	return identity, nil
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
