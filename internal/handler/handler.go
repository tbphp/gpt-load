// Package handler provides HTTP handlers for the application
package handler

import (
	"crypto/subtle"
	"net/http"

	"gpt-load/internal/utils"
	"time"

	"gpt-load/internal/config"
	"gpt-load/internal/services"
	"gpt-load/internal/types"

	"github.com/gin-gonic/gin"
	"go.uber.org/dig"
	"gorm.io/gorm"
)

// Server contains dependencies for HTTP handlers
type Server struct {
	DB                         *gorm.DB
	config                     types.ConfigManager
	SettingsManager            *config.SystemSettingsManager
	GroupManager               *services.GroupManager
	KeyManualValidationService *services.KeyManualValidationService
	TaskService                *services.TaskService
	KeyService                 *services.KeyService
	KeyImportService           *services.KeyImportService
	KeyDeleteService           *services.KeyDeleteService
	LogService                 *services.LogService
	CommonHandler              *CommonHandler
}

// NewServerParams defines the dependencies for the NewServer constructor.
type NewServerParams struct {
	dig.In
	DB                         *gorm.DB
	Config                     types.ConfigManager
	SettingsManager            *config.SystemSettingsManager
	GroupManager               *services.GroupManager
	KeyManualValidationService *services.KeyManualValidationService
	TaskService                *services.TaskService
	KeyService                 *services.KeyService
	KeyImportService           *services.KeyImportService
	KeyDeleteService           *services.KeyDeleteService
	LogService                 *services.LogService
	CommonHandler              *CommonHandler
}

// NewServer creates a new handler instance with dependencies injected by dig.
func NewServer(params NewServerParams) *Server {
	return &Server{
		DB:                         params.DB,
		config:                     params.Config,
		SettingsManager:            params.SettingsManager,
		GroupManager:               params.GroupManager,
		KeyManualValidationService: params.KeyManualValidationService,
		TaskService:                params.TaskService,
		KeyService:                 params.KeyService,
		KeyImportService:           params.KeyImportService,
		KeyDeleteService:           params.KeyDeleteService,
		LogService:                 params.LogService,
		CommonHandler:              params.CommonHandler,
	}
}

// LoginRequest represents the login request payload
type LoginRequest struct {
	AuthKey string `json:"auth_key" binding:"required"`
}

// LoginResponse represents the login response
type LoginResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// Login handles authentication verification
func (s *Server) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request format",
		})
		return
	}

	authConfig := s.config.GetAuthConfig()

	// Special handling for setup mode
	if authConfig.Key == "__SETUP_MODE__" {
		// In setup mode, allow access with any password to access setup interface
		c.JSON(http.StatusOK, LoginResponse{
			Success: true,
			Message: "Setup mode - please complete initial configuration",
		})
		return
	}

	// Check if stored password is a hash (bcrypt hashes start with $2a$, $2b$, or $2y$)
	var isValid bool
	if len(authConfig.Key) > 4 && (authConfig.Key[:4] == "$2a$" || authConfig.Key[:4] == "$2b$" || authConfig.Key[:4] == "$2y$") {
		// It's a bcrypt hash, use bcrypt verification
		isValid = utils.CheckPasswordHash(req.AuthKey, authConfig.Key)
	} else {
		// It's a plain text key, use constant time comparison (for backward compatibility)
		isValid = subtle.ConstantTimeCompare([]byte(req.AuthKey), []byte(authConfig.Key)) == 1
	}

	if isValid {
		c.JSON(http.StatusOK, LoginResponse{
			Success: true,
			Message: "Authentication successful",
		})
	} else {
		c.JSON(http.StatusUnauthorized, LoginResponse{
			Success: false,
			Message: "Authentication failed",
		})
	}
}

// Health handles health check requests
func (s *Server) Health(c *gin.Context) {
	uptime := "unknown"
	if startTime, exists := c.Get("serverStartTime"); exists {
		if st, ok := startTime.(time.Time); ok {
			uptime = time.Since(st).String()
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"uptime":    uptime,
	})
}
