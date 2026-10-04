package gateway

import (
	"gpt-load/internal/clientcatalog"
	"gpt-load/internal/state"
)

func buildCodexModelList(snapshot *state.ConfigSnapshot, accessKey state.AccessKeyView, limit int64, clientVersion string) ([]byte, error) {
	if limit < 13 {
		return nil, errModelListTooLarge
	}
	result, err := clientcatalog.Build(snapshot, accessKey, clientVersion, limit)
	return result.Body, err
}
