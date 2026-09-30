package control

import (
	"encoding/json"

	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription/providers/codex"
)

// 仅补充展示；不制造额度观测成功、观测时间或任何可用额度。
func withCredentialPlan(current *CredentialObservationResponse, plan string) *CredentialObservationResponse {
	if plan == "" || current != nil && current.Snapshot != nil && current.Snapshot.Plan.Name != "" {
		return current
	}
	body, err := json.Marshal(map[string]string{"plan_type": plan})
	if err != nil {
		return current
	}
	raw, err := codex.NormalizeQuota(body, nil)
	if err != nil {
		return current
	}
	var hint CredentialObservationSnapshot
	if json.Unmarshal(raw, &hint) != nil || hint.Plan.Name == "" {
		return current
	}
	result := CredentialObservationResponse{State: string(models.CredentialObservationUnavailable)}
	if current != nil {
		result = *current
	}
	if result.Snapshot == nil {
		result.Snapshot = &hint
	} else {
		snapshot := *result.Snapshot
		snapshot.Plan = hint.Plan
		result.Snapshot = &snapshot
	}
	return &result
}
