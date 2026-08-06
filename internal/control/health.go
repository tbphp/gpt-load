package control

import (
	"fmt"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/health"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/platform/version"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/state"
)

type RequestLogStatsReader interface {
	Stats() requestlog.Stats
}

type healthCountsResponse struct {
	Total       int `json:"total"`
	Available   int `json:"available"`
	Cooldown    int `json:"cooldown"`
	Blacklisted int `json:"blacklisted"`
	Disabled    int `json:"disabled"`
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

type healthProblemKeyResponse struct {
	KeyID                   uint                   `json:"key_id"`
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
	ObservedAtMS       int64                      `json:"observed_at_ms"`
	Version            string                     `json:"version"`
	UptimeSeconds      int64                      `json:"uptime_seconds"`
	SnapshotRevision   uint64                     `json:"snapshot_revision"`
	StatsWindowSeconds int64                      `json:"stats_window_seconds"`
	Counts             healthCountsResponse       `json:"counts"`
	Groups             []healthGroupResponse      `json:"groups"`
	CooldownKeys       []healthProblemKeyResponse `json:"cooldown_keys"`
	BlacklistedKeys    []healthProblemKeyResponse `json:"blacklisted_keys"`
	RequestLog         requestLogHealthResponse   `json:"request_log"`
}

type healthBucket string

const (
	healthBucketAvailable   healthBucket = "available"
	healthBucketCooldown    healthBucket = "cooldown"
	healthBucketBlacklisted healthBucket = "blacklisted"
	healthBucketDisabled    healthBucket = "disabled"
)

func classifyHealthKey(
	group state.GroupCatalogView,
	key state.KeyRuntimeView,
	now time.Time,
) healthBucket {
	if !group.Enabled ||
		(group.WeightManual != nil && *group.WeightManual == 0) ||
		key.Status != state.KeyStatusActive ||
		(key.WeightManual != nil && *key.WeightManual == 0) {
		return healthBucketDisabled
	}
	switch key.RuntimeState(now) {
	case state.KeyRuntimeBlacklisted:
		return healthBucketBlacklisted
	case state.KeyRuntimeCooldown:
		return healthBucketCooldown
	default:
		return healthBucketAvailable
	}
}

func addHealthCount(counts *healthCountsResponse, bucket healthBucket) {
	counts.Total++
	switch bucket {
	case healthBucketAvailable:
		counts.Available++
	case healthBucketCooldown:
		counts.Cooldown++
	case healthBucketBlacklisted:
		counts.Blacklisted++
	case healthBucketDisabled:
		counts.Disabled++
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
	keyID uint,
) (string, error) {
	if service == nil || service.encryption == nil || keyID == 0 {
		return "", fmt.Errorf(
			"map runtime health problem key: %w",
			app_errors.ErrInternalServer,
		)
	}
	ciphertext, exists := ciphertexts[keyID]
	if !exists || ciphertext == "" {
		return "", fmt.Errorf(
			"map runtime health problem key %d: ciphertext unavailable: %w",
			keyID,
			app_errors.ErrInternalServer,
		)
	}
	plaintext, err := service.encryption.Decrypt(ciphertext)
	if err != nil {
		return "", fmt.Errorf(
			"map runtime health problem key %d: decrypt credential: %v: %w",
			keyID,
			err,
			app_errors.ErrInternalServer,
		)
	}
	mask := utils.MaskAPIKey(plaintext)
	if mask == "" {
		return "", fmt.Errorf(
			"map runtime health problem key %d: empty credential: %w",
			keyID,
			app_errors.ErrInternalServer,
		)
	}
	return mask, nil
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
		ObservedAtMS:       observedAtMS,
		SnapshotRevision:   observation.snapshot.Revision,
		StatsWindowSeconds: int64(health.StatsWindow / time.Second),
		Groups:             []healthGroupResponse{},
		CooldownKeys:       []healthProblemKeyResponse{},
		BlacklistedKeys:    []healthProblemKeyResponse{},
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
		if bucket != healthBucketCooldown && bucket != healthBucketBlacklisted {
			continue
		}
		mask, err := service.healthProblemMask(observation.problemCiphertexts, key.ID)
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
		detail := healthProblemKeyResponse{
			KeyID:                   key.ID,
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
			result.CooldownKeys = append(result.CooldownKeys, detail)
		} else {
			detail.Recovery = healthRecoveryResponse{Mode: "configuration_required"}
			if validationGroup, exists := observation.snapshot.Groups[key.GroupID]; exists {
				if _, valid := buildGroupValidationTarget(validationGroup); valid {
					detail.Recovery = healthRecoveryResponse{
						Automatic: true,
						Mode:      "validation_probe",
					}
				}
			}
			result.BlacklistedKeys = append(result.BlacklistedKeys, detail)
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
