package subscription

import (
	"sort"
	"sync"

	providerobservation "gpt-load/internal/subscription/providers/observation"
)

// PassiveQuotaObservation is one credential's not-yet-persisted passive quota
// snapshot, captured from a single upstream response's headers.
type PassiveQuotaObservation struct {
	CredentialID       uint
	IdentityGeneration uint64
	ObservedAtMS       int64
	Windows            []providerobservation.QuotaWindow
	Version            uint64
}

type passiveQuotaEntry struct {
	identityGeneration uint64
	observedAtMS       int64
	windows            []providerobservation.QuotaWindow
	version            uint64
	dirty              bool
}

// passiveQuotaPending is the process-local, credential-keyed dirty set for
// passive quota observations. It holds at most one entry per credential and
// never grows with request volume, only with the number of accounts that
// have produced a valid quota signal.
type passiveQuotaPending struct {
	mu            sync.Mutex
	entries       map[uint]*passiveQuotaEntry
	nextVersion   uint64
	dirtyNotifier func()
}

func newPassiveQuotaPending() *passiveQuotaPending {
	return &passiveQuotaPending{entries: make(map[uint]*passiveQuotaEntry)}
}

// RecordPassiveQuotaObservation stores one response's passive quota windows
// as the pending snapshot for credentialID, replacing whatever the previous
// response left there.
//
// Responses are deliberately never combined. A pending entry carries a single
// observation time, so mixing fields captured at different instants would let
// an older value be persisted under a newer timestamp and outrank an active
// refresh committed in between. Dropping the older response loses nothing:
// the stored snapshot keeps its current value for any window this response
// omits, and the next response carrying that window refreshes it.
//
// A response with no windows is a no-op: it must not advance the pending
// observation time. An observedAtMS older than the pending entry is dropped.
func (manager *CredentialManager) RecordPassiveQuotaObservation(
	credentialID uint,
	identityGeneration uint64,
	observedAtMS int64,
	windows []providerobservation.QuotaWindow,
) {
	if manager == nil || manager.passiveQuota == nil || credentialID == 0 || len(windows) == 0 {
		return
	}
	manager.passiveQuota.record(credentialID, identityGeneration, observedAtMS, windows)
}

// DirtyPassiveQuotaObservations returns up to limit pending observations that
// have not yet been acknowledged, ordered by credential ID for determinism.
func (manager *CredentialManager) DirtyPassiveQuotaObservations(limit int) []PassiveQuotaObservation {
	if manager == nil || manager.passiveQuota == nil {
		return nil
	}
	return manager.passiveQuota.dirtyObservations(limit)
}

// AckPassiveQuotaObservation clears the dirty flag for credentialID only when
// version exactly matches the currently pending version, so a write that
// landed after this version was read is never dropped by a stale ACK.
func (manager *CredentialManager) AckPassiveQuotaObservation(credentialID uint, version uint64) {
	if manager == nil || manager.passiveQuota == nil {
		return
	}
	manager.passiveQuota.ack(credentialID, version)
}

// SetPassiveQuotaDirtyNotifier installs the process-owned non-blocking wake-up
// invoked after a record introduces a new dirty version.
func (manager *CredentialManager) SetPassiveQuotaDirtyNotifier(notifier func()) {
	if manager == nil || manager.passiveQuota == nil {
		return
	}
	manager.passiveQuota.mu.Lock()
	manager.passiveQuota.dirtyNotifier = notifier
	manager.passiveQuota.mu.Unlock()
}

func (pending *passiveQuotaPending) record(
	credentialID uint,
	identityGeneration uint64,
	observedAtMS int64,
	windows []providerobservation.QuotaWindow,
) {
	pending.mu.Lock()
	entry, exists := pending.entries[credentialID]
	if !exists || entry.identityGeneration != identityGeneration {
		entry = &passiveQuotaEntry{identityGeneration: identityGeneration}
		pending.entries[credentialID] = entry
	} else if observedAtMS < entry.observedAtMS {
		pending.mu.Unlock()
		return
	}
	entry.windows = append([]providerobservation.QuotaWindow(nil), windows...)
	entry.observedAtMS = observedAtMS
	pending.nextVersion++
	entry.version = pending.nextVersion
	entry.dirty = true
	notifier := pending.dirtyNotifier
	pending.mu.Unlock()
	if notifier != nil {
		notifier()
	}
}

func (pending *passiveQuotaPending) dirtyObservations(limit int) []PassiveQuotaObservation {
	if pending == nil || limit <= 0 {
		return nil
	}
	pending.mu.Lock()
	defer pending.mu.Unlock()
	ids := make([]uint, 0, len(pending.entries))
	for credentialID := range pending.entries {
		ids = append(ids, credentialID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	result := make([]PassiveQuotaObservation, 0, limit)
	for _, credentialID := range ids {
		entry := pending.entries[credentialID]
		if entry == nil || !entry.dirty {
			continue
		}
		result = append(result, PassiveQuotaObservation{
			CredentialID:       credentialID,
			IdentityGeneration: entry.identityGeneration,
			ObservedAtMS:       entry.observedAtMS,
			Windows:            append([]providerobservation.QuotaWindow(nil), entry.windows...),
			Version:            entry.version,
		})
		if len(result) >= limit {
			break
		}
	}
	return result
}

// ack clears the dirty flag for an exactly matching version.
func (pending *passiveQuotaPending) ack(credentialID uint, version uint64) {
	pending.mu.Lock()
	defer pending.mu.Unlock()
	entry, ok := pending.entries[credentialID]
	if !ok || entry.version != version {
		return
	}
	entry.dirty = false
}
