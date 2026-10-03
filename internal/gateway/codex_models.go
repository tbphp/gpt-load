package gateway

import (
	"encoding/json"
	"strconv"
	"strings"

	"gpt-load/internal/catalog"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
)

type codexTruncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int64  `json:"limit"`
}

func buildCodexModelList(
	snapshot *state.ConfigSnapshot,
	accessKey state.AccessKeyView,
	limit int64,
	clientVersion string,
) ([]byte, error) {
	body := []byte(`{"models":[`)
	if int64(len(body)+2) > limit {
		return nil, errModelListTooLarge
	}
	ids := collectCodexVisibleModelIDs(snapshot, accessKey)
	for index, id := range ids {
		overrides := catalog.ClientModelOverrides{}
		if snapshot != nil {
			overrides = snapshot.ClientModelOverrides[id].Clone()
		}
		var model map[string]any
		var err error
		if snapshot != nil && snapshot.AutoModels != nil {
			if _, automatic := snapshot.AutoModels.Lookup(id); automatic {
				model, err = catalog.BuildCodexFallbackClientModel(id, index)
			}
		}
		if model == nil && err == nil {
			model, _, _, err = catalog.BuildCodexClientModel(id, index, overrides)
		}
		if err != nil {
			return nil, err
		}
		if codexClientUsesModelMessages(clientVersion) {
			delete(model, "base_instructions")
		}
		item, err := json.Marshal(model)
		if err != nil {
			return nil, err
		}
		required := int64(len(item))
		if index > 0 {
			required++
		}
		if required > limit-int64(len(body))-2 {
			return nil, errModelListTooLarge
		}
		if index > 0 {
			body = append(body, ',')
		}
		body = append(body, item...)
	}
	return append(body, ']', '}'), nil
}

func collectCodexVisibleModelIDs(snapshot *state.ConfigSnapshot, accessKey state.AccessKeyView) []string {
	if snapshot == nil {
		return []string{}
	}
	if len(accessKey.Filters.Protocols) > 0 {
		if _, allowed := accessKey.Filters.Protocols[protocol.OpenAIResponses]; !allowed {
			return []string{}
		}
	}
	visible := make(map[string]struct{})
	for modelID, targets := range snapshot.ExecutionCandidates[protocol.OpenAIResponses][execution.OperationResponsesCreate] {
		if modelID == state.NoModelRouteKey {
			continue
		}
		if len(accessKey.Filters.Models) > 0 {
			if _, allowed := accessKey.Filters.Models[modelID]; !allowed {
				continue
			}
		}
		if anyVisibleTarget(targets, accessKey.Filters.Groups) &&
			catalog.ClientModelCatalogEnabled(modelID, snapshot.ClientModelOverrides[modelID]) {
			visible[modelID] = struct{}{}
		}
	}
	if snapshot.AutoModels != nil && snapshot.AutoModels.Enabled() {
		for _, entry := range snapshot.AutoModels.Config().Models {
			compiled, exists := snapshot.AutoModels.Lookup(entry.Name)
			if !exists || !compiled.Enabled {
				continue
			}
			_, allowed := allowedAutoPresets(
				snapshot,
				accessKey,
				compiled,
				dialect.RequestMetadata{Operation: execution.OperationResponsesCreate},
				scheduler.Query{ClientProtocol: protocol.OpenAIResponses},
			)
			if allowed && catalog.ClientModelCatalogEnabled(entry.Name, snapshot.ClientModelOverrides[entry.Name]) {
				visible[entry.Name] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(visible))
	for modelID := range visible {
		result = append(result, modelID)
	}
	catalog.SortClientModelCatalog(result, snapshot.ClientModelOverrides)
	return result
}

func codexClientUsesModelMessages(clientVersion string) bool {
	parts := strings.Split(clientVersion, ".")
	if len(parts) != 3 {
		return false
	}
	var version [3]int
	for index, part := range parts {
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 {
			return false
		}
		version[index] = parsed
	}
	// 0.159.2 的客户端契约已将 base_instructions 变为可选的旧字段。
	for index, minimum := range [3]int{0, 159, 2} {
		if version[index] != minimum {
			return version[index] > minimum
		}
	}
	return true
}
