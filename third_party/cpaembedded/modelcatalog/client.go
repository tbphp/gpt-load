package modelcatalog

import (
	_ "embed"
	"encoding/json"
	"slices"
)

// ClientVersion identifies the official Codex contract shared by the gateway and CPA.
const ClientVersion = "0.167.0"
const ClientSHA256 = "1384625c0b6b3e67d3c60da29becea2b017f2c283fc1e2d05b8edafb4698eb10"

// Source: openai/codex rust-v0.167.0, codex-rs/models-manager/models.json.
//
//go:embed codex_client_models_0.167.0.json
var clientSnapshot []byte

// ClientJSON returns an independent copy of the pinned client template snapshot.
func ClientJSON() []byte { return slices.Clone(clientSnapshot) }

var clientReasoningLevels = func() map[string][]string {
	var catalog struct {
		Models []struct {
			Slug   string `json:"slug"`
			Levels []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if err := json.Unmarshal(clientSnapshot, &catalog); err != nil {
		panic(err)
	}
	result := make(map[string][]string, len(catalog.Models))
	for _, model := range catalog.Models {
		for _, level := range model.Levels {
			result[model.Slug] = append(result[model.Slug], level.Effort)
		}
	}
	return result
}()

// ReasoningLevels returns the official client contract for an exact model match.
// Unmatched models retain CPA's own level metadata.
func ReasoningLevels(model string) []string { return slices.Clone(clientReasoningLevels[model]) }
