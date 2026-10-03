package gateway

import (
	"gpt-load/internal/clientcatalog"
	"gpt-load/internal/state"
)

type codexTruncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int64  `json:"limit"`
}

func buildCodexModelList(snapshot *state.ConfigSnapshot, accessKey state.AccessKeyView, limit int64, clientVersion string) ([]byte, error) {
	if limit < 13 {
		return nil, errModelListTooLarge
	}
	result, err := clientcatalog.Build(snapshot, accessKey, clientVersion, limit)
	return result.Body, err
}

func collectCodexVisibleModelIDs(snapshot *state.ConfigSnapshot, accessKey state.AccessKeyView) []string {
	return clientcatalog.Selected(snapshot, accessKey)
}
