package control

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gpt-load/internal/channel"
	"gpt-load/internal/health"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/platform/version"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/state"
)

type RequestLogStatsReader interface {
	Stats() requestlog.Stats
}

type healthCountsResponse struct {
	Credentials int `json:"credentials"`
	Available   int `json:"available"`
	Cooldown    int `json:"cooldown"`
	Blacklisted int `json:"blacklisted"`
}

type healthGroupResponse struct {
	ID      uint                 `json:"id"`
	Name    string               `json:"name"`
	Enabled bool                 `json:"enabled"`
	Counts  healthCountsResponse `json:"counts"`
}

type healthRecoveryResponse struct {
	Automatic bool   `json:"automatic"`
	Mode      string `json:"mode"`
	AtMS      *int64 `json:"at_ms"`
}

type healthProblemCredentialResponse struct {
	CredentialID            uint                   `json:"credential_id"`
	GroupID                 uint                   `json:"group_id"`
	GroupName               string                 `json:"group_name"`
	Mask                    string                 `json:"mask"`
	LastFailureCategory     string                 `json:"last_failure_category"`
	LastStatusCode          *int                   `json:"last_status_code"`
	CooldownUntilMS         *int64                 `json:"cooldown_until_ms"`
	FailureCount            int                    `json:"failure_count"`
	RecentSuccessCount      uint64                 `json:"recent_success_count"`
	RecentProblemCount      uint64                 `json:"recent_problem_count"`
	ConsecutiveProblemCount uint64                 `json:"consecutive_problem_count"`
	WeightManual            *int                   `json:"weight_manual"`
	WeightAuto              int                    `json:"weight_auto"`
	Recovery                healthRecoveryResponse `json:"recovery"`
}

// healthQuotaCredentialResponse 描述额度即将耗尽的订阅凭据。
//
// 刻意不含掩码：调用方（首页「需要处理」）按分组说话，
// 而生成掩码要对每条凭据逐条解密，为一行提示付这个代价不值得。
type healthQuotaCredentialResponse struct {
	CredentialID uint    `json:"credential_id"`
	GroupID      uint    `json:"group_id"`
	GroupName    string  `json:"group_name"`
	Remaining    float64 `json:"remaining"`
	ResetAtMS    int64   `json:"reset_at_ms"`
}

type requestLogHealthResponse struct {
	EnqueuedTotal               uint64 `json:"enqueued_total"`
	PersistedTotal              uint64 `json:"persisted_total"`
	DroppedNotRunningTotal      uint64 `json:"dropped_not_running_total"`
	DroppedQueueFullTotal       uint64 `json:"dropped_queue_full_total"`
	DroppedStoppingTotal        uint64 `json:"dropped_stopping_total"`
	DroppedPersistFailedTotal   uint64 `json:"dropped_persist_failed_total"`
	DroppedShutdownTotal        uint64 `json:"dropped_shutdown_total"`
	DroppedTotal                uint64 `json:"dropped_total"`
	WriteFailureTotal           uint64 `json:"write_failure_total"`
	RetentionDeleteFailureTotal uint64 `json:"retention_delete_failure_total"`
	QueueDepth                  int    `json:"queue_depth"`
	QueueCapacity               int    `json:"queue_capacity"`
	LastWriteFailureAtMS        *int64 `json:"last_write_failure_at_ms"`
	LastRetentionFailureAtMS    *int64 `json:"last_retention_failure_at_ms"`
}

type runtimeHealthResponse struct {
	ObservedAtMS           int64                              `json:"observed_at_ms"`
	Version                string                             `json:"version"`
	UptimeSeconds          int64                              `json:"uptime_seconds"`
	SnapshotRevision       uint64                             `json:"snapshot_revision"`
	StatsWindowSeconds     int64                              `json:"stats_window_seconds"`
	Counts                 healthCountsResponse               `json:"counts"`
	Groups                 []healthGroupResponse              `json:"groups"`
	CooldownCredentials    []healthProblemCredentialResponse  `json:"cooldown_credentials"`
	BlacklistedCredentials []healthProblemCredentialResponse  `json:"blacklisted_credentials"`
	LowQuotaCredentials    []healthQuotaCredentialResponse    `json:"low_quota_credentials"`
	BlockedAccessKeys      []healthAccessKeyCostLimitResponse `json:"blocked_access_keys"`
	RequestLog             requestLogHealthResponse           `json:"request_log"`
}

type healthAccessKeyCostLimitResponse struct {
	AccessKeyID       uint                           `json:"access_key_id"`
	Name              string                         `json:"name"`
	MaskedKey         string                         `json:"masked_key"`
	Recoverable       bool                           `json:"recoverable"`
	NextAvailableAtMS *int64                         `json:"next_available_at_ms"`
	BlockingRules     []AccessKeyCostLimitRuleStatus `json:"blocking_rules"`
}

// healthLowQuotaRemainingRatio 是「额度快用完」的唯一阈值来源。
// 与管理 UI 账号卡的 danger 档保持一致，避免同一句结论在两处算出不同答案。
const healthLowQuotaRemainingRatio = 0.3

type healthBucket string

const (
	healthBucketAvailable   healthBucket = "available"
	healthBucketCooldown    healthBucket = "cooldown"
	healthBucketBlacklisted healthBucket = "blacklisted"
	healthBucketDisabled    healthBucket = "disabled"
)

func classifyHealthKey(
	group state.GroupCatalogView,
	key state.CredentialRuntimeView,
	now time.Time,
) healthBucket {
	if !group.Enabled ||
		(group.WeightManual != nil && *group.WeightManual == 0) ||
		key.Status != state.CredentialStatusActive ||
		(key.AuthState != "" && key.AuthState != state.CredentialAuthStateReady) ||
		(key.WeightManual != nil && *key.WeightManual == 0) {
		return healthBucketDisabled
	}
	switch key.RuntimeState(now) {
	case state.CredentialRuntimeBlacklisted:
		return healthBucketBlacklisted
	case state.CredentialRuntimeCooldown:
		return healthBucketCooldown
	default:
		return healthBucketAvailable
	}
}

func addHealthCount(counts *healthCountsResponse, bucket healthBucket) {
	switch bucket {
	case healthBucketAvailable:
		counts.Credentials++
		counts.Available++
	case healthBucketCooldown:
		counts.Credentials++
		counts.Cooldown++
	case healthBucketBlacklisted:
		counts.Credentials++
		counts.Blacklisted++
	}
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func optionalHealthStatusCode(value int) *int {
	if value < 100 || value > 999 {
		return nil
	}
	cloned := value
	return &cloned
}

func (service *Service) healthProblemMask(
	ciphertexts map[uint]string,
	credentialID uint,
	channelID channel.ID,
	connectionType string,
) (string, error) {
	if service == nil || service.encryption == nil || credentialID == 0 {
		return "", fmt.Errorf(
			"map runtime health problem key: %w",
			app_errors.ErrInternalServer,
		)
	}
	ciphertext, exists := ciphertexts[credentialID]
	if !exists || ciphertext == "" {
		return "", fmt.Errorf(
			"map runtime health problem key %d: ciphertext unavailable: %w",
			credentialID,
			app_errors.ErrInternalServer,
		)
	}
	plaintext, err := service.encryption.Decrypt(ciphertext)
	if err != nil {
		return "", fmt.Errorf(
			"map runtime health problem key %d: decrypt credential: %v: %w",
			credentialID,
			err,
			app_errors.ErrInternalServer,
		)
	}
	expectedConnection, known := service.channelRegistry.ConnectionType(channelID)
	if !known || expectedConnection != strings.TrimSpace(connectionType) {
		plaintext = ""
		return "", fmt.Errorf("map runtime health credential %d: channel connection mismatch: %w", credentialID, app_errors.ErrInternalServer)
	}
	if driver, bound := service.subscriptions.Driver(channelID); bound {
		credential, parseErr := driver.Parse([]byte(plaintext))
		plaintext = ""
		if parseErr != nil {
			return "", fmt.Errorf("map runtime health subscription credential %d: %w", credentialID, app_errors.ErrInternalServer)
		}
		if mask := maskEmail(credential.Account().Email); mask != "" {
			return mask, nil
		}
		return fmt.Sprintf("Subscription #%d", credentialID), nil
	}
	validated, err := normalizeStoredCredential(service.channelRegistry, channelID, plaintext)
	if err != nil {
		return "", fmt.Errorf(
			"map runtime health problem credential %d: validate credential: %v: %w",
			credentialID,
			err,
			app_errors.ErrInternalServer,
		)
	}
	return maskCanonicalCredential(validated.CanonicalJSON())
}

func (service *Service) RuntimeHealth() (runtimeHealthResponse, error) {
	observation, err := service.captureRuntimeHealthObservation()
	if err != nil {
		return runtimeHealthResponse{}, err
	}
	if service.stats == nil || service.requestLogStats == nil {
		return runtimeHealthResponse{}, fmt.Errorf(
			"runtime health dependencies unavailable: %w",
			app_errors.ErrInternalServer,
		)
	}
	observedAtMS, err := safeEpochMilliseconds(observation.observedAt)
	if err != nil {
		return runtimeHealthResponse{}, fmt.Errorf("map runtime health observed_at_ms: %w", err)
	}
	result := runtimeHealthResponse{
		ObservedAtMS:           observedAtMS,
		SnapshotRevision:       observation.snapshot.Revision,
		StatsWindowSeconds:     int64(health.StatsWindow / time.Second),
		Groups:                 []healthGroupResponse{},
		CooldownCredentials:    []healthProblemCredentialResponse{},
		BlacklistedCredentials: []healthProblemCredentialResponse{},
		LowQuotaCredentials:    []healthQuotaCredentialResponse{},
		BlockedAccessKeys:      []healthAccessKeyCostLimitResponse{},
	}
	groupIDs := make([]uint, 0, len(observation.snapshot.GroupCatalog))
	for groupID := range observation.snapshot.GroupCatalog {
		groupIDs = append(groupIDs, groupID)
	}
	sort.Slice(groupIDs, func(i, j int) bool { return groupIDs[i] < groupIDs[j] })
	groupIndexes := make(map[uint]int, len(groupIDs))
	for _, groupID := range groupIDs {
		group := observation.snapshot.GroupCatalog[groupID]
		groupIndexes[groupID] = len(result.Groups)
		result.Groups = append(result.Groups, healthGroupResponse{
			ID: group.ID, Name: group.Name, Enabled: group.Enabled,
		})
	}
	for _, key := range observation.keys {
		group := observation.snapshot.GroupCatalog[key.GroupID]
		index := groupIndexes[key.GroupID]
		bucket := classifyHealthKey(group, key, observation.observedAt)
		addHealthCount(&result.Counts, bucket)
		addHealthCount(&result.Groups[index].Counts, bucket)
		// 额度独立于分桶：调度器会跳过额度耗尽的凭据，但 classifyHealthKey 不看额度，
		// 所以这批凭据在上面仍被计入 available，必须单列出来。
		// FreshQuotaRemaining 在观测过期或已过重置时刻时返回 nil，过期数字绝不报警。
		if bucket == healthBucketAvailable || bucket == healthBucketCooldown {
			if remaining := key.FreshQuotaRemaining(observation.observedAt); remaining != nil &&
				*remaining <= healthLowQuotaRemainingRatio {
				resetAtMS, err := safeEpochMilliseconds(key.QuotaResetAt)
				if err != nil {
					return runtimeHealthResponse{}, fmt.Errorf(
						"map low quota credential %d reset_at_ms: %w", key.ID, err,
					)
				}
				result.LowQuotaCredentials = append(
					result.LowQuotaCredentials,
					healthQuotaCredentialResponse{
						CredentialID: key.ID,
						GroupID:      key.GroupID,
						GroupName:    group.Name,
						Remaining:    *remaining,
						ResetAtMS:    resetAtMS,
					},
				)
			}
		}
		if bucket != healthBucketCooldown && bucket != healthBucketBlacklisted {
			continue
		}
		groupView := observation.snapshot.Groups[key.GroupID]
		mask, err := service.healthProblemMask(
			observation.problemCiphertexts,
			key.ID,
			groupView.ChannelID,
			groupView.ConnectionType,
		)
		if err != nil {
			return runtimeHealthResponse{}, err
		}
		stats := service.stats.Snapshot(key.ID, observation.observedAt)
		lastFailureCategory := stats.LastFailureCategory
		lastStatusCode := stats.LastStatusCode
		if lastFailureCategory == health.FailureCategoryOK {
			lastFailureCategory = health.FailureCategoryAmbiguous
			lastStatusCode = 0
		}
		detail := healthProblemCredentialResponse{
			CredentialID:            key.ID,
			GroupID:                 key.GroupID,
			GroupName:               group.Name,
			Mask:                    mask,
			LastFailureCategory:     lastFailureCategory.String(),
			LastStatusCode:          optionalHealthStatusCode(lastStatusCode),
			FailureCount:            key.FailureCount,
			RecentSuccessCount:      stats.Success,
			RecentProblemCount:      stats.Problem,
			ConsecutiveProblemCount: stats.ConsecutiveProblem,
			WeightManual:            cloneInt(key.WeightManual),
			WeightAuto:              key.WeightAuto,
		}
		if bucket == healthBucketCooldown {
			cooldownUntilMS, err := optionalSafeEpochMilliseconds(key.CooldownUntil)
			if err != nil {
				return runtimeHealthResponse{}, fmt.Errorf(
					"map runtime health cooldown_until_ms: %w",
					err,
				)
			}
			detail.CooldownUntilMS = cooldownUntilMS
			detail.Recovery = healthRecoveryResponse{
				Automatic: true,
				Mode:      "cooldown_expiry",
				AtMS:      cooldownUntilMS,
			}
			result.CooldownCredentials = append(result.CooldownCredentials, detail)
		} else {
			detail.Recovery = healthRecoveryResponse{Mode: "configuration_required"}
			if service.executor != nil && service.channelRegistry != nil {
				if validationGroup, exists := observation.snapshot.Groups[key.GroupID]; exists {
					if _, valid := buildGroupValidationTarget(validationGroup); valid {
						detail.Recovery = healthRecoveryResponse{Automatic: true, Mode: "validation_probe"}
					}
				}
			}
			result.BlacklistedCredentials = append(result.BlacklistedCredentials, detail)
		}
	}
	if service.accessQuota != nil {
		accessKeyIDs := make([]uint, 0, len(observation.snapshot.AccessKeysByID))
		for accessKeyID := range observation.snapshot.AccessKeysByID {
			accessKeyIDs = append(accessKeyIDs, accessKeyID)
		}
		sort.Slice(accessKeyIDs, func(i, j int) bool { return accessKeyIDs[i] < accessKeyIDs[j] })
		for _, accessKeyID := range accessKeyIDs {
			accessKey := observation.snapshot.AccessKeysByID[accessKeyID]
			if accessKey.Status != state.AccessKeyStatusActive {
				continue
			}
			view := service.accessQuota.Snapshot(accessKeyID, observation.observedAt)
			if view.Allowed {
				continue
			}
			if !validAccessKeySuffix(accessKey.KeySuffix) {
				return runtimeHealthResponse{}, fmt.Errorf(
					"map blocked access key %d suffix: %w",
					accessKeyID,
					app_errors.ErrInternalServer,
				)
			}
			status := mapAccessKeyCostLimitStatus(view)
			result.BlockedAccessKeys = append(result.BlockedAccessKeys, healthAccessKeyCostLimitResponse{
				AccessKeyID: accessKeyID, Name: accessKey.Name,
				MaskedKey:         maskedAccessKey(accessKey.KeySuffix),
				Recoverable:       status.Recoverable,
				NextAvailableAtMS: cloneCostLimitMilliseconds(status.NextAvailableAtMS),
				BlockingRules:     blockingCostLimitRuleStatuses(status),
			})
		}
	}
	requestLog, err := mapRequestLogHealth(service.requestLogStats.Stats())
	if err != nil {
		return runtimeHealthResponse{}, fmt.Errorf("map request log health: %w", err)
	}
	result.RequestLog = requestLog
	return result, nil
}

func mapRequestLogHealth(stats requestlog.Stats) (requestLogHealthResponse, error) {
	for _, value := range []uint64{
		stats.EnqueuedTotal,
		stats.PersistedTotal,
		stats.DroppedNotRunningTotal,
		stats.DroppedQueueFullTotal,
		stats.DroppedStoppingTotal,
		stats.DroppedPersistFailedTotal,
		stats.DroppedShutdownTotal,
		stats.DroppedTotal,
		stats.WriteFailureTotal,
		stats.RetentionDeleteFailureTotal,
	} {
		if value > uint64(maxSafeInteger) {
			return requestLogHealthResponse{}, fmt.Errorf("map request log health: unsafe counter")
		}
	}
	if stats.QueueDepth < 0 || uint64(stats.QueueDepth) > uint64(maxSafeInteger) ||
		stats.QueueCapacity < 0 || uint64(stats.QueueCapacity) > uint64(maxSafeInteger) {
		return requestLogHealthResponse{}, fmt.Errorf("map request log health: unsafe queue")
	}
	lastWriteFailureAtMS, err := optionalSafeEpochMilliseconds(stats.LastWriteFailureAt)
	if err != nil {
		return requestLogHealthResponse{}, fmt.Errorf("map last write failure timestamp: %w", err)
	}
	lastRetentionFailureAtMS, err := optionalSafeEpochMilliseconds(stats.LastRetentionFailureAt)
	if err != nil {
		return requestLogHealthResponse{}, fmt.Errorf("map last retention failure timestamp: %w", err)
	}
	return requestLogHealthResponse{
		EnqueuedTotal:               stats.EnqueuedTotal,
		PersistedTotal:              stats.PersistedTotal,
		DroppedNotRunningTotal:      stats.DroppedNotRunningTotal,
		DroppedQueueFullTotal:       stats.DroppedQueueFullTotal,
		DroppedStoppingTotal:        stats.DroppedStoppingTotal,
		DroppedPersistFailedTotal:   stats.DroppedPersistFailedTotal,
		DroppedShutdownTotal:        stats.DroppedShutdownTotal,
		DroppedTotal:                stats.DroppedTotal,
		WriteFailureTotal:           stats.WriteFailureTotal,
		RetentionDeleteFailureTotal: stats.RetentionDeleteFailureTotal,
		QueueDepth:                  stats.QueueDepth,
		QueueCapacity:               stats.QueueCapacity,
		LastWriteFailureAtMS:        lastWriteFailureAtMS,
		LastRetentionFailureAtMS:    lastRetentionFailureAtMS,
	}, nil
}

func (server *Server) handleRuntimeHealth(c *gin.Context) {
	result, err := server.service.RuntimeHealth()
	if err != nil {
		writeServiceError(c, "runtime_health", err)
		return
	}
	now := server.now().UTC()
	uptime := now.Sub(server.startedAt)
	if uptime < 0 {
		uptime = 0
	}
	result.Version = version.Version
	result.UptimeSeconds = int64(uptime / time.Second)
	response.SuccessI18n(c, "common.success", result)
}
