package state

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	providerobservation "gpt-load/internal/subscription/providers/observation"
)

type CredentialStatus string

const (
	CredentialStatusActive   CredentialStatus = "active"
	CredentialStatusDisabled CredentialStatus = "disabled"
)

type CredentialAuthState string

const (
	CredentialAuthStateReady                   CredentialAuthState = "ready"
	CredentialAuthStateRefreshing              CredentialAuthState = "refreshing"
	CredentialAuthStateReauthorizationRequired CredentialAuthState = "reauthorization_required"
	CredentialAuthStateOutcomeUnknown          CredentialAuthState = "outcome_unknown"
)

type CredentialEntry struct {
	ID                 uint
	GroupID            uint
	Version            uint64
	IdentityGeneration uint64
	Fingerprint        string
	WeightManual       *int
	WeightAuto         int
	Status             CredentialStatus
	AuthState          CredentialAuthState
	CooldownUntil      time.Time
	Blacklisted        bool
	FailureCount       int
	FailureGeneration  uint64
	EncryptedValue     string
	EncryptedProxy     string
	ProxyFingerprint   string
	quotaRemaining     *float64
	quotaResetAt       time.Time
}

type CredentialMeta struct {
	ID                 uint
	GroupID            uint
	Version            uint64
	IdentityGeneration uint64
	WeightManual       *int
	WeightAuto         int
}

type CredentialRef struct {
	ID                 uint
	GroupID            uint
	Version            uint64
	IdentityGeneration uint64
	Fingerprint        string
	EncryptedValue     string
	EncryptedProxy     string
	ProxyFingerprint   string
	FailureGeneration  uint64
}

type CredentialRegistry struct {
	mu               sync.RWMutex
	buckets          map[uint]map[uint]*CredentialEntry
	credentialGroups map[uint]uint
}

func NewCredentialRegistry() *CredentialRegistry {
	return &CredentialRegistry{
		buckets:          make(map[uint]map[uint]*CredentialEntry),
		credentialGroups: make(map[uint]uint),
	}
}

func ValidateCredentialEntries(entries []CredentialEntry) error {
	seen := make(map[uint]struct{}, len(entries))
	for _, entry := range entries {
		if entry.ID == 0 {
			return fmt.Errorf("credential id is required")
		}
		if entry.GroupID == 0 {
			return fmt.Errorf("credential %d group id is required", entry.ID)
		}
		if entry.Status != CredentialStatusActive && entry.Status != CredentialStatusDisabled {
			return fmt.Errorf("credential %d has invalid status %q", entry.ID, entry.Status)
		}
		if !entry.AuthState.valid() {
			return fmt.Errorf("credential %d has invalid auth state %q", entry.ID, entry.AuthState)
		}
		if err := validateManualWeight(fmt.Sprintf("credential %d", entry.ID), entry.WeightManual); err != nil {
			return err
		}
		if entry.WeightAuto < 0 || entry.WeightAuto > MaxWeight {
			return fmt.Errorf("credential %d auto weight must be between 0 and %d", entry.ID, MaxWeight)
		}
		if entry.EncryptedValue == "" {
			return fmt.Errorf("credential %d encrypted value is required", entry.ID)
		}
		if (entry.EncryptedProxy == "") != (entry.ProxyFingerprint == "") {
			return fmt.Errorf("credential %d proxy identity is incomplete", entry.ID)
		}
		if entry.Version == 0 {
			return fmt.Errorf("credential %d version is required", entry.ID)
		}
		if entry.IdentityGeneration == 0 {
			return fmt.Errorf("credential %d identity generation is required", entry.ID)
		}
		if strings.TrimSpace(entry.Fingerprint) == "" {
			return fmt.Errorf("credential %d fingerprint is required", entry.ID)
		}
		if _, duplicate := seen[entry.ID]; duplicate {
			return fmt.Errorf("duplicate credential id %d", entry.ID)
		}
		seen[entry.ID] = struct{}{}
	}
	return nil
}

func (r *CredentialRegistry) ReplaceCredentials(entries []CredentialEntry) error {
	if err := ValidateCredentialEntries(entries); err != nil {
		return err
	}

	buckets := make(map[uint]map[uint]*CredentialEntry)
	credentialGroups := make(map[uint]uint, len(entries))
	for _, entry := range entries {
		if buckets[entry.GroupID] == nil {
			buckets[entry.GroupID] = make(map[uint]*CredentialEntry)
		}
		cloned := cloneCredentialEntry(entry)
		buckets[entry.GroupID][entry.ID] = &cloned
		credentialGroups[entry.ID] = entry.GroupID
	}

	r.mu.Lock()
	r.buckets = buckets
	r.credentialGroups = credentialGroups
	r.mu.Unlock()
	return nil
}

func (r *CredentialRegistry) ApplyCredentialImport(groupID uint, entries []CredentialEntry) error {
	if groupID == 0 {
		return fmt.Errorf("group id is required")
	}
	if err := ValidateCredentialEntries(entries); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.GroupID != groupID {
			return fmt.Errorf("credential %d belongs to group %d, want %d", entry.ID, entry.GroupID, groupID)
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range entries {
		if existingGroupID, exists := r.credentialGroups[entry.ID]; exists && existingGroupID != groupID {
			return fmt.Errorf("credential %d already belongs to group %d", entry.ID, existingGroupID)
		}
	}
	for _, entry := range entries {
		if r.buckets[groupID] == nil {
			r.buckets[groupID] = make(map[uint]*CredentialEntry)
		}
		cloned := cloneCredentialEntry(entry)
		r.buckets[groupID][entry.ID] = &cloned
		r.credentialGroups[entry.ID] = groupID
	}
	return nil
}

// SnapshotGroupCredentialEntriesExact returns detached selected entries without
// applying the configuration replacement resets used by cloneCredentialEntry.
func (r *CredentialRegistry) SnapshotGroupCredentialEntriesExact(
	groupID uint,
	credentialIDs []uint,
) ([]CredentialEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := r.validateGroupCredentialIDsLocked(groupID, credentialIDs); err != nil {
		return nil, err
	}
	entries := make([]CredentialEntry, 0, len(credentialIDs))
	for _, credentialID := range credentialIDs {
		entries = append(entries, detachCredentialEntryExact(*r.buckets[groupID][credentialID]))
	}
	return entries, nil
}

// RestoreGroupCredentialEntriesExact atomically restores only selected entries,
// including their runtime failure generation. Unselected entries are untouched.
func (r *CredentialRegistry) RestoreGroupCredentialEntriesExact(groupID uint, entries []CredentialEntry) error {
	if groupID == 0 {
		return fmt.Errorf("group id is required")
	}
	if len(entries) == 0 {
		return fmt.Errorf("credential entries are required")
	}
	if err := ValidateCredentialEntries(entries); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.GroupID != groupID {
			return fmt.Errorf("credential %d belongs to group %d, want %d", entry.ID, entry.GroupID, groupID)
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range entries {
		if existingGroupID, exists := r.credentialGroups[entry.ID]; exists && existingGroupID != groupID {
			return fmt.Errorf("credential %d already belongs to group %d", entry.ID, existingGroupID)
		}
	}
	if r.buckets[groupID] == nil {
		r.buckets[groupID] = make(map[uint]*CredentialEntry)
	}
	for _, entry := range entries {
		detached := detachCredentialEntryExact(entry)
		r.buckets[groupID][entry.ID] = &detached
		r.credentialGroups[entry.ID] = groupID
	}
	return nil
}

// MatchesGroup compares the persisted configuration-owned fields for one
// group. Runtime health state is intentionally excluded.
func (r *CredentialRegistry) MatchesGroup(groupID uint, entries []CredentialEntry) bool {
	if groupID == 0 || ValidateCredentialEntries(entries) != nil {
		return false
	}
	for _, entry := range entries {
		if entry.GroupID != groupID {
			return false
		}
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.matchesGroupLocked(groupID, entries)
}

// ReconcileGroup makes one group match DB truth while preserving runtime
// health state for entries whose persisted configuration is already equal.
func (r *CredentialRegistry) ReconcileGroup(groupID uint, entries []CredentialEntry) (bool, error) {
	if groupID == 0 {
		return false, fmt.Errorf("group id is required")
	}
	if err := ValidateCredentialEntries(entries); err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.GroupID != groupID {
			return false, fmt.Errorf(
				"credential %d belongs to group %d, want %d",
				entry.ID,
				entry.GroupID,
				groupID,
			)
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range entries {
		if existingGroupID, exists := r.credentialGroups[entry.ID]; exists &&
			existingGroupID != groupID {
			return false, fmt.Errorf(
				"credential %d already belongs to group %d",
				entry.ID,
				existingGroupID,
			)
		}
	}
	if r.matchesGroupLocked(groupID, entries) {
		return false, nil
	}

	previous := r.buckets[groupID]
	next := make(map[uint]*CredentialEntry, len(entries))
	for _, desired := range entries {
		if existing := previous[desired.ID]; existing != nil &&
			samePersistedCredentialConfig(*existing, desired) {
			next[desired.ID] = existing
			continue
		}
		cloned := cloneCredentialEntry(desired)
		next[desired.ID] = &cloned
	}
	for credentialID := range previous {
		delete(r.credentialGroups, credentialID)
	}
	if len(next) == 0 {
		delete(r.buckets, groupID)
	} else {
		r.buckets[groupID] = next
		for credentialID := range next {
			r.credentialGroups[credentialID] = groupID
		}
	}
	return true, nil
}

func (r *CredentialRegistry) matchesGroupLocked(groupID uint, entries []CredentialEntry) bool {
	current := r.buckets[groupID]
	if len(current) != len(entries) {
		return false
	}
	for _, desired := range entries {
		existing := current[desired.ID]
		if existing == nil || !samePersistedCredentialConfig(*existing, desired) {
			return false
		}
	}
	return true
}

func samePersistedCredentialConfig(left, right CredentialEntry) bool {
	if left.ID != right.ID ||
		left.GroupID != right.GroupID ||
		left.Version != right.Version ||
		left.IdentityGeneration != right.IdentityGeneration ||
		left.Fingerprint != right.Fingerprint ||
		left.Status != right.Status ||
		left.EncryptedValue != right.EncryptedValue ||
		left.EncryptedProxy != right.EncryptedProxy ||
		left.ProxyFingerprint != right.ProxyFingerprint {
		return false
	}
	if left.WeightManual == nil || right.WeightManual == nil {
		return left.WeightManual == nil && right.WeightManual == nil
	}
	return *left.WeightManual == *right.WeightManual
}

func (r *CredentialRegistry) RemoveCredential(credentialID uint) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	groupID, ok := r.credentialGroups[credentialID]
	if !ok {
		return false
	}
	delete(r.buckets[groupID], credentialID)
	if len(r.buckets[groupID]) == 0 {
		delete(r.buckets, groupID)
	}
	delete(r.credentialGroups, credentialID)
	return true
}

func (r *CredentialRegistry) UpdateGroupCredentialStatuses(
	groupID uint,
	credentialIDs []uint,
	status CredentialStatus,
) error {
	if status != CredentialStatusActive && status != CredentialStatusDisabled {
		return fmt.Errorf("invalid credential status %q", status)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.validateGroupCredentialIDsLocked(groupID, credentialIDs); err != nil {
		return err
	}
	for _, credentialID := range credentialIDs {
		r.buckets[groupID][credentialID].Status = status
	}
	return nil
}

func (r *CredentialRegistry) RemoveGroupCredentials(groupID uint, credentialIDs []uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.validateGroupCredentialIDsLocked(groupID, credentialIDs); err != nil {
		return err
	}
	for _, credentialID := range credentialIDs {
		delete(r.buckets[groupID], credentialID)
		delete(r.credentialGroups, credentialID)
	}
	if len(r.buckets[groupID]) == 0 {
		delete(r.buckets, groupID)
	}
	return nil
}

func (r *CredentialRegistry) validateGroupCredentialIDsLocked(groupID uint, credentialIDs []uint) error {
	if groupID == 0 {
		return fmt.Errorf("group id is required")
	}
	if len(credentialIDs) == 0 {
		return fmt.Errorf("credential ids are required")
	}
	seen := make(map[uint]struct{}, len(credentialIDs))
	for _, credentialID := range credentialIDs {
		if credentialID == 0 {
			return fmt.Errorf("credential id is required")
		}
		if _, duplicate := seen[credentialID]; duplicate {
			return fmt.Errorf("duplicate credential id %d", credentialID)
		}
		seen[credentialID] = struct{}{}
		actualGroupID, exists := r.credentialGroups[credentialID]
		if !exists {
			return fmt.Errorf("credential %d not found", credentialID)
		}
		if actualGroupID != groupID {
			return fmt.Errorf("credential %d belongs to group %d, want %d", credentialID, actualGroupID, groupID)
		}
		if r.buckets[groupID][credentialID] == nil {
			return fmt.Errorf("credential %d not found in group %d", credentialID, groupID)
		}
	}
	return nil
}

func (r *CredentialRegistry) RemoveGroup(groupID uint) bool {
	if groupID == 0 {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	bucket, exists := r.buckets[groupID]
	if !exists {
		return false
	}
	for credentialID := range bucket {
		delete(r.credentialGroups, credentialID)
	}
	delete(r.buckets, groupID)
	return true
}

func (r *CredentialRegistry) SetCredentialStatus(credentialID uint, status CredentialStatus) error {
	if status != CredentialStatusActive && status != CredentialStatusDisabled {
		return fmt.Errorf("invalid credential status %q", status)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	groupID, ok := r.credentialGroups[credentialID]
	if !ok {
		return fmt.Errorf("credential %d not found", credentialID)
	}
	r.buckets[groupID][credentialID].Status = status
	return nil
}

func (r *CredentialRegistry) UpdateCredentialConfig(
	credentialID uint,
	status CredentialStatus,
	weightManual *int,
) error {
	if status != CredentialStatusActive && status != CredentialStatusDisabled {
		return fmt.Errorf("invalid credential status %q", status)
	}
	if err := validateManualWeight(fmt.Sprintf("credential %d", credentialID), weightManual); err != nil {
		return err
	}
	clonedWeight := cloneWeight(weightManual)
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entryLocked(credentialID)
	if !ok {
		return fmt.Errorf("credential %d not found", credentialID)
	}
	entry.Status = status
	entry.WeightManual = clonedWeight
	return nil
}

// ReplaceCredentialSecretIfMatch publishes one durable secret rotation without
// changing the logical account identity or its runtime health state.
func (r *CredentialRegistry) ReplaceCredentialSecretIfMatch(
	credentialID uint,
	expectedVersion, nextVersion uint64,
	fingerprint, encryptedValue string,
) bool {
	if credentialID == 0 || expectedVersion == 0 ||
		nextVersion <= expectedVersion || strings.TrimSpace(fingerprint) == "" || encryptedValue == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entryLocked(credentialID)
	if !ok || entry.Version != expectedVersion {
		return false
	}
	entry.Version = nextVersion
	entry.Fingerprint = fingerprint
	entry.EncryptedValue = encryptedValue
	entry.AuthState = CredentialAuthStateReady
	return true
}

func (r *CredentialRegistry) SetCredentialAuthState(credentialID uint, authState CredentialAuthState) bool {
	if !authState.valid() {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entryLocked(credentialID)
	if !ok {
		return false
	}
	entry.AuthState = authState.normalize()
	if entry.AuthState != CredentialAuthStateReady {
		entry.quotaRemaining = nil
		entry.quotaResetAt = time.Time{}
	}
	return true
}

// EncryptedCredentialData returns encrypted credential data for the selected
// stable credential ID. Decryption belongs to the gateway execution boundary.
func (r *CredentialRegistry) EncryptedCredentialData(credentialID uint) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	groupID, ok := r.credentialGroups[credentialID]
	if !ok {
		return "", false
	}
	return r.buckets[groupID][credentialID].EncryptedValue, true
}

func (r *CredentialRegistry) ActiveEncryptedCredentialData(credentialID, expectedGroupID uint) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	groupID, ok := r.credentialGroups[credentialID]
	if !ok || groupID != expectedGroupID {
		return "", false
	}
	entry, ok := r.buckets[groupID][credentialID]
	if !ok || entry.Status != CredentialStatusActive || entry.AuthState.normalize() != CredentialAuthStateReady {
		return "", false
	}
	return entry.EncryptedValue, true
}

func (r *CredentialRegistry) CaptureActiveCredentialRefs(groupIDs []uint) []CredentialRef {
	selectedGroups := make(map[uint]struct{}, len(groupIDs))
	for _, groupID := range groupIDs {
		if groupID != 0 {
			selectedGroups[groupID] = struct{}{}
		}
	}

	r.mu.RLock()
	refs := make([]CredentialRef, 0)
	for groupID := range selectedGroups {
		for _, entry := range r.buckets[groupID] {
			if entry.Status != CredentialStatusActive || entry.AuthState.normalize() != CredentialAuthStateReady {
				continue
			}
			refs = append(refs, CredentialRef{
				ID: entry.ID, GroupID: entry.GroupID,
				Version: entry.Version, IdentityGeneration: entry.IdentityGeneration,
				Fingerprint: entry.Fingerprint, EncryptedValue: entry.EncryptedValue,
				EncryptedProxy: entry.EncryptedProxy, ProxyFingerprint: entry.ProxyFingerprint,
				FailureGeneration: entry.FailureGeneration,
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

func (r *CredentialRegistry) ActiveEncryptedCredentialDataIfMatch(ref CredentialRef) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	groupID, ok := r.credentialGroups[ref.ID]
	if !ok || groupID != ref.GroupID {
		return "", false
	}
	entry, ok := r.buckets[groupID][ref.ID]
	if !ok ||
		entry.ID != ref.ID ||
		entry.GroupID != ref.GroupID ||
		entry.Version != ref.Version ||
		entry.IdentityGeneration != ref.IdentityGeneration ||
		entry.Fingerprint != ref.Fingerprint ||
		entry.EncryptedValue != ref.EncryptedValue ||
		entry.EncryptedProxy != ref.EncryptedProxy ||
		entry.ProxyFingerprint != ref.ProxyFingerprint ||
		entry.Status != CredentialStatusActive || entry.AuthState.normalize() != CredentialAuthStateReady {
		return "", false
	}
	// FailureGeneration is intentionally excluded: failure accounting must not
	// invalidate a request that already captured this key identity.
	return entry.EncryptedValue, true
}

func (r *CredentialRegistry) ActiveCredentialIDs() []uint {
	r.mu.RLock()
	ids := make([]uint, 0, len(r.credentialGroups))
	for _, bucket := range r.buckets {
		for _, entry := range bucket {
			if entry.Status == CredentialStatusActive {
				ids = append(ids, entry.ID)
			}
		}
	}
	r.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// CredentialRef returns one detached durable identity reference, including
// disabled or temporarily unavailable credentials.
func (r *CredentialRegistry) CredentialRef(credentialID uint) (CredentialRef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.entryLocked(credentialID)
	if !ok {
		return CredentialRef{}, false
	}
	return CredentialRef{
		ID: entry.ID, GroupID: entry.GroupID, Version: entry.Version,
		IdentityGeneration: entry.IdentityGeneration, Fingerprint: entry.Fingerprint,
		EncryptedValue: entry.EncryptedValue, EncryptedProxy: entry.EncryptedProxy,
		ProxyFingerprint: entry.ProxyFingerprint, FailureGeneration: entry.FailureGeneration,
	}, true
}

// CollectCredentialCandidates returns currently schedulable credentials.
func (r *CredentialRegistry) CollectCredentialCandidates(groupIDs []uint, excluded func(uint) bool, now time.Time) []CredentialMeta {
	r.mu.RLock()
	metas := make([]CredentialMeta, 0)
	for _, groupID := range groupIDs {
		for _, entry := range r.buckets[groupID] {
			view := runtimeView(entry)
			if view.RuntimeState(now) != CredentialRuntimeAvailable || entry.AuthState.normalize() != CredentialAuthStateReady {
				continue
			}
			meta := CredentialMeta{
				ID: view.ID, GroupID: view.GroupID,
				Version: view.Version, IdentityGeneration: view.IdentityGeneration,
				WeightManual: cloneWeight(view.WeightManual), WeightAuto: view.WeightAuto,
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

// SetCredentialQuotaObservation publishes an ephemeral provider observation for
// management-plane health display. Passing nil clears the observation.
func (r *CredentialRegistry) SetCredentialQuotaObservation(
	credentialID uint,
	remaining *float64,
	resetAt time.Time,
) bool {
	if credentialID == 0 {
		return false
	}
	if remaining != nil && (math.IsNaN(*remaining) || math.IsInf(*remaining, 0) ||
		*remaining < 0 || *remaining > 1 || resetAt.IsZero()) {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entryLocked(credentialID)
	if !ok {
		return false
	}
	entry.quotaRemaining = cloneFloat(remaining)
	if remaining == nil {
		entry.quotaResetAt = time.Time{}
		return true
	}
	entry.quotaResetAt = resetAt
	return true
}

// ApplyQuotaWindows publishes the tightest account-scope remaining ratio
// derived from windows to the management-plane health display, or clears it
// when no account-scope window carries both a usable ratio and a reset time.
// It is shared by the active and passive credential observation writers so
// both pick the same bottleneck window using one rule.
func (r *CredentialRegistry) ApplyQuotaWindows(credentialID uint, windows []providerobservation.QuotaWindow) bool {
	if r == nil || credentialID == 0 {
		return false
	}
	var remaining *float64
	var resetAtMS int64
	for _, window := range windows {
		if window.Scope != "account" || window.ResetAtMS == nil {
			continue
		}
		value, known := quotaWindowRemainingRatio(window)
		if !known {
			continue
		}
		if remaining == nil || value < *remaining {
			cloned := value
			remaining = &cloned
			resetAtMS = *window.ResetAtMS
		} else if value == *remaining && *window.ResetAtMS > resetAtMS {
			// Equal bottlenecks must all recover before this credential is usable.
			resetAtMS = *window.ResetAtMS
		}
	}
	if remaining == nil || resetAtMS == 0 {
		return r.SetCredentialQuotaObservation(credentialID, nil, time.Time{})
	}
	return r.SetCredentialQuotaObservation(credentialID, remaining, time.UnixMilli(resetAtMS).UTC())
}

func quotaWindowRemainingRatio(window providerobservation.QuotaWindow) (float64, bool) {
	if window.State == "exhausted" {
		return 0, true
	}
	if window.Utilization != nil {
		return math.Max(0, math.Min(1, 1-*window.Utilization)), true
	}
	if window.Remaining != nil && window.Limit != nil && *window.Limit > 0 {
		return math.Max(0, math.Min(1, *window.Remaining / *window.Limit)), true
	}
	return 0, false
}

func (r *CredentialRegistry) SetCooldown(credentialID uint, until time.Time) bool {
	exists, _ := r.SetCooldownWithChange(credentialID, until)
	return exists
}

func (r *CredentialRegistry) SetCooldownWithChange(credentialID uint, until time.Time) (bool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entryLocked(credentialID)
	if !ok {
		return false, false
	}
	if !until.After(entry.CooldownUntil) {
		return true, false
	}
	entry.CooldownUntil = until
	return true, true
}

func (r *CredentialRegistry) SetBlacklistedWithChange(credentialID uint) (bool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entryLocked(credentialID)
	if !ok {
		return false, false
	}
	if entry.Blacklisted {
		return true, false
	}
	entry.Blacklisted = true
	entry.FailureGeneration++
	return true, true
}

func (r *CredentialRegistry) SetAutoWeight(credentialID uint, weight int) bool {
	if weight < 1 || weight > MaxWeight {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entryLocked(credentialID)
	if !ok {
		return false
	}
	entry.WeightAuto = weight
	return true
}

func (r *CredentialRegistry) RestoreRuntimeState(credentialID uint, weight int) bool {
	if weight < 1 || weight > MaxWeight {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entryLocked(credentialID)
	if !ok {
		return false
	}
	entry.WeightAuto = weight
	entry.CooldownUntil = time.Time{}
	entry.Blacklisted = false
	entry.FailureCount = 0
	entry.FailureGeneration++
	return true
}

func (r *CredentialRegistry) SetBlacklisted(credentialID uint) bool {
	exists, _ := r.SetBlacklistedWithChange(credentialID)
	return exists
}

func (r *CredentialRegistry) IncrFailure(credentialID uint) (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entryLocked(credentialID)
	if !ok {
		return 0, false
	}
	entry.FailureCount++
	entry.FailureGeneration++
	return entry.FailureCount, true
}

func (r *CredentialRegistry) ClearFailure(credentialID uint) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entryLocked(credentialID)
	if !ok {
		return false
	}
	if entry.FailureCount != 0 {
		entry.FailureCount = 0
		entry.FailureGeneration++
	}
	return true
}

func (r *CredentialRegistry) Recover(credentialID uint) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entryLocked(credentialID)
	if !ok {
		return false
	}
	if entry.Blacklisted || entry.FailureCount != 0 {
		entry.Blacklisted = false
		entry.FailureCount = 0
		entry.FailureGeneration++
	}
	return true
}

func (r *CredentialRegistry) RecoverIfMatch(ref CredentialRef, weight int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if weight < 1 || weight > MaxWeight {
		return false
	}
	groupID, ok := r.credentialGroups[ref.ID]
	if !ok || groupID != ref.GroupID {
		return false
	}
	entry, ok := r.buckets[groupID][ref.ID]
	if !ok || entry.Status != CredentialStatusActive || !entry.Blacklisted ||
		entry.GroupID != ref.GroupID || entry.Version != ref.Version ||
		entry.IdentityGeneration != ref.IdentityGeneration ||
		entry.Fingerprint != ref.Fingerprint || entry.EncryptedValue != ref.EncryptedValue ||
		entry.EncryptedProxy != ref.EncryptedProxy || entry.ProxyFingerprint != ref.ProxyFingerprint ||
		entry.FailureGeneration != ref.FailureGeneration {
		return false
	}
	entry.WeightAuto = weight
	entry.Blacklisted = false
	entry.FailureCount = 0
	entry.FailureGeneration++
	return true
}

func (r *CredentialRegistry) BlacklistedCredentials() []CredentialRef {
	r.mu.RLock()
	refs := make([]CredentialRef, 0)
	for _, bucket := range r.buckets {
		for _, entry := range bucket {
			if entry.Status != CredentialStatusActive || !entry.Blacklisted {
				continue
			}
			refs = append(refs, CredentialRef{
				ID: entry.ID, GroupID: entry.GroupID,
				Version: entry.Version, IdentityGeneration: entry.IdentityGeneration,
				Fingerprint: entry.Fingerprint, EncryptedValue: entry.EncryptedValue,
				EncryptedProxy: entry.EncryptedProxy, ProxyFingerprint: entry.ProxyFingerprint,
				FailureGeneration: entry.FailureGeneration,
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

func (r *CredentialRegistry) entryLocked(credentialID uint) (*CredentialEntry, bool) {
	groupID, ok := r.credentialGroups[credentialID]
	if !ok {
		return nil, false
	}
	entry, ok := r.buckets[groupID][credentialID]
	return entry, ok
}

func cloneCredentialEntry(entry CredentialEntry) CredentialEntry {
	entry.WeightManual = cloneWeight(entry.WeightManual)
	entry.quotaRemaining = cloneFloat(entry.quotaRemaining)
	entry.FailureGeneration = 0
	if entry.WeightAuto == 0 {
		entry.WeightAuto = DefaultWeight
	}
	return entry
}

func (state CredentialAuthState) normalize() CredentialAuthState {
	if state == "" {
		return CredentialAuthStateReady
	}
	return state
}

func (state CredentialAuthState) valid() bool {
	switch state.normalize() {
	case CredentialAuthStateReady, CredentialAuthStateRefreshing,
		CredentialAuthStateReauthorizationRequired, CredentialAuthStateOutcomeUnknown:
		return true
	default:
		return false
	}
}

func detachCredentialEntryExact(entry CredentialEntry) CredentialEntry {
	entry.WeightManual = cloneWeight(entry.WeightManual)
	entry.quotaRemaining = cloneFloat(entry.quotaRemaining)
	return entry
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
