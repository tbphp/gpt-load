package state

import (
	"encoding/json"
	"reflect"
	"testing"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/protocol"
)

func TestClientModelProfileUsesAccessKeyCandidateScope(t *testing.T) {
	large, small := int64(200000), int64(64000)
	runtime := &catalog.Runtime{}
	runtime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {Models: map[string]catalog.Model{
			"large": {Metadata: catalog.ModelMetadata{Limits: catalog.ModelLimits{Context: &large}, Modalities: catalog.ModelModalities{Input: []string{"text", "image"}}}},
			"small": {Metadata: catalog.ModelMetadata{Limits: catalog.ModelLimits{Context: &small}, Modalities: catalog.ModelModalities{Input: []string{"text"}}}},
		}},
	}})
	groups := []GroupConfig{}
	for index, upstream := range []string{"large", "small", "unknown", "disabled"} {
		groups = append(groups, GroupConfig{ID: uint(index + 1), Name: upstream, ChannelID: channel.OpenAI,
			ConnectionType: "api_key", Params: json.RawMessage(`{}`), Enabled: upstream != "disabled",
			Models: []ModelConfig{{ID: upstream, Alias: "client"}}})
	}
	snapshot, err := Compile(CompileInput{ChannelRegistry: channel.NewRegistry(), Groups: groups})
	if err != nil {
		t.Fatal(err)
	}
	snapshot.ClientModelOverrides = map[string]catalog.ClientModelOverrides{}
	for _, test := range []struct {
		name       string
		filters    FilterSet
		context    *int64
		modalities []string
		sources    int
		unknown    int
	}{
		{"large only", FilterSet{Groups: map[uint]struct{}{1: {}}}, &large, []string{"text", "image"}, 1, 0},
		{"known intersection", FilterSet{Groups: map[uint]struct{}{1: {}, 2: {}}}, &small, []string{"text"}, 2, 0},
		{"unknown constrains", FilterSet{}, nil, []string{"text"}, 3, 1},
		{"model denied", FilterSet{Models: map[string]struct{}{"other": {}}}, nil, []string{"text"}, 0, 0},
		{"protocol denied", FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.Gemini: {}}}, nil, []string{"text"}, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := ResolveClientModelProfile(snapshot, runtime, "client", test.filters, []protocol.Protocol{protocol.OpenAIResponses})
			if !reflect.DeepEqual(result.Automatic.ContextWindow, test.context) || !reflect.DeepEqual(result.Automatic.InputModalities, test.modalities) ||
				result.SourceCount != test.sources || result.UnknownSourceCount != test.unknown {
				t.Fatalf("profile = %#v", result)
			}
		})
	}
	override := int64(32000)
	snapshot.ClientModelOverrides["client"] = catalog.ClientModelOverrides{ContextWindow: &override}
	result := ResolveClientModelProfile(snapshot, runtime, "client", FilterSet{Groups: map[uint]struct{}{1: {}}}, nil)
	if result.Automatic.ContextWindow == nil || *result.Automatic.ContextWindow != large || *result.Effective.ContextWindow != override {
		t.Fatalf("manual override must apply after scoped aggregation: %#v", result)
	}
	*result.Effective.ContextWindow = 1
	if *snapshot.ClientModelOverrides["client"].ContextWindow != override {
		t.Fatal("profile result aliases snapshot override")
	}
}
