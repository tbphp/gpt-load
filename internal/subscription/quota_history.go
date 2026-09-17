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

const quotaHistoryIntervalMS int64 = 60 * 60_000
const quotaHistoryReboundBasisPoints int64 = 100
const quotaHistoryCapacity = 4096

// QuotaHistoryMinimumWindowSeconds 仅为日级及更长周期保留额度历史。
const QuotaHistoryMinimumWindowSeconds int64 = 24 * 60 * 60

type quotaHistoryKey struct {
	credentialID uint
	identity     uint64
	window       string
}

type quotaHistorySample struct {
	key      quotaHistoryKey
	version  uint64
	row      models.CredentialQuotaHistory
	window   providerobservation.QuotaWindow
	critical bool
}

type quotaHistorySampleKey struct {
	window quotaHistoryKey
	atMS   int64
}

type quotaHistoryState struct {
	latest       quotaHistorySample
	admittedAtMS int64
}

type quotaHistorySource struct {
	window       string
	observedAtMS int64
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

// recordHistoryLocked 普通历史每小时采样；明显回升保留前后真实点。
// 最新观测与待写历史分别有界，跳过普通历史不影响实时额度更新。
func (pending *passiveQuotaPending) recordHistoryLocked(groupID, credentialID uint, identity uint64, observedAtMS int64, windows []providerobservation.QuotaWindow) {
	if observedAtMS < 0 {
		return
	}
	keys := make([]quotaHistoryKey, len(windows))
	named := make(map[quotaHistoryKey]bool)
	counts := make(map[quotaHistoryKey]int)
	for index, window := range windows {
		keys[index] = pending.historySourceKeyLocked(quotaHistoryKey{credentialID: credentialID, identity: identity, window: quotaHistoryWindowKey(window)})
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
		sample := quotaHistorySample{key: key, version: pending.nextVersion, window: copied, row: models.CredentialQuotaHistory{
			GroupID: groupID, CredentialID: credentialID, TargetIdentity: QuotaHistoryTargetIdentity(identity),
			WindowKey: key.window, WindowID: window.ID, SourceID: window.SourceID,
			Label: window.Label, LabelKey: window.LabelKey, Scope: window.Scope,
			ObservedAtMS: observedAtMS, UsedBasisPoints: int64(math.Round(utilization * 10_000)),
			ResetAtMS: copied.ResetAtMS, WindowSeconds: copied.WindowSeconds,
		}}
		last, persisted := pending.historyTimes[key]
		if known && state.admittedAtMS > last {
			last = state.admittedAtMS
		}
		rebound := known && state.latest.row.UsedBasisPoints-sample.row.UsedBasisPoints >= quotaHistoryReboundBasisPoints
		admit := rebound || (!known && !persisted) || observedAtMS-last >= quotaHistoryIntervalMS
		if admit {
			queued := false
			if rebound {
				queued = pending.queueHistoryReboundLocked(state.latest, sample)
				if queued {
					sample.critical = true
				}
			} else if len(pending.history) < quotaHistoryCapacity {
				pending.history[quotaHistorySampleKey{window: key, atMS: observedAtMS}] = sample
				queued = true
			}
			if queued {
				last = observedAtMS
			}
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
		pending.historyStates[key] = quotaHistoryState{latest: sample, admittedAtMS: last}
	}
}

// 回升前后点一起入队，容量不足时不保留不完整的边界。
func (pending *passiveQuotaPending) queueHistoryReboundLocked(preceding, sample quotaHistorySample) bool {
	samples := [2]quotaHistorySample{preceding, sample}
	needed := 0
	for _, point := range samples {
		key := quotaHistorySampleKey{window: point.key, atMS: point.row.ObservedAtMS}
		if _, queued := pending.history[key]; !queued {
			needed++
		}
	}
	if len(pending.history)+needed > quotaHistoryCapacity {
		return false
	}
	for _, point := range samples {
		point.critical = true
		pending.history[quotaHistorySampleKey{window: point.key, atMS: point.row.ObservedAtMS}] = point
	}
	return true
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
	if current, ok := pending.history[key]; ok && current.version == sample.version && current.critical == sample.critical {
		delete(pending.history, key)
	}
}

func (pending *passiveQuotaPending) hasPendingHistory() bool {
	pending.mu.Lock()
	defer pending.mu.Unlock()
	return len(pending.history) > 0
}

func (pending *passiveQuotaPending) rememberHistoryTime(key quotaHistoryKey, at int64) {
	pending.mu.Lock()
	defer pending.mu.Unlock()
	key = pending.historySourceKeyLocked(key)
	pending.rememberHistoryTimeLocked(key, at)
}

func (pending *passiveQuotaPending) rememberHistoryTimeLocked(key quotaHistoryKey, at int64) {
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

func (pending *passiveQuotaPending) historySourceKeyLocked(key quotaHistoryKey) quotaHistoryKey {
	if source, ok := pending.historySources[key]; ok {
		key.window = source.window
	}
	return key
}

// 来源名称仅从主动观测的唯一匹配解析；后台缓存后，HTTP/WS 共用采样状态。
func (pending *passiveQuotaPending) rememberHistorySources(credentialID uint, identity uint64, at int64, windows []providerobservation.QuotaWindow) bool {
	pending.mu.Lock()
	defer pending.mu.Unlock()
	reordered := false
	for _, window := range windows {
		if window.SourceID == "" || window.Scope == "" || window.Scope == "account" || window.WindowSeconds == nil || *window.WindowSeconds < QuotaHistoryMinimumWindowSeconds {
			continue
		}
		patch := providerobservation.QuotaWindow{SourceName: window.Scope, WindowSeconds: window.WindowSeconds}
		raw := quotaHistoryKey{credentialID: credentialID, identity: identity, window: quotaHistoryWindowKey(patch)}
		if passiveQuotaSourceByName(windows, patch) == "" {
			delete(pending.historySources, raw)
			continue
		}
		canonical := quotaHistoryKey{credentialID: credentialID, identity: identity, window: quotaHistoryWindowKey(window)}
		if raw == canonical {
			continue
		}
		if _, exists := pending.historySources[raw]; !exists && len(pending.historySources) >= quotaHistoryCapacity {
			var oldest quotaHistoryKey
			oldestAt := int64(math.MaxInt64)
			for candidate, source := range pending.historySources {
				if source.observedAtMS < oldestAt {
					oldest, oldestAt = candidate, source.observedAtMS
				}
			}
			delete(pending.historySources, oldest)
		}
		pending.historySources[raw] = quotaHistorySource{window: canonical.window, observedAtMS: at}
		if pending.mergeHistorySourceLocked(raw, canonical) {
			reordered = true
		}
		if last, exists := pending.historyTimes[raw]; exists {
			delete(pending.historyTimes, raw)
			pending.rememberHistoryTimeLocked(canonical, last)
		}
	}
	return reordered
}

// 来源首次解析或缓存淘汰后，按真实时间合并两种传输的待写点和最新观测。
// 同事件副本仍以具名值为准；重新判断回升，避免被丢弃的副本制造关键点。
func (pending *passiveQuotaPending) mergeHistorySourceLocked(raw, canonical quotaHistoryKey) bool {
	state, exists := pending.historyStates[raw]
	if !exists {
		return false
	}
	previous, known := pending.historyStates[canonical]
	type candidate struct {
		sample quotaHistorySample
		queued bool
	}
	byTime := make(map[int64]candidate)
	add := func(sample quotaHistorySample, queued bool) {
		at := sample.row.ObservedAtMS
		current, exists := byTime[at]
		if !exists || sample.version < current.sample.version ||
			(sample.version == current.sample.version && sample.window.SourceName != "" && current.sample.window.SourceName == "") {
			sample.critical = false
			current.sample = sample
		}
		current.queued = current.queued || queued
		byTime[at] = current
	}
	add(state.latest, false)
	if known {
		add(previous.latest, false)
	}
	old := make(map[quotaHistorySampleKey]quotaHistorySample)
	for key, sample := range pending.history {
		if key.window == raw || key.window == canonical {
			old[key] = sample
			add(sample, true)
			delete(pending.history, key)
		}
	}
	points := make([]candidate, 0, len(byTime))
	for _, point := range byTime {
		points = append(points, point)
		if point.queued {
			pending.history[quotaHistorySampleKey{window: point.sample.key, atMS: point.sample.row.ObservedAtMS}] = point.sample
		}
	}
	sort.Slice(points, func(i, j int) bool { return points[i].sample.row.ObservedAtMS < points[j].sample.row.ObservedAtMS })
	admitted := max(state.admittedAtMS, previous.admittedAtMS)
	for index := 1; index < len(points); index++ {
		before, after := points[index-1].sample, points[index].sample
		if before.row.UsedBasisPoints-after.row.UsedBasisPoints >= quotaHistoryReboundBasisPoints && pending.queueHistoryReboundLocked(before, after) {
			admitted = max(admitted, after.row.ObservedAtMS)
		}
	}
	latest := points[len(points)-1].sample
	latest.key, latest.row.WindowKey = canonical, canonical.window
	delete(pending.historyStates, raw)
	pending.historyStates[canonical] = quotaHistoryState{latest: latest, admittedAtMS: admitted}
	count := 0
	for key, sample := range pending.history {
		if key.window != raw && key.window != canonical {
			continue
		}
		count++
		original, exists := old[key]
		if !exists || original.version != sample.version || original.critical != sample.critical || original.row.UsedBasisPoints != sample.row.UsedBasisPoints {
			return true
		}
	}
	return count != len(old)
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
		if err := manager.db.WithContext(ctx).Select("snapshot_json").Take(&observation, "credential_id = ?", sample.row.CredentialID).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return true, err
			}
		}
		var snapshot providerobservation.Snapshot
		if json.Unmarshal(observation.SnapshotJSON, &snapshot) == nil {
			if manager.passiveQuota.rememberHistorySources(sample.row.CredentialID, sample.key.identity, sample.row.ObservedAtMS, snapshot.QuotaWindows) {
				// 新补入的回升前点必须先写；重新按真实观测时间取批次。
				return true, nil
			}
			if index := matchPassiveQuotaWindow(snapshot.QuotaWindows, sample.window); index >= 0 {
				window := snapshot.QuotaWindows[index]
				// 解析来源以统一 HTTP/WS 历史窗口，周期仍取该次真实观测。
				canonical := sample.window
				canonical.ID, canonical.SourceID = window.ID, window.SourceID
				sample.row.WindowKey = quotaHistoryWindowKey(canonical)
				sample.row.WindowID, sample.row.SourceID = window.ID, window.SourceID
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
			(!sample.critical && sample.row.ObservedAtMS-latest.ObservedAtMS < quotaHistoryIntervalMS && latest.UsedBasisPoints-sample.row.UsedBasisPoints < quotaHistoryReboundBasisPoints)) {
			manager.passiveQuota.rememberHistoryTime(sample.key, latest.ObservedAtMS)
			manager.passiveQuota.mu.Lock()
			key := manager.passiveQuota.historySourceKeyLocked(sample.key)
			if state, ok := manager.passiveQuota.historyStates[key]; ok && state.admittedAtMS == sample.row.ObservedAtMS {
				state.admittedAtMS = latest.ObservedAtMS
				manager.passiveQuota.historyStates[key] = state
			}
			manager.passiveQuota.mu.Unlock()
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
	return manager.passiveQuota.hasPendingHistory(), nil
}
