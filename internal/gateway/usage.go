package gateway

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/accessquota"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/state"
)

// AccessKeyUsageReader 查询长期保留的 AccessKey 累计预估成本。
type AccessKeyUsageReader interface {
	QueryAccessKeyTotalCost(context.Context, uint) (int64, error)
}

type accessKeyUsageResponse struct {
	IsActive bool    `json:"is_active"`
	Balance  float64 `json:"balance"`
	Used     float64 `json:"used"`
	Total    float64 `json:"total"`
}

func (handler *Handler) handleUsage(c *gin.Context, request *dataPlaneRequestContext) {
	c.Header("Cache-Control", "no-store")
	result := accessKeyUsageResponse{IsActive: true}
	var totalRule *accessquota.RuleView
	// 与配置发布同步，避免读到另一次配置的额度状态。
	current := handler.manager.WithCurrentSnapshotRead(func(snapshot *state.ConfigSnapshot) bool {
		if snapshot != request.snapshot {
			return false
		}
		if handler.accessQuota != nil {
			for _, rule := range handler.accessQuota.Snapshot(request.accessKey.ID, handler.quotaNow()).Rules {
				if rule.Kind == accessquota.KindTotal {
					totalRule = &rule
					break
				}
			}
		}
		return true
	})
	if !current {
		_ = handler.writeReason(c, reasonConfigurationChanged)
		return
	}
	if totalRule != nil {
		result.Total = float64(totalRule.LimitNanoUSD) / 1e9
		result.Used = float64(totalRule.UsedNanoUSD) / 1e9
		result.Balance = float64(totalRule.RemainingNanoUSD) / 1e9
	} else {
		if handler.usageReader == nil {
			handler.writeUsageUnavailable(c)
			return
		}
		used, err := handler.usageReader.QueryAccessKeyTotalCost(c.Request.Context(), request.accessKey.ID)
		if err != nil {
			utils.LogPlaneBestEffort(handler.logger, logrus.WarnLevel, utils.LogPlaneData,
				logrus.Fields{"event": "access_key_usage_query_failed", "access_key_id": request.accessKey.ID},
				"Access key usage query failed")
			handler.writeUsageUnavailable(c)
			return
		}
		result.Used = float64(used) / 1e9
	}
	c.JSON(http.StatusOK, result)
}

func (handler *Handler) writeUsageUnavailable(c *gin.Context) {
	_ = handler.writeReason(c, reason{Status: http.StatusServiceUnavailable,
		Code: "usage_unavailable", Message: "Usage is temporarily unavailable."})
}
