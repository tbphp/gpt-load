package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

const quotaHistorySourceRetryInterval = time.Minute

type quotaHistoryCredential struct {
	credentialID uint
	identity     uint64
}

type quotaHistorySource struct {
	windows      []providerobservation.QuotaWindow
	observedAtMS int64
}

type quotaHistoryObservation struct {
	groupID      uint
	observedAtMS int64
	version      uint64
	windows      []providerobservation.QuotaWindow
}

// 未知来源先保存完整观测。该账号的待解析观测清空之前，新观测也不能越过它们采样。
func (pending *passiveQuotaPending) recordHistoryLocked(groupID, credentialID uint, identity uint64, at int64, windows []providerobservation.QuotaWindow) {
	if at < 0 {
		return
	}
	eligible := make([]providerobservation.QuotaWindow, 0, len(windows))
	for _, window := range windows {
		if window.WindowSeconds != nil && *window.WindowSeconds >= QuotaHistoryMinimumWindowSeconds {
			eligible = append(eligible, window)
		}
	}
	if len(eligible) == 0 {
		return
	}
	key := quotaHistoryCredential{credentialID: credentialID, identity: identity}
	if source, known := pending.historySources[key]; known && len(pending.historyObservations[key]) == 0 {
		if normalized, complete := normalizeQuotaHistoryWindows(eligible, source.windows); complete {
			pending.recordHistorySampleLocked(groupID, credentialID, identity, at, pending.nextVersion, normalized)
			source.observedAtMS = max(source.observedAtMS, at)
			pending.historySources[key] = source
			return
		}
	}
	if pending.historyObservationCount >= quotaHistoryCapacity {
		return
	}
	pending.historyObservations[key] = append(pending.historyObservations[key], quotaHistoryObservation{
		groupID: groupID, observedAtMS: at, version: pending.nextVersion, windows: cloneQuotaWindows(eligible),
	})
	pending.historyObservationCount++
}

// 只补充身份和展示元数据，百分比、周期和重置时间始终保留该次响应的实际值。
func normalizeQuotaHistoryWindows(windows, sources []providerobservation.QuotaWindow) ([]providerobservation.QuotaWindow, bool) {
	normalized := append([]providerobservation.QuotaWindow(nil), windows...)
	complete := true
	for index, window := range normalized {
		position := matchPassiveQuotaWindow(sources, window)
		if position < 0 {
			if window.SourceID == "" && window.SourceName != "" {
				complete = false
			}
			continue
		}
		source := sources[position]
		window.ID, window.SourceID = source.ID, source.SourceID
		window.Label, window.LabelKey, window.Scope = source.Label, source.LabelKey, source.Scope
		normalized[index] = window
	}
	return normalized, complete
}

func (pending *passiveQuotaPending) rememberHistorySources(credentialID uint, identity uint64, at int64, windows []providerobservation.QuotaWindow) {
	pending.mu.Lock()
	defer pending.mu.Unlock()
	pending.rememberHistorySourcesLocked(quotaHistoryCredential{credentialID: credentialID, identity: identity}, at, windows)
}

func (pending *passiveQuotaPending) rememberHistorySourcesLocked(key quotaHistoryCredential, at int64, windows []providerobservation.QuotaWindow) {
	if _, known := pending.historySources[key]; !known && len(pending.historySources) >= quotaHistoryCapacity {
		var oldest quotaHistoryCredential
		oldestAt := int64(math.MaxInt64)
		for candidate, source := range pending.historySources {
			if source.observedAtMS < oldestAt {
				oldest, oldestAt = candidate, source.observedAtMS
			}
		}
		delete(pending.historySources, oldest)
	}
	pending.historySources[key] = quotaHistorySource{windows: cloneQuotaWindows(windows), observedAtMS: at}
}

func (pending *passiveQuotaPending) historyObservationCredentials(limit int) []quotaHistoryCredential {
	pending.mu.Lock()
	defer pending.mu.Unlock()
	keys := make([]quotaHistoryCredential, 0, len(pending.historyObservations))
	now := time.Now()
	for key := range pending.historyObservations {
		if pending.historyObservationReadyLocked(key, now) {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].credentialID != keys[j].credentialID {
			return keys[i].credentialID < keys[j].credentialID
		}
		return keys[i].identity < keys[j].identity
	})
	return keys[:min(limit, len(keys))]
}

func (pending *passiveQuotaPending) historyObservationReadyLocked(key quotaHistoryCredential, now time.Time) bool {
	return !now.Before(pending.historyRetryAt[key])
}

func (pending *passiveQuotaPending) discardHistoryObservationsLocked(key quotaHistoryCredential) {
	pending.historyObservationCount -= len(pending.historyObservations[key])
	delete(pending.historyObservations, key)
	delete(pending.historyRetryAt, key)
}

// 数据库解析在后台完成；同一账号积累的观测在一个内存锁内按时间处理完再开放直接采样。
func (manager *CredentialManager) resolveQuotaHistoryObservations(ctx context.Context) error {
	for _, key := range manager.passiveQuota.historyObservationCredentials(passiveQuotaFlushBatchSize) {
		ref, current := manager.registry.CredentialRef(key.credentialID)
		if !current || ref.IdentityGeneration != key.identity {
			manager.passiveQuota.mu.Lock()
			manager.passiveQuota.discardHistoryObservationsLocked(key)
			manager.passiveQuota.mu.Unlock()
			continue
		}
		var observation models.CredentialObservation
		if err := manager.db.WithContext(ctx).Select("snapshot_json").Take(&observation, "credential_id = ?", key.credentialID).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var snapshot providerobservation.Snapshot
		if json.Unmarshal(observation.SnapshotJSON, &snapshot) != nil {
			snapshot.QuotaWindows = nil
		}
		manager.passiveQuota.mu.Lock()
		ref, current = manager.registry.CredentialRef(key.credentialID)
		if !current || ref.IdentityGeneration != key.identity {
			manager.passiveQuota.discardHistoryObservationsLocked(key)
			manager.passiveQuota.mu.Unlock()
			continue
		}
		observations := manager.passiveQuota.historyObservations[key]
		sort.SliceStable(observations, func(i, j int) bool {
			if observations[i].observedAtMS != observations[j].observedAtMS {
				return observations[i].observedAtMS < observations[j].observedAtMS
			}
			return observations[i].version < observations[j].version
		})
		processed := len(observations)
		for index, observation := range observations {
			if observation.groupID != ref.GroupID {
				continue
			}
			normalized, complete := normalizeQuotaHistoryWindows(observation.windows, snapshot.QuotaWindows)
			if !complete {
				// 同一事件的副本必须一起解析；后续观测也不能越过这个时间点。
				processed = index
				break
			}
			manager.passiveQuota.recordHistorySampleLocked(observation.groupID, key.credentialID, key.identity, observation.observedAtMS, observation.version, normalized)
		}
		if len(observations) > 0 {
			manager.passiveQuota.rememberHistorySourcesLocked(key, observations[len(observations)-1].observedAtMS, snapshot.QuotaWindows)
		}
		manager.passiveQuota.discardHistoryObservationsLocked(key)
		if processed < len(observations) {
			clear(observations[:processed])
			retained := observations[processed:]
			manager.passiveQuota.historyObservations[key] = retained
			manager.passiveQuota.historyObservationCount += len(retained)
			manager.passiveQuota.historyRetryAt[key] = time.Now().Add(quotaHistorySourceRetryInterval)
		}
		manager.passiveQuota.mu.Unlock()
	}
	return nil
}
