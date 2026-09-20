package gateway

import (
	"encoding/json"
	"math"

	"gpt-load/internal/catalog"
	"gpt-load/internal/protocol"
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
) ([]byte, error) {
	body := []byte(`{"models":[`)
	if int64(len(body)+2) > limit {
		return nil, errModelListTooLarge
	}
	ids, err := collectVisibleModelIDs(snapshot, accessKey, protocol.OpenAICompletions, math.MaxInt64)
	if err != nil {
		return nil, err
	}
	for index, id := range ids {
		overrides := catalog.ClientModelOverrides{}
		if snapshot != nil {
			overrides = snapshot.ClientModelOverrides[id].Clone()
		}
		model, _, _, err := catalog.BuildCodexClientModel(id, index, overrides)
		if err != nil {
			return nil, err
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
