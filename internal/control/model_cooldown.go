package control

import (
	"sort"
	"time"
)

type ModelCooldownResponse struct {
	Model           string `json:"model"`
	CooldownUntilMS int64  `json:"cooldown_until_ms"`
}

func hasModelCooldown(limits map[string]time.Time, now time.Time) bool {
	for _, until := range limits {
		if until.After(now) {
			return true
		}
	}
	return false
}

func modelCooldownResponses(limits map[string]time.Time, now time.Time) ([]ModelCooldownResponse, error) {
	result := make([]ModelCooldownResponse, 0, len(limits))
	for model, until := range limits {
		if !until.After(now) {
			continue
		}
		milliseconds, err := safeEpochMilliseconds(until)
		if err != nil {
			return nil, err
		}
		result = append(result, ModelCooldownResponse{Model: model, CooldownUntilMS: milliseconds})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Model < result[j].Model })
	return result, nil
}
