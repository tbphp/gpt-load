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

const maxControlJSONBodyBytes int64 = 32 << 20

func NewServer(cfg *config.Config, service *Service) *Server {
	return &Server{authDigest: sha256.Sum256([]byte(cfg.AuthKey)), service: service}
}

func (s *Server) RegisterRoutes(engine *gin.Engine) {
	api := engine.Group("/api")
	api.Use(i18n.Middleware(), s.authenticate())
	api.GET("/groups", s.handleListGroups)
	api.POST("/groups", s.handleCreateGroup)
	api.POST("/groups/:group_id/keys/import", s.handleImportGroupKeys)
	api.POST("/groups/:group_id/models/discover", s.handleDiscoverGroupModels)
	api.POST("/models/discover", s.handleDiscoverModels)
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

func (s *Server) handleCreateGroup(c *gin.Context) {
	var request GroupCreateRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "create_group", mapControlJSONError(err))
		return
	}
	result, err := s.service.CreateGroup(c.Request.Context(), request)
	if err != nil {
		writeServiceError(c, "create_group", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleImportGroupKeys(c *gin.Context) {
	id, ok := groupID(c, "import_group_keys")
	if !ok {
		return
	}
	var request GroupKeyImportRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "import_group_keys", mapControlJSONError(err))
		return
	}
	result, err := s.service.ImportGroupKeys(c.Request.Context(), id, request)
	if err != nil {
		writeServiceError(c, "import_group_keys", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleDiscoverGroupModels(c *gin.Context) {
	id, ok := groupID(c, "discover_group_models")
	if !ok {
		return
	}
	if err := bindOptionalEmptyJSONObject(c); err != nil {
		writeServiceError(c, "discover_group_models", mapControlJSONError(err))
		return
	}
	result, err := s.service.DiscoverGroupModels(c.Request.Context(), id)
	if err != nil {
		writeServiceError(c, "discover_group_models", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleDiscoverModels(c *gin.Context) {
	var request ModelDiscoveryRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeDiscoveryError(c, mapControlJSONError(err))
		return
	}
	result, err := s.service.DiscoverModels(c.Request.Context(), request)
	if err != nil {
		writeDiscoveryError(c, err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleCreateAccessKey(c *gin.Context) {
	var request AccessKeyCreateRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "create_access_key", mapControlJSONError(err))
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
		writeServiceError(c, "update_access_key", mapControlJSONError(err))
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
	decoder, err := newControlJSONDecoder(c)
	if err != nil {
		return err
	}
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

func bindOptionalEmptyJSONObject(c *gin.Context) error {
	decoder, err := newControlJSONDecoder(c)
	if err != nil {
		return err
	}
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if object == nil || len(object) != 0 {
		return fmt.Errorf("request body must be an empty JSON object")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode JSON request: multiple values")
		}
		return err
	}
	return nil
}

func newControlJSONDecoder(c *gin.Context) (*json.Decoder, error) {
	if c.Request.ContentLength > maxControlJSONBodyBytes {
		return nil, &http.MaxBytesError{Limit: maxControlJSONBodyBytes}
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxControlJSONBodyBytes)
	return json.NewDecoder(c.Request.Body), nil
}

func mapControlJSONError(err error) *app_errors.APIError {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return app_errors.ErrRequestTooLarge
	}
	return app_errors.ErrInvalidJSON
}

func accessKeyID(c *gin.Context) (uint, bool) {
	parsed, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
	if err != nil || parsed == 0 {
		writeServiceError(c, "access_key_id", app_errors.ErrBadRequest)
		return 0, false
	}
	return uint(parsed), true
}

func groupID(c *gin.Context, operation string) (uint, bool) {
	parsed, err := strconv.ParseUint(c.Param("group_id"), 10, strconv.IntSize)
	if err != nil || parsed == 0 {
		writeServiceError(c, operation, app_errors.ErrBadRequest)
		return 0, false
	}
	return uint(parsed), true
}

func writeServiceError(c *gin.Context, operation string, err error) {
	writeServiceErrorResponse(c, operation, err)
}

func writeDiscoveryError(c *gin.Context, err error) {
	writeServiceErrorResponse(c, "discover_models", err)
}

func writeServiceErrorResponse(
	c *gin.Context,
	operation string,
	err error,
) {
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
	case app_errors.ErrRequestTooLarge.Code:
		return "request_too_large"
	case app_errors.ErrBadRequest.Code, app_errors.ErrInvalidJSON.Code, app_errors.ErrValidation.Code:
		return "bad_request"
	case app_errors.ErrResourceNotFound.Code:
		if operation == "list_groups" || operation == "import_group_keys" ||
			operation == "discover_group_models" {
			return "group.not_found"
		}
		return "key.not_found"
	case app_errors.ErrNoActiveUpstreamKey.Code:
		return "group.no_active_upstream_key"
	case app_errors.ErrDuplicateResource.Code:
		if operation == "create_group" {
			return "group.name_exists"
		}
		return "bad_request"
	case app_errors.ErrUpstreamURLConflict.Code:
		return "group.upstream_url_conflict"
	case app_errors.ErrBadGateway.Code:
		return "bad_gateway"
	default:
		return "internal_error"
	}
}

func logServiceError(operation string, err error, code string) {
	fields := logrus.Fields{
		"operation":  operation,
		"error_code": code,
		"error_type": fmt.Sprintf("%T", err),
	}
	logrus.WithFields(fields).Error("Control operation failed")
}
