// Package middleware provides HTTP middleware for the application
package middleware

import (
	"crypto/subtle"
	"fmt"
	"strings"
	"time"

	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/response"
	"gpt-load/internal/services"
	"gpt-load/internal/types"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// Logger creates a high-performance logging middleware
func Logger(config types.LogConfig) gin.HandlerFunc {
	return func(c *gin.Context) {

		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		// Process request
		c.Next()

		// Calculate response time
		latency := time.Since(start)

		// Get basic information
		method := c.Request.Method
		statusCode := c.Writer.Status()

		// Build full path (avoid string concatenation)
		fullPath := path
		if raw != "" {
			fullPath = path + "?" + raw
		}

		// Get key information (if exists)
		keyInfo := ""
		if keyIndex, exists := c.Get("keyIndex"); exists {
			if keyPreview, exists := c.Get("keyPreview"); exists {
				keyInfo = fmt.Sprintf(" - Key[%v] %v", keyIndex, keyPreview)
			}
		}

		// Get retry information (if exists)
		retryInfo := ""
		if retryCount, exists := c.Get("retryCount"); exists {
			retryInfo = fmt.Sprintf(" - Retry[%d]", retryCount)
		}

		// Filter health check and other monitoring endpoint logs to reduce noise
		if isMonitoringEndpoint(path) {
			// Only log errors for monitoring endpoints
			if statusCode >= 400 {
				logrus.Warnf("%s %s - %d - %v", method, fullPath, statusCode, latency)
			}
			return
		}

		// Choose log level based on status code
		if statusCode >= 500 {
			logrus.Errorf("%s %s - %d - %v%s%s", method, fullPath, statusCode, latency, keyInfo, retryInfo)
		} else if statusCode >= 400 {
			logrus.Warnf("%s %s - %d - %v%s%s", method, fullPath, statusCode, latency, keyInfo, retryInfo)
		} else {
			logrus.Infof("%s %s - %d - %v%s%s", method, fullPath, statusCode, latency, keyInfo, retryInfo)
		}
	}
}

// CORS creates a CORS middleware
func CORS(config types.CORSConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !config.Enabled {
			c.Next()
			return
		}

		origin := c.Request.Header.Get("Origin")

		// Check if origin is allowed
		allowed := false
		for _, allowedOrigin := range config.AllowedOrigins {
			if allowedOrigin == "*" || allowedOrigin == origin {
				allowed = true
				break
			}
		}

		if allowed {
			c.Header("Access-Control-Allow-Origin", origin)
		}

		// Set other CORS headers
		c.Header("Access-Control-Allow-Methods", strings.Join(config.AllowedMethods, ", "))
		c.Header("Access-Control-Allow-Headers", strings.Join(config.AllowedHeaders, ", "))

		if config.AllowCredentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		}

		// Handle preflight requests
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// Auth creates an authentication middleware
func Auth(authConfig types.AuthConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		if isMonitoringEndpoint(path) {
			c.Next()
			return
		}

		key := extractAuthKey(c)

		isValid := key != "" && subtle.ConstantTimeCompare([]byte(key), []byte(authConfig.Key)) == 1

		if !isValid {
			response.Error(c, app_errors.ErrUnauthorized)
			c.Abort()
			return
		}

		c.Next()
	}
}

// ProxyAuth
func ProxyAuth(gm *services.GroupManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check key
		key := extractAuthKey(c)
		if key == "" {
			response.Error(c, app_errors.ErrUnauthorized)
			c.Abort()
			return
		}

		group, err := gm.GetGroupByName(c.Param("group_name"))
		if err != nil {
			response.Error(c, app_errors.NewAPIError(app_errors.ErrInternalServer, "Failed to retrieve proxy group"))
			c.Abort()
			return
		}

		// Check both key collections to prevent timing attacks
		_, existsInEffective := group.EffectiveConfig.ProxyKeysMap[key]
		_, existsInGroup := group.ProxyKeysMap[key]

		if existsInEffective || existsInGroup {
			c.Next()
			return
		}

		response.Error(c, app_errors.ErrUnauthorized)
		c.Abort()
	}
}

// Recovery creates a recovery middleware with custom error handling
func Recovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		logrus.Errorf("Panic recovered: %v", recovered)
		response.Error(c, app_errors.ErrInternalServer)
		c.Abort()
	})
}

// RateLimiter creates a simple rate limiting middleware
func RateLimiter(config types.PerformanceConfig) gin.HandlerFunc {
	// Simple semaphore-based rate limiting
	semaphore := make(chan struct{}, config.MaxConcurrentRequests)

	return func(c *gin.Context) {
		select {
		case semaphore <- struct{}{}:
			defer func() { <-semaphore }()
			c.Next()
		default:
			response.Error(c, app_errors.NewAPIError(app_errors.ErrInternalServer, "Too many concurrent requests"))
			c.Abort()
		}
	}
}

// ErrorHandler creates an error handling middleware
func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		// Handle any errors that occurred during request processing
		if len(c.Errors) > 0 {
			err := c.Errors.Last().Err

			// Check if it's our custom error type
			if apiErr, ok := err.(*app_errors.APIError); ok {
				response.Error(c, apiErr)
				return
			}

			// Handle other errors
			logrus.Errorf("Unhandled error: %v", err)
			response.Error(c, app_errors.ErrInternalServer)
		}
	}
}

// isMonitoringEndpoint checks if the path is a monitoring endpoint
func isMonitoringEndpoint(path string) bool {
	monitoringPaths := []string{"/health"}
	for _, monitoringPath := range monitoringPaths {
		if path == monitoringPath {
			return true
		}
	}
	return false
}

// extractAuthKey extracts a auth key.
func extractAuthKey(c *gin.Context) string {

	// Bearer token
	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		const bearerPrefix = "Bearer "
		if strings.HasPrefix(authHeader, bearerPrefix) {
			return authHeader[len(bearerPrefix):]
		}
	}

	// X-Api-Key
	if key := c.GetHeader("X-Api-Key"); key != "" {
		return key
	}

	// X-Goog-Api-Key
	if key := c.GetHeader("X-Goog-Api-Key"); key != "" {
		return key
	}

	// Query key
	if key := c.Query("key"); key != "" {
		return key
	}

	return ""
}

// StaticCache creates a middleware for caching static resources
func StaticCache() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		if isStaticResource(path) {
			c.Header("Cache-Control", "public, max-age=2592000, immutable")
			c.Header("Expires", time.Now().AddDate(1, 0, 0).UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT"))
		}

		c.Next()
	}
}

// isStaticResource 判断是否为静态资源
func isStaticResource(path string) bool {
	staticPrefixes := []string{"/assets/"}
	staticSuffixes := []string{
		".js", ".css", ".ico", ".png", ".jpg", ".jpeg",
		".gif", ".svg", ".woff", ".woff2", ".ttf", ".eot",
		".webp", ".avif", ".map",
	}

	// 检查路径前缀
	for _, prefix := range staticPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}

	// 检查文件扩展名
	for _, suffix := range staticSuffixes {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}

	return false
}
