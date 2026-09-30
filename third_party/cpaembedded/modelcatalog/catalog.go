// Package modelcatalog shares pinned Codex capabilities without exposing CPA types.
package modelcatalog

import (
	_ "embed"
	"encoding/json"
)

const SourceCommit = "690c37fdbe62dc05f609f3a3e609d07ea4d16bf1"
const SHA256 = "c03ac24322c75c844c94040575788374e1c4ca0c261530ac0534385c9eebfeb7"

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
