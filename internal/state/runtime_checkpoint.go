package state

import (
	"sort"
	"time"
)

// KeyRuntimeCheckpoint contains only the mutable health state that is safe to
// carry across a process restart. Persisted key configuration remains owned by
// SQLite and is matched by ID plus group ID during restore.
type KeyRuntimeCheckpoint struct {
	ID            uint      `json:"id"`
	GroupID       uint      `json:"group_id"`
	WeightAuto    int       `json:"weight_auto"`
	CooldownUntil time.Time `json:"cooldown_until"`
	Blacklisted   bool      `json:"blacklisted"`
	FailureCount  int       `json:"failure_count"`
}

// CaptureRuntimeCheckpoint returns detached runtime health state in stable
// order. It intentionally excludes credentials and failure generations.
func (r *KeyRegistry) CaptureRuntimeCheckpoint() []KeyRuntimeCheckpoint {
	r.mu.RLock()
	checkpoints := make([]KeyRuntimeCheckpoint, 0, len(r.keyGroups))
	for _, bucket := range r.buckets {
		for _, entry := range bucket {
			checkpoints = append(checkpoints, KeyRuntimeCheckpoint{
				ID:            entry.ID,
				GroupID:       entry.GroupID,
				WeightAuto:    entry.WeightAuto,
				CooldownUntil: entry.CooldownUntil,
				Blacklisted:   entry.Blacklisted,
				FailureCount:  entry.FailureCount,
			})
		}
	}
	r.mu.RUnlock()
	sort.Slice(checkpoints, func(i, j int) bool {
		if checkpoints[i].GroupID != checkpoints[j].GroupID {
			return checkpoints[i].GroupID < checkpoints[j].GroupID
		}
		return checkpoints[i].ID < checkpoints[j].ID
	})
	return checkpoints
}

// RestoreRuntimeCheckpoint applies matching runtime health state and skips
// keys that were removed or moved to another group while the process was down.
func (r *KeyRegistry) RestoreRuntimeCheckpoint(checkpoints []KeyRuntimeCheckpoint) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	restored := 0
	for _, checkpoint := range checkpoints {
		if checkpoint.ID == 0 || checkpoint.GroupID == 0 ||
			checkpoint.WeightAuto < 0 || checkpoint.WeightAuto > MaxWeight ||
			checkpoint.FailureCount < 0 {
			continue
		}
		groupID, ok := r.keyGroups[checkpoint.ID]
		if !ok || groupID != checkpoint.GroupID {
			continue
		}
		entry, ok := r.buckets[groupID][checkpoint.ID]
		if !ok {
			continue
		}
		entry.WeightAuto = checkpoint.WeightAuto
		entry.CooldownUntil = checkpoint.CooldownUntil
		entry.Blacklisted = checkpoint.Blacklisted
		entry.FailureCount = checkpoint.FailureCount
		restored++
	}
	return restored
}
