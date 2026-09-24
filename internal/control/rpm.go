package control

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/rpm"
	"gpt-load/internal/storage/models"
)

type rpmStatisticsReader interface {
	QueryRPM(context.Context, rpm.Kind, uint, time.Time) (requestlog.RPMReport, error)
	RPMPeaks(context.Context, rpm.Kind, []uint, time.Time) (map[uint]int64, error)
}

func (s *Service) rpmReader() rpmStatisticsReader {
	if reader, ok := s.usageStats.(rpmStatisticsReader); ok {
		return reader
	}
	reader, _ := s.requestLogs.(rpmStatisticsReader)
	return reader
}

func (s *Service) rpmPeaks(ctx context.Context, kind rpm.Kind, ids []uint, now time.Time) map[uint]int64 {
	reader := s.rpmReader()
	if reader == nil || len(ids) == 0 {
		return nil
	}
	peaks, err := reader.RPMPeaks(ctx, kind, ids, now)
	if err != nil {
		return nil
	}
	return peaks
}

// 已观测到的零值排在无数据之前；相同峰值保留原有稳定排序。
func compareRPMPeaks(left, right *int64) int {
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return -1
	}
	if right == nil {
		return 1
	}
	if *left > *right {
		return 1
	}
	if *left < *right {
		return -1
	}
	return 0
}

func (s *Server) handleAccessKeyRPM(c *gin.Context) {
	id, ok := accessKeyID(c)
	if !ok {
		return
	}
	var key models.AccessKey
	if err := s.service.db.WithContext(c.Request.Context()).Select("id").Take(&key, id).Error; err != nil {
		writeServiceError(c, "get_access_key", app_errors.ParseDBError(err))
		return
	}
	s.writeRPM(c, rpm.AccessKey, id)
}

func (s *Server) handleCredentialRPM(c *gin.Context) {
	group, ok := groupID(c, "get_group_credential")
	if !ok {
		return
	}
	id, ok := credentialID(c, "get_group_credential")
	if !ok {
		return
	}
	var credential models.Credential
	if err := s.service.db.WithContext(c.Request.Context()).Select("id").Where("group_id = ? AND id = ?", group, id).Take(&credential).Error; err != nil {
		writeServiceError(c, "get_group_credential", app_errors.ParseDBError(err))
		return
	}
	s.writeRPM(c, rpm.Credential, id)
}

func (s *Server) writeRPM(c *gin.Context, kind rpm.Kind, id uint) {
	if c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery {
		writeServiceError(c, "query_rpm", app_errors.ErrBadRequest)
		return
	}
	now := s.service.now()
	reader := s.service.rpmReader()
	if reader == nil {
		writeServiceError(c, "query_rpm", app_errors.ErrInternalServer)
		return
	}
	result, err := reader.QueryRPM(c.Request.Context(), kind, id, now)
	if err != nil {
		writeServiceError(c, "query_rpm", app_errors.ErrInternalServer)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}
