// Package modelcatalog shares pinned Codex capabilities without exposing CPA types.
package modelcatalog

import (
	_ "embed"
	"encoding/json"
)

const SourceCommit = "26f77650ca9f30630f244989ad47962c2ef02b33"
const SHA256 = "89072af37d0ebf001992829aff18162a4492ed4be039adf73df0e6e74fec0d9b"

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
