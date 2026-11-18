// Package modelcatalog shares pinned Codex capabilities without exposing CPA types.
package modelcatalog

import (
	_ "embed"
	"encoding/json"
)

const SourceCommit = "829b3b7f5cb34ca0c568d753e481ae0e054b206b"
const SHA256 = "55464990f6740eed2af66939e8b2320a9e8a801dee282f72b896cf52732e7060"

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
