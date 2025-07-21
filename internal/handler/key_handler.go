package handler

import (
	"fmt"
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"
	"gpt-load/internal/response"
	"log"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// validateGroupIDFromQuery validates and parses group ID from a query parameter.
func validateGroupIDFromQuery(c *gin.Context) (uint, error) {
	groupIDStr := c.Query("group_id")
	if groupIDStr == "" {
		return 0, fmt.Errorf("group_id query parameter is required")
	}

	groupID, err := strconv.Atoi(groupIDStr)
	if err != nil || groupID <= 0 {
		return 0, fmt.Errorf("invalid group_id format")
	}

	return uint(groupID), nil
}

// validateKeysText validates the keys text input
func validateKeysText(keysText string) error {
	if strings.TrimSpace(keysText) == "" {
		return fmt.Errorf("keys text cannot be empty")
	}

	return nil
}

// findGroupByID is a helper function to find a group by its ID.
func (s *Server) findGroupByID(c *gin.Context, groupID uint) (*models.Group, bool) {
	var group models.Group
	if err := s.DB.First(&group, groupID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			response.Error(c, app_errors.ErrResourceNotFound)
		} else {
			response.Error(c, app_errors.ParseDBError(err))
		}
		return nil, false
	}
	return &group, true
}

// KeyTextRequest defines a generic payload for operations requiring a group ID and a text block of keys.
type KeyTextRequest struct {
	GroupID  uint   `json:"group_id" binding:"required"`
	KeysText string `json:"keys_text" binding:"required"`
}

// GroupIDRequest defines a generic payload for operations requiring only a group ID.
type GroupIDRequest struct {
	GroupID uint `json:"group_id" binding:"required"`
}

// AddMultipleKeys handles creating new keys from a text block within a specific group.
func (s *Server) AddMultipleKeys(c *gin.Context) {
	var req KeyTextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInvalidJSON, err.Error()))
		return
	}

	if _, ok := s.findGroupByID(c, req.GroupID); !ok {
		return
	}

	if err := validateKeysText(req.KeysText); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
		return
	}

	result, err := s.KeyService.AddMultipleKeys(req.GroupID, req.KeysText)
	if err != nil {
		if strings.Contains(err.Error(), "batch size exceeds the limit") {
			response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
		} else if err.Error() == "no valid keys found in the input text" {
			response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
		} else {
			response.Error(c, app_errors.ParseDBError(err))
		}
		return
	}

	response.Success(c, result)
}

// AddMultipleKeysAsync handles creating new keys from a text block within a specific group.
func (s *Server) AddMultipleKeysAsync(c *gin.Context) {
	var req KeyTextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInvalidJSON, err.Error()))
		return
	}

	group, ok := s.findGroupByID(c, req.GroupID)
	if !ok {
		return
	}

	if err := validateKeysText(req.KeysText); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
		return
	}

	taskStatus, err := s.KeyImportService.StartImportTask(group, req.KeysText)
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrTaskInProgress, err.Error()))
		return
	}

	response.Success(c, taskStatus)
}

// ListKeysInGroup handles listing all keys within a specific group with pagination.
func (s *Server) ListKeysInGroup(c *gin.Context) {
	groupID, err := validateGroupIDFromQuery(c)
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrBadRequest, err.Error()))
		return
	}

	if _, ok := s.findGroupByID(c, groupID); !ok {
		return
	}

	statusFilter := c.Query("status")
	if statusFilter != "" && statusFilter != models.KeyStatusActive && statusFilter != models.KeyStatusInvalid {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, "Invalid status filter"))
		return
	}

	searchKeyword := c.Query("key")

	query := s.KeyService.ListKeysInGroupQuery(groupID, statusFilter, searchKeyword)

	var keys []models.APIKey
	paginatedResult, err := response.Paginate(c, query, &keys)
	if err != nil {
		response.Error(c, app_errors.ParseDBError(err))
		return
	}

	response.Success(c, paginatedResult)
}

// DeleteMultipleKeys handles deleting keys from a text block within a specific group.
func (s *Server) DeleteMultipleKeys(c *gin.Context) {
	var req KeyTextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInvalidJSON, err.Error()))
		return
	}

	if _, ok := s.findGroupByID(c, req.GroupID); !ok {
		return
	}

	if err := validateKeysText(req.KeysText); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
		return
	}

	result, err := s.KeyService.DeleteMultipleKeys(req.GroupID, req.KeysText)
	if err != nil {
		if strings.Contains(err.Error(), "batch size exceeds the limit") {
			response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
		} else if err.Error() == "no valid keys found in the input text" {
			response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
		} else {
			response.Error(c, app_errors.ParseDBError(err))
		}
		return
	}

	response.Success(c, result)
}

// RestoreMultipleKeys handles restoring keys from a text block within a specific group.
func (s *Server) RestoreMultipleKeys(c *gin.Context) {
	var req KeyTextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInvalidJSON, err.Error()))
		return
	}

	if _, ok := s.findGroupByID(c, req.GroupID); !ok {
		return
	}

	if err := validateKeysText(req.KeysText); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
		return
	}

	result, err := s.KeyService.RestoreMultipleKeys(req.GroupID, req.KeysText)
	if err != nil {
		if strings.Contains(err.Error(), "batch size exceeds the limit") {
			response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
		} else if err.Error() == "no valid keys found in the input text" {
			response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
		} else {
			response.Error(c, app_errors.ParseDBError(err))
		}
		return
	}

	response.Success(c, result)
}

// TestMultipleKeys handles a one-off validation test for multiple keys.
func (s *Server) TestMultipleKeys(c *gin.Context) {
	var req KeyTextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInvalidJSON, err.Error()))
		return
	}

	groupDB, ok := s.findGroupByID(c, req.GroupID)
	if !ok {
		return
	}

	group, err := s.GroupManager.GetGroupByName(groupDB.Name)
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrResourceNotFound, fmt.Sprintf("Group '%s' not found", groupDB.Name)))
		return
	}

	if err := validateKeysText(req.KeysText); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
		return
	}

	results, err := s.KeyService.TestMultipleKeys(group, req.KeysText)
	if err != nil {
		if strings.Contains(err.Error(), "batch size exceeds the limit") {
			response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
		} else if err.Error() == "no valid keys found in the input text" {
			response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
		} else {
			response.Error(c, app_errors.ParseDBError(err))
		}
		return
	}

	response.Success(c, results)
}

// ValidateGroupKeys initiates a manual validation task for all keys in a group.
func (s *Server) ValidateGroupKeys(c *gin.Context) {
	var req GroupIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInvalidJSON, err.Error()))
		return
	}

	groupDB, ok := s.findGroupByID(c, req.GroupID)
	if !ok {
		return
	}

	group, err := s.GroupManager.GetGroupByName(groupDB.Name)
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrResourceNotFound, fmt.Sprintf("Group '%s' not found", groupDB.Name)))
		return
	}

	taskStatus, err := s.KeyManualValidationService.StartValidationTask(group)
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrTaskInProgress, err.Error()))
		return
	}

	response.Success(c, taskStatus)
}

// RestoreAllInvalidKeys sets the status of all 'inactive' keys in a group to 'active'.
func (s *Server) RestoreAllInvalidKeys(c *gin.Context) {
	var req GroupIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInvalidJSON, err.Error()))
		return
	}

	if _, ok := s.findGroupByID(c, req.GroupID); !ok {
		return
	}

	rowsAffected, err := s.KeyService.RestoreAllInvalidKeys(req.GroupID)
	if err != nil {
		response.Error(c, app_errors.ParseDBError(err))
		return
	}

	response.Success(c, gin.H{"message": fmt.Sprintf("%d keys restored.", rowsAffected)})
}

// ClearAllInvalidKeys deletes all 'inactive' keys from a group.
func (s *Server) ClearAllInvalidKeys(c *gin.Context) {
	var req GroupIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInvalidJSON, err.Error()))
		return
	}

	if _, ok := s.findGroupByID(c, req.GroupID); !ok {
		return
	}

	rowsAffected, err := s.KeyService.ClearAllInvalidKeys(req.GroupID)
	if err != nil {
		response.Error(c, app_errors.ParseDBError(err))
		return
	}

	response.Success(c, gin.H{"message": fmt.Sprintf("%d invalid keys cleared.", rowsAffected)})
}

// ExportKeys handles exporting keys to a text file.
func (s *Server) ExportKeys(c *gin.Context) {
	groupID, err := validateGroupIDFromQuery(c)
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrBadRequest, err.Error()))
		return
	}

	statusFilter := c.Query("status")
	if statusFilter == "" {
		statusFilter = "all"
	}

	switch statusFilter {
	case "all", models.KeyStatusActive, models.KeyStatusInvalid:
	default:
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, "Invalid status filter"))
		return
	}

	group, ok := s.findGroupByID(c, groupID)
	if !ok {
		return
	}

	filename := fmt.Sprintf("keys-%s-%s.txt", group.Name, statusFilter)
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Header("Content-Type", "text/plain; charset=utf-8")

	err = s.KeyService.StreamKeysToWriter(groupID, statusFilter, c.Writer)
	if err != nil {
		log.Printf("Failed to stream keys: %v", err)
	}
}
