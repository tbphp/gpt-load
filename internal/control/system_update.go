package control

import (
	"fmt"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
)

type systemUpdateResponse struct {
	Update *releaseUpdateResponse `json:"update"`
}

type releaseUpdateResponse struct {
	Version       string `json:"version"`
	ReleaseURL    string `json:"release_url"`
	PublishedAtMS int64  `json:"published_at_ms"`
}

func (s *Server) handleSystemUpdate(c *gin.Context) {
	if c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery {
		writeServiceError(c, "system_update", app_errors.ErrBadRequest)
		return
	}
	if s.releaseChecker == nil {
		writeServiceError(c, "system_update", app_errors.ErrInternalServer)
		return
	}
	available, err := s.releaseChecker.Check(c.Request.Context())
	if err != nil {
		writeServiceError(
			c,
			"system_update",
			fmt.Errorf("check public release update: %w: %w", err, app_errors.ErrBadGateway),
		)
		return
	}
	var update *releaseUpdateResponse
	if available != nil {
		update = &releaseUpdateResponse{
			Version:       available.Version,
			ReleaseURL:    available.ReleaseURL,
			PublishedAtMS: available.PublishedAtMS,
		}
	}
	response.SuccessI18n(c, "common.success", systemUpdateResponse{Update: update})
}
