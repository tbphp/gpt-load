// Package catalog loads and publishes the validated Models.dev model catalog.
package catalog

import (
	"encoding/json"

	"gpt-load/internal/pricing"
)

// ModelCost is the exact provider price data retained for one model.
type ModelCost struct {
	Prices       pricing.Prices
	ContextTiers []pricing.ContextTier
	ModePrices   map[pricing.Mode]pricing.Prices
}

// ModelCapabilities contains optional Models.dev capability declarations.
// Nil distinguishes an omitted declaration from an explicit false value.
type ModelCapabilities struct {
	Attachment       *bool
	Reasoning        *bool
	ToolCall         *bool
	StructuredOutput *bool
	Temperature      *bool
}

// ModelModalities contains the ordered input and output modalities retained
// from Models.dev.
type ModelModalities struct {
	Input  []string
	Output []string
}

// ModelLimits contains optional token limits retained from Models.dev.
type ModelLimits struct {
	Context *int64
	Input   *int64
	Output  *int64
}

// ModelMetadata is the display-only Models.dev metadata retained for one
// model. It never changes routing, protocol support, or pricing behavior.
type ModelMetadata struct {
	Description  string
	Family       string
	Capabilities ModelCapabilities
	Modalities   ModelModalities
	Limits       ModelLimits
	Knowledge    string
	ReleaseDate  string
	LastUpdated  string
	OpenWeights  *bool
	Status       string
}

// Model is the retained Models.dev model identity, display metadata, and
// optional price data.
type Model struct {
	ID       string
	Name     string
	Metadata ModelMetadata
	Cost     *ModelCost
}

// Provider is the retained Models.dev provider identity and model map.
type Provider struct {
	ID     string
	Name   string
	Models map[string]Model
}

// Snapshot is one complete validated catalog generation.
type Snapshot struct {
	Providers map[string]Provider
}

// Metadata contains validators and timestamps for one catalog generation.
type Metadata struct {
	ETag                    string
	LastModified            string
	CheckedAtMillis         int64
	SuccessfulFetchAtMillis int64
}

// SyncResult is the result of one conditional Models.dev request.
type SyncResult struct {
	Metadata    Metadata
	RawJSON     json.RawMessage
	Snapshot    *Snapshot
	NotModified bool
}

// CachedCatalog is a revalidated durable last-known-good catalog.
type CachedCatalog struct {
	Metadata Metadata
	RawJSON  json.RawMessage
	Snapshot *Snapshot
}

func cloneSnapshot(snapshot *Snapshot) *Snapshot {
	if snapshot == nil {
		return nil
	}
	cloned := &Snapshot{Providers: make(map[string]Provider, len(snapshot.Providers))}
	for id, provider := range snapshot.Providers {
		cloned.Providers[id] = cloneProvider(provider)
	}
	return cloned
}

func cloneProvider(provider Provider) Provider {
	if provider.Models != nil {
		models := make(map[string]Model, len(provider.Models))
		for id, model := range provider.Models {
			models[id] = cloneModel(model)
		}
		provider.Models = models
	}
	return provider
}

func cloneModel(model Model) Model {
	model.Metadata.Capabilities.Attachment = cloneBool(model.Metadata.Capabilities.Attachment)
	model.Metadata.Capabilities.Reasoning = cloneBool(model.Metadata.Capabilities.Reasoning)
	model.Metadata.Capabilities.ToolCall = cloneBool(model.Metadata.Capabilities.ToolCall)
	model.Metadata.Capabilities.StructuredOutput = cloneBool(model.Metadata.Capabilities.StructuredOutput)
	model.Metadata.Capabilities.Temperature = cloneBool(model.Metadata.Capabilities.Temperature)
	model.Metadata.Modalities.Input = append([]string(nil), model.Metadata.Modalities.Input...)
	model.Metadata.Modalities.Output = append([]string(nil), model.Metadata.Modalities.Output...)
	model.Metadata.Limits.Context = cloneInt64(model.Metadata.Limits.Context)
	model.Metadata.Limits.Input = cloneInt64(model.Metadata.Limits.Input)
	model.Metadata.Limits.Output = cloneInt64(model.Metadata.Limits.Output)
	model.Metadata.OpenWeights = cloneBool(model.Metadata.OpenWeights)
	if model.Cost != nil {
		cost := *model.Cost
		cost.ContextTiers = append([]pricing.ContextTier(nil), cost.ContextTiers...)
		if cost.ModePrices != nil {
			modePrices := make(map[pricing.Mode]pricing.Prices, len(cost.ModePrices))
			for mode, prices := range cost.ModePrices {
				modePrices[mode] = prices
			}
			cost.ModePrices = modePrices
		}
		model.Cost = &cost
	}
	return model
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
