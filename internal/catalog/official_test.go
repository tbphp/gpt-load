package catalog

import (
	"reflect"
	"testing"

	"gpt-load/internal/protocol"
)

func TestOfficialSuggestionsUseOrderedFallbacksAndFieldLevelOverrides(t *testing.T) {
	snapshot := &Snapshot{Providers: map[string]Provider{
		"openai": {
			ID:        "openai",
			Name:      "OpenAI Catalog",
			APIURL:    "https://catalog.openai.example/v1///",
			NPM:       "catalog-must-not-replace-local-preset",
			Mark:      "XX",
			Protocols: []protocol.Protocol{protocol.Gemini},
			Models:    map[string]Model{"gpt-x": {ID: "gpt-x", Name: "GPT X"}},
		},
		"anthropic": {
			ID:     "anthropic",
			Name:   " ",
			APIURL: "https://user:secret@api.example.com/v1?unsafe=true",
		},
		"google": {
			ID:     "google",
			Name:   "Google Catalog",
			APIURL: "http://google.example.test/v1beta/",
		},
	}}

	got := OfficialSuggestions(snapshot)
	want := []Provider{
		{ID: "openai", Name: "OpenAI Catalog", APIURL: "https://catalog.openai.example/v1", Mark: "OA", Protocols: []protocol.Protocol{protocol.OpenAICompletions, protocol.OpenAIResponses}},
		{ID: "anthropic", Name: "Anthropic", APIURL: "https://api.anthropic.com/v1", Mark: "AN", Protocols: []protocol.Protocol{protocol.Anthropic}},
		{ID: "google", Name: "Google Catalog", APIURL: "http://google.example.test/v1beta", Mark: "GE", Protocols: []protocol.Protocol{protocol.Gemini}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OfficialSuggestions() = %#v, want %#v", got, want)
	}

	got[0].Protocols[0] = protocol.Gemini
	again := OfficialSuggestions(snapshot)
	if again[0].Protocols[0] != protocol.OpenAICompletions {
		t.Fatalf("official suggestions share mutable protocol storage: %#v", again[0].Protocols)
	}
}

func TestOfficialSuggestionsReturnFallbacksForNilOrEmptySnapshot(t *testing.T) {
	for _, snapshot := range []*Snapshot{nil, {}, {Providers: map[string]Provider{}}} {
		got := OfficialSuggestions(snapshot)
		if len(got) != 3 || got[0].ID != "openai" || got[1].ID != "anthropic" || got[2].ID != "google" {
			t.Fatalf("OfficialSuggestions(%#v) = %#v", snapshot, got)
		}
	}
}

func TestIsOfficialProviderID(t *testing.T) {
	for _, providerID := range []string{"openai", "anthropic", "google"} {
		if !IsOfficialProviderID(providerID) {
			t.Fatalf("IsOfficialProviderID(%q) = false, want true", providerID)
		}
	}
	for _, providerID := range []string{"", "OpenAI", "private-company"} {
		if IsOfficialProviderID(providerID) {
			t.Fatalf("IsOfficialProviderID(%q) = true, want false", providerID)
		}
	}
}

func TestSearchProvidersExcludesOfficialSortsAndInfersConservatively(t *testing.T) {
	snapshot := &Snapshot{Providers: map[string]Provider{
		"openai":          {ID: "openai", Name: "OpenAI"},
		"anthropic-cloud": {ID: "anthropic-cloud", Name: "Zeta Anthropic", NPM: "@ai-sdk/anthropic", Models: map[string]Model{"claude": {ID: "claude", Name: "Claude"}}},
		"gemini-cloud":    {ID: "gemini-cloud", Name: "alpha Google", NPM: "@ai-sdk/google"},
		"compatible":      {ID: "compatible", Name: "Alpha OpenAI Compatible", NPM: "@ai-sdk/openai-compatible"},
		"openrouter":      {ID: "openrouter", Name: "Beta OpenRouter", NPM: "@openrouter/ai-sdk-provider"},
		"openai-sdk":      {ID: "openai-sdk", Name: "Delta OpenAI SDK", NPM: "@ai-sdk/openai"},
		"unknown":         {ID: "unknown", Name: "Gamma Unknown", NPM: "@vendor/unknown", Protocols: []protocol.Protocol{protocol.OpenAIResponses}},
	}}

	got := SearchProviders(snapshot, "")
	wantIDs := []string{"gemini-cloud", "compatible", "openrouter", "openai-sdk", "unknown", "anthropic-cloud"}
	if len(got) != len(wantIDs) {
		t.Fatalf("SearchProviders() count = %d, want %d: %#v", len(got), len(wantIDs), got)
	}
	for index, wantID := range wantIDs {
		if got[index].ID != wantID {
			t.Fatalf("SearchProviders()[%d].ID = %q, want %q", index, got[index].ID, wantID)
		}
		if got[index].Mark == "" {
			t.Fatalf("SearchProviders()[%d].Mark is empty", index)
		}
	}
	assertProtocols(t, got[0].Protocols, protocol.Gemini)
	assertProtocols(t, got[1].Protocols, protocol.OpenAICompletions)
	assertProtocols(t, got[2].Protocols, protocol.OpenAICompletions)
	assertProtocols(t, got[3].Protocols, protocol.OpenAICompletions)
	assertProtocols(t, got[4].Protocols)
	assertProtocols(t, got[5].Protocols, protocol.Anthropic)
	for _, provider := range got {
		for _, inferred := range provider.Protocols {
			if inferred == protocol.OpenAIResponses {
				t.Fatalf("non-official provider %q inferred responses", provider.ID)
			}
		}
	}

	filtered := SearchProviders(snapshot, "gOoGlE")
	if len(filtered) != 1 || filtered[0].ID != "gemini-cloud" {
		t.Fatalf("case-insensitive name search = %#v", filtered)
	}
	filtered = SearchProviders(snapshot, "OPENROUTER")
	if len(filtered) != 1 || filtered[0].ID != "openrouter" {
		t.Fatalf("case-insensitive ID search = %#v", filtered)
	}

	got[len(got)-1].Models["claude"] = Model{ID: "mutated"}
	again := SearchProviders(snapshot, "anthropic")
	if again[0].Models["claude"].ID != "claude" {
		t.Fatalf("search result shares model map: %#v", again[0].Models)
	}
}

func TestSearchProvidersReturnsNoMoreProvidersForNilSnapshot(t *testing.T) {
	if got := SearchProviders(nil, ""); len(got) != 0 {
		t.Fatalf("SearchProviders(nil) = %#v, want empty", got)
	}
}

func TestSearchProvidersAlwaysGeneratesLocalTextMark(t *testing.T) {
	snapshot := &Snapshot{Providers: map[string]Provider{
		"symbols": {ID: "symbols", Name: "🔥", Models: map[string]Model{}},
	}}
	got := SearchProviders(snapshot, "")
	if len(got) != 1 || got[0].Mark != "SY" {
		t.Fatalf("SearchProviders() mark = %#v, want local ID fallback mark SY", got)
	}
}

func TestSearchProviderMetadataByIDsPreservesRequestedOrderAndOnlyMatches(t *testing.T) {
	snapshot := &Snapshot{Providers: map[string]Provider{
		"alpha": {
			ID: "alpha", Name: "Alpha", APIURL: "https://alpha.example/v1///",
			NPM: "@ai-sdk/openai-compatible", Models: map[string]Model{"alpha-model": {ID: "alpha-model"}},
		},
		"beta": {
			ID: "beta", Name: "Beta", APIURL: "https://beta.example/v1",
			NPM: "@ai-sdk/anthropic", Models: map[string]Model{"beta-model": {ID: "beta-model"}},
		},
	}}

	got := SearchProviderMetadataByIDs(snapshot, []string{"beta", "missing", "beta", "alpha"})
	if len(got) != 2 || got[0].ID != "beta" || got[1].ID != "alpha" {
		t.Fatalf("metadata by IDs = %#v, want beta then alpha", got)
	}
	if got[0].APIURL != "https://beta.example/v1" || got[0].Protocols[0] != protocol.Anthropic || got[0].Mark != "BE" {
		t.Fatalf("beta metadata = %#v", got[0])
	}
	if got[1].APIURL != "https://alpha.example/v1" || got[1].Protocols[0] != protocol.OpenAICompletions || got[1].Mark != "AL" {
		t.Fatalf("alpha metadata = %#v", got[1])
	}
	if got[0].Models != nil || got[1].Models != nil {
		t.Fatalf("metadata by IDs exposes model maps: %#v", got)
	}
}

func assertProtocols(t *testing.T, got []protocol.Protocol, want ...protocol.Protocol) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("protocols = %#v, want %#v", got, want)
	}
}
