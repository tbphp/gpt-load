// Package handler provides HTTP handlers for the application
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"
	"gpt-load/internal/response"
	"gpt-load/internal/utils"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gpt-load/internal/channel"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gorm.io/datatypes"
)

// isValidChannelType checks if the channel type is valid by checking against the registered channels.
func isValidChannelType(channelType string) bool {
	channels := channel.GetChannels()
	for _, t := range channels {
		if t == channelType {
			return true
		}
	}
	return false
}

// UpstreamDefinition defines the structure for an upstream in the request.
type UpstreamDefinition struct {
	URL    string `json:"url"`
	Weight int    `json:"weight"`
}

// validateAndCleanUpstreams validates and cleans the upstreams JSON.
func validateAndCleanUpstreams(upstreams json.RawMessage) (datatypes.JSON, error) {
	if len(upstreams) == 0 {
		return nil, fmt.Errorf("upstreams field is required")
	}

	var defs []UpstreamDefinition
	if err := json.Unmarshal(upstreams, &defs); err != nil {
		return nil, fmt.Errorf("invalid format for upstreams: %w", err)
	}

	if len(defs) == 0 {
		return nil, fmt.Errorf("at least one upstream is required")
	}

	hasActiveUpstream := false
	for i := range defs {
		defs[i].URL = strings.TrimSpace(defs[i].URL)
		if defs[i].URL == "" {
			return nil, fmt.Errorf("upstream URL cannot be empty")
		}
		// Basic URL format validation
		if !strings.HasPrefix(defs[i].URL, "http://") && !strings.HasPrefix(defs[i].URL, "https://") {
			return nil, fmt.Errorf("invalid URL format for upstream: %s", defs[i].URL)
		}
		if defs[i].Weight < 0 {
			return nil, fmt.Errorf("upstream weight must be a non-negative integer")
		}
		if defs[i].Weight > 0 {
			hasActiveUpstream = true
		}
	}

	if !hasActiveUpstream {
		return nil, fmt.Errorf("at least one upstream must have a weight greater than 0")
	}

	cleanedUpstreams, err := json.Marshal(defs)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cleaned upstreams: %w", err)
	}

	return cleanedUpstreams, nil
}

// isValidGroupName checks if the group name is valid.
func isValidGroupName(name string) bool {
	if name == "" {
		return false
	}
	// 允许使用小写字母、数字、下划线和中划线，长度在 3 到 30 个字符之间
	match, _ := regexp.MatchString("^[a-z0-9_-]{3,30}$", name)
	return match
}

// isValidValidationEndpoint checks if the validation endpoint is a valid path.
func isValidValidationEndpoint(endpoint string) bool {
	if endpoint == "" {
		return true
	}
	if !strings.HasPrefix(endpoint, "/") {
		return false
	}
	if strings.Contains(endpoint, "://") {
		return false
	}
	return true
}

// validateAndCleanConfig validates the group config against the GroupConfig struct and system-defined rules.
func (s *Server) validateAndCleanConfig(configMap map[string]any) (map[string]any, error) {
	if configMap == nil {
		return nil, nil
	}

	// 1. Check for unknown fields by comparing against the GroupConfig struct definition.
	var tempGroupConfig models.GroupConfig
	groupConfigType := reflect.TypeOf(tempGroupConfig)
	validFields := make(map[string]bool)
	for i := 0; i < groupConfigType.NumField(); i++ {
		jsonTag := groupConfigType.Field(i).Tag.Get("json")
		fieldName := strings.Split(jsonTag, ",")[0]
		if fieldName != "" && fieldName != "-" {
			validFields[fieldName] = true
		}
	}

	for key := range configMap {
		if !validFields[key] {
			return nil, fmt.Errorf("unknown config field: '%s'", key)
		}
	}

	// 2. Validate the values of the provided fields using the central system settings validator.
	if err := s.SettingsManager.ValidateGroupConfigOverrides(configMap); err != nil {
		return nil, err
	}

	// 3. Unmarshal and marshal back to clean the map and ensure correct types.
	configBytes, err := json.Marshal(configMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config map: %w", err)
	}

	var validatedConfig models.GroupConfig
	if err := json.Unmarshal(configBytes, &validatedConfig); err != nil {
		return nil, fmt.Errorf("failed to unmarshal into validated config: %w", err)
	}

	validatedBytes, err := json.Marshal(validatedConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal validated config: %w", err)
	}
	var finalMap map[string]any
	if err := json.Unmarshal(validatedBytes, &finalMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal into final map: %w", err)
	}

	return finalMap, nil
}

// GroupCreateRequest defines the payload for creating a group.
type GroupCreateRequest struct {
	Name               string              `json:"name"`
	DisplayName        string              `json:"display_name"`
	Description        string              `json:"description"`
	Upstreams          json.RawMessage     `json:"upstreams"`
	ChannelType        string              `json:"channel_type"`
	Sort               int                 `json:"sort"`
	TestModel          string              `json:"test_model"`
	ValidationEndpoint string              `json:"validation_endpoint"`
	ParamOverrides     map[string]any      `json:"param_overrides"`
	Config             map[string]any      `json:"config"`
	HeaderRules        []models.HeaderRule `json:"header_rules"`
	ProxyKeys          string              `json:"proxy_keys"`
}

// CreateGroup handles the creation of a new group.
func (s *Server) CreateGroup(c *gin.Context) {
	var req GroupCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInvalidJSON, err.Error()))
		return
	}

	// Data Cleaning and Validation
	name := strings.TrimSpace(req.Name)
	if !isValidGroupName(name) {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, "无效的分组名称。只能包含小写字母、数字、中划线或下划线，长度3-30位"))
		return
	}

	channelType := strings.TrimSpace(req.ChannelType)
	if !isValidChannelType(channelType) {
		supported := strings.Join(channel.GetChannels(), ", ")
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, fmt.Sprintf("Invalid channel type. Supported types are: %s", supported)))
		return
	}

	testModel := strings.TrimSpace(req.TestModel)
	if testModel == "" {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, "Test model is required"))
		return
	}

	cleanedUpstreams, err := validateAndCleanUpstreams(req.Upstreams)
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
		return
	}

	cleanedConfig, err := s.validateAndCleanConfig(req.Config)
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, fmt.Sprintf("Invalid config format: %v", err)))
		return
	}

	validationEndpoint := strings.TrimSpace(req.ValidationEndpoint)
	if !isValidValidationEndpoint(validationEndpoint) {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, "无效的测试路径。如果提供，必须是以 / 开头的有效路径，且不能是完整的URL。"))
		return
	}

	// Validate and normalize header rules if provided
	var headerRulesJSON datatypes.JSON
	if len(req.HeaderRules) > 0 {
		normalizedHeaderRules := make([]models.HeaderRule, 0)
		seenKeys := make(map[string]bool)

		for _, rule := range req.HeaderRules {
			key := strings.TrimSpace(rule.Key)
			if key == "" {
				continue
			}

			// Normalize to canonical form
			canonicalKey := http.CanonicalHeaderKey(key)

			// Check for duplicate keys
			if seenKeys[canonicalKey] {
				response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, fmt.Sprintf("Duplicate header key: %s", canonicalKey)))
				return
			}
			seenKeys[canonicalKey] = true

			normalizedHeaderRules = append(normalizedHeaderRules, models.HeaderRule{
				Key:    canonicalKey,
				Value:  rule.Value,
				Action: rule.Action,
			})
		}

		if len(normalizedHeaderRules) > 0 {
			headerRulesBytes, err := json.Marshal(normalizedHeaderRules)
			if err != nil {
				response.Error(c, app_errors.NewAPIError(app_errors.ErrInternalServer, fmt.Sprintf("Failed to process header rules: %v", err)))
				return
			}
			headerRulesJSON = headerRulesBytes
		}
	}
	if headerRulesJSON == nil {
		headerRulesJSON = datatypes.JSON("[]")
	}

	group := models.Group{
		Name:               name,
		DisplayName:        strings.TrimSpace(req.DisplayName),
		Description:        strings.TrimSpace(req.Description),
		Upstreams:          cleanedUpstreams,
		ChannelType:        channelType,
		Sort:               req.Sort,
		TestModel:          testModel,
		ValidationEndpoint: validationEndpoint,
		ParamOverrides:     req.ParamOverrides,
		Config:             cleanedConfig,
		HeaderRules:        headerRulesJSON,
		ProxyKeys:          strings.TrimSpace(req.ProxyKeys),
	}

	if err := s.DB.Create(&group).Error; err != nil {
		response.Error(c, app_errors.ParseDBError(err))
		return
	}

	if err := s.GroupManager.Invalidate(); err != nil {
		logrus.WithContext(c.Request.Context()).WithError(err).Error("failed to invalidate group cache")
	}
	response.Success(c, s.newGroupResponse(&group))
}

// ListGroups handles listing all groups.
func (s *Server) ListGroups(c *gin.Context) {
	var groups []models.Group
	if err := s.DB.Order("sort asc, id desc").Find(&groups).Error; err != nil {
		response.Error(c, app_errors.ParseDBError(err))
		return
	}

	var groupResponses []GroupResponse
	for i := range groups {
		groupResponses = append(groupResponses, *s.newGroupResponse(&groups[i]))
	}

	response.Success(c, groupResponses)
}

// GroupUpdateRequest defines the payload for updating a group.
// Using a dedicated struct avoids issues with zero values being ignored by GORM's Update.
type GroupUpdateRequest struct {
	Name               *string             `json:"name,omitempty"`
	DisplayName        *string             `json:"display_name,omitempty"`
	Description        *string             `json:"description,omitempty"`
	Upstreams          json.RawMessage     `json:"upstreams"`
	ChannelType        *string             `json:"channel_type,omitempty"`
	Sort               *int                `json:"sort"`
	TestModel          string              `json:"test_model"`
	ValidationEndpoint *string             `json:"validation_endpoint,omitempty"`
	ParamOverrides     map[string]any      `json:"param_overrides"`
	Config             map[string]any      `json:"config"`
	HeaderRules        []models.HeaderRule `json:"header_rules"`
	ProxyKeys          *string             `json:"proxy_keys,omitempty"`
}

// UpdateGroup handles updating an existing group.
func (s *Server) UpdateGroup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrBadRequest, "Invalid group ID format"))
		return
	}

	var group models.Group
	if err := s.DB.First(&group, id).Error; err != nil {
		response.Error(c, app_errors.ParseDBError(err))
		return
	}

	var req GroupUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInvalidJSON, err.Error()))
		return
	}

	// Start a transaction
	tx := s.DB.Begin()
	if tx.Error != nil {
		response.Error(c, app_errors.ErrDatabase)
		return
	}
	defer tx.Rollback() // Rollback on panic

	// Apply updates from the request, with cleaning and validation
	if req.Name != nil {
		cleanedName := strings.TrimSpace(*req.Name)
		if !isValidGroupName(cleanedName) {
			response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, "无效的分组名称格式。只能包含小写字母、数字、中划线或下划线，长度3-30位"))
			return
		}
		group.Name = cleanedName
	}

	if req.DisplayName != nil {
		group.DisplayName = strings.TrimSpace(*req.DisplayName)
	}

	if req.Description != nil {
		group.Description = strings.TrimSpace(*req.Description)
	}

	if req.Upstreams != nil {
		cleanedUpstreams, err := validateAndCleanUpstreams(req.Upstreams)
		if err != nil {
			response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, err.Error()))
			return
		}
		group.Upstreams = cleanedUpstreams
	}

	if req.ChannelType != nil {
		cleanedChannelType := strings.TrimSpace(*req.ChannelType)
		if !isValidChannelType(cleanedChannelType) {
			supported := strings.Join(channel.GetChannels(), ", ")
			response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, fmt.Sprintf("Invalid channel type. Supported types are: %s", supported)))
			return
		}
		group.ChannelType = cleanedChannelType
	}
	if req.Sort != nil {
		group.Sort = *req.Sort
	}
	if req.TestModel != "" {
		cleanedTestModel := strings.TrimSpace(req.TestModel)
		if cleanedTestModel == "" {
			response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, "Test model cannot be empty or just spaces."))
			return
		}
		group.TestModel = cleanedTestModel
	}
	if req.ParamOverrides != nil {
		group.ParamOverrides = req.ParamOverrides
	}
	if req.ValidationEndpoint != nil {
		validationEndpoint := strings.TrimSpace(*req.ValidationEndpoint)
		if !isValidValidationEndpoint(validationEndpoint) {
			response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, "无效的测试路径。如果提供，必须是以 / 开头的有效路径，且不能是完整的URL。"))
			return
		}
		group.ValidationEndpoint = validationEndpoint
	}

	if req.Config != nil {
		cleanedConfig, err := s.validateAndCleanConfig(req.Config)
		if err != nil {
			response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, fmt.Sprintf("Invalid config format: %v", err)))
			return
		}
		group.Config = cleanedConfig
	}

	if req.ProxyKeys != nil {
		group.ProxyKeys = strings.TrimSpace(*req.ProxyKeys)
	}

	// Handle header rules update
	if req.HeaderRules != nil {
		var headerRulesJSON datatypes.JSON
		normalizedHeaderRules := make([]models.HeaderRule, 0)
		seenKeys := make(map[string]bool)

		for _, rule := range req.HeaderRules {
			key := strings.TrimSpace(rule.Key)
			if key == "" {
				continue
			}

			// Normalize to canonical form
			canonicalKey := http.CanonicalHeaderKey(key)

			// Check for duplicate keys
			if seenKeys[canonicalKey] {
				response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, fmt.Sprintf("Duplicate header key: %s", canonicalKey)))
				return
			}
			seenKeys[canonicalKey] = true

			normalizedHeaderRules = append(normalizedHeaderRules, models.HeaderRule{
				Key:    canonicalKey,
				Value:  rule.Value,
				Action: rule.Action,
			})
		}

		if len(normalizedHeaderRules) > 0 {
			headerRulesBytes, err := json.Marshal(normalizedHeaderRules)
			if err != nil {
				response.Error(c, app_errors.NewAPIError(app_errors.ErrInternalServer, fmt.Sprintf("Failed to process header rules: %v", err)))
				return
			}
			headerRulesJSON = headerRulesBytes
		} else {
			headerRulesJSON = datatypes.JSON("[]")
		}
		group.HeaderRules = headerRulesJSON
	}

	// Save the updated group object
	if err := tx.Save(&group).Error; err != nil {
		response.Error(c, app_errors.ParseDBError(err))
		return
	}

	if err := tx.Commit().Error; err != nil {
		response.Error(c, app_errors.ErrDatabase)
		return
	}

	if err := s.GroupManager.Invalidate(); err != nil {
		logrus.WithContext(c.Request.Context()).WithError(err).Error("failed to invalidate group cache")
	}
	response.Success(c, s.newGroupResponse(&group))
}

// GroupResponse defines the structure for a group response, excluding sensitive or large fields.
type GroupResponse struct {
	ID                 uint                `json:"id"`
	Name               string              `json:"name"`
	Endpoint           string              `json:"endpoint"`
	DisplayName        string              `json:"display_name"`
	Description        string              `json:"description"`
	Upstreams          datatypes.JSON      `json:"upstreams"`
	ChannelType        string              `json:"channel_type"`
	Sort               int                 `json:"sort"`
	TestModel          string              `json:"test_model"`
	ValidationEndpoint string              `json:"validation_endpoint"`
	ParamOverrides     datatypes.JSONMap   `json:"param_overrides"`
	Config             datatypes.JSONMap   `json:"config"`
	HeaderRules        []models.HeaderRule `json:"header_rules"`
	ProxyKeys          string              `json:"proxy_keys"`
	LastValidatedAt    *time.Time          `json:"last_validated_at"`
	CreatedAt          time.Time           `json:"created_at"`
	UpdatedAt          time.Time           `json:"updated_at"`
}

// newGroupResponse creates a new GroupResponse from a models.Group.
func (s *Server) newGroupResponse(group *models.Group) *GroupResponse {
	appURL := s.SettingsManager.GetAppUrl()
	endpoint := ""
	if appURL != "" {
		u, err := url.Parse(appURL)
		if err == nil {
			u.Path = strings.TrimRight(u.Path, "/") + "/proxy/" + group.Name
			endpoint = u.String()
		}
	}

	// Parse header rules from JSON
	var headerRules []models.HeaderRule
	if len(group.HeaderRules) > 0 {
		if err := json.Unmarshal(group.HeaderRules, &headerRules); err != nil {
			logrus.WithError(err).Error("Failed to unmarshal header rules")
			headerRules = make([]models.HeaderRule, 0)
		}
	}

	return &GroupResponse{
		ID:                 group.ID,
		Name:               group.Name,
		Endpoint:           endpoint,
		DisplayName:        group.DisplayName,
		Description:        group.Description,
		Upstreams:          group.Upstreams,
		ChannelType:        group.ChannelType,
		Sort:               group.Sort,
		TestModel:          group.TestModel,
		ValidationEndpoint: group.ValidationEndpoint,
		ParamOverrides:     group.ParamOverrides,
		Config:             group.Config,
		HeaderRules:        headerRules,
		ProxyKeys:          group.ProxyKeys,
		LastValidatedAt:    group.LastValidatedAt,
		CreatedAt:          group.CreatedAt,
		UpdatedAt:          group.UpdatedAt,
	}
}

// DeleteGroup handles deleting a group.
func (s *Server) DeleteGroup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrBadRequest, "Invalid group ID format"))
		return
	}

	// First, get all API keys for this group to clean up from memory store
	var apiKeys []models.APIKey
	if err := s.DB.Where("group_id = ?", id).Find(&apiKeys).Error; err != nil {
		response.Error(c, app_errors.ParseDBError(err))
		return
	}

	// Extract key IDs for memory store cleanup
	var keyIDs []uint
	for _, key := range apiKeys {
		keyIDs = append(keyIDs, key.ID)
	}

	// Use a transaction to ensure atomicity
	tx := s.DB.Begin()
	if tx.Error != nil {
		response.Error(c, app_errors.ErrDatabase)
		return
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// First check if the group exists
	var group models.Group
	if err := tx.First(&group, id).Error; err != nil {
		tx.Rollback()
		response.Error(c, app_errors.ParseDBError(err))
		return
	}

	// Delete associated API keys first due to foreign key constraint
	if err := tx.Where("group_id = ?", id).Delete(&models.APIKey{}).Error; err != nil {
		tx.Rollback()
		response.Error(c, app_errors.ErrDatabase)
		return
	}

	// Then delete the group
	if err := tx.Delete(&models.Group{}, id).Error; err != nil {
		tx.Rollback()
		response.Error(c, app_errors.ParseDBError(err))
		return
	}

	// Clean up memory store (Redis) within the transaction to ensure atomicity
	// If Redis cleanup fails, the entire transaction will be rolled back
	if len(keyIDs) > 0 {
		if err := s.KeyService.KeyProvider.RemoveKeysFromStore(uint(id), keyIDs); err != nil {
			tx.Rollback()
			logrus.WithFields(logrus.Fields{
				"groupID":  id,
				"keyCount": len(keyIDs),
				"error":    err,
			}).Error("Failed to remove keys from memory store, rolling back transaction")

			response.Error(c, app_errors.NewAPIError(app_errors.ErrDatabase,
				"Failed to delete group: unable to clean up cache"))
			return
		}
	}

	// Commit the transaction only if both DB and Redis operations succeed
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		response.Error(c, app_errors.ErrDatabase)
		return
	}

	if err := s.GroupManager.Invalidate(); err != nil {
		logrus.WithContext(c.Request.Context()).WithError(err).Error("failed to invalidate group cache")
	}
	response.Success(c, gin.H{"message": "Group and associated keys deleted successfully"})
}

// ConfigOption represents a single configurable option for a group.
type ConfigOption struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	DefaultValue any    `json:"default_value"`
}

// GetGroupConfigOptions returns a list of available configuration options for groups.
func (s *Server) GetGroupConfigOptions(c *gin.Context) {
	var options []ConfigOption

	// 1. Get all system setting definitions from the struct tags
	defaultSettings := utils.DefaultSystemSettings()
	settingDefinitions := utils.GenerateSettingsMetadata(&defaultSettings)
	defMap := make(map[string]models.SystemSettingInfo)
	for _, def := range settingDefinitions {
		defMap[def.Key] = def
	}

	// 2. Get current system setting values
	currentSettings := s.SettingsManager.GetSettings()
	currentSettingsValue := reflect.ValueOf(currentSettings)
	currentSettingsType := currentSettingsValue.Type()
	jsonToFieldMap := make(map[string]string)
	for i := 0; i < currentSettingsType.NumField(); i++ {
		field := currentSettingsType.Field(i)
		jsonTag := strings.Split(field.Tag.Get("json"), ",")[0]
		if jsonTag != "" {
			jsonToFieldMap[jsonTag] = field.Name
		}
	}

	// 3. Iterate over GroupConfig fields to maintain order and build the response
	groupConfigType := reflect.TypeOf(models.GroupConfig{})

	for i := 0; i < groupConfigType.NumField(); i++ {
		field := groupConfigType.Field(i)
		jsonTag := field.Tag.Get("json")
		key := strings.Split(jsonTag, ",")[0]

		if key == "" || key == "-" {
			continue
		}

		if definition, ok := defMap[key]; ok {
			var defaultValue any
			if fieldName, ok := jsonToFieldMap[key]; ok {
				defaultValue = currentSettingsValue.FieldByName(fieldName).Interface()
			}

			option := ConfigOption{
				Key:          key,
				Name:         definition.Name,
				Description:  definition.Description,
				DefaultValue: defaultValue,
			}
			options = append(options, option)
		}
	}

	response.Success(c, options)
}

// KeyStats defines the statistics for API keys in a group.
type KeyStats struct {
	TotalKeys   int64 `json:"total_keys"`
	ActiveKeys  int64 `json:"active_keys"`
	InvalidKeys int64 `json:"invalid_keys"`
}

// RequestStats defines the statistics for requests over a period.
type RequestStats struct {
	TotalRequests  int64   `json:"total_requests"`
	FailedRequests int64   `json:"failed_requests"`
	FailureRate    float64 `json:"failure_rate"`
}

// GroupStatsResponse defines the complete statistics for a group.
type GroupStatsResponse struct {
	KeyStats    KeyStats     `json:"key_stats"`
	HourlyStats RequestStats `json:"hourly_stats"` // 1 hour
	DailyStats  RequestStats `json:"daily_stats"`  // 24 hours
	WeeklyStats RequestStats `json:"weekly_stats"` // 7 days
}

// calculateRequestStats is a helper to compute request statistics.
func calculateRequestStats(total, failed int64) RequestStats {
	stats := RequestStats{
		TotalRequests:  total,
		FailedRequests: failed,
	}
	if total > 0 {
		stats.FailureRate, _ = strconv.ParseFloat(fmt.Sprintf("%.4f", float64(failed)/float64(total)), 64)
	}
	return stats
}

// GetGroupStats handles retrieving detailed statistics for a specific group.
func (s *Server) GetGroupStats(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrBadRequest, "Invalid group ID format"))
		return
	}
	groupID := uint(id)

	// 1. 验证分组是否存在
	var group models.Group
	if err := s.DB.First(&group, groupID).Error; err != nil {
		response.Error(c, app_errors.ParseDBError(err))
		return
	}

	var resp GroupStatsResponse
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errors []error

	// 并发执行所有统计查询

	// 2. Key 统计
	wg.Add(1)
	go func() {
		defer wg.Done()
		var totalKeys, activeKeys int64

		if err := s.DB.Model(&models.APIKey{}).Where("group_id = ?", groupID).Count(&totalKeys).Error; err != nil {
			mu.Lock()
			errors = append(errors, fmt.Errorf("failed to get total keys: %w", err))
			mu.Unlock()
			return
		}
		if err := s.DB.Model(&models.APIKey{}).Where("group_id = ? AND status = ?", groupID, models.KeyStatusActive).Count(&activeKeys).Error; err != nil {
			mu.Lock()
			errors = append(errors, fmt.Errorf("failed to get active keys: %w", err))
			mu.Unlock()
			return
		}

		mu.Lock()
		resp.KeyStats = KeyStats{
			TotalKeys:   totalKeys,
			ActiveKeys:  activeKeys,
			InvalidKeys: totalKeys - activeKeys,
		}
		mu.Unlock()
	}()

	// 3. 1小时请求统计 (查询 request_logs 表)
	wg.Add(1)
	go func() {
		defer wg.Done()
		var total, failed int64
		now := time.Now()
		oneHourAgo := now.Add(-1 * time.Hour)

		if err := s.DB.Model(&models.RequestLog{}).Where("group_id = ? AND timestamp BETWEEN ? AND ? AND request_type = ?", groupID, oneHourAgo, now, models.RequestTypeFinal).Count(&total).Error; err != nil {
			mu.Lock()
			errors = append(errors, fmt.Errorf("failed to get hourly total requests: %w", err))
			mu.Unlock()
			return
		}
		if err := s.DB.Model(&models.RequestLog{}).Where("group_id = ? AND timestamp BETWEEN ? AND ? AND is_success = ? AND request_type = ?", groupID, oneHourAgo, now, false, models.RequestTypeFinal).Count(&failed).Error; err != nil {
			mu.Lock()
			errors = append(errors, fmt.Errorf("failed to get hourly failed requests: %w", err))
			mu.Unlock()
			return
		}

		mu.Lock()
		resp.HourlyStats = calculateRequestStats(total, failed)
		mu.Unlock()
	}()

	// 4. 24小时和7天统计 (查询 group_hourly_stats 表)
	// 辅助函数，用于从 group_hourly_stats 查询
	queryHourlyStats := func(duration time.Duration) (RequestStats, error) {
		var result struct {
			SuccessCount int64
			FailureCount int64
		}
		now := time.Now()
		// 结束时间为当前小时的整点，查询时不包含该小时
		// 开始时间为结束时间减去统计周期
		endTime := now.Truncate(time.Hour)
		startTime := endTime.Add(-duration)

		err := s.DB.Model(&models.GroupHourlyStat{}).
			Select("SUM(success_count) as success_count, SUM(failure_count) as failure_count").
			Where("group_id = ? AND time >= ? AND time < ?", groupID, startTime, endTime).
			Scan(&result).Error
		if err != nil {
			return RequestStats{}, err
		}
		return calculateRequestStats(result.SuccessCount+result.FailureCount, result.FailureCount), nil
	}

	// 24小时统计
	wg.Add(1)
	go func() {
		defer wg.Done()
		stats, err := queryHourlyStats(24 * time.Hour)
		if err != nil {
			mu.Lock()
			errors = append(errors, fmt.Errorf("failed to get daily stats: %w", err))
			mu.Unlock()
			return
		}
		mu.Lock()
		resp.DailyStats = stats
		mu.Unlock()
	}()

	// 7天统计
	wg.Add(1)
	go func() {
		defer wg.Done()
		stats, err := queryHourlyStats(7 * 24 * time.Hour)
		if err != nil {
			mu.Lock()
			errors = append(errors, fmt.Errorf("failed to get weekly stats: %w", err))
			mu.Unlock()
			return
		}
		mu.Lock()
		resp.WeeklyStats = stats
		mu.Unlock()
	}()

	wg.Wait()

	if len(errors) > 0 {
		// 只记录第一个错误，但表明可能存在多个错误
		logrus.WithContext(c.Request.Context()).WithError(errors[0]).Error("Errors occurred while fetching group stats")
		response.Error(c, app_errors.NewAPIError(app_errors.ErrDatabase, "Failed to retrieve some statistics"))
		return
	}

	response.Success(c, resp)
}

// GroupCopyRequest defines the payload for copying a group.
type GroupCopyRequest struct {
	CopyKeys string `json:"copy_keys"` // "none"|"valid_only"|"all"
}

// GroupCopyResponse defines the response for group copy operation.
type GroupCopyResponse struct {
	Group *GroupResponse `json:"group"`
}

// generateUniqueGroupName generates a unique group name by appending _copy and numbers if needed.
func (s *Server) generateUniqueGroupName(baseName string) string {
	var groups []models.Group
	if err := s.DB.Select("name").Find(&groups).Error; err != nil {
		return baseName + "_copy"
	}

	// Create a map of existing names for quick lookup
	existingNames := make(map[string]bool)
	for _, group := range groups {
		existingNames[group.Name] = true
	}

	// Try base name with _copy suffix first
	copyName := baseName + "_copy"
	if !existingNames[copyName] {
		return copyName
	}

	// Try appending numbers to _copy suffix
	for i := 2; i <= 1000; i++ {
		candidate := fmt.Sprintf("%s_copy_%d", baseName, i)
		if !existingNames[candidate] {
			return candidate
		}
	}

	return copyName
}

// CopyGroup handles copying a group with optional content.
func (s *Server) CopyGroup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrBadRequest, "Invalid group ID format"))
		return
	}
	sourceGroupID := uint(id)

	var req GroupCopyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInvalidJSON, err.Error()))
		return
	}

	// Validate copy keys option
	if req.CopyKeys != "" && req.CopyKeys != "none" && req.CopyKeys != "valid_only" && req.CopyKeys != "all" {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, "Invalid copy_keys value. Must be 'none', 'valid_only', or 'all'"))
		return
	}
	if req.CopyKeys == "" {
		req.CopyKeys = "all"
	}

	// Check if source group exists
	var sourceGroup models.Group
	if err := s.DB.First(&sourceGroup, sourceGroupID).Error; err != nil {
		response.Error(c, app_errors.ParseDBError(err))
		return
	}

	// Start transaction
	tx := s.DB.Begin()
	if tx.Error != nil {
		response.Error(c, app_errors.ErrDatabase)
		return
	}
	defer tx.Rollback()

	// Create new group by copying source group and overriding specific fields
	newGroup := sourceGroup
	newGroup.ID = 0
	newGroup.Name = s.generateUniqueGroupName(sourceGroup.Name)
	if sourceGroup.DisplayName != "" {
		newGroup.DisplayName = sourceGroup.DisplayName + " Copy"
	}
	newGroup.CreatedAt = time.Time{}
	newGroup.UpdatedAt = time.Time{}
	newGroup.LastValidatedAt = nil

	// Create the new group
	if err := tx.Create(&newGroup).Error; err != nil {
		response.Error(c, app_errors.ParseDBError(err))
		return
	}

	// Prepare key data for async import task
	var sourceKeyValues []string

	if req.CopyKeys != "none" {
		var sourceKeys []models.APIKey
		query := tx.Where("group_id = ?", sourceGroupID)

		// Filter by status if only copying valid keys
		if req.CopyKeys == "valid_only" {
			query = query.Where("status = ?", models.KeyStatusActive)
		}

		if err := query.Find(&sourceKeys).Error; err != nil {
			response.Error(c, app_errors.ParseDBError(err))
			return
		}

		// Extract key values for async import task
		for _, sourceKey := range sourceKeys {
			sourceKeyValues = append(sourceKeyValues, sourceKey.KeyValue)
		}
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		response.Error(c, app_errors.ErrDatabase)
		return
	}

	// Update caches after successful transaction
	if err := s.GroupManager.Invalidate(); err != nil {
		logrus.WithContext(c.Request.Context()).WithError(err).Error("failed to invalidate group cache")
	}

	// Start async key import task if there are keys to copy (reuse existing logic)
	if len(sourceKeyValues) > 0 {
		// Convert key values array to text format expected by KeyImportService
		keysText := strings.Join(sourceKeyValues, "\n")

		// Directly reuse the AddMultipleKeysAsync logic from key_handler.go
		if _, err := s.KeyImportService.StartImportTask(&newGroup, keysText); err != nil {
			logrus.WithFields(logrus.Fields{
				"groupId":  newGroup.ID,
				"keyCount": len(sourceKeyValues),
				"error":    err,
			}).Error("Failed to start async key import task for group copy")
		} else {
			logrus.WithFields(logrus.Fields{
				"groupId":  newGroup.ID,
				"keyCount": len(sourceKeyValues),
			}).Info("Started async key import task for group copy")
		}
	}

	// Prepare response
	groupResponse := s.newGroupResponse(&newGroup)
	copyResponse := &GroupCopyResponse{
		Group: groupResponse,
	}

	response.Success(c, copyResponse)
}

// List godoc
func (s *Server) List(c *gin.Context) {
	var groups []models.Group
	if err := s.DB.Select("id, name,display_name").Find(&groups).Error; err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrDatabase, "无法获取分组列表"))
		return
	}
	response.Success(c, groups)
}
