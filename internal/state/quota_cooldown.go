package state

import (
	"strings"
	"time"
)

// SetQuotaCooldown 将限额与其他原因的冷却分别保存，空模型表示账号级限额。
func (r *CredentialRegistry) SetQuotaCooldown(ref CredentialRef, model string, until, now time.Time) (bool, bool) {
	if strings.TrimSpace(model) != model || model != "" && !until.After(now) {
		return false, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entryLocked(ref.ID)
	if !ok || entry.GroupID != ref.GroupID || entry.IdentityGeneration != ref.IdentityGeneration ||
		entry.QuotaCooldownGeneration != ref.QuotaCooldownGeneration {
		return false, false
	}
	if model == "" {
		if !until.After(entry.QuotaCooldownUntil) {
			return true, false
		}
		entry.QuotaCooldownUntil = until
		r.scheduling.SyncCredential(runtimeView(entry))
		return true, true
	}
	pruneModelCooldowns(entry.QuotaModelCooldowns, now)
	if !until.After(entry.QuotaModelCooldowns[model]) {
		return true, false
	}
	if entry.QuotaModelCooldowns == nil {
		entry.QuotaModelCooldowns = make(map[string]time.Time)
	}
	entry.QuotaModelCooldowns[model] = until
	return true, true
}

// RestoreQuotaCooldowns 只解除限额冷却，保留其他健康状态。
func (r *CredentialRegistry) RestoreQuotaCooldowns(credentialID uint) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entryLocked(credentialID)
	if !ok {
		return false
	}
	entry.QuotaCooldownUntil = time.Time{}
	entry.QuotaModelCooldowns = nil
	// 即使当前没有冷却，也阻止恢复前的在途限额结果写回。
	entry.QuotaCooldownGeneration++
	r.scheduling.SyncCredential(runtimeView(entry))
	return true
}

func effectiveCooldownUntil(entry *CredentialEntry) time.Time {
	if entry.QuotaCooldownUntil.After(entry.CooldownUntil) {
		return entry.QuotaCooldownUntil
	}
	return entry.CooldownUntil
}

func effectiveModelCooldowns(entry *CredentialEntry) map[string]time.Time {
	limits := cloneModelCooldowns(entry.ModelCooldowns)
	if len(entry.QuotaModelCooldowns) > 0 && limits == nil {
		limits = make(map[string]time.Time, len(entry.QuotaModelCooldowns))
	}
	for model, until := range entry.QuotaModelCooldowns {
		if until.After(limits[model]) {
			limits[model] = until
		}
	}
	return limits
}
