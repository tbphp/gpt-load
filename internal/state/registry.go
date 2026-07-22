package state

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

type KeyStatus string

const (
	KeyStatusActive   KeyStatus = "active"
	KeyStatusDisabled KeyStatus = "disabled"
)

type KeyEntry struct {
	ID             uint
	GroupID        uint
	WeightManual   *int
	WeightAuto     int
	Status         KeyStatus
	CooldownUntil  time.Time
	Blacklisted    bool
	FailureCount   int
	EncryptedValue string
}

type KeyMeta struct {
	ID           uint
	GroupID      uint
	WeightManual *int
	WeightAuto   int
}

type KeyRef struct {
	ID             uint
	GroupID        uint
	EncryptedValue string
}

type KeyRegistry struct {
	mu        sync.RWMutex
	buckets   map[uint]map[uint]*KeyEntry
	keyGroups map[uint]uint
}

func NewKeyRegistry() *KeyRegistry {
	return &KeyRegistry{
		buckets:   make(map[uint]map[uint]*KeyEntry),
		keyGroups: make(map[uint]uint),
	}
}

func ValidateKeyEntries(entries []KeyEntry) error {
	seen := make(map[uint]struct{}, len(entries))
	for _, entry := range entries {
		if entry.ID == 0 {
			return fmt.Errorf("key id is required")
		}
		if entry.GroupID == 0 {
			return fmt.Errorf("key %d group id is required", entry.ID)
		}
		if entry.Status != KeyStatusActive && entry.Status != KeyStatusDisabled {
			return fmt.Errorf("key %d has invalid status %q", entry.ID, entry.Status)
		}
		if err := validateManualWeight(fmt.Sprintf("key %d", entry.ID), entry.WeightManual); err != nil {
			return err
		}
		if entry.WeightAuto < 0 || entry.WeightAuto > MaxWeight {
			return fmt.Errorf("key %d auto weight must be between 1 and %d", entry.ID, MaxWeight)
		}
		if entry.EncryptedValue == "" {
			return fmt.Errorf("key %d encrypted value is required", entry.ID)
		}
		if _, duplicate := seen[entry.ID]; duplicate {
			return fmt.Errorf("duplicate key id %d", entry.ID)
		}
		seen[entry.ID] = struct{}{}
	}
	return nil
}

func (r *KeyRegistry) Replace(entries []KeyEntry) error {
	if err := ValidateKeyEntries(entries); err != nil {
		return err
	}

	buckets := make(map[uint]map[uint]*KeyEntry)
	keyGroups := make(map[uint]uint, len(entries))
	for _, entry := range entries {
		if buckets[entry.GroupID] == nil {
			buckets[entry.GroupID] = make(map[uint]*KeyEntry)
		}
		cloned := cloneKeyEntry(entry)
		buckets[entry.GroupID][entry.ID] = &cloned
		keyGroups[entry.ID] = entry.GroupID
	}

	r.mu.Lock()
	r.buckets = buckets
	r.keyGroups = keyGroups
	r.mu.Unlock()
	return nil
}

func (r *KeyRegistry) ApplyImport(groupID uint, entries []KeyEntry) error {
	if groupID == 0 {
		return fmt.Errorf("group id is required")
	}
	if err := ValidateKeyEntries(entries); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.GroupID != groupID {
			return fmt.Errorf("key %d belongs to group %d, want %d", entry.ID, entry.GroupID, groupID)
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range entries {
		if existingGroupID, exists := r.keyGroups[entry.ID]; exists && existingGroupID != groupID {
			return fmt.Errorf("key %d already belongs to group %d", entry.ID, existingGroupID)
		}
	}
	for _, entry := range entries {
		if r.buckets[groupID] == nil {
			r.buckets[groupID] = make(map[uint]*KeyEntry)
		}
		cloned := cloneKeyEntry(entry)
		r.buckets[groupID][entry.ID] = &cloned
		r.keyGroups[entry.ID] = groupID
	}
	return nil
}

func (r *KeyRegistry) RemoveKey(keyID uint) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	groupID, ok := r.keyGroups[keyID]
	if !ok {
		return false
	}
	delete(r.buckets[groupID], keyID)
	if len(r.buckets[groupID]) == 0 {
		delete(r.buckets, groupID)
	}
	delete(r.keyGroups, keyID)
	return true
}

func (r *KeyRegistry) SetKeyStatus(keyID uint, status KeyStatus) error {
	if status != KeyStatusActive && status != KeyStatusDisabled {
		return fmt.Errorf("invalid key status %q", status)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	groupID, ok := r.keyGroups[keyID]
	if !ok {
		return fmt.Errorf("key %d not found", keyID)
	}
	r.buckets[groupID][keyID].Status = status
	return nil
}

func (r *KeyRegistry) EncryptedValue(keyID uint) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	groupID, ok := r.keyGroups[keyID]
	if !ok {
		return "", false
	}
	return r.buckets[groupID][keyID].EncryptedValue, true
}

func (r *KeyRegistry) ActiveEncryptedValue(keyID, expectedGroupID uint) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	groupID, ok := r.keyGroups[keyID]
	if !ok || groupID != expectedGroupID {
		return "", false
	}
	entry, ok := r.buckets[groupID][keyID]
	if !ok || entry.Status != KeyStatusActive {
		return "", false
	}
	return entry.EncryptedValue, true
}

func (r *KeyRegistry) ActiveKeyIDs() []uint {
	r.mu.RLock()
	ids := make([]uint, 0, len(r.keyGroups))
	for _, bucket := range r.buckets {
		for _, entry := range bucket {
			if entry.Status == KeyStatusActive {
				ids = append(ids, entry.ID)
			}
		}
	}
	r.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (r *KeyRegistry) CollectCandidates(groupIDs []uint, excluded func(uint) bool, now time.Time) []KeyMeta {
	r.mu.RLock()
	metas := make([]KeyMeta, 0)
	for _, groupID := range groupIDs {
		for _, entry := range r.buckets[groupID] {
			if entry.Status != KeyStatusActive || entry.Blacklisted || entry.CooldownUntil.After(now) {
				continue
			}
			meta := KeyMeta{
				ID: entry.ID, GroupID: entry.GroupID,
				WeightManual: cloneWeight(entry.WeightManual), WeightAuto: entry.WeightAuto,
			}
			metas = append(metas, meta)
		}
	}
	r.mu.RUnlock()

	filtered := metas[:0]
	for _, meta := range metas {
		if excluded != nil && excluded(meta.ID) {
			continue
		}
		filtered = append(filtered, meta)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].GroupID != filtered[j].GroupID {
			return filtered[i].GroupID < filtered[j].GroupID
		}
		return filtered[i].ID < filtered[j].ID
	})
	return filtered
}

func (r *KeyRegistry) SetCooldown(keyID uint, until time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entryLocked(keyID)
	if !ok {
		return false
	}
	if until.After(entry.CooldownUntil) {
		entry.CooldownUntil = until
	}
	return true
}

func (r *KeyRegistry) SetAutoWeight(keyID uint, weight int) bool {
	if weight < 1 || weight > MaxWeight {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entryLocked(keyID)
	if !ok {
		return false
	}
	entry.WeightAuto = weight
	return true
}

func (r *KeyRegistry) SetBlacklisted(keyID uint) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entryLocked(keyID)
	if !ok {
		return false
	}
	entry.Blacklisted = true
	return true
}

func (r *KeyRegistry) IncrFailure(keyID uint) (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entryLocked(keyID)
	if !ok {
		return 0, false
	}
	entry.FailureCount++
	return entry.FailureCount, true
}

func (r *KeyRegistry) ClearFailure(keyID uint) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entryLocked(keyID)
	if !ok {
		return false
	}
	entry.FailureCount = 0
	return true
}

func (r *KeyRegistry) Recover(keyID uint) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entryLocked(keyID)
	if !ok {
		return false
	}
	entry.Blacklisted = false
	entry.FailureCount = 0
	return true
}

func (r *KeyRegistry) RecoverIfMatch(ref KeyRef, weight int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if weight < 1 || weight > MaxWeight {
		return false
	}
	groupID, ok := r.keyGroups[ref.ID]
	if !ok || groupID != ref.GroupID {
		return false
	}
	entry, ok := r.buckets[groupID][ref.ID]
	if !ok || entry.Status != KeyStatusActive || !entry.Blacklisted || entry.GroupID != ref.GroupID || entry.EncryptedValue != ref.EncryptedValue {
		return false
	}
	entry.WeightAuto = weight
	entry.Blacklisted = false
	entry.FailureCount = 0
	return true
}

func (r *KeyRegistry) BlacklistedKeys() []KeyRef {
	r.mu.RLock()
	refs := make([]KeyRef, 0)
	for _, bucket := range r.buckets {
		for _, entry := range bucket {
			if entry.Status != KeyStatusActive || !entry.Blacklisted {
				continue
			}
			refs = append(refs, KeyRef{
				ID: entry.ID, GroupID: entry.GroupID, EncryptedValue: entry.EncryptedValue,
			})
		}
	}
	r.mu.RUnlock()
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].GroupID != refs[j].GroupID {
			return refs[i].GroupID < refs[j].GroupID
		}
		return refs[i].ID < refs[j].ID
	})
	return refs
}

func (r *KeyRegistry) entryLocked(keyID uint) (*KeyEntry, bool) {
	groupID, ok := r.keyGroups[keyID]
	if !ok {
		return nil, false
	}
	entry, ok := r.buckets[groupID][keyID]
	return entry, ok
}

func cloneKeyEntry(entry KeyEntry) KeyEntry {
	entry.WeightManual = cloneWeight(entry.WeightManual)
	if entry.WeightAuto == 0 {
		entry.WeightAuto = DefaultWeight
	}
	return entry
}
