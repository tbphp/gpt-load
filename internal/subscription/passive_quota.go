package subscription

import (
	"sort"
	"sync"
	"time"

	providerobservation "gpt-load/internal/subscription/providers/observation"
)

// PassiveQuotaSample 保留一份额度信号及其原始观测时间。
type PassiveQuotaSample struct {
	ObservedAtMS int64
	Windows      []providerobservation.QuotaWindow
	Credits      *providerobservation.CreditSummary
}

// PassiveQuotaObservation 保存一个凭据的最新待写样本。
// WS 可附带同一连接的握手样本，时间独立保留，不累积事件历史。
type PassiveQuotaObservation struct {
	CredentialID       uint
	IdentityGeneration uint64
	ObservedAtMS       int64
	Windows            []providerobservation.QuotaWindow
	Credits            *providerobservation.CreditSummary
	Preceding          *PassiveQuotaSample
	Version            uint64
}

type passiveQuotaEntry struct {
	identityGeneration uint64
	observedAtMS       int64
	windows            []providerobservation.QuotaWindow
	credits            *providerobservation.CreditSummary
	preceding          *PassiveQuotaSample
	version            uint64
	dirty              bool
}

// passiveQuotaPending 每个凭据仅保留一份实时快照；历史待解析观测与待写点分别有界。
type passiveQuotaPending struct {
	mu                      sync.Mutex
	entries                 map[uint]*passiveQuotaEntry
	history                 map[quotaHistorySampleKey]quotaHistorySample
	historyStates           map[quotaHistoryKey]quotaHistoryState
	historySources          map[quotaHistoryCredential]quotaHistorySource
	historyObservations     map[quotaHistoryCredential][]quotaHistoryObservation
	historyRetryAt          map[quotaHistoryCredential]time.Time
	historyObservationCount int
	nextVersion             uint64
	dirtyNotifier           func()
}

func newPassiveQuotaPending() *passiveQuotaPending {
	return &passiveQuotaPending{
		entries:             make(map[uint]*passiveQuotaEntry),
		history:             make(map[quotaHistorySampleKey]quotaHistorySample),
		historyStates:       make(map[quotaHistoryKey]quotaHistoryState),
		historySources:      make(map[quotaHistoryCredential]quotaHistorySource),
		historyObservations: make(map[quotaHistoryCredential][]quotaHistoryObservation),
		historyRetryAt:      make(map[quotaHistoryCredential]time.Time),
	}
}

// RecordPassiveQuotaObservation stores one response's passive quota windows
// as the pending snapshot for credentialID, replacing whatever the previous
// response left there.
//
// 窗口仍由最新响应替换。点数有自己的观测时间，未携带点数的响应保留
// 待写余额及其原始时间，落盘时独立校验，不能借窗口的新时间覆盖主动刷新。
//
// A response with neither windows nor credits is a no-op: it must not advance the pending
// observation time. Older windows are dropped; credits use their own observation time.
// Removed credentials and responses from an outdated target are also ignored.
func (manager *CredentialManager) RecordPassiveQuotaObservation(
	credentialID uint,
	identityGeneration uint64,
	observedAtMS int64,
	windows []providerobservation.QuotaWindow,
	credits ...*providerobservation.CreditSummary,
) {
	var summary *providerobservation.CreditSummary
	if len(credits) > 0 {
		summary = credits[0]
	}
	manager.recordPassiveQuotaObservation(credentialID, identityGeneration, observedAtMS, windows, nil, summary)
}

// RecordPassiveQuotaPair 让 WS 的最新事件携带同一连接的握手证据。
// 两份样本分别校验时间；每个凭据仍仅保留一组，不合并其他请求或事件。
func (manager *CredentialManager) RecordPassiveQuotaPair(
	credentialID uint,
	identityGeneration uint64,
	preceding, latest PassiveQuotaSample,
) {
	var earlier *PassiveQuotaSample
	if (len(preceding.Windows) > 0 || preceding.Credits != nil) && preceding.ObservedAtMS <= latest.ObservedAtMS {
		earlier = &preceding
	}
	manager.recordPassiveQuotaObservation(credentialID, identityGeneration, latest.ObservedAtMS, latest.Windows, earlier, latest.Credits)
}

func (manager *CredentialManager) recordPassiveQuotaObservation(
	credentialID uint,
	identityGeneration uint64,
	observedAtMS int64,
	windows []providerobservation.QuotaWindow,
	preceding *PassiveQuotaSample,
	credits *providerobservation.CreditSummary,
) {
	if manager == nil || manager.passiveQuota == nil || manager.registry == nil || credentialID == 0 || (len(windows) == 0 && credits == nil) {
		return
	}
	manager.passiveQuota.record(credentialID, identityGeneration, observedAtMS, windows, preceding, func() (uint, bool) {
		// 在短内存锁内核对当前身份；不等待后台持有的数据库 mutation 锁。
		ref, ok := manager.registry.CredentialRef(credentialID)
		if !ok || ref.IdentityGeneration != identityGeneration {
			return 0, false
		}
		return ref.GroupID, true
	}, credits)
}

// DirtyPassiveQuotaObservations returns up to limit pending observations that
// have not yet been acknowledged, ordered by credential ID for determinism.
func (manager *CredentialManager) DirtyPassiveQuotaObservations(limit int) []PassiveQuotaObservation {
	if manager == nil || manager.passiveQuota == nil {
		return nil
	}
	return manager.passiveQuota.dirtyObservations(limit)
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
	preceding *PassiveQuotaSample,
	current func() (uint, bool),
	credits *providerobservation.CreditSummary,
) {
	pending.mu.Lock()
	groupID, accepted := current()
	if !accepted {
		pending.mu.Unlock()
		return
	}
	entry, exists := pending.entries[credentialID]
	replaceWindows := true
	if !exists || entry.identityGeneration != identityGeneration {
		entry = &passiveQuotaEntry{identityGeneration: identityGeneration}
		pending.entries[credentialID] = entry
	} else {
		replaceWindows = observedAtMS >= entry.observedAtMS
	}
	creditAtMS := observedAtMS
	if credits == nil && preceding != nil {
		credits = preceding.Credits
		creditAtMS = preceding.ObservedAtMS
	}
	creditAccepted := false
	if credits != nil {
		summary := cloneCreditSummary(credits)
		if summary.ObservedAtMS == nil {
			summary.ObservedAtMS = cloneInt64(&creditAtMS)
		}
		if entry.credits == nil || entry.credits.ObservedAtMS == nil || *summary.ObservedAtMS >= *entry.credits.ObservedAtMS {
			entry.credits = summary
			creditAccepted = true
		}
	}
	if !replaceWindows && !creditAccepted {
		pending.mu.Unlock()
		return
	}
	if replaceWindows {
		entry.windows = cloneQuotaWindows(windows)
		entry.preceding = clonePassiveQuotaSample(preceding)
		entry.observedAtMS = observedAtMS
	}
	pending.nextVersion++
	entry.version = pending.nextVersion
	entry.dirty = true
	if replaceWindows {
		if preceding != nil {
			pending.recordHistoryLocked(groupID, credentialID, identityGeneration, preceding.ObservedAtMS, preceding.Windows)
		}
		pending.recordHistoryLocked(groupID, credentialID, identityGeneration, observedAtMS, windows)
	}
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
			Windows:            cloneQuotaWindows(entry.windows),
			Credits:            cloneCreditSummary(entry.credits),
			Preceding:          clonePassiveQuotaSample(entry.preceding),
			Version:            entry.version,
		})
		if len(result) >= limit {
			break
		}
	}
	return result
}

func clonePassiveQuotaSample(sample *PassiveQuotaSample) *PassiveQuotaSample {
	if sample == nil {
		return nil
	}
	return &PassiveQuotaSample{ObservedAtMS: sample.ObservedAtMS, Windows: cloneQuotaWindows(sample.Windows), Credits: cloneCreditSummary(sample.Credits)}
}

func cloneCreditSummary(credits *providerobservation.CreditSummary) *providerobservation.CreditSummary {
	if credits == nil {
		return nil
	}
	cloned := *credits
	cloned.ObservedAtMS = cloneInt64(credits.ObservedAtMS)
	if credits.HasCredits != nil {
		value := *credits.HasCredits
		cloned.HasCredits = &value
	}
	if credits.Unlimited != nil {
		value := *credits.Unlimited
		cloned.Unlimited = &value
	}
	return &cloned
}

// cloneQuotaWindows detaches a window slice from its caller, including the
// values behind its optional numeric fields. Sharing those pointers would let
// a later mutation change what is about to be persisted, under an observation
// time captured before that change.
func cloneQuotaWindows(windows []providerobservation.QuotaWindow) []providerobservation.QuotaWindow {
	if len(windows) == 0 {
		return nil
	}
	cloned := make([]providerobservation.QuotaWindow, 0, len(windows))
	for _, window := range windows {
		window.Used = cloneFloat(window.Used)
		window.Limit = cloneFloat(window.Limit)
		window.Remaining = cloneFloat(window.Remaining)
		window.Utilization = cloneFloat(window.Utilization)
		window.ResetAtMS = cloneInt64(window.ResetAtMS)
		window.WindowSeconds = cloneInt64(window.WindowSeconds)
		window.ModelIDs = append([]string(nil), window.ModelIDs...)
		cloned = append(cloned, window)
	}
	return cloned
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// ack drops the pending entry for an exactly matching version. Evicting
// rather than just clearing a flag is what keeps the map bounded: a
// credential that is deleted after producing quota headers would otherwise
// retain its cloned windows for the life of the process, so an instance with
// credential churn would grow without bound. A version mismatch means a newer
// response arrived while this one was being written, so that entry stays.
func (pending *passiveQuotaPending) ack(credentialID uint, version uint64) {
	pending.mu.Lock()
	defer pending.mu.Unlock()
	entry, ok := pending.entries[credentialID]
	if !ok || entry.version != version {
		return
	}
	delete(pending.entries, credentialID)
}
