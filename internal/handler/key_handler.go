package handler

import (
	"fmt"
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"
	"gpt-load/internal/response"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var validKeyStatuses = []string{
	models.KeyStatusActive,
	models.KeyStatusInvalid,
	models.KeyStatusRateLimited,
	models.KeyStatusAuthFailed,
	models.KeyStatusForbidden,
	models.KeyStatusBadRequest,
	models.KeyStatusServerError,
	models.KeyStatusNetworkError,
}

var validExportStatuses = append([]string{"all"}, validKeyStatuses...)

// handleServiceError handles common service error patterns
func handleServiceError(c *gin.Context, err error) {
	if strings.Contains(err.Error(), "batch size exceeds the limit") {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
	} else if err.Error() == "no valid keys found in the input text" {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
	} else {
		response.Error(c, app_errors.ParseDBError(err))
	}
}

// validateStatus validates if status is in valid statuses list
func validateStatus(status string) bool {
	return contains(validKeyStatuses, status)
}

// bindJSONAndValidateGroup is a helper function for common request binding and group validation
func (s *Server) bindJSONAndValidateGroup(c *gin.Context, req interface{}, needGroup bool) (*models.Group, bool) {
	if err := c.ShouldBindJSON(req); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInvalidJSON, err.Error()))
		return nil, false
	}

	if !needGroup {
		return nil, true
	}

	// Extract group ID using reflection or type assertion
	var groupID uint
	switch v := req.(type) {
	case *KeyTextRequest:
		groupID = v.GroupID
	case *GroupIDRequest:
		groupID = v.GroupID
	case *ValidateGroupKeysRequest:
		groupID = v.GroupID
	case *GroupStatusRequest:
		groupID = v.GroupID
	default:
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInvalidJSON, "unknown request type"))
		return nil, false
	}

	return s.findGroupByID(c, groupID)
}

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

// contains checks if a string slice contains a specific string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
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

// ValidateGroupKeysRequest defines the payload for validating keys in a group.
type ValidateGroupKeysRequest struct {
	GroupID uint   `json:"group_id" binding:"required"`
	Status  string `json:"status,omitempty"`
}

// AddMultipleKeys handles creating new keys from a text block within a specific group.
func (s *Server) AddMultipleKeys(c *gin.Context) {
	var req KeyTextRequest
	if _, ok := s.bindJSONAndValidateGroup(c, &req, true); !ok {
		return
	}

	if err := validateKeysText(req.KeysText); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
		return
	}

	result, err := s.KeyService.AddMultipleKeys(req.GroupID, req.KeysText)
	if err != nil {
		handleServiceError(c, err)
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
	if statusFilter != "" && !contains(validKeyStatuses, statusFilter) {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, "Invalid status filter"))
		return
	}

	searchKeyword := c.Query("key_value")

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
	if _, ok := s.bindJSONAndValidateGroup(c, &req, true); !ok {
		return
	}

	if err := validateKeysText(req.KeysText); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
		return
	}

	result, err := s.KeyService.DeleteMultipleKeys(req.GroupID, req.KeysText)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	response.Success(c, result)
}

// DeleteMultipleKeysAsync handles deleting keys from a text block within a specific group using async task.
func (s *Server) DeleteMultipleKeysAsync(c *gin.Context) {
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

	taskStatus, err := s.KeyDeleteService.StartDeleteTask(group, req.KeysText)
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrTaskInProgress, err.Error()))
		return
	}

	response.Success(c, taskStatus)
}

// RestoreMultipleKeys handles restoring keys from a text block within a specific group.
func (s *Server) RestoreMultipleKeys(c *gin.Context) {
	var req KeyTextRequest
	if _, ok := s.bindJSONAndValidateGroup(c, &req, true); !ok {
		return
	}

	if err := validateKeysText(req.KeysText); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
		return
	}

	result, err := s.KeyService.RestoreMultipleKeys(req.GroupID, req.KeysText)
	if err != nil {
		handleServiceError(c, err)
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

	start := time.Now()
	results, err := s.KeyService.TestMultipleKeys(group, req.KeysText)
	duration := time.Since(start).Milliseconds()
	if err != nil {
		handleServiceError(c, err)
		return
	}

	response.Success(c, gin.H{
		"results":        results,
		"total_duration": duration,
	})
}

// ValidateGroupKeys initiates a manual validation task for all keys in a group.
func (s *Server) ValidateGroupKeys(c *gin.Context) {
	var req ValidateGroupKeysRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInvalidJSON, err.Error()))
		return
	}

	// Validate status if provided
	if req.Status != "" && !validateStatus(req.Status) {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, "Invalid status value"))
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

	taskStatus, err := s.KeyManualValidationService.StartValidationTask(group, req.Status)
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrTaskInProgress, err.Error()))
		return
	}

	response.Success(c, taskStatus)
}

// RestoreAllInvalidKeys sets the status of all 'inactive' keys in a group to 'active'.
func (s *Server) RestoreAllInvalidKeys(c *gin.Context) {
	var req GroupIDRequest
	if _, ok := s.bindJSONAndValidateGroup(c, &req, true); !ok {
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
	if _, ok := s.bindJSONAndValidateGroup(c, &req, true); !ok {
		return
	}

	rowsAffected, err := s.KeyService.ClearAllInvalidKeys(req.GroupID)
	if err != nil {
		response.Error(c, app_errors.ParseDBError(err))
		return
	}

	response.Success(c, gin.H{"message": fmt.Sprintf("%d invalid keys cleared.", rowsAffected)})
}

// ClearAllKeys deletes all keys from a group.
func (s *Server) ClearAllKeys(c *gin.Context) {
	var req GroupIDRequest
	if _, ok := s.bindJSONAndValidateGroup(c, &req, true); !ok {
		return
	}

	rowsAffected, err := s.KeyService.ClearAllKeys(req.GroupID)
	if err != nil {
		response.Error(c, app_errors.ParseDBError(err))
		return
	}

	response.Success(c, gin.H{"message": fmt.Sprintf("%d keys cleared.", rowsAffected)})
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

	if !contains(validExportStatuses, statusFilter) {
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

// GroupStatusRequest defines a generic payload for operations requiring a group ID and status.
type GroupStatusRequest struct {
	GroupID uint   `json:"group_id" binding:"required"`
	Status  string `json:"status" binding:"required"`
}

// RestoreKeysByStatus restores keys with a specific status in a group.
func (s *Server) RestoreKeysByStatus(c *gin.Context) {
	var req GroupStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInvalidJSON, err.Error()))
		return
	}

	if !validateStatus(req.Status) {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, "Invalid status value"))
		return
	}

	if _, ok := s.findGroupByID(c, req.GroupID); !ok {
		return
	}

	rowsAffected, err := s.KeyService.RestoreKeysByStatus(req.GroupID, req.Status)
	if err != nil {
		response.Error(c, app_errors.ParseDBError(err))
		return
	}

	response.Success(c, gin.H{"message": fmt.Sprintf("%d keys restored.", rowsAffected)})
}

// ClearKeysByStatus deletes keys with a specific status in a group.
func (s *Server) ClearKeysByStatus(c *gin.Context) {
	var req GroupStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInvalidJSON, err.Error()))
		return
	}

	if !validateStatus(req.Status) {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, "Invalid status value"))
		return
	}

	if _, ok := s.findGroupByID(c, req.GroupID); !ok {
		return
	}

	rowsAffected, err := s.KeyService.ClearKeysByStatus(req.GroupID, req.Status)
	if err != nil {
		response.Error(c, app_errors.ParseDBError(err))
		return
	}

	response.Success(c, gin.H{"message": fmt.Sprintf("%d keys cleared.", rowsAffected)})
}
