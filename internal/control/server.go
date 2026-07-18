package control

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/i18n"
	"gpt-load/internal/platform/response"
)

type Server struct {
	authDigest [sha256.Size]byte
	service    *Service
}

func NewServer(cfg *config.Config, service *Service) *Server {
	return &Server{authDigest: sha256.Sum256([]byte(cfg.AuthKey)), service: service}
}

func (s *Server) RegisterRoutes(engine *gin.Engine) {
	api := engine.Group("/api")
	api.Use(i18n.Middleware(), s.authenticate())
	api.GET("/groups", s.handleListGroups)
	api.POST("/import", s.handleImport)
	api.POST("/access-keys", s.handleCreateAccessKey)
	api.GET("/access-keys", s.handleListAccessKeys)
	api.PUT("/access-keys/:id", s.handleUpdateAccessKey)
	api.DELETE("/access-keys/:id", s.handleDeleteAccessKey)
}

func (s *Server) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		fields := strings.Fields(c.GetHeader("Authorization"))
		token := ""
		if len(fields) == 2 {
			token = fields[1]
		}
		requestDigest := sha256.Sum256([]byte(token))
		formatValid := len(fields) == 2 && strings.EqualFold(fields[0], "Bearer")
		matches := subtle.ConstantTimeCompare(requestDigest[:], s.authDigest[:]) == 1
		if !formatValid || !matches {
			response.ErrorI18nFromAPIError(c, app_errors.ErrUnauthorized, "auth.invalid_key")
			c.Abort()
			return
		}
		c.Next()
	}
}

func (s *Server) handleListGroups(c *gin.Context) {
	groups, err := s.service.ListGroups(c.Request.Context())
	if err != nil {
		writeServiceError(c, "list_groups", err)
		return
	}
	response.SuccessI18n(c, "common.success", groups)
}

func (s *Server) handleImport(c *gin.Context) {
	var request ImportRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "import", app_errors.ErrInvalidJSON)
		return
	}
	result, err := s.service.Import(c.Request.Context(), request)
	if err != nil {
		writeServiceError(c, "import", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleCreateAccessKey(c *gin.Context) {
	var request AccessKeyCreateRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "create_access_key", app_errors.ErrInvalidJSON)
		return
	}
	result, err := s.service.CreateAccessKey(c.Request.Context(), request)
	if err != nil {
		writeServiceError(c, "create_access_key", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleListAccessKeys(c *gin.Context) {
	result, err := s.service.ListAccessKeys(c.Request.Context())
	if err != nil {
		writeServiceError(c, "list_access_keys", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleUpdateAccessKey(c *gin.Context) {
	id, ok := accessKeyID(c)
	if !ok {
		return
	}
	var request AccessKeyUpdateRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "update_access_key", app_errors.ErrInvalidJSON)
		return
	}
	result, err := s.service.UpdateAccessKey(c.Request.Context(), id, request)
	if err != nil {
		writeServiceError(c, "update_access_key", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleDeleteAccessKey(c *gin.Context) {
	id, ok := accessKeyID(c)
	if !ok {
		return
	}
	if err := s.service.DeleteAccessKey(c.Request.Context(), id); err != nil {
		writeServiceError(c, "delete_access_key", err)
		return
	}
	response.SuccessI18n(c, "common.success", nil)
}

func bindStrictJSON(c *gin.Context, target any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}

	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode JSON request: multiple values")
		}
		return err
	}
	return nil
}

func accessKeyID(c *gin.Context) (uint, bool) {
	parsed, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
	if err != nil || parsed == 0 {
		writeServiceError(c, "access_key_id", app_errors.ErrBadRequest)
		return 0, false
	}
	return uint(parsed), true
}

func writeServiceError(c *gin.Context, operation string, err error) {
	var apiErr *app_errors.APIError
	if errors.As(err, &apiErr) {
		if apiErr.HTTPStatus >= http.StatusInternalServerError {
			logServiceError(operation, err, apiErr.Code)
		}
		response.ErrorI18nFromAPIError(c, apiErr, serviceErrorMessageID(operation, apiErr))
		return
	}

	logServiceError(operation, err, app_errors.ErrInternalServer.Code)
	response.ErrorI18nFromAPIError(c, app_errors.ErrInternalServer, "internal_error")
}

func serviceErrorMessageID(operation string, apiErr *app_errors.APIError) string {
	switch apiErr.Code {
	case app_errors.ErrBadRequest.Code, app_errors.ErrInvalidJSON.Code, app_errors.ErrValidation.Code:
		return "bad_request"
	case app_errors.ErrResourceNotFound.Code:
		if operation == "list_groups" {
			return "group.not_found"
		}
		return "key.not_found"
	case app_errors.ErrDuplicateResource.Code:
		if operation == "import" {
			return "group.name_exists"
		}
		return "bad_request"
	default:
		return "internal_error"
	}
}

func logServiceError(operation string, err error, code string) {
	logrus.WithFields(logrus.Fields{
		"operation":  operation,
		"error_code": code,
		"error_type": fmt.Sprintf("%T", err),
	}).Error("Control operation failed")
}
