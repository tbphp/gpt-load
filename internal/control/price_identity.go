package control

import (
	"fmt"

	"gpt-load/internal/channel"
	"gpt-load/internal/pricing"
)

var modelPriceChannelRegistry = channel.NewRegistry()

// PriceIdentityForChannelModel returns one exact persisted pricing identity.
func PriceIdentityForChannelModel(channelID, modelID string) (pricing.Identity, error) {
	identity := pricing.Identity{ChannelID: channelID, ModelID: modelID}
	if _, err := pricing.NewTable([]pricing.Rule{{Identity: identity}}); err != nil {
		return pricing.Identity{}, fmt.Errorf("validate price identity: %w", err)
	}
	if _, ok := modelPriceChannelRegistry.CatalogProviderID(channel.ID(channelID)); !ok {
		return pricing.Identity{}, fmt.Errorf("validate price identity: unknown channel %q", channelID)
	}
	return identity, nil
}
