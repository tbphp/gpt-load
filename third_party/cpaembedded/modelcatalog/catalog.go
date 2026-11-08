// Package modelcatalog shares pinned Codex capabilities without exposing CPA types.
package modelcatalog

import (
	_ "embed"
	"encoding/json"
)

const SourceCommit = "8e990d32bed1ab44d4c761097d5e51b53d95069b"
const SHA256 = "81bca47653de1084b7efb8abe699d07b584792c7806d7f837b55be3263e82258"

//go:embed codex.json
var snapshot []byte

func JSON() []byte { return append([]byte(nil), snapshot...) }

var capabilities = func() map[string]bool {
	var models []struct {
		ID      string `json:"id"`
		Updates bool   `json:"support_configuration_update"`
	}
	if err := json.Unmarshal(snapshot, &models); err != nil {
		panic(err)
	}
	result := make(map[string]bool, len(models))
	for _, m := range models {
		result[m.ID] = m.Updates
	}
	return result
}()

func SupportsReasoningUpdates(model string) bool { return capabilities[model] }
