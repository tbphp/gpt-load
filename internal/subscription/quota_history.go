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

const quotaHistoryCapacity = 4096

// QuotaHistoryMinimumWindowSeconds 仅为日级及更长周期保留额度历史。
const QuotaHistoryMinimumWindowSeconds int64 = 24 * 60 * 60

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

type quotaHistorySampleKey struct {
	window quotaHistoryKey
	atMS   int64
}

type quotaHistoryState struct {
	latest quotaHistorySample
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
	source := window.SourceID
	if source == "" {
		source = window.SourceName
	}
	value := fmt.Sprintf("%s\x00%s\x00%d", source, window.ID, seconds)
	if source != "" && seconds > 0 {
		value = fmt.Sprintf("%s\x00%d", source, seconds)
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// quotaHistoryDisplayedRemainingPercent 与前端额度标签的 Math.round 保持一致。
func quotaHistoryDisplayedRemainingPercent(usedBasisPoints int64) int64 {
	return (10_000 - usedBasisPoints + 50) / 100
}

// recordHistorySampleLocked 仅在前端显示的剩余额度整数变化时保留真实观测。
// 最新观测与待写历史分别有界，跳过同一显示值不影响实时额度更新。
func (pending *passiveQuotaPending) recordHistorySampleLocked(groupID, credentialID uint, identity uint64, observedAtMS int64, version uint64, windows []providerobservation.QuotaWindow) {
	if observedAtMS < 0 {
		return
	}
	keys := make([]quotaHistoryKey, len(windows))
	named := make(map[quotaHistoryKey]bool)
	counts := make(map[quotaHistoryKey]int)
	for index, window := range windows {
		keys[index] = quotaHistoryKey{credentialID: credentialID, identity: identity, window: quotaHistoryWindowKey(window)}
		if window.SourceName != "" {
			named[keys[index]] = true
		}
	}
	for index, window := range windows {
		if window.SourceName != "" || !named[keys[index]] {
			counts[keys[index]]++
		}
	}
	for index, window := range windows {
		key := keys[index]
		// 与实时快照一致：具名窗口优先，多个同级副本属于歧义。
		if counts[key] != 1 || (window.SourceName == "" && named[key]) {
			continue
		}
		if window.WindowSeconds == nil || *window.WindowSeconds < QuotaHistoryMinimumWindowSeconds {
			continue
		}
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
			(window.ResetAtMS != nil && *window.ResetAtMS < 0) {
			continue
		}
		state, known := pending.historyStates[key]
		if known && observedAtMS <= state.latest.row.ObservedAtMS {
			continue
		}
		copied := cloneQuotaWindows([]providerobservation.QuotaWindow{window})[0]
		sample := quotaHistorySample{key: key, version: version, window: copied, row: models.CredentialQuotaHistory{
			GroupID: groupID, CredentialID: credentialID, TargetIdentity: QuotaHistoryTargetIdentity(identity),
			WindowKey: key.window, WindowID: window.ID, SourceID: window.SourceID,
			Label: window.Label, LabelKey: window.LabelKey, Scope: window.Scope,
			ObservedAtMS: observedAtMS, UsedBasisPoints: int64(math.Round(utilization * 10_000)),
			ResetAtMS: copied.ResetAtMS, WindowSeconds: copied.WindowSeconds,
		}}
		if (!known || quotaHistoryDisplayedRemainingPercent(state.latest.row.UsedBasisPoints) != quotaHistoryDisplayedRemainingPercent(sample.row.UsedBasisPoints)) &&
			len(pending.history) < quotaHistoryCapacity {
			pending.history[quotaHistorySampleKey{window: key, atMS: observedAtMS}] = sample
		}
		if !known && len(pending.historyStates) >= quotaHistoryCapacity {
			var oldest quotaHistoryKey
			oldestAt := int64(math.MaxInt64)
			for candidate, cached := range pending.historyStates {
				if cached.latest.row.ObservedAtMS < oldestAt {
					oldest, oldestAt = candidate, cached.latest.row.ObservedAtMS
				}
			}
			delete(pending.historyStates, oldest)
		}
		pending.historyStates[key] = quotaHistoryState{latest: sample}
	}
}

func (pending *passiveQuotaPending) historyBatch(limit int) []quotaHistorySample {
	pending.mu.Lock()
	defer pending.mu.Unlock()
	keys := make([]quotaHistorySampleKey, 0, len(pending.history))
	for key := range pending.history {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := pending.history[keys[i]], pending.history[keys[j]]
		if left.row.ObservedAtMS != right.row.ObservedAtMS {
			return left.row.ObservedAtMS < right.row.ObservedAtMS
		}
		if keys[i].window.credentialID != keys[j].window.credentialID {
			return keys[i].window.credentialID < keys[j].window.credentialID
		}
		return keys[i].window.window < keys[j].window.window
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
	key := quotaHistorySampleKey{window: sample.key, atMS: sample.row.ObservedAtMS}
	if current, ok := pending.history[key]; ok && current.version == sample.version {
		delete(pending.history, key)
	}
}

func (pending *passiveQuotaPending) hasReadyHistory() bool {
	pending.mu.Lock()
	defer pending.mu.Unlock()
	if len(pending.history) > 0 {
		return true
	}
	// 未解析观测等待后续工作线程唤醒；冷却期间不触发立即自重试。
	now := time.Now()
	for key := range pending.historyObservations {
		if pending.historyObservationReadyLocked(key, now) {
			return true
		}
	}
	return false
}

// flushQuotaHistory 不持有入队内存锁或凭据 mutation 锁执行数据库操作。
// 历史使用独立 INSERT，不阻塞/回滚最新额度快照的保存。
func (manager *CredentialManager) flushQuotaHistory(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := manager.resolveQuotaHistoryObservations(ctx); err != nil {
		return true, err
	}
	for _, sample := range manager.passiveQuota.historyBatch(passiveQuotaFlushBatchSize) {
		ref, ok := manager.registry.CredentialRef(sample.row.CredentialID)
		if !ok || ref.IdentityGeneration != sample.key.identity || ref.GroupID != sample.row.GroupID {
			manager.passiveQuota.ackHistory(sample)
			continue
		}
		// 在后台补充展示元数据；只保存该次响应实际观测到的百分比。
		var observation models.CredentialObservation
		if err := manager.db.WithContext(ctx).Select("snapshot_json").Take(&observation, "credential_id = ?", sample.row.CredentialID).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return true, err
			}
		}
		var snapshot providerobservation.Snapshot
		if json.Unmarshal(observation.SnapshotJSON, &snapshot) == nil {
			manager.passiveQuota.rememberHistorySources(sample.row.CredentialID, sample.key.identity, sample.row.ObservedAtMS, snapshot.QuotaWindows)
			if index := matchPassiveQuotaWindow(snapshot.QuotaWindows, sample.window); index >= 0 {
				window := snapshot.QuotaWindows[index]
				// 来源身份已在采样前确定；这里只更新展示元数据。
				sample.row.WindowID = window.ID
				sample.row.Label, sample.row.LabelKey, sample.row.Scope = window.Label, window.LabelKey, window.Scope
			}
		}
		if sample.row.Label == "" {
			sample.row.Label = sample.row.WindowID
			if sample.window.SourceName != "" {
				sample.row.Label = sample.window.SourceName
			}
		}
		if sample.row.Scope == "" {
			sample.row.Scope = "account"
			if sample.window.SourceName != "" {
				sample.row.Scope = sample.window.SourceName
			} else if sample.row.SourceID != "" && sample.row.SourceID != "codex" {
				sample.row.Scope = sample.row.SourceID
			}
		}
		if len(sample.row.Label) > 255 || len(sample.row.LabelKey) > 64 || len(sample.row.Scope) > 255 {
			manager.passiveQuota.ackHistory(sample)
			continue
		}
		var latest models.CredentialQuotaHistory
		err := manager.db.WithContext(ctx).Select("observed_at_ms", "used_basis_points").Where("credential_id = ? AND target_identity = ? AND window_key = ?",
			sample.row.CredentialID, sample.row.TargetIdentity, sample.row.WindowKey).
			Order("observed_at_ms DESC").Take(&latest).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return true, err
		}
		if err == nil && (sample.row.ObservedAtMS <= latest.ObservedAtMS ||
			quotaHistoryDisplayedRemainingPercent(sample.row.UsedBasisPoints) == quotaHistoryDisplayedRemainingPercent(latest.UsedBasisPoints)) {
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
		manager.passiveQuota.ackHistory(sample)
	}
	return manager.passiveQuota.hasReadyHistory(), nil
}
