package state

import (
	"sort"
	"time"

	"gpt-load/internal/policy"
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
	QuotaWindows       []policy.QuotaWindowFact
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

func (view CredentialRuntimeView) Clone() CredentialRuntimeView {
	view.WeightManual = cloneWeight(view.WeightManual)
	view.ModelCooldowns = cloneModelCooldowns(view.ModelCooldowns)
	view.QuotaRemaining = cloneFloat(view.QuotaRemaining)
	view.QuotaWindows = policy.CloneQuotaWindows(view.QuotaWindows)
	return view
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
		CooldownUntil:      entry.CooldownUntil,
		ModelCooldowns:     cloneModelCooldowns(entry.ModelCooldowns),
		Blacklisted:        entry.Blacklisted,
		FailureCount:       entry.FailureCount,
		QuotaRemaining:     cloneFloat(entry.quotaRemaining),
		QuotaResetAt:       entry.quotaResetAt,
		QuotaWindows:       policy.CloneQuotaWindows(entry.quotaFacts),
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
	return r.SnapshotForScope(0, 0)
}

// SnapshotForScope returns an immutable view of runtime health for credentials matching
// groupID and/or credentialID, filtering before cloning to minimize allocation overhead.
// If both groupID and credentialID are 0, it behaves identically to Snapshot().
func (r *CredentialRegistry) SnapshotForScope(groupID, credentialID uint) []CredentialRuntimeView {
	if r == nil {
		return []CredentialRuntimeView{}
	}
	r.mu.RLock()
	views := make([]CredentialRuntimeView, 0, 8)
	collect := func(bucket map[uint]*CredentialEntry) {
		for _, entry := range bucket {
			views = append(views, runtimeView(entry))
		}
	}
	switch {
	case credentialID != 0 && groupID != 0:
		if entry, ok := r.buckets[groupID][credentialID]; ok {
			views = append(views, runtimeView(entry))
		}
	case credentialID != 0:
		if entry, ok := r.entryLocked(credentialID); ok {
			views = append(views, runtimeView(entry))
		}
	case groupID != 0:
		collect(r.buckets[groupID])
	default:
		for _, bucket := range r.buckets {
			collect(bucket)
		}
	}
	r.mu.RUnlock()
	sortRuntimeViews(views)
	return views
}
