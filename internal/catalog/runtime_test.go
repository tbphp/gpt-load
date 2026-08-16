package catalog

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"gpt-load/internal/pricing"
)

func TestRuntimePublishAndLoadDeepCloneEveryMutableLayer(t *testing.T) {
	runtime := &Runtime{}
	attachment := true
	openWeights := false
	contextLimit := int64(1_000_000)
	source := &Snapshot{Providers: map[string]Provider{
		"openai": {
			ID:   "openai",
			Name: "OpenAI",
			Models: map[string]Model{
				"gpt-x": {
					ID:   "gpt-x",
					Name: "GPT X",
					Metadata: ModelMetadata{
						Description: "General model",
						Capabilities: ModelCapabilities{
							Attachment: &attachment,
						},
						Modalities: ModelModalities{
							Input:  []string{"text", "image"},
							Output: []string{"text"},
						},
						Limits:      ModelLimits{Context: &contextLimit},
						OpenWeights: &openWeights,
					},
					Cost: &ModelCost{
						Prices: pricing.Prices{Input: pricing.Price{NanoUSDPerMillion: 1, Set: true}},
						ModePrices: map[pricing.Mode]pricing.Prices{
							pricing.ModeFast: {Input: pricing.Price{NanoUSDPerMillion: 3, Set: true}},
						},
						ContextTiers: []pricing.ContextTier{{
							InputThresholdTokens: 10,
							Prices:               pricing.Prices{Output: pricing.Price{NanoUSDPerMillion: 2, Set: true}},
						}},
					},
				},
			},
		},
	}}
	runtime.Publish(source)

	provider := source.Providers["openai"]
	model := provider.Models["gpt-x"]
	*model.Metadata.Capabilities.Attachment = false
	model.Metadata.Modalities.Input[0] = "audio"
	model.Metadata.Modalities.Output[0] = "image"
	*model.Metadata.Limits.Context = 99
	*model.Metadata.OpenWeights = true
	model.Cost.Prices.Input.NanoUSDPerMillion = 99
	model.Cost.ContextTiers[0].Prices.Output.NanoUSDPerMillion = 99
	model.Cost.ModePrices[pricing.ModeFast] = pricing.Prices{Input: pricing.Price{NanoUSDPerMillion: 99, Set: true}}
	provider.Models["gpt-x"] = model
	provider.Name = "mutated source"
	source.Providers["openai"] = provider

	first := runtime.Load()
	assertRuntimeSnapshot(t, first)
	loadedProvider := first.Providers["openai"]
	loadedModel := loadedProvider.Models["gpt-x"]
	*loadedModel.Metadata.Capabilities.Attachment = false
	loadedModel.Metadata.Modalities.Input[0] = "audio"
	loadedModel.Metadata.Modalities.Output[0] = "image"
	*loadedModel.Metadata.Limits.Context = 88
	*loadedModel.Metadata.OpenWeights = true
	loadedModel.Cost.Prices.Input.NanoUSDPerMillion = 88
	loadedModel.Cost.ContextTiers[0].Prices.Output.NanoUSDPerMillion = 88
	loadedModel.Cost.ModePrices[pricing.ModeFast] = pricing.Prices{Input: pricing.Price{NanoUSDPerMillion: 88, Set: true}}
	loadedProvider.Models["gpt-x"] = loadedModel
	loadedProvider.Name = "mutated load"
	first.Providers["openai"] = loadedProvider

	assertRuntimeSnapshot(t, runtime.Load())

	runtime.Publish(nil)
	if got := runtime.Load(); got != nil {
		t.Fatalf("Load() after Publish(nil) = %#v, want nil", got)
	}
}

func TestRuntimeConcurrentLoadPublishReturnsWholeSnapshots(t *testing.T) {
	runtime := &Runtime{}
	const workers = 16
	const iterations = 100
	start := make(chan struct{})
	errors := make(chan error, workers*iterations)
	var waitGroup sync.WaitGroup

	for worker := range workers {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			<-start
			for iteration := range iterations {
				name := fmt.Sprintf("provider-%d-%d", worker, iteration)
				runtime.Publish(&Snapshot{Providers: map[string]Provider{
					"provider": {ID: "provider", Name: name, Models: map[string]Model{}},
				}})
				loaded := runtime.Load()
				if loaded == nil || loaded.Providers["provider"].Name == "" {
					errors <- fmt.Errorf("worker %d iteration %d loaded partial snapshot %#v", worker, iteration, loaded)
					return
				}
			}
		}(worker)
	}
	close(start)
	waitGroup.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func assertRuntimeSnapshot(t *testing.T, snapshot *Snapshot) {
	t.Helper()
	provider := snapshot.Providers["openai"]
	model := provider.Models["gpt-x"]
	if provider.Name != "OpenAI" ||
		model.Metadata.Capabilities.Attachment == nil || !*model.Metadata.Capabilities.Attachment ||
		!reflect.DeepEqual(model.Metadata.Modalities.Input, []string{"text", "image"}) ||
		!reflect.DeepEqual(model.Metadata.Modalities.Output, []string{"text"}) ||
		model.Metadata.Limits.Context == nil || *model.Metadata.Limits.Context != 1_000_000 ||
		model.Metadata.OpenWeights == nil || *model.Metadata.OpenWeights ||
		model.Cost.Prices.Input.NanoUSDPerMillion != 1 ||
		model.Cost.ContextTiers[0].Prices.Output.NanoUSDPerMillion != 2 ||
		model.Cost.ModePrices[pricing.ModeFast].Input.NanoUSDPerMillion != 3 {
		t.Fatalf("runtime snapshot = %#v", snapshot)
	}
}
