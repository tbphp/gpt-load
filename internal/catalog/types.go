// Package catalog loads and publishes the validated Models.dev model catalog.
package catalog

import (
	"encoding/json"

	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
)

// ModelCost is the exact provider price data retained for one model.
type ModelCost struct {
	Prices       pricing.Prices
	ContextTiers []pricing.ContextTier
}

// Model is the retained Models.dev model identity and optional price data.
type Model struct {
	ID   string
	Name string
	Cost *ModelCost
}

// Provider is the retained Models.dev provider identity and model map. Mark and
// Protocols are local suggestion metadata and are never read from Models.dev.
type Provider struct {
	ID        string
	Name      string
	APIURL    string
	NPM       string
	Mark      string
	Protocols []protocol.Protocol
	Models    map[string]Model
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
	provider.Protocols = append([]protocol.Protocol(nil), provider.Protocols...)
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
	if model.Cost == nil {
		return model
	}
	cost := *model.Cost
	cost.ContextTiers = append([]pricing.ContextTier(nil), cost.ContextTiers...)
	model.Cost = &cost
	return model
}
