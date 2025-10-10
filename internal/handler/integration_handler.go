package handler

import (
	"strings"

	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"
	"gpt-load/internal/response"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// IntegrationGroupInfo represents group info for integration response
type IntegrationGroupInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	ChannelType string `json:"channel_type"`
	Path        string `json:"path"`
}

// IntegrationInfoResponse represents the integration info response
type IntegrationInfoResponse struct {
	Code    int                    `json:"code"`
	Message string                 `json:"message"`
	Data    []IntegrationGroupInfo `json:"data"`
}

// GetIntegrationInfo handles the integration info request
func (s *Server) GetIntegrationInfo(c *gin.Context) {
	key := c.Query("key")
	if key == "" {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, "Proxy key is required"))
		return
	}

	path := c.Request.URL.Path
	isGroupSpecific := strings.HasPrefix(path, "/proxy/")

	var groupsToCheck []*models.Group

	if isGroupSpecific {
		parts := strings.Split(strings.TrimPrefix(path, "/proxy/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, "Invalid group path"))
			return
		}

		groupName := parts[0]

		// Get group from GroupManager cache (already has ProxyKeysMap parsed)
		group, err := s.GroupManager.GetGroupByName(groupName)
		if err != nil {
			response.Error(c, app_errors.NewAPIError(app_errors.ErrResourceNotFound, "Group not found"))
			return
		}

		groupsToCheck = []*models.Group{group}
	} else {
		// Get all groups
		groups, err := s.GroupService.ListGroups(c.Request.Context())
		if err != nil {
			response.Error(c, app_errors.NewAPIError(app_errors.ErrInternalServer, "Internal server error"))
			return
		}

		// Convert to pointer slice and load from cache to get ProxyKeysMap
		for i := range groups {
			cachedGroup, err := s.GroupManager.GetGroupByName(groups[i].Name)
			if err != nil {
				logrus.Warnf("Failed to get group %s from cache: %v", groups[i].Name, err)
				continue
			}
			groupsToCheck = append(groupsToCheck, cachedGroup)
		}
	}

	var result []IntegrationGroupInfo
	for _, group := range groupsToCheck {
		if hasProxyKeyPermission(group, key) {
			channelType := getEffectiveChannelType(group)
			path := buildPath(isGroupSpecific, group.Name, channelType, group.ValidationEndpoint)

			result = append(result, IntegrationGroupInfo{
				Name:        group.Name,
				DisplayName: group.DisplayName,
				ChannelType: channelType,
				Path:        path,
			})
		}
	}

	if len(result) == 0 {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrValidation, "Invalid or unauthorized proxy key"))
		return
	}

	response.Success(c, result)
}

// getEffectiveChannelType returns the effective channel type
func getEffectiveChannelType(group *models.Group) string {
	if group.ChannelType != "openai" {
		return group.ChannelType
	}

	if group.ValidationEndpoint == "" {
		return "openai"
	}

	defaultEndpoint := "/v1/chat/completions"

	if group.ValidationEndpoint == defaultEndpoint {
		return "openai"
	}

	return "custom"
}

// hasProxyKeyPermission checks if the key has permission to access the group
func hasProxyKeyPermission(group *models.Group, key string) bool {
	_, exists1 := group.ProxyKeysMap[key]
	_, exists2 := group.EffectiveConfig.ProxyKeysMap[key]
	return exists1 || exists2
}

// buildPath returns the appropriate path based on request type and channel type
func buildPath(isGroupSpecific bool, groupName string, channelType string, validationEndpoint string) string {
	if channelType == "custom" {
		if isGroupSpecific {
			return validationEndpoint
		}
		return "/proxy/" + groupName + validationEndpoint
	}

	if isGroupSpecific {
		return ""
	}
	return "/proxy/" + groupName
}
