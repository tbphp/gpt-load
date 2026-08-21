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
	WeightAuto         int
	Status             CredentialStatus
	AuthState          CredentialAuthState
	CooldownUntil      time.Time
	Blacklisted        bool
	FailureCount       int
	QuotaRemaining     *float64
	QuotaResetAt       time.Time
}

func (view CredentialRuntimeView) AuthReady() bool {
	return view.AuthState.normalize() == CredentialAuthStateReady
}

func (view CredentialRuntimeView) ObservedQuotaRemaining() *float64 {
	return cloneFloat(view.QuotaRemaining)
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
	return CredentialRuntimeAvailable
}

func runtimeView(entry *CredentialEntry) CredentialRuntimeView {
	return CredentialRuntimeView{
		ID:                 entry.ID,
		GroupID:            entry.GroupID,
		Version:            entry.Version,
		IdentityGeneration: entry.IdentityGeneration,
		WeightManual:       cloneWeight(entry.WeightManual),
		WeightAuto:         entry.WeightAuto,
		Status:             entry.Status,
		AuthState:          entry.AuthState.normalize(),
		CooldownUntil:      entry.CooldownUntil,
		Blacklisted:        entry.Blacklisted,
		FailureCount:       entry.FailureCount,
		QuotaRemaining:     cloneFloat(entry.quotaRemaining),
		QuotaResetAt:       entry.quotaResetAt,
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
