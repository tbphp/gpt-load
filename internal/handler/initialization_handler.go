package handler

import (
	"net/http"

	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/response"
	"gpt-load/internal/services"
	"gpt-load/internal/types"
	"gpt-load/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// InitializationHandler handles initialization-related requests
type InitializationHandler struct {
	initService   *services.InitializationService
	configManager types.ConfigManager
}

// NewInitializationHandler creates a new initialization handler
func NewInitializationHandler(initService *services.InitializationService, configManager types.ConfigManager) *InitializationHandler {
	return &InitializationHandler{
		initService:   initService,
		configManager: configManager,
	}
}

// SetupStatusRequest represents the setup status check request
type SetupStatusResponse struct {
	IsFirstTimeSetup bool   `json:"is_first_time_setup"`
	SetupMode        bool   `json:"setup_mode"`
	Message          string `json:"message"`
}

// InitialPasswordRequest represents the initial password setup request
type InitialPasswordRequest struct {
	Password        string `json:"password" binding:"required,min=8"`
	ConfirmPassword string `json:"confirm_password" binding:"required"`
}

// PasswordStrengthRequest represents password strength check request
type PasswordStrengthRequest struct {
	Password string `json:"password" binding:"required"`
}

// CheckSetupStatus checks if the system needs initial setup
func (h *InitializationHandler) CheckSetupStatus(c *gin.Context) {
	isFirstTime, err := h.initService.IsFirstTimeSetup()
	if err != nil {
		logrus.Errorf("Failed to check setup status: %v", err)
		response.Error(c, app_errors.NewAPIError(app_errors.ErrInternalServer, err.Error()))
		return
	}

	var message string
	if isFirstTime {
		message = "系统需要初始化设置，请设置管理员密码"
	} else {
		message = "系统已完成初始化"
	}

	// Check if we're in setup mode (temporary auth key)
	authConfig := h.configManager.GetAuthConfig()
	setupMode := authConfig.Key == "__SETUP_MODE__"

	c.JSON(http.StatusOK, SetupStatusResponse{
		IsFirstTimeSetup: isFirstTime,
		SetupMode:        setupMode,
		Message:          message,
	})
}

// CheckPasswordStrength checks the strength of a password
func (h *InitializationHandler) CheckPasswordStrength(c *gin.Context) {
	var req PasswordStrengthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrBadRequest, err.Error()))
		return
	}

	validation := utils.CheckPasswordStrength(req.Password)
	c.JSON(http.StatusOK, validation)
}

// SetInitialPassword sets the initial admin password
func (h *InitializationHandler) SetInitialPassword(c *gin.Context) {
	// Check if we're in setup mode
	authConfig := h.configManager.GetAuthConfig()
	if authConfig.Key != "__SETUP_MODE__" {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "系统不在初始化模式，无法设置初始密码",
		})
		return
	}

	var req InitialPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, app_errors.NewAPIError(app_errors.ErrBadRequest, err.Error()))
		return
	}

	// Check password confirmation
	if req.Password != req.ConfirmPassword {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "两次输入的密码不一致",
		})
		return
	}

	// Set initial password
	if err := h.initService.SetInitialPassword(req.Password); err != nil {
		logrus.Errorf("Failed to set initial password: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// Get the newly set auth key and update config manager
	authKey, err := h.initService.GetAuthKey()
	if err != nil {
		logrus.Errorf("Failed to get auth key after setting: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "密码设置成功，但获取认证密钥失败",
		})
		return
	}

	// Update config manager with the real auth key
	h.configManager.SetAuthKey(authKey)

	logrus.Info("初始密码设置成功，系统退出初始化模式")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "管理员密码设置成功",
	})
}
