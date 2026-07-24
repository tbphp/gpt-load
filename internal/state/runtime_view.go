package state

import (
	"sort"
	"time"
)

type KeyRuntimeView struct {
	ID            uint
	GroupID       uint
	WeightManual  *int
	WeightAuto    int
	Status        KeyStatus
	CooldownUntil time.Time
	Blacklisted   bool
	FailureCount  int
}

type KeyRuntimeState string

const (
	KeyRuntimeAvailable   KeyRuntimeState = "available"
	KeyRuntimeDisabled    KeyRuntimeState = "disabled"
	KeyRuntimeBlacklisted KeyRuntimeState = "blacklisted"
	KeyRuntimeCooldown    KeyRuntimeState = "cooldown"
)

func (view KeyRuntimeView) RuntimeState(now time.Time) KeyRuntimeState {
	if view.Status != KeyStatusActive {
		return KeyRuntimeDisabled
	}
	if view.Blacklisted {
		return KeyRuntimeBlacklisted
	}
	if view.CooldownUntil.After(now) {
		return KeyRuntimeCooldown
	}
	return KeyRuntimeAvailable
}

func runtimeView(entry *KeyEntry) KeyRuntimeView {
	return KeyRuntimeView{
		ID:            entry.ID,
		GroupID:       entry.GroupID,
		WeightManual:  cloneWeight(entry.WeightManual),
		WeightAuto:    entry.WeightAuto,
		Status:        entry.Status,
		CooldownUntil: entry.CooldownUntil,
		Blacklisted:   entry.Blacklisted,
		FailureCount:  entry.FailureCount,
	}
}

func sortRuntimeViews(views []KeyRuntimeView) {
	sort.Slice(views, func(i, j int) bool {
		if views[i].GroupID != views[j].GroupID {
			return views[i].GroupID < views[j].GroupID
		}
		return views[i].ID < views[j].ID
	})
}

func (r *KeyRegistry) Snapshot() []KeyRuntimeView {
	r.mu.RLock()
	views := make([]KeyRuntimeView, 0, len(r.keyGroups))
	for _, bucket := range r.buckets {
		for _, entry := range bucket {
			views = append(views, runtimeView(entry))
		}
	}
	r.mu.RUnlock()
	sortRuntimeViews(views)
	return views
}
