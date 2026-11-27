package state

import (
	"sort"
	"time"
)

type CredentialRuntimeView struct {
	ID                 uint
	GroupID            uint
	Version            uint64
	IdentityGeneration uint64
	WeightManual       *int
	Status             CredentialStatus
	AuthState          CredentialAuthState
	CooldownUntil      time.Time
	ModelCooldowns     map[string]time.Time
	Blacklisted        bool
	FailureCount       int
	QuotaRemaining     *float64
	QuotaResetAt       time.Time
	QuotaIncomplete    bool

	QuotaReservePercent int
}

func (view CredentialRuntimeView) AuthReady() bool {
	return view.AuthState.normalize() == CredentialAuthStateReady
}

func (view CredentialRuntimeView) ObservedQuotaRemaining() *float64 {
	return cloneFloat(view.QuotaRemaining)
}

func (view CredentialRuntimeView) QuotaProtected() bool {
	// 利用率转换为剩余比例时会有浮点误差，阈值相等仍应触发保护。
	return view.QuotaReservePercent > 0 && (view.QuotaRemaining == nil || view.QuotaIncomplete ||
		*view.QuotaRemaining <= float64(view.QuotaReservePercent)/100+1e-9)
}

// RuntimeView 合并不同原因的冷却，供管理面使用同一份健康投影。
func (entry CredentialEntry) RuntimeView() CredentialRuntimeView {
	return runtimeView(&entry)
}

type CredentialRuntimeState string

const (
	CredentialRuntimeAvailable   CredentialRuntimeState = "available"
	CredentialRuntimeDisabled    CredentialRuntimeState = "disabled"
	CredentialRuntimeBlacklisted CredentialRuntimeState = "blacklisted"
	CredentialRuntimeCooldown    CredentialRuntimeState = "cooldown"
)

func (view CredentialRuntimeView) RuntimeState(now time.Time) CredentialRuntimeState {
	if view.Status != CredentialStatusActive {
		return CredentialRuntimeDisabled
	}
	if view.Blacklisted {
		return CredentialRuntimeBlacklisted
	}
	if view.CooldownUntil.After(now) {
		return CredentialRuntimeCooldown
	}
	if view.QuotaProtected() {
		return CredentialRuntimeDisabled
	}
	return CredentialRuntimeAvailable
}

func runtimeView(entry *CredentialEntry) CredentialRuntimeView {
	return CredentialRuntimeView{
		ID:                 entry.ID,
		GroupID:            entry.GroupID,
		Version:            entry.Version,
		IdentityGeneration: entry.IdentityGeneration,
		WeightManual:       cloneWeight(entry.WeightManual),
		Status:             entry.Status,
		AuthState:          entry.AuthState.normalize(),
		CooldownUntil:      effectiveCooldownUntil(entry),
		ModelCooldowns:     effectiveModelCooldowns(entry),
		Blacklisted:        entry.Blacklisted,
		FailureCount:       entry.FailureCount,
		QuotaRemaining:     cloneFloat(entry.quotaRemaining),
		QuotaResetAt:       entry.quotaResetAt,
		QuotaIncomplete:    entry.quotaIncomplete,

		QuotaReservePercent: entry.QuotaReservePercent,
	}
}

func sortRuntimeViews(views []CredentialRuntimeView) {
	sort.Slice(views, func(i, j int) bool {
		if views[i].GroupID != views[j].GroupID {
			return views[i].GroupID < views[j].GroupID
		}
		return views[i].ID < views[j].ID
	})
}

func (r *CredentialRegistry) Snapshot() []CredentialRuntimeView {
	r.mu.RLock()
	views := make([]CredentialRuntimeView, 0, len(r.credentialGroups))
	for _, bucket := range r.buckets {
		for _, entry := range bucket {
			views = append(views, runtimeView(entry))
		}
	}
	r.mu.RUnlock()
	sortRuntimeViews(views)
	return views
}
