package catalog

import (
	"net/url"
	"sort"
	"strings"
	"unicode"

	"gpt-load/internal/protocol"
)

var officialProviderIDs = map[string]struct{}{
	"openai":    {},
	"anthropic": {},
	"google":    {},
}

// IsOfficialProviderID reports whether providerID is one of the built-in
// provider presets that remain selectable without a loaded catalog snapshot.
func IsOfficialProviderID(providerID string) bool {
	_, ok := officialProviderIDs[providerID]
	return ok
}

// OfficialSuggestions returns the three local provider presets with safe
// field-level name and API URL updates from the current catalog.
func OfficialSuggestions(snapshot *Snapshot) []Provider {
	providers := []Provider{
		{ID: "openai", Name: "OpenAI", APIURL: "https://api.openai.com/v1", Mark: "OA", Protocols: []protocol.Protocol{protocol.OpenAICompletions, protocol.OpenAIResponses}},
		{ID: "anthropic", Name: "Anthropic", APIURL: "https://api.anthropic.com/v1", Mark: "AN", Protocols: []protocol.Protocol{protocol.Anthropic}},
		{ID: "google", Name: "Google", APIURL: "https://generativelanguage.googleapis.com/v1beta", Mark: "GE", Protocols: []protocol.Protocol{protocol.Gemini}},
	}
	if snapshot == nil {
		return providers
	}
	for index := range providers {
		catalogProvider, ok := snapshot.Providers[providers[index].ID]
		if !ok {
			continue
		}
		if name := strings.TrimSpace(catalogProvider.Name); name != "" {
			providers[index].Name = name
		}
		if apiURL, ok := normalizeSuggestionURL(catalogProvider.APIURL); ok {
			providers[index].APIURL = apiURL
		}
	}
	return providers
}

// SearchProviders returns cloned non-official catalog suggestions in stable
// display order with only conservative protocol inference.
func SearchProviders(snapshot *Snapshot, query string) []Provider {
	if snapshot == nil || len(snapshot.Providers) == 0 {
		return nil
	}
	normalizedQuery := strings.ToLower(strings.TrimSpace(query))
	providers := make([]Provider, 0, len(snapshot.Providers))
	for id, provider := range snapshot.Providers {
		if _, official := officialProviderIDs[id]; official {
			continue
		}
		if normalizedQuery != "" &&
			!strings.Contains(strings.ToLower(provider.ID), normalizedQuery) &&
			!strings.Contains(strings.ToLower(provider.Name), normalizedQuery) {
			continue
		}
		provider = cloneProvider(provider)
		provider.Mark = providerMark(provider.Name, provider.ID)
		provider.Protocols = inferProtocols(provider.NPM)
		if apiURL, ok := normalizeSuggestionURL(provider.APIURL); ok {
			provider.APIURL = apiURL
		} else {
			provider.APIURL = ""
		}
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(left, right int) bool {
		leftName := strings.ToLower(strings.TrimSpace(providers[left].Name))
		rightName := strings.ToLower(strings.TrimSpace(providers[right].Name))
		if leftName == rightName {
			return providers[left].ID < providers[right].ID
		}
		return leftName < rightName
	})
	return providers
}

// SearchProviderMetadataBounded returns a deterministic top-N metadata result
// without cloning provider model maps. Empty queries deliberately return no
// catalog-only providers; callers should present local official suggestions
// until the user searches.
func SearchProviderMetadataBounded(snapshot *Snapshot, query string, limit int) []Provider {
	if snapshot == nil || limit <= 0 {
		return nil
	}
	normalizedQuery := strings.ToLower(strings.TrimSpace(query))
	if normalizedQuery == "" {
		return nil
	}
	type candidate struct {
		id       string
		nameSort string
		provider Provider
	}
	selected := make([]candidate, 0, limit)
	less := func(left, right candidate) bool {
		if left.nameSort == right.nameSort {
			return left.id < right.id
		}
		return left.nameSort < right.nameSort
	}
	for id, provider := range snapshot.Providers {
		if _, official := officialProviderIDs[id]; official {
			continue
		}
		if !strings.Contains(strings.ToLower(provider.ID), normalizedQuery) &&
			!strings.Contains(strings.ToLower(provider.Name), normalizedQuery) {
			continue
		}
		entry := candidate{
			id: id, nameSort: strings.ToLower(strings.TrimSpace(provider.Name)), provider: provider,
		}
		position := sort.Search(len(selected), func(index int) bool {
			return !less(selected[index], entry)
		})
		if position >= limit {
			continue
		}
		selected = append(selected, candidate{})
		copy(selected[position+1:], selected[position:])
		selected[position] = entry
		if len(selected) > limit {
			selected = selected[:limit]
		}
	}
	providers := make([]Provider, 0, len(selected))
	for _, entry := range selected {
		provider := entry.provider
		provider.Models = nil
		provider.Protocols = nil
		provider.Mark = providerMark(provider.Name, provider.ID)
		provider.Protocols = inferProtocols(provider.NPM)
		if apiURL, ok := normalizeSuggestionURL(provider.APIURL); ok {
			provider.APIURL = apiURL
		} else {
			provider.APIURL = ""
		}
		providers = append(providers, provider)
	}
	return providers
}

// SearchProvidersBounded is retained as the metadata-only bounded search
// boundary used by provider suggestion callers.
func SearchProvidersBounded(snapshot *Snapshot, query string, limit int) []Provider {
	return SearchProviderMetadataBounded(snapshot, query, limit)
}

func normalizeSuggestionURL(value string) (string, bool) {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", false
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")
	return parsed.String(), true
}

func inferProtocols(npm string) []protocol.Protocol {
	switch strings.ToLower(strings.TrimSpace(npm)) {
	case "@ai-sdk/anthropic":
		return []protocol.Protocol{protocol.Anthropic}
	case "@ai-sdk/google", "@ai-sdk/google-vertex":
		return []protocol.Protocol{protocol.Gemini}
	case "@ai-sdk/openai", "@ai-sdk/openai-compatible", "@openrouter/ai-sdk-provider":
		return []protocol.Protocol{protocol.OpenAICompletions}
	default:
		return nil
	}
}

func providerMark(name, id string) string {
	value := strings.TrimSpace(name)
	if value == "" {
		value = id
	}
	mark := markFromText(value)
	if mark == "" && value != id {
		mark = markFromText(id)
	}
	return mark
}

func markFromText(value string) string {
	words := strings.FieldsFunc(value, func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsDigit(character)
	})
	mark := make([]rune, 0, 2)
	if len(words) > 1 {
		for _, word := range words {
			for _, character := range word {
				mark = append(mark, unicode.ToUpper(character))
				break
			}
			if len(mark) == 2 {
				break
			}
		}
	} else if len(words) == 1 {
		for _, character := range words[0] {
			mark = append(mark, unicode.ToUpper(character))
			if len(mark) == 2 {
				break
			}
		}
	}
	return string(mark)
}
