package handler

import (
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/i18n"
	"gpt-load/internal/models"
	"gpt-load/internal/response"
	"gpt-load/internal/utils"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// GetSettings handles the GET /api/settings request.
// It retrieves all system settings, groups them by category, and returns them.
func (s *Server) GetSettings(c *gin.Context) {
	currentSettings := s.SettingsManager.GetSettings()
	settingsInfo := utils.GenerateSettingsMetadata(&currentSettings)

	// Translate settings info
	for i := range settingsInfo {
		// Translate name if it's an i18n key
		if strings.HasPrefix(settingsInfo[i].Name, "config.") {
			settingsInfo[i].Name = i18n.Message(c, settingsInfo[i].Name)
		}
		// Translate description if it's an i18n key
		if strings.HasPrefix(settingsInfo[i].Description, "config.") {
			settingsInfo[i].Description = i18n.Message(c, settingsInfo[i].Description)
		}
		// Translate category if it's an i18n key
		if strings.HasPrefix(settingsInfo[i].Category, "config.") {
			settingsInfo[i].Category = i18n.Message(c, settingsInfo[i].Category)
		}
	}

	// Group settings by category while preserving order
	categorized := make(map[string][]models.SystemSettingInfo)
	var categoryOrder []string
	for _, s := range settingsInfo {
		if _, exists := categorized[s.Category]; !exists {
			categoryOrder = append(categoryOrder, s.Category)
		}
		categorized[s.Category] = append(categorized[s.Category], s)
	}

	// Create the response structure in the correct order
	var responseData []models.CategorizedSettings
	for _, categoryName := range categoryOrder {
		responseData = append(responseData, models.CategorizedSettings{
			CategoryName: categoryName,
			Settings:     categorized[categoryName],
		})
	}

	response.Success(c, responseData)
}

// UpdateSettings handles the PUT /api/settings request.
func (s *Server) UpdateSettings(c *gin.Context) {
	var settingsMap map[string]any
	if err := c.ShouldBindJSON(&settingsMap); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInvalidJSON, err.Error()))
		return
	}

	if len(settingsMap) == 0 {
		response.Success(c, nil)
		return
	}

	// Sanitize proxy_keys input
	if proxyKeys, ok := settingsMap["proxy_keys"]; ok {
		if proxyKeysStr, ok := proxyKeys.(string); ok {
			cleanedKeys := utils.SplitAndTrim(proxyKeysStr, ",")
			settingsMap["proxy_keys"] = strings.Join(cleanedKeys, ",")
		}
	}

	// 更新配置
	if err := s.SettingsManager.UpdateSettings(settingsMap); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrDatabase, err.Error()))
		return
	}

	time.Sleep(100 * time.Millisecond) // 等待异步更新配置

	response.SuccessI18n(c, "settings.update_success", nil)
}
