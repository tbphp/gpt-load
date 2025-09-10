package handler

import (
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/response"

	"github.com/gin-gonic/gin"
)

// GetTaskStatus handles requests for the status of the global long-running task.
func (s *Server) GetTaskStatus(c *gin.Context) {
	taskStatus, err := s.TaskService.GetTaskStatus()
	if err != nil {
		response.ErrorI18nFromAPIError(c, app_errors.ErrInternalServer, "task.get_status_failed")
		return
	}
	response.Success(c, taskStatus)
}
