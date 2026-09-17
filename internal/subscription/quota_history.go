package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gpt-load/internal/storage/models"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

const quotaHistoryIntervalMS int64 = 60_000
const quotaHistoryCapacity = 4096

type quotaHistoryKey struct {
	credentialID uint
	identity     uint64
	window       string
}

type quotaHistorySample struct {
	key     quotaHistoryKey
	version uint64
	row     models.CredentialQuotaHistory
	window  providerobservation.QuotaWindow
}

// QuotaHistoryTargetIdentity 与 Token 版本独立，切换账号或上游目标时隔离历史。
func QuotaHistoryTargetIdentity(generation uint64) string {
	return fmt.Sprintf("%016x", generation)
}

func quotaHistoryWindowKey(window providerobservation.QuotaWindow) string {
	seconds := int64(0)
	if window.WindowSeconds != nil {
		seconds = *window.WindowSeconds
	}
	value := fmt.Sprintf("%s\x00%s\x00%d", window.SourceID, window.ID, seconds)
	if window.SourceID != "" && seconds > 0 {
		value = fmt.Sprintf("%s\x00%d", window.SourceID, seconds)
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// recordHistoryLocked 只保留各窗口最新真实样本，内存上限不随请求数增长。
func (pending *passiveQuotaPending) recordHistoryLocked(groupID, credentialID uint, identity uint64, observedAtMS int64, windows []providerobservation.QuotaWindow) {
	if observedAtMS < 0 {
		return
	}
	for _, window := range windows {
		if window.ID == "" || len(window.ID) > 128 || len(window.SourceID) > 128 {
			continue
		}
		var utilization float64
		switch {
		case window.Utilization != nil:
			utilization = *window.Utilization
		case window.Unit == "percent" && window.Remaining != nil:
			utilization = 1 - *window.Remaining/100
		case window.Limit != nil && *window.Limit > 0 && window.Used != nil:
			utilization = *window.Used / *window.Limit
		default:
			continue
		}
		if math.IsNaN(utilization) || math.IsInf(utilization, 0) || utilization < 0 || utilization > 1 ||
			(window.ResetAtMS != nil && *window.ResetAtMS < 0) ||
			(window.WindowSeconds != nil && *window.WindowSeconds <= 0) {
			continue
		}
		key := quotaHistoryKey{credentialID: credentialID, identity: identity, window: quotaHistoryWindowKey(window)}
		if last, known := pending.historyTimes[key]; known && observedAtMS-last < quotaHistoryIntervalMS {
			continue
		}
		previous, exists := pending.history[key]
		if (!exists && len(pending.history) >= quotaHistoryCapacity) || (exists && previous.row.ObservedAtMS > observedAtMS) {
			continue
		}
		copied := cloneQuotaWindows([]providerobservation.QuotaWindow{window})[0]
		pending.history[key] = quotaHistorySample{key: key, version: pending.nextVersion, window: copied, row: models.CredentialQuotaHistory{
			GroupID: groupID, CredentialID: credentialID, TargetIdentity: QuotaHistoryTargetIdentity(identity),
			WindowKey: key.window, WindowID: window.ID, SourceID: window.SourceID,
			Label: window.Label, LabelKey: window.LabelKey, Scope: window.Scope,
			ObservedAtMS: observedAtMS, UsedBasisPoints: int64(math.Round(utilization * 10_000)),
			ResetAtMS: copied.ResetAtMS, WindowSeconds: copied.WindowSeconds,
		}}
	}
}

func (pending *passiveQuotaPending) historyBatch(limit int) []quotaHistorySample {
	pending.mu.Lock()
	defer pending.mu.Unlock()
	keys := make([]quotaHistoryKey, 0, len(pending.history))
	for key := range pending.history {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := pending.history[keys[i]], pending.history[keys[j]]
		if left.row.ObservedAtMS != right.row.ObservedAtMS {
			return left.row.ObservedAtMS < right.row.ObservedAtMS
		}
		if keys[i].credentialID != keys[j].credentialID {
			return keys[i].credentialID < keys[j].credentialID
		}
		return keys[i].window < keys[j].window
	})
	result := make([]quotaHistorySample, 0, min(limit, len(keys)))
	for _, key := range keys[:min(limit, len(keys))] {
		result = append(result, pending.history[key])
	}
	return result
}

func (pending *passiveQuotaPending) ackHistory(sample quotaHistorySample) {
	pending.mu.Lock()
	defer pending.mu.Unlock()
	if current, ok := pending.history[sample.key]; ok && current.version == sample.version {
		delete(pending.history, sample.key)
	}
}

func (pending *passiveQuotaPending) rememberHistoryTime(key quotaHistoryKey, at int64) {
	pending.mu.Lock()
	defer pending.mu.Unlock()
	if _, exists := pending.historyTimes[key]; !exists && len(pending.historyTimes) >= quotaHistoryCapacity {
		var oldest quotaHistoryKey
		oldestAt := int64(math.MaxInt64)
		for candidate, observedAt := range pending.historyTimes {
			if observedAt < oldestAt {
				oldest, oldestAt = candidate, observedAt
			}
		}
		// 淘汰仅影响查询缓存；再次出现的窗口仍从数据库恢复采样间隔。
		delete(pending.historyTimes, oldest)
	}
	if old, ok := pending.historyTimes[key]; !ok || at > old {
		pending.historyTimes[key] = at
	}
}

// flushQuotaHistory 不持有入队内存锁或凭据 mutation 锁执行数据库操作。
// 历史使用独立 INSERT，不阻塞/回滚最新额度快照的保存。
func (manager *CredentialManager) flushQuotaHistory(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	for _, sample := range manager.passiveQuota.historyBatch(passiveQuotaFlushBatchSize) {
		ref, ok := manager.registry.CredentialRef(sample.row.CredentialID)
		if !ok || ref.IdentityGeneration != sample.key.identity || ref.GroupID != sample.row.GroupID {
			manager.passiveQuota.ackHistory(sample)
			continue
		}
		// 在后台补充展示元数据；只保存该次响应实际观测到的百分比。
		var observation models.CredentialObservation
		if err := manager.db.WithContext(ctx).Take(&observation, "credential_id = ?", sample.row.CredentialID).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return true, err
			}
		}
		var snapshot providerobservation.Snapshot
		if json.Unmarshal(observation.SnapshotJSON, &snapshot) == nil {
			if index := matchPassiveQuotaWindow(snapshot.QuotaWindows, sample.window); index >= 0 {
				window := snapshot.QuotaWindows[index]
				sample.row.WindowID, sample.row.SourceID = window.ID, window.SourceID
				sample.row.Label, sample.row.LabelKey, sample.row.Scope = window.Label, window.LabelKey, window.Scope
			}
		}
		if sample.row.Label == "" {
			sample.row.Label = sample.row.WindowID
		}
		if sample.row.Scope == "" {
			sample.row.Scope = "account"
			if sample.row.SourceID != "" && sample.row.SourceID != "codex" {
				sample.row.Scope = sample.row.SourceID
			}
		}
		if len(sample.row.Label) > 255 || len(sample.row.LabelKey) > 64 || len(sample.row.Scope) > 255 {
			manager.passiveQuota.ackHistory(sample)
			continue
		}
		var latest models.CredentialQuotaHistory
		err := manager.db.WithContext(ctx).Where("credential_id = ? AND target_identity = ? AND window_key = ?",
			sample.row.CredentialID, sample.row.TargetIdentity, sample.row.WindowKey).
			Order("observed_at_ms DESC").Take(&latest).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return true, err
		}
		if err == nil && sample.row.ObservedAtMS-latest.ObservedAtMS < quotaHistoryIntervalMS {
			manager.passiveQuota.rememberHistoryTime(sample.key, latest.ObservedAtMS)
			manager.passiveQuota.ackHistory(sample)
			continue
		}
		// 再次核对目标；旧目标的行即使随后落盘，也被 TargetIdentity 隔离。
		ref, ok = manager.registry.CredentialRef(sample.row.CredentialID)
		if !ok || ref.IdentityGeneration != sample.key.identity {
			manager.passiveQuota.ackHistory(sample)
			continue
		}
		if err := manager.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&sample.row).Error; err != nil {
			return true, fmt.Errorf("persist quota history: %w", err)
		}
		manager.passiveQuota.rememberHistoryTime(sample.key, sample.row.ObservedAtMS)
		manager.passiveQuota.ackHistory(sample)
	}
	return len(manager.passiveQuota.historyBatch(1)) > 0, nil
}
