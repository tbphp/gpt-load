package control

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestDraftDiscoveryMergesLiveAndLocalCatalogByExactIDWithoutURLInference(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {
			ID: "openai", Name: "OpenAI", Models: map[string]catalog.Model{
				"shared": {ID: "shared", Name: "Shared display"},
				"catalog-only": {
					ID: "catalog-only", Name: "Catalog only",
					Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(1)}},
				},
				"SHARED": {ID: "SHARED", Name: "Case distinct"},
			},
		},
	}})
	zero := int64(0)
	if err := fixture.db.Create(&models.ModelPrice{
		ChannelID:                         string(channel.OpenAI),
		ModelID:                           "shared",
		InputPriceNanoUSDPerMillionTokens: &zero,
	}).Error; err != nil {
		t.Fatal(err)
	}
	fixture.service.executor = newRecordingDiscoveryExecutor(&recordingDiscoveryExecutorTarget{
		value: protocol.OpenAICompletions,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			return []string{"shared", "live-only", "shared"}, nil
		},
	})
	got, err := fixture.service.DiscoverModels(t.Context(), ModelDiscoveryRequest{
		ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`), Credentials: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	openAI := "OpenAI"
	want := []ModelCandidate{
		{ID: "shared", Name: "Shared display", Sources: []string{"live", "catalog"}, PricingStatus: PricingStatusConfigured},
		{ID: "live-only", Name: "live-only", Sources: []string{"live"}, PricingStatus: PricingStatusPending},
		{ID: "SHARED", Name: "Case distinct", Sources: []string{"catalog"}, PricingStatus: PricingStatusPending},
		{
			ID: "catalog-only", Name: "Catalog only", Sources: []string{"catalog"},
			PricingStatus: PricingStatusConfigured, PricingSource: &openAI,
		},
	}
	if !reflect.DeepEqual(got.Models, want) {
		t.Fatalf("merged candidates = %#v, want %#v", got.Models, want)
	}

	withoutProvider, err := fixture.service.DiscoverModels(t.Context(), ModelDiscoveryRequest{
		ChannelID: channel.OpenAICompatible,
		Params:    json.RawMessage(`{"base_url":"https://api.openai.com/v1"}`), Credentials: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(withoutProvider.Models, []ModelCandidate{
		{ID: "shared", Name: "shared", Sources: []string{"live"}, PricingStatus: PricingStatusPending},
		{ID: "live-only", Name: "live-only", Sources: []string{"live"}, PricingStatus: PricingStatusPending},
	}) {
		t.Fatalf("URL-inferred candidates = %#v", withoutProvider.Models)
	}
}

func TestDiscoveryPricingStatusUsesAutomaticPriceReferenceForDraftScopes(t *testing.T) {
	fixture := newServiceFixture(t)
	openAI := "openai"
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"anthropic": {
			ID: "anthropic", Models: map[string]catalog.Model{
				"shared-model": {ID: "shared-model", Name: "Anthropic model"},
			},
		},
		"openai": {
			ID: "openai", Models: map[string]catalog.Model{
				"shared-model": {
					ID: "shared-model", Name: "OpenAI model",
					Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(1)}},
				},
			},
		},
	}})
	fixture.service.executor = newRecordingDiscoveryExecutor(
		&recordingDiscoveryExecutorTarget{
			value: protocol.OpenAICompletions,
			listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
				return []string{"shared-model"}, nil
			},
		},
		&recordingDiscoveryExecutorTarget{
			value: protocol.Anthropic,
			listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
				return []string{"shared-model"}, nil
			},
		},
	)

	request := ModelDiscoveryRequest{
		ChannelID: channel.OpenAICompatible,
		Params:    json.RawMessage(`{"base_url":"https://proxy.example/v1"}`), Credentials: "secret",
	}
	custom, err := fixture.service.DiscoverModels(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(custom.Models, []ModelCandidate{
		{
			ID: "shared-model", Name: "shared-model", Sources: []string{"live"},
			PricingStatus: PricingStatusConfigured, PricingSource: &openAI,
		},
	}) {
		t.Fatalf("custom draft candidates = %#v", custom.Models)
	}

	savedCustom, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ChannelID:   channel.OpenAICompatible,
		Params:      json.RawMessage(`{"base_url":"https://saved-custom.example/v1"}`),
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Credentials: "saved-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	customGroup, err := fixture.service.DiscoverGroupModels(t.Context(), savedCustom.GroupID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(customGroup.Models, []ModelCandidate{
		{
			ID: "shared-model", Name: "shared-model", Sources: []string{"live"},
			PricingStatus: PricingStatusConfigured, PricingSource: &openAI,
		},
	}) {
		t.Fatalf("custom Group candidates = %#v", customGroup.Models)
	}

	request.ChannelID = channel.Anthropic
	request.Params = json.RawMessage(`{}`)
	provider, err := fixture.service.DiscoverModels(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(provider.Models, []ModelCandidate{
		{
			ID: "shared-model", Name: "Anthropic model", Sources: []string{"live", "catalog"},
			PricingStatus: PricingStatusPending,
		},
	}) {
		t.Fatalf("provider draft candidates = %#v", provider.Models)
	}
}

func TestSavedGroupDiscoveryUsesPersistedProviderAndSharedPricingStatus(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {
			ID: "openai", Models: map[string]catalog.Model{
				"shared": {ID: "shared", Name: "Shared display"},
				"catalog-only": {
					ID: "catalog-only", Name: "Catalog only",
					Cost: &catalog.ModelCost{Prices: pricing.Prices{Input: priceTestValue(1)}},
				},
			},
		},
	}})
	fixture.service.executor = newRecordingDiscoveryExecutor(&recordingDiscoveryExecutorTarget{
		value: protocol.OpenAICompletions,
		listFn: func(context.Context, string, string, state.HeaderRules) ([]string, error) {
			return []string{"shared", "live-only"}, nil
		},
	})
	providerID := "openai"
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ChannelID:   channel.OpenAI,
		Params:      json.RawMessage(`{}`),
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{{ID: "shared"}}},
		Credentials: "saved-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if err := fixture.db.Model(&models.ModelPrice{}).
		Where("channel_id = ? AND model_id = ?", channel.OpenAI, "shared").
		Update("input_price_nano_usd_per_million_tokens", &zero).Error; err != nil {
		t.Fatal(err)
	}

	got, err := fixture.service.DiscoverGroupModels(t.Context(), created.GroupID)
	if err != nil {
		t.Fatal(err)
	}
	want := []ModelCandidate{
		{ID: "shared", Name: "Shared display", Sources: []string{"live", "catalog"}, PricingStatus: PricingStatusConfigured},
		{ID: "live-only", Name: "live-only", Sources: []string{"live"}, PricingStatus: PricingStatusPending},
		{
			ID: "catalog-only", Name: "Catalog only", Sources: []string{"catalog"},
			PricingStatus: PricingStatusConfigured, PricingSource: &providerID,
		},
	}
	if !reflect.DeepEqual(got.Models, want) {
		t.Fatalf("saved group candidates = %#v, want %#v", got.Models, want)
	}
}
