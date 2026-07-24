package control

import (
	"fmt"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/health"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
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
	Automatic bool       `json:"automatic"`
	Mode      string     `json:"mode"`
	At        *time.Time `json:"at"`
}

type healthProblemKeyResponse struct {
	KeyID                   uint                   `json:"key_id"`
	GroupID                 uint                   `json:"group_id"`
	GroupName               string                 `json:"group_name"`
	CooldownUntil           *time.Time             `json:"cooldown_until,omitempty"`
	FailureCount            int                    `json:"failure_count"`
	RecentSuccessCount      uint64                 `json:"recent_success_count"`
	RecentFailureCount      uint64                 `json:"recent_failure_count"`
	ConsecutiveFailureCount uint64                 `json:"consecutive_failure_count"`
	WeightManual            *int                   `json:"weight_manual"`
	WeightAuto              int                    `json:"weight_auto"`
	Recovery                healthRecoveryResponse `json:"recovery"`
}

type requestLogHealthResponse struct {
	EnqueuedTotal                uint64     `json:"enqueued_total"`
	PersistedTotal               uint64     `json:"persisted_total"`
	DroppedNotRunningTotal       uint64     `json:"dropped_not_running_total"`
	DroppedQueueFullTotal        uint64     `json:"dropped_queue_full_total"`
	DroppedStoppingTotal         uint64     `json:"dropped_stopping_total"`
	DroppedPersistFailedTotal    uint64     `json:"dropped_persist_failed_total"`
	DroppedShutdownTotal         uint64     `json:"dropped_shutdown_total"`
	DroppedTotal                 uint64     `json:"dropped_total"`
	WriteFailureTotal            uint64     `json:"write_failure_total"`
	RetentionInvalidSettingTotal uint64     `json:"retention_invalid_setting_total"`
	RetentionDeleteFailureTotal  uint64     `json:"retention_delete_failure_total"`
	QueueDepth                   int        `json:"queue_depth"`
	QueueCapacity                int        `json:"queue_capacity"`
	LastWriteFailureAt           *time.Time `json:"last_write_failure_at"`
	LastRetentionFailureAt       *time.Time `json:"last_retention_failure_at"`
}

type runtimeHealthResponse struct {
	ObservedAt         time.Time                  `json:"observed_at"`
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

func optionalUTC(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (service *Service) RuntimeHealth() (runtimeHealthResponse, error) {
	observation, err := service.captureRuntimeObservation()
	if err != nil {
		return runtimeHealthResponse{}, err
	}
	if service.stats == nil || service.requestLogStats == nil {
		return runtimeHealthResponse{}, fmt.Errorf(
			"runtime health dependencies unavailable: %w",
			app_errors.ErrInternalServer,
		)
	}
	result := runtimeHealthResponse{
		ObservedAt:         observation.observedAt,
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
		stats := service.stats.Snapshot(key.ID, observation.observedAt)
		detail := healthProblemKeyResponse{
			KeyID:                   key.ID,
			GroupID:                 key.GroupID,
			GroupName:               group.Name,
			FailureCount:            key.FailureCount,
			RecentSuccessCount:      stats.Success,
			RecentFailureCount:      stats.Failure,
			ConsecutiveFailureCount: stats.ConsecutiveFailure,
			WeightManual:            cloneInt(key.WeightManual),
			WeightAuto:              key.WeightAuto,
		}
		if bucket == healthBucketCooldown {
			detail.CooldownUntil = optionalUTC(key.CooldownUntil)
			detail.Recovery = healthRecoveryResponse{
				Automatic: true,
				Mode:      "cooldown_expiry",
				At:        optionalUTC(key.CooldownUntil),
			}
			result.CooldownKeys = append(result.CooldownKeys, detail)
		} else {
			detail.Recovery = healthRecoveryResponse{
				Automatic: true,
				Mode:      "validation_probe",
			}
			result.BlacklistedKeys = append(result.BlacklistedKeys, detail)
		}
	}
	result.RequestLog = mapRequestLogHealth(service.requestLogStats.Stats())
	return result, nil
}

func mapRequestLogHealth(stats requestlog.Stats) requestLogHealthResponse {
	return requestLogHealthResponse{
		EnqueuedTotal:                stats.EnqueuedTotal,
		PersistedTotal:               stats.PersistedTotal,
		DroppedNotRunningTotal:       stats.DroppedNotRunningTotal,
		DroppedQueueFullTotal:        stats.DroppedQueueFullTotal,
		DroppedStoppingTotal:         stats.DroppedStoppingTotal,
		DroppedPersistFailedTotal:    stats.DroppedPersistFailedTotal,
		DroppedShutdownTotal:         stats.DroppedShutdownTotal,
		DroppedTotal:                 stats.DroppedTotal,
		WriteFailureTotal:            stats.WriteFailureTotal,
		RetentionInvalidSettingTotal: stats.RetentionInvalidSettingTotal,
		RetentionDeleteFailureTotal:  stats.RetentionDeleteFailureTotal,
		QueueDepth:                   stats.QueueDepth,
		QueueCapacity:                stats.QueueCapacity,
		LastWriteFailureAt:           optionalUTC(stats.LastWriteFailureAt),
		LastRetentionFailureAt:       optionalUTC(stats.LastRetentionFailureAt),
	}
}

func (server *Server) handleRuntimeHealth(c *gin.Context) {
	result, err := server.service.RuntimeHealth()
	if err != nil {
		writeServiceError(c, "runtime_health", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}
