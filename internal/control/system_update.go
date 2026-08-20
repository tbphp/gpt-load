package control

import (
	"fmt"
	"strconv"

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
	force, err := systemUpdateForce(c)
	if err != nil {
		writeServiceError(c, "system_update", app_errors.ErrBadRequest)
		return
	}
	if s.releaseChecker == nil {
		writeServiceError(c, "system_update", app_errors.ErrInternalServer)
		return
	}
	available, err := s.releaseChecker.Check(c.Request.Context(), force)
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

func systemUpdateForce(c *gin.Context) (bool, error) {
	if c.Request.URL.ForceQuery {
		return false, app_errors.ErrBadRequest
	}
	if c.Request.URL.RawQuery == "" {
		return false, nil
	}
	query := c.Request.URL.Query()
	values, ok := query["force"]
	if !ok || len(values) != 1 || len(query) != 1 {
		return false, app_errors.ErrBadRequest
	}
	force, err := strconv.ParseBool(values[0])
	if err != nil {
		return false, app_errors.ErrBadRequest
	}
	return force, nil
}
