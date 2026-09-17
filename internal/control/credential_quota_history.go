package control

import (
	"encoding/json"
	"errors"
	"net/url"
	"sort"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gpt-load/internal/platform/epochms"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription"
)

const quotaHistoryRetentionDays = 35

type quotaHistoryPointResponse struct {
	ObservedAtMS    int64  `json:"observed_at_ms"`
	UsedBasisPoints int64  `json:"used_basis_points"`
	ResetAtMS       *int64 `json:"reset_at_ms"`
}

type quotaHistoryWindowResponse struct {
	Key           string                      `json:"key"`
	ID            string                      `json:"id"`
	SourceID      string                      `json:"source_id"`
	Label         string                      `json:"label"`
	LabelKey      string                      `json:"label_key"`
	Scope         string                      `json:"scope"`
	WindowSeconds *int64                      `json:"window_seconds"`
	Points        []quotaHistoryPointResponse `json:"points"`
}

type quotaHistoryResponse struct {
	FromMS        int64                        `json:"from_ms"`
	ToMS          int64                        `json:"to_ms"`
	ObservedAtMS  int64                        `json:"observed_at_ms"`
	BucketWidthMS int64                        `json:"bucket_width_ms"`
	HasHistory    bool                         `json:"has_history"`
	Windows       []quotaHistoryWindowResponse `json:"windows"`
}

func (server *Server) handleCredentialQuotaHistory(c *gin.Context) {
	groupID, err := parseCanonicalSafePlatformUint(c.Param("group_id"))
	if err != nil || groupID == 0 {
		writeServiceError(c, "quota_history", app_errors.ErrBadRequest)
		return
	}
	credentialID, err := parseCanonicalSafePlatformUint(c.Param("credential_id"))
	if err != nil || credentialID == 0 {
		writeServiceError(c, "quota_history", app_errors.ErrBadRequest)
		return
	}
	values, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil || len(values) != 2 || len(values["from_ms"]) != 1 || len(values["to_ms"]) != 1 {
		writeServiceError(c, "quota_history", app_errors.ErrBadRequest)
		return
	}
	query, apiErr := parseUsageQuery(c.Request.URL.RawQuery)
	if apiErr != nil || query.ToMS-query.FromMS > quotaHistoryRetentionDays*epochms.MillisecondsPerDay {
		writeServiceError(c, "quota_history", app_errors.ErrBadRequest)
		return
	}
	observedAt, err := safeEpochMilliseconds(server.service.now())
	if err != nil {
		writeServiceError(c, "quota_history", err)
		return
	}
	result := quotaHistoryResponse{FromMS: query.FromMS, ToMS: query.ToMS, ObservedAtMS: observedAt, Windows: []quotaHistoryWindowResponse{}}
	// 每窗口约 240 个点；仍按重置周期分桶，防止抽样跨越重置边界。
	result.BucketWidthMS = max(int64(60_000), ((query.ToMS-query.FromMS-1)/240/60_000+1)*60_000)
	err = server.service.withReadSnapshot(c.Request.Context(), func(tx *gorm.DB) error {
		var group models.Group
		if err := tx.Take(&group, groupID).Error; err != nil {
			return err
		}
		if group.ConnectionType != models.ConnectionTypeSubscription {
			return app_errors.ErrBadRequest
		}
		var credential models.Credential
		if err := tx.Where("id = ? AND group_id = ?", credentialID, groupID).Take(&credential).Error; err != nil {
			return err
		}
		identity := subscription.QuotaHistoryTargetIdentity(stateloader.CredentialIdentityGeneration(
			credential.IdentityFingerprint, group.ChannelID, string(group.ConnectionType), json.RawMessage(group.Params)))
		scope := func() *gorm.DB {
			return tx.Model(&models.CredentialQuotaHistory{}).
				Where("group_id = ? AND credential_id = ? AND target_identity = ?", groupID, credentialID, identity)
		}
		var first models.CredentialQuotaHistory
		if err := scope().Select("id").Take(&first).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		result.HasHistory = first.ID != 0
		var rows []models.CredentialQuotaHistory
		// 排名在数据库内完成，不把一分钟的全部历史搬进应用再降采样。
		ranked := scope().Where("observed_at_ms >= ? AND observed_at_ms < ?", query.FromMS, query.ToMS).
			Select("credential_quota_histories.*, ROW_NUMBER() OVER (PARTITION BY window_key, reset_at_ms, observed_at_ms - observed_at_ms % ? ORDER BY observed_at_ms DESC) AS sample_rank", result.BucketWidthMS)
		if err := tx.Table("(?) AS history_samples", ranked).Where("sample_rank = 1").Order("observed_at_ms ASC").Find(&rows).Error; err != nil {
			return err
		}
		windows := make(map[string]*quotaHistoryWindowResponse)
		for _, row := range rows {
			if row.ObservedAtMS > maxSafeInteger || row.UsedBasisPoints < 0 || row.UsedBasisPoints > 10_000 ||
				(row.ResetAtMS != nil && *row.ResetAtMS > maxSafeInteger) || (row.WindowSeconds != nil && *row.WindowSeconds > maxSafeInteger) {
				return app_errors.ErrInternalServer
			}
			window := windows[row.WindowKey]
			if window == nil {
				window = &quotaHistoryWindowResponse{Key: row.WindowKey, ID: row.WindowID, SourceID: row.SourceID, Scope: row.Scope, WindowSeconds: row.WindowSeconds, Points: []quotaHistoryPointResponse{}}
				windows[row.WindowKey] = window
			}
			window.Label, window.LabelKey = row.Label, row.LabelKey
			window.Points = append(window.Points, quotaHistoryPointResponse{ObservedAtMS: row.ObservedAtMS, UsedBasisPoints: row.UsedBasisPoints, ResetAtMS: row.ResetAtMS})
		}
		for _, window := range windows {
			result.Windows = append(result.Windows, *window)
		}
		sort.Slice(result.Windows, func(i, j int) bool {
			left, right := result.Windows[i], result.Windows[j]
			if left.Scope != right.Scope {
				return left.Scope < right.Scope
			}
			if left.WindowSeconds != nil && right.WindowSeconds != nil && *left.WindowSeconds != *right.WindowSeconds {
				return *left.WindowSeconds < *right.WindowSeconds
			}
			return left.Key < right.Key
		})
		return nil
	})
	if err != nil {
		var apiError *app_errors.APIError
		if errors.As(err, &apiError) {
			writeServiceError(c, "quota_history", apiError)
		} else {
			writeServiceError(c, "quota_history", app_errors.ParseDBError(err))
		}
		return
	}
	c.Header("Cache-Control", "no-store")
	response.SuccessI18n(c, "common.success", result)
}
