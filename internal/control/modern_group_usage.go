package control

import (
	"context"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/epochms"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/requestlog"
)

type groupUsageReader interface {
	QueryGroupUsage(context.Context, []uint, int64, int64) (requestlog.GroupUsageReport, error)
}

type modernGroupUsageItem struct {
	GroupID uint `json:"group_id"`
	usageAggregateResponse
}

type modernGroupUsageResponse struct {
	FromMS           int64                         `json:"from_ms"`
	ToMS             int64                         `json:"to_ms"`
	ObservedAtMS     int64                         `json:"observed_at_ms"`
	DataComplete     bool                          `json:"data_complete"`
	CollectionHealth usageCollectionHealthResponse `json:"collection_health"`
	Items            []modernGroupUsageItem        `json:"items"`
}

func parseModernGroupUsageIDs(raw string) ([]uint, error) {
	values, err := url.ParseQuery(raw)
	if err != nil || len(values) != 1 || len(values["group_ids"]) != 1 {
		return nil, app_errors.ErrBadRequest
	}
	parts := strings.Split(values.Get("group_ids"), ",")
	if len(parts) > requestlog.MaxGroupUsageBatchSize {
		return nil, app_errors.ErrBadRequest
	}
	ids := make([]uint, 0, len(parts))
	seen := make(map[uint]bool, len(parts))
	for _, part := range parts {
		id, err := parseCanonicalSafePlatformUint(part)
		if err != nil || id == 0 || seen[id] {
			return nil, app_errors.ErrBadRequest
		}
		ids = append(ids, id)
		seen[id] = true
	}
	return ids, nil
}

func (server *Server) handleModernGroupUsage(c *gin.Context) {
	ids, err := parseModernGroupUsageIDs(c.Request.URL.RawQuery)
	if err != nil {
		writeServiceError(c, "modern_group_usage", err)
		return
	}
	observedAt, err := safeEpochMilliseconds(server.service.now())
	if err != nil {
		writeServiceError(c, "modern_group_usage", err)
		return
	}
	reader, ok := server.service.usageStats.(groupUsageReader)
	if !ok || server.service.requestLogStats == nil {
		writeServiceError(c, "modern_group_usage", app_errors.ErrInternalServer)
		return
	}
	fromMS := max(int64(0), observedAt-epochms.MillisecondsPerDay)
	report, err := reader.QueryGroupUsage(c.Request.Context(), ids, fromMS, observedAt)
	if err != nil {
		writeServiceError(c, "modern_group_usage", app_errors.ParseDBError(err))
		return
	}
	stats := server.service.requestLogStats.Stats()
	lastFailure, err := optionalSafeEpochMilliseconds(stats.LastWriteFailureAt)
	if err != nil || stats.DroppedTotal > uint64(maxSafeInteger) || stats.WriteFailureTotal > uint64(maxSafeInteger) {
		writeServiceError(c, "modern_group_usage", app_errors.ErrInternalServer)
		return
	}
	result := modernGroupUsageResponse{
		FromMS: fromMS, ToMS: observedAt, ObservedAtMS: observedAt, DataComplete: report.DataComplete,
		Items: make([]modernGroupUsageItem, 0, len(ids)),
		CollectionHealth: usageCollectionHealthResponse{
			Scope: "current_process", DroppedTotal: stats.DroppedTotal, WriteFailureTotal: stats.WriteFailureTotal, LastWriteFailureAtMS: lastFailure,
		},
	}
	for _, id := range ids {
		aggregate, err := mapUsageAggregate(report.Items[id])
		if err != nil {
			writeServiceError(c, "modern_group_usage", err)
			return
		}
		result.Items = append(result.Items, modernGroupUsageItem{GroupID: id, usageAggregateResponse: aggregate})
	}
	c.Header("Cache-Control", "no-store")
	response.SuccessI18n(c, "common.success", result)
}
